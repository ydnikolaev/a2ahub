package space

import (
	"context"
	"errors"
	"fmt"
	"os"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// WriteState is the funnel's own claim about a Submit outcome — the
// contract P7's cache persistence (out of this phase's footprint) is
// built against. Kept stable across phases per spec 05 §9.
type WriteState string

const (
	// WriteStatePendingMerge is returned after a fresh push+PR-open: the
	// PR is open and auto-merge is armed, but not yet merged.
	WriteStatePendingMerge WriteState = "pending-merge"
	// WriteStateAlreadyOpen is returned by the idempotent short-circuit
	// (step 0) when a re-run finds an already-open PR for the
	// deterministic branch.
	WriteStateAlreadyOpen WriteState = "already-open"
	// WriteStateAlreadyMerged is returned by the idempotent short-circuit
	// when the deterministic branch's PR was ALREADY merged before this
	// call — including the repair path, where a re-run found the PR open and
	// landed it: the artifact was submitted by an earlier invocation either
	// way, which is what the caller's "already submitted" message says.
	//
	// The go-auditor read that repair case as the same lie WriteStateMerged
	// exists to prevent (2026-07-25). Adjudicated: it is not. The short-circuit
	// only fires when FindPRByHeadBranch already found a PR, which means a
	// PREVIOUS invocation pushed and opened it — so "already submitted" is
	// literally true, and the only imprecision left is that the caller cannot
	// tell "it was merged before I ran" from "I landed what a prior call
	// opened". That distinction changes nothing a caller does (nothing is
	// pending either way), so it stays one state rather than three.
	WriteStateAlreadyMerged WriteState = "already-merged"
	// WriteStateMerged is returned when THIS call did the whole thing:
	// a fresh push + PR-open, GitHub declined to arm auto-merge because the
	// PR was already mergeable, the required check read explicitly green,
	// and the funnel merged it directly (WAVE M4).
	//
	// It is a SEPARATE value from WriteStateAlreadyMerged, and that
	// distinction is not bookkeeping: `a2a submit` prints "already
	// submitted" for already-merged, which would be a plain lie about a
	// write this call had just performed for the first time. Every consumer
	// treats it like the already-* states for the purpose that matters —
	// there is nothing pending and no marker to track — but the sentence
	// the user reads is true. (M4 first shipped this as a reuse of
	// already-merged, because the consumers switching on WriteState were off
	// that wave's allowlist; that was an allowlist artifact, not a design
	// conclusion, and the lead split it.)
	WriteStateMerged WriteState = "merged"
)

// FileWrite is one file the write funnel commits — a path (relative to
// the mirror clone's working directory) and its full content.
type FileWrite struct {
	Path    string
	Content []byte
}

// SubmitRequest is one write-funnel invocation (§4.2 D-002, §7): commit
// Files as ONE commit (an artifact file + its first lifecycle event, per
// D-026) on the deterministic branch a2a/<System>/<ArtifactID>, push, open
// a PR with auto-merge enabled.
type SubmitRequest struct {
	// RepoDir is the local mirror clone's working directory (already
	// cloned/fetched via CloneOrFetch) that the commit is made in.
	RepoDir string
	// System is the authoring system (branch name + section guard).
	System string
	// Verb names the WRITE this request is — "submit", "ack", "accept",
	// "contract-publish", … It is part of the branch name, which is what
	// makes two different transitions on one artifact two different
	// branches (see BranchName). Required: a write that cannot say which
	// write it is cannot be told apart from the previous one.
	Verb string
	// ArtifactID is the artifact's §3.3 id (branch name suffix).
	ArtifactID string
	// Files are committed together, exactly once. Every Path must be
	// under System's own section (or decisions/, the funnel-level
	// exception) — checked BEFORE any git action.
	Files []FileWrite
	// CommitMessage, CommitAuthorName/Email: P6 supplies these (the exact
	// "a2a(<type>): <id>" convention, OP-205, is a CLI-layer concern).
	CommitMessage     string
	CommitAuthorName  string
	CommitAuthorEmail string

	// RemoteURL is the push target (real GitHub URL, or a local fixture
	// path in tests).
	RemoteURL string
	// Repo identifies the GitHub repo for the OpenPR/FindPRByHeadBranch
	// calls (owner/name) — distinct from RemoteURL, which is what `git
	// push` uses.
	Repo host.Repo
	// BaseBranch is the PR's target branch (normatively "main", §4.2).
	BaseBranch string
	// PRTitle/PRBody are passed through to host.OpenPR verbatim.
	PRTitle string
	PRBody  string

	Credential host.Credential

	// AllowForkFallback opts THIS write into the P28 fork path: if the
	// push is refused because the credential may not write to Repo, the
	// funnel ensures the submitter's own fork, pushes there, and opens a
	// cross-fork PR. Off by default — a space write is authored by a
	// system that owns its section and is expected to have write access,
	// so a refusal there is a credential fault to report, not to route
	// around. `a2a feedback submit` is the one caller that sets it: its
	// audience is precisely the non-collaborator.
	AllowForkFallback bool

	// MinBinaryVersion is space.yaml's pin for the CC-085 guard (caller
	// already parsed the manifest; the funnel does not parse YAML).
	MinBinaryVersion string

	// AllowSpaceInfrastructure opts THIS write into admitting the fixed set
	// of space-infrastructure paths ("space.yaml", "CODEOWNERS",
	// "BRANCH-PROTECTION.md", anything under ".github/") past the section
	// guard, IN ADDITION TO the caller's own section. Off by default — an
	// ordinary system write has no business touching the space's shared
	// infrastructure, and admitting it unconditionally would let any writer
	// edit CODEOWNERS or the CI workflow through the funnel meant to enforce
	// the single-writer boundary. V3 diff-authz (internal/cli/cmd_validate_ci.go)
	// already carves these exact paths out of per-author section review and
	// treats them as governed instead by CODEOWNERS review + branch
	// protection — so this flag lets the two guards agree on what
	// "space infrastructure" means rather than silently disagreeing. The one
	// caller that sets it is spec 35's `a2a space update` migration path.
	AllowSpaceInfrastructure bool
}

// WriteResult is what Submit returns: the contract P7's cache persistence
// (pending-merge marker) and P8's gated verbs are built against (spec 05
// §7, §9 — "keep that return contract stable across phases").
type WriteResult struct {
	Branch    string
	PRNumber  int
	PRURL     string
	CommitSHA string
	State     WriteState
	// AutoMergeNote is non-empty when the PR opened but auto-merge could NOT
	// be armed — the repository forbids it, or the PR is already mergeable.
	// The write succeeded either way, so this is not an error; it is the one
	// thing the caller must be told, because State says "pending-merge" and
	// nothing will act on that pending unless a human does.
	AutoMergeNote string
}

// SubmitValidator is the consumer-side seam (rails ISP/DI) for V2
// validation of the artifact+event pair before it enters the write funnel
// (P3 internal/validate, wired for real at P6). internal/space depends on
// this interface only — never a concrete validate.Engine (ADR-001's
// import grant is a ceiling, not a mandate; plan 05 Placement decisions).
type SubmitValidator interface {
	// ValidateSubmit validates files about to be committed and returns a
	// non-nil error describing every violation found (or nil).
	ValidateSubmit(ctx context.Context, files []FileWrite) error
}

// WriteFunnel implements the D-002/D-026 single write funnel: the ONLY
// code path internal/space exposes for mutating a space (rails: "one
// write shape"). It is the sole caller of internal/host.
type WriteFunnel struct {
	host      host.Host
	validator SubmitValidator
	// binaryVersion is injected via constructor DI (plan 05 Placement
	// decision: "the version stamp lives in cmd/a2a; space never reads
	// build info itself") — used only for the CC-085 guard.
	binaryVersion string
}

// NewWriteFunnel constructs a WriteFunnel. h and validator are required
// (a nil dependency used at runtime is a constructor bug, rails
// anti-pattern #10) — callers wire fakes in tests, the real engines at
// cmd/a2a (P6).
func NewWriteFunnel(h host.Host, validator SubmitValidator, binaryVersion string) *WriteFunnel {
	return &WriteFunnel{host: h, validator: validator, binaryVersion: binaryVersion}
}

// Submit runs the write funnel end to end (spec 05 §7):
//
//	(0) FindPRByHeadBranch short-circuit for a2a/<system>/<id> — an
//	    existing open/merged PR returns immediately, no second
//	    push/open cycle (AC-301.1 idempotency).
//	(1) section guard (wrong-section files refused before any git
//	    action) + the min_binary_version guard (CC-085) + the
//	    SubmitValidator seam (V2).
//	(2) ONE commit = every req.Files entry.
//	(3) host.PushBranch to a2a/<system>/<id>.
//	(4) host.OpenPR with auto-merge enabled (uniform, D-002).
//	(5) return the write-result.
func (f *WriteFunnel) Submit(ctx context.Context, req SubmitRequest) (WriteResult, error) {
	const op = "Submit"
	if req.Verb == "" {
		return WriteResult{}, &Error{Op: op, Input: req.ArtifactID, Err: ErrMissingVerb}
	}
	branch := BranchName(req.System, req.Verb, req.ArtifactID)

	// Step 0: idempotent-retry short-circuit — before ANY other check or
	// git action (spec 05 §7 idempotency note). Only an OPEN PR
	// short-circuits: it is the interrupted run's own PR, still in flight.
	// A MERGED one is a write that already completed, and the next write on
	// the same branch is a NEW transition, not a repeat — treating it as a
	// repeat is how `ack` then `accept` used to lose the accept.
	existing, err := f.host.FindPRByHeadBranch(ctx, host.FindPRRequest{
		Repo: req.Repo, Branch: branch, Credential: req.Credential,
	})
	if err != nil {
		return WriteResult{}, &Error{Op: op, Input: branch, Err: err}
	}
	if existing != nil && existing.State != "merged" {
		// Re-arm auto-merge before reporting success. OpenPR is NOT atomic
		// on GitHub — creating the PR and arming auto-merge are two calls —
		// so the interrupted run this short-circuit exists for may have
		// created a PR and died before arming it. Reporting "already
		// submitted" over a PR that will never merge on its own is exactly
		// the silent stall this path is meant to recover from.
		//
		// Observed live 2026-07-24 (P36's matrix): a 504 on OpenPR left a PR
		// open with a green required check and `auto_merge: null`, and every
		// retry answered "already-open". Arming is idempotent, so the common
		// case — the PR was fully configured — costs one no-op call.
		if am, isAutoMerger := f.host.(host.AutoMerger); isAutoMerger {
			err := am.EnableAutoMerge(ctx, host.EnableAutoMergeRequest{
				Repo: req.Repo, PRNumber: existing.Number, Credential: req.Credential,
			})
			// "Already clean" is GitHub declining because the PR can be
			// merged right now — nothing to wait for. That is not a failed
			// repair; it means the write must LAND the PR itself instead
			// (WAVE M4), guarded by tryLandCleanPR's explicit-green check —
			// never merge, either way, must not fail the retry.
			if err != nil {
				if !host.IsAutoMergeAlreadyClean(err) {
					return WriteResult{}, &Error{Op: op, Input: branch, Err: err}
				}
				merged, merr := f.tryLandCleanPR(ctx, req, existing.Number)
				if merr != nil {
					return WriteResult{}, &Error{Op: op, Input: branch, Err: merr}
				}
				if merged {
					existing.State = "merged"
				}
			}
		}
		return existingPRResult(branch, existing), nil
	}

	// Step 1a: section guard — wrong-section files refused before any
	// git action (shared refusal path, AC-201.3 precondition). A file passes
	// either by being inside the authoring system's own section (or the
	// decisions/ exception), or — only when the caller opted in — by being
	// one of the fixed space-infrastructure paths.
	for _, file := range req.Files {
		if !sectionOK(req.System, file.Path) &&
			(!req.AllowSpaceInfrastructure || !spaceInfraOK(file.Path)) {
			return WriteResult{}, &Error{Op: op, Input: file.Path, Err: ErrWrongSection}
		}
	}

	// Step 1b: CC-085 min_binary_version guard — refuse write, stay
	// read-only. Fails CLOSED on an unparseable version (versionOlderThan
	// itself already fails closed).
	if req.MinBinaryVersion != "" {
		older, err := versionOlderThan(f.binaryVersion, req.MinBinaryVersion)
		if err != nil {
			return WriteResult{}, &Error{Op: op, Err: err}
		}
		if older {
			return WriteResult{}, &Error{
				Op: op,
				Input: fmt.Sprintf("local binary %s < space.yaml min_binary_version %s — run 'a2a update'",
					f.binaryVersion, req.MinBinaryVersion),
				Err: ErrStaleBinaryVersion,
			}
		}
	}

	// Step 1c: V2 validation via the submit-validator seam.
	if f.validator != nil {
		if err := f.validator.ValidateSubmit(ctx, req.Files); err != nil {
			return WriteResult{}, &Error{Op: op, Err: err}
		}
	}

	// Step 2: assemble ONE commit = every req.Files entry (D-026).
	sha, fresh, err := f.commitOne(ctx, req, branch)
	if err != nil {
		return WriteResult{}, &Error{Op: op, Err: err}
	}
	if !fresh {
		// Nothing to commit: the branch already carries exactly this
		// content. Either it is already IN the space (a genuine repeat of
		// the same write — nothing left to do), or a previous attempt made
		// the commit but never got it merged, in which case the push/PR
		// steps still have work.
		onBase, berr := f.commitIsOnBase(ctx, req, sha)
		if berr != nil {
			return WriteResult{}, &Error{Op: op, Err: berr}
		}
		if onBase {
			return WriteResult{Branch: branch, CommitSHA: sha, State: WriteStateAlreadyMerged}, nil
		}
	}

	// Step 3: push the ephemeral branch — into req.Repo itself, or (when
	// the caller allowed it and the push was refused for ACCESS) into the
	// submitter's own fork.
	head := branch
	if err := f.push(ctx, req, branch, req.RemoteURL); err != nil {
		if !req.AllowForkFallback || !errors.Is(err, host.ErrPushForbidden) {
			return WriteResult{}, &Error{Op: op, Input: branch, Err: err}
		}
		forkHead, forked, ferr := f.pushViaFork(ctx, req, branch)
		if ferr != nil {
			return WriteResult{}, ferr
		}
		if forked != nil {
			// The fork already carries this branch's PR — the fork-head
			// idempotency check step 0 could not make, because the fork's
			// owner was unknown until now.
			return existingPRResult(branch, forked), nil
		}
		head = forkHead
	}

	// Step 4: open the PR — UNIFORM, auto-merge always (D-002; spec 05
	// §T1 "Gating needs no OpenPR parameter"). Head is owner-qualified on
	// the fork path; the PR still targets req.Repo's base branch.
	pr, err := f.host.OpenPR(ctx, host.OpenPRRequest{
		Repo: req.Repo, Head: head, Base: req.BaseBranch,
		Title: req.PRTitle, Body: req.PRBody, Credential: req.Credential,
	})
	if err != nil {
		return WriteResult{}, &Error{Op: op, Input: branch, Err: err}
	}

	// Step 4b (WAVE M4): OpenPR's own auto-merge arming can be refused
	// because the PR is ALREADY mergeable — nothing to wait for. That is
	// not a failure (armed stays false, a note is carried), but reporting
	// "pending-merge" over it and stopping there is exactly the defect this
	// wave closes: nothing else will ever merge that PR. Try to land it
	// directly, gated by tryLandCleanPR's explicit-green guard.
	state := WriteStatePendingMerge
	note := pr.AutoMergeNote
	if !pr.AutoMergeArmed && host.AutoMergeNoteIsAlreadyClean(pr.AutoMergeNote) {
		merged, merr := f.tryLandCleanPR(ctx, req, pr.Number)
		if merr != nil {
			return WriteResult{}, &Error{Op: op, Input: branch, Err: merr}
		}
		if merged {
			state, note = WriteStateMerged, ""
		}
	}

	// Step 5: return the write-result (cache persistence is P7's, not
	// this phase's — spec 05 §7).
	return WriteResult{
		Branch: branch, PRNumber: pr.Number, PRURL: pr.URL,
		CommitSHA: sha, State: state,
		// Carried, not swallowed: "pending-merge" over a PR nobody will
		// merge is the one outcome the caller must not read as done.
		AutoMergeNote: note,
	}, nil
}

// tryLandCleanPR attempts to merge a PR GitHub has already told us is
// mergeable right now (the "clean status" auto-merge refusal — see
// host.Merger's doc). Both places that arm auto-merge — the fresh-OpenPR
// path above and the idempotent-retry repair path — route through this ONE
// function so they cannot drift (WAVE M4 / AC-1050.12).
//
// merged=true, err=nil: MergePR succeeded — the PR is now merged.
// merged=false, err=nil: the guard was not satisfied, OR the host implements
//
//	no Merger — today's pending-merge behaviour is unchanged, note preserved.
//
// err != nil: MergePR (or the CheckStatus read) itself failed — the caller
//
//	must surface it, never swallow a real failure as "not merged".
//
// The guard is AC-1050.13's entire safety argument, and is deliberately
// strict: merge ONLY when CheckStatus reports the required context PRESENT
// and EXPLICITLY green — State == "completed" AND Conclusion == "success"
// AND no ambiguity. Every other shape falls through to false:
//   - never reported at all: CheckStatus's own "no check matched" answer is
//     {State: "queued", Conclusion: "", Name: ""} (github.go's CheckStatus,
//     the not-ok branch of selectRequiredCheckRun) — State != "completed",
//     so it is indistinguishable here from "still running", which is
//     exactly the point: this function does not need a separate "absent"
//     case because it never treats anything but a completed success as
//     green in the first place.
//   - pending: State == "in_progress" or "queued" — same, not "completed".
//   - ambiguous: len(Ambiguous) > 0 — P34's own signal that the pick was
//     not unique; a merge decided on an uncertain pick is not "explicitly"
//     anything.
//   - failing: Conclusion != "success" once State == "completed".
//
// a2a must never merge something CI has not passed; this is the one place
// that decision is made, and it is made by requiring a positive answer, not
// by excluding known-bad ones.
func (f *WriteFunnel) tryLandCleanPR(ctx context.Context, req SubmitRequest, prNumber int) (merged bool, err error) {
	merger, ok := f.host.(host.Merger)
	if !ok {
		return false, nil
	}
	// Both failures below are returned, not swallowed — an unevaluable or
	// failed landing must never be reported as a completed write, which is
	// this whole wave's point. But the message has to say WHAT ALREADY
	// HAPPENED: the branch is pushed and the PR is open by the time this runs,
	// so a bare wrapped transport error reads to an agent as "the write
	// failed" and invites it to give up rather than re-run. Naming the PR and
	// the safe next move is the difference between an actionable error and a
	// confusing one; re-running is safe because the funnel is idempotent by
	// head branch (verified live during the 2026-07-24 GitHub outage: the
	// re-run produced no duplicate).
	status, err := f.host.CheckStatus(ctx, host.StatusRequest{
		Repo: req.Repo, PRNumber: prNumber, Credential: req.Credential,
	})
	if err != nil {
		return false, fmt.Errorf("the write landed and PR #%d is open, but its required check could not be read, "+
			"so a2a did not merge it — re-running is safe and will retry the merge: %w", prNumber, err)
	}
	if !checkStatusExplicitlyGreen(status) {
		return false, nil
	}
	if err := merger.MergePR(ctx, host.MergePRRequest{
		Repo: req.Repo, PRNumber: prNumber, Credential: req.Credential,
	}); err != nil {
		return false, fmt.Errorf("the write landed and PR #%d is green, but merging it failed, "+
			"so the artifact is not on the base branch yet — re-running is safe and will retry: %w", prNumber, err)
	}
	return true, nil
}

// checkStatusExplicitlyGreen is AC-1050.13's guard condition in one place:
// present AND explicitly successful, nothing looser. See tryLandCleanPR's
// doc for why each of absent/pending/ambiguous/failing falls through false.
func checkStatusExplicitlyGreen(s host.CheckStatusResult) bool {
	return s.State == "completed" && s.Conclusion == "success" && len(s.Ambiguous) == 0
}

// BranchName renders the funnel's deterministic branch:
// a2a/<system>/<verb>/<artifact-id>.
//
// The VERB segment is load-bearing. Keyed on the artifact alone, the branch
// identified WHAT was written about rather than WHICH write it was, so once
// any write by a system on an artifact had merged, every later write by
// that system on that artifact matched a merged PR and short-circuited —
// `ack` then `accept` lost the accept, and a contract's publish/deprecate/
// retire all collapsed into its submit. Silently, with exit 0.
func BranchName(system, verb, artifactID string) string {
	return fmt.Sprintf("a2a/%s/%s/%s", system, verb, artifactID)
}

// commitIsOnBase reports whether sha is already contained in the base
// branch as the mirror last fetched it — i.e. this write's content is
// already in the space. An unresolvable base ref answers "no": writing a
// duplicate is recoverable, silently dropping a write is not.
func (f *WriteFunnel) commitIsOnBase(ctx context.Context, req SubmitRequest, sha string) (bool, error) {
	base := req.BaseBranch
	if base == "" {
		base = "main"
	}
	if err := runGit(ctx, req.RepoDir, "rev-parse", "--verify", "origin/"+base); err != nil {
		return false, nil //nolint:nilerr // reason: an unknown base is "not on base", not a failure
	}
	err := runGit(ctx, req.RepoDir, "merge-base", "--is-ancestor", sha, "origin/"+base)
	return err == nil, nil
}

// existingPRResult renders the idempotent short-circuit's result for a PR
// the host already has open or merged for this branch.
func existingPRResult(branch string, pr *host.PRInfo) WriteResult {
	state := WriteStateAlreadyOpen
	if pr.State == "merged" {
		state = WriteStateAlreadyMerged
	}
	return WriteResult{Branch: branch, PRNumber: pr.Number, PRURL: pr.URL, State: state}
}

// push sends the committed branch to remoteURL.
func (f *WriteFunnel) push(ctx context.Context, req SubmitRequest, branch, remoteURL string) error {
	_, err := f.host.PushBranch(ctx, host.PushBranchRequest{
		RepoDir: req.RepoDir, LocalRef: branch, Branch: branch,
		RemoteURL: remoteURL, Credential: req.Credential,
	})
	return err
}

// pushViaFork is the P28 fallback, reached only when a push into req.Repo
// was refused for ACCESS and req.AllowForkFallback is set. It ensures the
// submitter's fork, re-checks idempotency against the now-known fork head
// (step 0 could not: it did not know the fork's owner), and pushes there.
//
// Returns either the owner-qualified head to open the PR from, or the PR
// that already exists for that head — never both.
func (f *WriteFunnel) pushViaFork(ctx context.Context, req SubmitRequest, branch string) (string, *host.PRInfo, error) {
	const op = "Submit"

	forker, ok := f.host.(host.Forker)
	if !ok {
		return "", nil, &Error{Op: op, Input: manualForkAdvice, Err: ErrForkFallbackUnavailable}
	}
	fork, err := forker.EnsureFork(ctx, host.EnsureForkRequest{Repo: req.Repo, Credential: req.Credential})
	if err != nil {
		return "", nil, &Error{Op: op, Input: manualForkAdvice, Err: fmt.Errorf("%w: %w", ErrForkFallbackUnavailable, err)}
	}

	existing, err := f.host.FindPRByHeadBranch(ctx, host.FindPRRequest{
		Repo: req.Repo, Branch: branch, HeadOwner: fork.Repo.Owner, Credential: req.Credential,
	})
	if err != nil {
		return "", nil, &Error{Op: op, Input: branch, Err: err}
	}
	if existing != nil {
		return "", existing, nil
	}

	if err := f.push(ctx, req, branch, fork.RemoteURL); err != nil {
		return "", nil, &Error{Op: op, Input: branch, Err: err}
	}
	return fork.Repo.Owner + ":" + branch, nil, nil
}

// manualForkAdvice is the one path that always works when the automatic
// fork flow cannot run: the operator forks and opens the PR by hand (the
// intake accepts fork PRs by design — feedback-intake.yml is
// pull_request_target).
const manualForkAdvice = "no write access and no automatic fork — fork the repo and open the pull request by hand"

// sectionOK reports whether path is inside system's own section, or under
// the space-level decisions/ exception (the one path the single-writer
// rule does not enforce per-system, §4.2 decision flow).
//
// The path must be a clean, relative, forward-slash space path: any
// absolute path, any `..` segment, or any non-canonical form (e.g.
// `axon/../other/evil.md`) is rejected outright — otherwise a crafted
// FileWrite.Path could collapse into a sibling system's section, or
// outside the repo entirely, while still passing the guard whose whole
// job is to enforce the single-writer boundary (D-002's single write funnel
// + section ownership — plan §4.2; NOT D-014, which is the unrelated
// data-never-instructions stance).
func sectionOK(system, path string) bool {
	if !isCleanRelativePath(path) {
		return false
	}
	if path == "decisions" || hasPathPrefix(path, "decisions/") {
		return true
	}
	return path == system || hasPathPrefix(path, system+"/")
}

// spaceInfraOK reports whether path is one of the fixed space-infrastructure
// paths — "space.yaml", "CODEOWNERS", "BRANCH-PROTECTION.md", or anything
// under ".github/" — mirroring what internal/cli/cmd_validate_ci.go already
// excludes from author-diff-authz (both guards must agree on this set,
// §"AllowSpaceInfrastructure" on SubmitRequest).
//
// Only reachable when the caller opted in via
// SubmitRequest.AllowSpaceInfrastructure; sectionOK's own admission (e.g.
// system "axon" writing "axon/CODEOWNERS", a file merely NAMED CODEOWNERS
// inside axon's own section) is unaffected and unrelated — this function
// checks the repo-root infrastructure files/dir ONLY, anchored (not
// substring-matched): "CODEOWNERS" matches, "axon/CODEOWNERS" does not;
// ".github/x" matches, ".githubfoo/x" does not.
//
// Shares isCleanRelativePath with sectionOK so a path admitted via this
// route is held to the exact same cleanliness bar as the section route —
// otherwise a crafted ".github/../axon/evil.md" could pass in claiming to
// be infrastructure while actually escaping into another system's section.
func spaceInfraOK(path string) bool { return IsInfrastructurePath(path) }

// IsInfrastructurePath is the EXPORTED form of the same predicate, and the
// single source of truth for "what counts as space infrastructure".
//
// It is exported because a caller that PLANS an infrastructure write (spec
// 35's `a2a space update`) has to know the exact set the funnel will admit.
// A second, hand-maintained copy on the planning side would drift, and the
// failure mode is nasty: the planner proposes a file, the funnel refuses it
// with ErrWrongSection, and the whole write fails on a path nobody asked
// for. One predicate, both sides.
func IsInfrastructurePath(path string) bool {
	if !isCleanRelativePath(path) {
		return false
	}
	switch path {
	case "space.yaml", "CODEOWNERS", "BRANCH-PROTECTION.md":
		return true
	}
	return hasPathPrefix(path, ".github/")
}

// isCleanRelativePath reports whether path is already in cleaned,
// non-escaping, relative form: not empty, not absolute, and identical to
// its own path.Clean result (which collapses `..`/`.`/double-slashes) with
// that cleaned form not itself escaping the root. Shared by every route
// through the section guard (sectionOK and spaceInfraOK) so no route can
// admit a path the other would have rejected as unclean.
func isCleanRelativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	cleaned := gopath.Clean(path)
	return cleaned == path && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func hasPathPrefix(path, prefix string) bool {
	return len(path) > len(prefix) && path[:len(prefix)] == prefix
}

// commitOne checks out branch (creating it from the current HEAD), writes
// every req.Files entry to disk under req.RepoDir, stages, and commits
// them as ONE commit — the D-026 shape. Returns the new commit SHA.
func (f *WriteFunnel) commitOne(ctx context.Context, req SubmitRequest, branch string) (sha string, fresh bool, err error) {
	if len(req.Files) == 0 {
		return "", false, fmt.Errorf("space: commitOne: no files to commit")
	}

	if err := runGit(ctx, req.RepoDir, "checkout", "-B", branch); err != nil {
		return "", false, err
	}

	paths := make([]string, 0, len(req.Files))
	for _, file := range req.Files {
		full := filepath.Join(req.RepoDir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", false, err
		}
		if err := os.WriteFile(full, file.Content, 0o644); err != nil {
			return "", false, err
		}
		paths = append(paths, file.Path)
	}

	addArgs := append([]string{"add"}, paths...)
	if err := runGit(ctx, req.RepoDir, addArgs...); err != nil {
		return "", false, err
	}

	// Nothing staged means the branch already carries exactly this content
	// — a re-run over the same mirror. git refuses an empty commit, so
	// report the branch tip and let the caller decide what it means: on the
	// base branch it is a write that already landed, off it a previous
	// attempt whose push or PR never completed.
	staged, err := runGitOutput(ctx, req.RepoDir, nil, "diff", "--cached", "--name-only")
	if err != nil {
		return "", false, err
	}
	if staged == "" {
		head, herr := runGitOutput(ctx, req.RepoDir, nil, "rev-parse", "HEAD")
		return head, false, herr
	}

	authorName := req.CommitAuthorName
	if authorName == "" {
		authorName = "a2a"
	}
	authorEmail := req.CommitAuthorEmail
	if authorEmail == "" {
		authorEmail = "a2a@a2ahub.invalid"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + authorName, "GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + authorName, "GIT_COMMITTER_EMAIL=" + authorEmail,
	}
	msg := req.CommitMessage
	if msg == "" {
		msg = "a2a: submit " + req.ArtifactID
	}
	if _, err := runGitOutput(ctx, req.RepoDir, env, "commit", "-m", msg); err != nil {
		return "", false, err
	}

	head, err := runGitOutput(ctx, req.RepoDir, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	return head, true, nil
}
