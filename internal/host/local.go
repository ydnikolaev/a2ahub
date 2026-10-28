package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TrailerApprovedGates is the one spelling of the approval trailer a write's
// head commit may carry. A land copies it from the head commit's message onto
// the merge commit it creates, byte for byte; the code that reserves the trailer
// against forgery reads this constant, never a second literal.
const TrailerApprovedGates = "A2A-Approved-Gates"

// maxLandAttempts bounds how many times one OpenPR rebuilds its candidate on a
// moved base tip (or a contended ref lock) before it gives up with
// ErrLandContended and keeps the head branch.
const maxLandAttempts = 8

// maxLandRecordBytes bounds a landed record so every record can be read back
// within the package's bounded-output reader.
const maxLandRecordBytes = maxRecoveryGitOutput

const (
	landedRefPrefix = "refs/a2a/landed/"
	prRefPrefix     = "refs/a2a/pr/"
	headRefPrefix   = "refs/heads/"
)

// errLocalGit marks a failed local git invocation (the local host has no HTTP
// API, so ErrRequestFailed's wording would mislead).
var errLocalGit = errors.New("host: local git operation failed")

// LandCandidate is the tree one land would produce, checked out read-only at
// MergeSHA in CheckoutDir. The host passes the facts and parses none of them.
type LandCandidate struct {
	// CheckoutDir holds a clone of the space checked out (detached) at MergeSHA.
	CheckoutDir string
	// BaseBranch is the branch the write lands on.
	BaseBranch string
	// BaseSHA is the tip T the candidate was built on. The clone holds T's
	// objects, so a check reads the tip's files as `git show <BaseSHA>:<path>`
	// in CheckoutDir.
	BaseSHA string
	// MergeSHA is the merge commit M the land would create.
	MergeSHA string
	// HeadSHA is the head branch's tip H.
	HeadSHA string
	// HeadMessage is H's full commit message, verbatim.
	HeadMessage string
	// Author is the configured land author (local:<system>).
	Author string
}

// LandVerdict is a land check's judgment of a candidate.
type LandVerdict struct {
	// OK is true when the candidate is valid and may land.
	OK bool
	// Codes are the validator's own codes (artifact codes and AUTHZ-001).
	Codes []string
	// Detail is the human report, bounded by the check.
	Detail string
}

// LandCheckFunc judges a candidate. An error means "could not judge" (manifest
// unreadable, git failure) and is never treated as a pass. The function is
// handed in at construction because this package imports no validator.
type LandCheckFunc func(ctx context.Context, candidate LandCandidate) (LandVerdict, error)

// gitExecFunc runs one git invocation. LocalHost holds it as a field so a test
// can stand in for a git that lacks a feature; production uses
// runRecoveryGitInput.
type gitExecFunc func(ctx context.Context, stdin []byte, stdoutLimit int, args ...string) recoveryGitResult

// LocalHost is a Host over a bare git repository on this machine's disk. A
// pushed head branch is validated by the injected land check against the exact
// base tip it would land on, and landed in the same OpenPR call by
// compare-and-swap, as a merge commit. Nothing is ever open: a write is either
// merged or it leaves nothing.
//
// Several LocalHost values (several processes) may write to one space
// concurrently. They share no state but the repository's refs: the base branch
// moves only through a compare-and-swap, and a write that loses the swap is
// rebuilt on the new tip and judged again.
type LocalHost struct {
	// gitOps supplies PushBranch, ReadRemoteBranch and the recovery readers.
	gitOps

	dir       string
	spaceURL  string
	landCheck LandCheckFunc
	author    string
	exec      gitExecFunc
}

var (
	_ Host                = (*LocalHost)(nil)
	_ OpenPRLister        = (*LocalHost)(nil)
	_ RemoteBranchReader  = (*LocalHost)(nil)
	_ LocalSpaceInspector = (*LocalHost)(nil)
)

// newLocalHost builds the host for the bare repository at dir. host.Open is its
// only production caller. An empty author is allowed: it opens, and OpenPR
// refuses before it reads or writes anything.
func newLocalHost(dir string, check LandCheckFunc, author string) *LocalHost {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	link := url.URL{Scheme: "file", Path: abs}
	return &LocalHost{
		dir:       abs,
		spaceURL:  link.String(),
		landCheck: check,
		author:    author,
		exec:      runRecoveryGitInput,
	}
}

// Capabilities implements Host.Capabilities: a local space needs no credential,
// has no reviewer, trusts the caller's own configured identity, lands inside
// the OpenPR call and lives on this machine's disk.
func (h *LocalHost) Capabilities() Capabilities {
	return Capabilities{
		NeedsCredential:           false,
		ReviewsWrites:             false,
		OwnerIdentitySelfAsserted: true,
		LandsSynchronously:        true,
		OnThisMachine:             true,
	}
}

// ReviewStatus implements Host.ReviewStatus: a local space has no reviewer, so
// nothing is approved. Human gates there are a policy of the land check, never
// inferred from this read.
func (h *LocalHost) ReviewStatus(context.Context, StatusRequest) (ReviewStatusResult, error) {
	return ReviewStatusResult{Approved: false}, nil
}

// TokenScopes implements Host.TokenScopes with the documented neutral answer:
// this host does not advertise scopes.
func (h *LocalHost) TokenScopes(context.Context, Credential) ([]string, bool, error) {
	return nil, false, nil
}

// ListOpenPRs implements OpenPRLister. Nothing is ever open on a local space.
func (h *LocalHost) ListOpenPRs(context.Context, ListOpenPRsRequest) ([]OpenPRSummary, error) {
	return nil, nil
}

// CheckStatus implements Host.CheckStatus: a landed number was validated before
// it landed, so its check is completed and successful, against the head the
// record names. An unknown number is an invalid request.
func (h *LocalHost) CheckStatus(ctx context.Context, req StatusRequest) (CheckStatusResult, error) {
	const op = "CheckStatus"
	if req.PRNumber <= 0 {
		return CheckStatusResult{}, &Error{Op: op, Err: ErrInvalidRequest}
	}
	rec, _, found, err := h.readRecordRef(ctx, op, prRefPrefix+strconv.Itoa(req.PRNumber))
	if err != nil {
		return CheckStatusResult{}, err
	}
	if !found {
		return CheckStatusResult{}, &Error{Op: op, Input: strconv.Itoa(req.PRNumber), Err: fmt.Errorf("%w: no landed write has that number", ErrInvalidRequest)}
	}
	return CheckStatusResult{State: "completed", Conclusion: "success", HeadSHA: rec.HeadSHA}, nil
}

// FindPRByHeadBranch implements Host.FindPRByHeadBranch: the newest landed
// record for the branch, or (nil, nil). A refused write leaves no record.
func (h *LocalHost) FindPRByHeadBranch(ctx context.Context, req FindPRRequest) (*PRInfo, error) {
	const op = "FindPRByHeadBranch"
	if req.Branch == "" {
		return nil, &Error{Op: op, Err: ErrInvalidRequest}
	}
	if err := h.checkRefName(ctx, op, headRefPrefix+req.Branch); err != nil {
		return nil, err
	}
	rec, _, found, err := h.readRecordRef(ctx, op, landedRefPrefix+req.Branch)
	if err != nil || !found {
		return nil, err
	}
	info := rec.prInfo()
	return &info, nil
}

// landRecord is the blob a landed write is recorded as, canonical JSON in this
// field order. It is private to this package and versioned by its field set.
type landRecord struct {
	Number  int    `json:"number"`
	Merge   string `json:"merge"`
	Head    string `json:"head"`
	HeadSHA string `json:"head_sha"`
	Base    string `json:"base"`
	BaseSHA string `json:"base_sha"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	URL     string `json:"url"`
}

// prInfo is the one place a record becomes a PRInfo, so every path that answers
// a caller (a fresh land, a retry, a concurrent winner, a lookup) answers with
// identical bytes.
func (r landRecord) prInfo() PRInfo {
	return PRInfo{
		Number:      r.Number,
		URL:         r.URL,
		State:       "merged",
		Title:       r.Title,
		Body:        r.Body,
		BaseBranch:  r.Base,
		HeadSHA:     r.HeadSHA,
		MergeMethod: MergeMethodMerge,
	}
}

// OpenPR implements Host.OpenPR by landing the write: see landOnce for one
// attempt and the spec's landing algorithm for the steps. It returns a PRInfo in
// state "merged" or an error; a refused or conflicting write leaves nothing but
// the error.
func (h *LocalHost) OpenPR(ctx context.Context, req OpenPRRequest) (PRInfo, error) {
	const op = "OpenPR"
	if h.author == "" {
		return PRInfo{}, &Error{Op: op, Input: req.Head, Err: fmt.Errorf(
			"%w: no system is configured for this project, so a write to a local space has no author (run from a configured project)", ErrInvalidRequest)}
	}
	if req.Head == "" || req.Base == "" || !utf8.ValidString(req.Title) || !utf8.ValidString(req.Body) ||
		strings.ContainsRune(req.Title, 0) || strings.ContainsRune(req.Body, 0) {
		return PRInfo{}, &Error{Op: op, Err: ErrInvalidRequest}
	}
	for _, name := range []string{req.Head, req.Base} {
		if err := h.checkRefName(ctx, op, headRefPrefix+name); err != nil {
			return PRInfo{}, err
		}
	}
	for attempt := 1; attempt <= maxLandAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return PRInfo{}, &Error{Op: op, Input: req.Head, Err: fmt.Errorf("%w: %w", errLocalGit, err)}
		}
		info, done, err := h.landOnce(ctx, req)
		if err != nil || done {
			return info, err
		}
	}
	return PRInfo{}, &Error{Op: op, Input: req.Head, Err: fmt.Errorf(
		"%w: the refs kept changing under the write (%s moved, or a ref lock was held) for %d attempts; the head branch is kept, so a retry resumes it", ErrLandContended, req.Base, maxLandAttempts)}
}

// landOnce is one pass of the landing algorithm (steps 0 to 9). done=false with
// no error means the base tip moved or a ref lock was contended: the caller
// rebuilds the candidate on the new tip.
func (h *LocalHost) landOnce(ctx context.Context, req OpenPRRequest) (info PRInfo, done bool, err error) {
	const op = "OpenPR"
	headRef := headRefPrefix + req.Head

	// Step 0 and 1: where does this head branch stand?
	headSHA, headExists, err := h.readRef(ctx, op, headRef)
	if err != nil {
		return PRInfo{}, false, err
	}
	rec, recBlob, recExists, err := h.readRecordRef(ctx, op, landedRefPrefix+req.Head)
	if err != nil {
		return PRInfo{}, false, err
	}
	if !headExists {
		switch {
		case recExists && (req.ExpectedHeadSHA == "" || req.ExpectedHeadSHA == rec.HeadSHA):
			// A retry after a successful land, which deleted the branch.
			return rec.prInfo(), true, nil
		case recExists:
			// The record answers an earlier write on this reused branch name.
			return PRInfo{}, false, &Error{Op: op, Input: req.Head, Err: fmt.Errorf(
				"%w: the head branch is gone and the last landed write on it is a different head", ErrPRHeadMoved)}
		default:
			return PRInfo{}, false, &Error{Op: op, Input: req.Head, Err: fmt.Errorf("%w: the head branch does not exist", ErrInvalidRequest)}
		}
	}
	if req.ExpectedHeadSHA != "" && req.ExpectedHeadSHA != headSHA {
		return PRInfo{}, false, &Error{Op: op, Input: req.Head, Err: ErrPRHeadMoved}
	}
	if recExists && rec.HeadSHA == headSHA {
		return rec.prInfo(), true, nil
	}

	// Step 2: the tip this write will land on.
	tip, tipExists, err := h.readRef(ctx, op, headRefPrefix+req.Base)
	if err != nil {
		return PRInfo{}, false, err
	}
	if !tipExists {
		return PRInfo{}, false, &Error{Op: op, Input: req.Base, Err: fmt.Errorf("%w: the base branch does not exist", ErrInvalidRequest)}
	}

	// Step 3: merge without touching a working tree.
	tree, conflict, err := h.mergeTree(ctx, op, tip, headSHA)
	if err != nil {
		return PRInfo{}, false, err
	}
	if conflict {
		deleteErr := h.deleteHeadBranch(ctx, op, req.Head, headSHA)
		return PRInfo{}, false, &Error{Op: op, Input: req.Head, Err: joinKept(ErrLandConflict, deleteErr)}
	}

	// Step 4: the merge commit, GitHub's shape.
	headMessage, err := h.commitMessage(ctx, op, headSHA)
	if err != nil {
		return PRInfo{}, false, err
	}
	number, err := h.nextNumber(ctx, op, tip)
	if err != nil {
		return PRInfo{}, false, err
	}
	mergeSHA, err := h.commitMerge(ctx, op, tree, tip, headSHA, mergeMessage(number, req, headMessage))
	if err != nil {
		return PRInfo{}, false, err
	}

	// Step 5 and 6: judge the exact tree the land would produce.
	verdict, err := h.judge(ctx, LandCandidate{
		BaseBranch: req.Base, BaseSHA: tip, MergeSHA: mergeSHA, HeadSHA: headSHA,
		HeadMessage: headMessage, Author: h.author,
	})
	if err != nil {
		return PRInfo{}, false, &Error{Op: op, Input: req.Head, Err: fmt.Errorf("land check could not judge the write: %w", err)}
	}
	if !verdict.OK {
		deleteErr := h.deleteHeadBranch(ctx, op, req.Head, headSHA)
		refusal := &LandRefusal{Codes: append([]string(nil), verdict.Codes...), Detail: verdict.Detail}
		return PRInfo{}, false, &Error{Op: op, Input: req.Head, Err: joinKept(fmt.Errorf("%w: %w", ErrLandRefused, refusal), deleteErr)}
	}

	// Step 7: one atomic transaction moves the base, records the land and removes
	// the head branch.
	next := landRecord{
		Number: number, Merge: mergeSHA, Head: req.Head, HeadSHA: headSHA, Base: req.Base, BaseSHA: tip,
		Title: req.Title, Body: req.Body, URL: h.landURL(number),
	}
	blob, err := h.writeRecord(ctx, op, next)
	if err != nil {
		return PRInfo{}, false, err
	}
	commands := h.landCommands(next, blob, recBlob)
	res := h.exec(ctx, []byte(commands), 4096, h.gitDirArg(), "update-ref", "--stdin")
	if res.err == nil && res.exitCode == 0 {
		return next.prInfo(), true, nil
	}

	// Step 9 then 8: tell "a concurrent identical call won" from "the tip moved".
	winner, _, won, readErr := h.readRecordRef(ctx, op, landedRefPrefix+req.Head)
	if readErr != nil {
		return PRInfo{}, false, readErr
	}
	if won && winner.HeadSHA == headSHA {
		return winner.prInfo(), true, nil
	}
	if res.err == nil && res.exitCode == 128 && bytes.Contains(res.stderr, []byte("cannot lock ref")) {
		return PRInfo{}, false, nil
	}
	return PRInfo{}, false, localGitError(op, "land transaction", res)
}

// landCommands is the update-ref --stdin script of step 7. oldRecord is the blob
// of the earlier write's record on a reused branch name ("" when there is none),
// which the new record replaces by compare-and-swap.
func (h *LocalHost) landCommands(rec landRecord, blob, oldRecord string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "update %s%s %s %s\n", headRefPrefix, rec.Base, rec.Merge, rec.BaseSHA)
	if oldRecord == "" {
		fmt.Fprintf(&b, "create %s%s %s\n", landedRefPrefix, rec.Head, blob)
	} else {
		fmt.Fprintf(&b, "update %s%s %s %s\n", landedRefPrefix, rec.Head, blob, oldRecord)
	}
	fmt.Fprintf(&b, "create %s%d %s\n", prRefPrefix, rec.Number, blob)
	fmt.Fprintf(&b, "delete %s%s %s\n", headRefPrefix, rec.Head, rec.HeadSHA)
	return b.String()
}

// landURL is the deterministic, non-empty URL of a landed write.
func (h *LocalHost) landURL(number int) string {
	return h.spaceURL + "#land-" + strconv.Itoa(number)
}

// joinKept appends the outcome of the head-branch cleanup to a refusal: the
// refusal is the answer, and a cleanup that failed is said, not swallowed.
func joinKept(refusal, deleteErr error) error {
	if deleteErr == nil {
		return refusal
	}
	return fmt.Errorf("%w; the head branch was kept: %w", refusal, deleteErr)
}

// mergeMessage is the merge commit's message: GitHub's subject, the title, and
// the approval trailer lines copied from the head commit when it carries any.
func mergeMessage(number int, req OpenPRRequest, headMessage string) string {
	var b strings.Builder
	b.WriteString("Merge pull request #" + strconv.Itoa(number) + " from " + req.Head + "\n")
	if req.Title != "" {
		b.WriteString("\n" + req.Title + "\n")
	}
	if lines := approvedGatesTrailers(headMessage); len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}
	return b.String()
}

// approvedGatesTrailers returns, byte for byte, the lines of the message's
// final paragraph (its trailer block) that open with the approval trailer's
// token and a colon. The host copies them and interprets none of them.
func approvedGatesTrailers(message string) []string {
	lines := strings.Split(message, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	var out []string
	for _, line := range lines[start:end] {
		if strings.HasPrefix(line, TrailerApprovedGates+":") {
			out = append(out, line)
		}
	}
	return out
}

// judge runs the land check over a throwaway clone checked out at the merge
// commit. The clone shares the space's objects, so it costs no copy, and it is
// always removed.
func (h *LocalHost) judge(ctx context.Context, candidate LandCandidate) (verdict LandVerdict, err error) {
	const op = "OpenPR"
	dir, err := os.MkdirTemp("", "a2a-land-")
	if err != nil {
		return LandVerdict{}, fmt.Errorf("%w: create the check directory: %w", errLocalGit, err)
	}
	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			verdict, err = LandVerdict{}, errors.Join(err, fmt.Errorf("remove the check directory: %w", removeErr))
		}
	}()
	clone := h.exec(ctx, nil, 4096, "-c", "core.hooksPath=/dev/null", "clone", "--quiet", "--shared", "--no-checkout",
		"--no-tags", "--single-branch", "--branch", candidate.BaseBranch, "--", h.dir, dir)
	if clone.err != nil || clone.exitCode != 0 {
		return LandVerdict{}, localGitError(op, "clone the space for the land check", clone)
	}
	checkout := h.exec(ctx, nil, 4096, "-C", dir, "-c", "core.hooksPath=/dev/null", "checkout", "--quiet", "--detach", candidate.MergeSHA)
	if checkout.err != nil || checkout.exitCode != 0 {
		return LandVerdict{}, localGitError(op, "check out the candidate", checkout)
	}
	candidate.CheckoutDir = dir
	return h.landCheck(ctx, candidate)
}

// gitDirArg points a git invocation at this space's repository, so no
// discovery from the working directory can pick a different one.
func (h *LocalHost) gitDirArg() string { return "--git-dir=" + h.dir }

// run executes git against the space's repository.
func (h *LocalHost) run(ctx context.Context, limit int, args ...string) recoveryGitResult {
	return h.runIn(ctx, nil, limit, args...)
}

func (h *LocalHost) runIn(ctx context.Context, stdin []byte, limit int, args ...string) recoveryGitResult {
	return h.exec(ctx, stdin, limit, append([]string{h.gitDirArg()}, args...)...)
}

// checkRefName refuses a name git would not accept as a ref, which also keeps
// a newline or a space out of the update-ref script.
func (h *LocalHost) checkRefName(ctx context.Context, op, ref string) error {
	res := h.run(ctx, 256, "check-ref-format", ref)
	switch {
	case res.err == nil && res.exitCode == 0:
		return nil
	case res.err == nil && res.exitCode == 1:
		// git's answer for a name that is not a valid ref.
		return &Error{Op: op, Input: ref, Err: fmt.Errorf("%w: not a valid ref name", ErrInvalidRequest)}
	default:
		return localGitError(op, "check the ref name", res)
	}
}

// readRef resolves one ref to an object id; exists=false means the ref is absent.
func (h *LocalHost) readRef(ctx context.Context, op, ref string) (sha string, exists bool, err error) {
	res := h.run(ctx, 256, "rev-parse", "--verify", "--quiet", ref)
	if res.err != nil {
		return "", false, localGitError(op, "read "+ref, res)
	}
	switch res.exitCode {
	case 0:
		sha = strings.TrimSpace(string(res.stdout))
		if !validRecoveryGitOID(sha) {
			return "", false, localGitError(op, "malformed object id for "+ref, res)
		}
		return sha, true, nil
	case 1:
		return "", false, nil
	default:
		return "", false, localGitError(op, "read "+ref, res)
	}
}

// readRecordRef reads and decodes the record blob a ref points at.
func (h *LocalHost) readRecordRef(ctx context.Context, op, ref string) (rec landRecord, blob string, found bool, err error) {
	blob, exists, err := h.readRef(ctx, op, ref)
	if err != nil || !exists {
		return landRecord{}, "", false, err
	}
	res := h.run(ctx, maxLandRecordBytes, "cat-file", "blob", blob)
	if res.err != nil || res.exitCode != 0 {
		return landRecord{}, "", false, localGitError(op, "read the landed record at "+ref, res)
	}
	if err := json.Unmarshal(res.stdout, &rec); err != nil {
		return landRecord{}, "", false, &Error{Op: op, Input: ref, Err: fmt.Errorf("%w: the landed record is not readable: %w", errLocalGit, err)}
	}
	return rec, blob, true, nil
}

// writeRecord stores the record as a blob and returns its object id.
func (h *LocalHost) writeRecord(ctx context.Context, op string, rec landRecord) (string, error) {
	var encoded bytes.Buffer
	enc := json.NewEncoder(&encoded)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return "", &Error{Op: op, Input: rec.Head, Err: fmt.Errorf("%w: %w", ErrInvalidRequest, err)}
	}
	if encoded.Len() > maxLandRecordBytes {
		return "", &Error{Op: op, Input: rec.Head, Err: fmt.Errorf("%w: the title and body are too large to record", ErrInvalidRequest)}
	}
	res := h.runIn(ctx, encoded.Bytes(), 256, "hash-object", "-w", "--stdin")
	if res.err != nil || res.exitCode != 0 {
		return "", localGitError(op, "store the landed record", res)
	}
	blob := strings.TrimSpace(string(res.stdout))
	if !validRecoveryGitOID(blob) {
		return "", localGitError(op, "malformed record object id", res)
	}
	return blob, nil
}

// mergeTree merges head into tip without a working tree (git 2.38 or newer).
func (h *LocalHost) mergeTree(ctx context.Context, op, tip, head string) (tree string, conflict bool, err error) {
	res := h.run(ctx, maxRecoveryGitOutput, "merge-tree", "--write-tree", tip, head)
	switch {
	case res.err != nil:
		return "", false, localGitError(op, "merge", res)
	case res.exitCode == 0:
		first, _, _ := strings.Cut(string(res.stdout), "\n")
		tree = strings.TrimSpace(first)
		if !validRecoveryGitOID(tree) {
			return "", false, localGitError(op, "malformed merge result", res)
		}
		return tree, false, nil
	case res.exitCode == 1:
		return "", true, nil
	case res.exitCode == 129:
		// git's usage exit: the option is unknown, which is what a git older
		// than 2.38 says to `merge-tree --write-tree`.
		return "", false, &Error{Op: op, Err: ErrGitTooOld}
	default:
		return "", false, localGitError(op, "merge", res)
	}
}

// commitMessage returns the commit's message verbatim: the bytes after the
// header block's blank line, with nothing normalised.
func (h *LocalHost) commitMessage(ctx context.Context, op, sha string) (string, error) {
	res := h.run(ctx, maxRecoveryGitOutput, "cat-file", "commit", sha)
	if res.err != nil || res.exitCode != 0 {
		return "", localGitError(op, "read the head commit", res)
	}
	_, message, _ := strings.Cut(string(res.stdout), "\n\n")
	return message, nil
}

// nextNumber is the number the next land gets: the first-parent depth of the
// tip, plus one, which is the depth of the merge commit it will create.
func (h *LocalHost) nextNumber(ctx context.Context, op, tip string) (int, error) {
	res := h.run(ctx, 64, "rev-list", "--first-parent", "--count", tip)
	if res.err != nil || res.exitCode != 0 {
		return 0, localGitError(op, "count the first-parent depth", res)
	}
	depth, err := strconv.Atoi(strings.TrimSpace(string(res.stdout)))
	if err != nil || depth < 1 {
		return 0, localGitError(op, "malformed first-parent depth", res)
	}
	return depth + 1, nil
}

// commitMerge creates the merge commit (parents: tip, then head) authored by the
// land author, signed by nobody.
func (h *LocalHost) commitMerge(ctx context.Context, op, tree, tip, head, message string) (string, error) {
	res := h.runIn(ctx, []byte(message), 256,
		"-c", "user.name="+h.author, "-c", "user.email="+authorEmail(h.author), "-c", "commit.gpgsign=false",
		"commit-tree", tree, "-p", tip, "-p", head)
	if res.err != nil || res.exitCode != 0 {
		return "", localGitError(op, "create the merge commit", res)
	}
	sha := strings.TrimSpace(string(res.stdout))
	if !validRecoveryGitOID(sha) {
		return "", localGitError(op, "malformed merge commit id", res)
	}
	return sha, nil
}

// authorEmail derives the merge commit's e-mail from the land author, which
// carries a colon (local:<system>) that an address cannot.
func authorEmail(author string) string {
	var b strings.Builder
	for _, c := range author {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('-')
		}
	}
	return b.String() + "@local.a2a.invalid"
}

// deleteHeadBranch removes the head branch by compare-and-swap, so a head that
// moved since it was read is never deleted.
func (h *LocalHost) deleteHeadBranch(ctx context.Context, op, head, headSHA string) error {
	res := h.run(ctx, 1024, "update-ref", "-d", headRefPrefix+head, headSHA)
	if res.err != nil || res.exitCode != 0 {
		return localGitError(op, "delete the head branch", res)
	}
	return nil
}

// localGitError reports a failed local git invocation. Its stderr is local
// (no credential rides on a disk repository), so a bounded excerpt is kept.
func localGitError(op, stage string, res recoveryGitResult) error {
	if res.err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: %s: %w", errLocalGit, stage, res.err)}
	}
	detail := strings.TrimSpace(string(res.stderr))
	if len(detail) > 300 {
		detail = detail[:300]
	}
	if detail == "" {
		return &Error{Op: op, Err: fmt.Errorf("%w: %s: git exited %d", errLocalGit, stage, res.exitCode)}
	}
	return &Error{Op: op, Err: fmt.Errorf("%w: %s: git exited %d: %s", errLocalGit, stage, res.exitCode, detail)}
}
