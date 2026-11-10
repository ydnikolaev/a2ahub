package host

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// trailerBuiltBy is the build note's token. The note is its own paragraph of
// the head commit's message, above any trailer paragraph.
const trailerBuiltBy = "A2A-Built-By"

// clientLandEmailDomain is the merge commit's address domain. The commit's
// identity is the token's login, carried as the author name; .invalid can never
// be mistaken for someone's real address.
const clientLandEmailDomain = "@client.a2a.invalid"

// maxPushStatusBytes bounds what git push --porcelain prints for the one ref a
// land pushes.
const maxPushStatusBytes = 4096

// LandGuardFunc refuses a client land before anything is built. checkoutDir
// holds the space checked out (detached) at tipSHA, the base tip the write would
// land on, with its full first-parent history. A non-nil error is returned to
// OpenPR's caller unchanged and nothing moves.
type LandGuardFunc func(ctx context.Context, checkoutDir, tipSHA string) error

// ClientLandHost is a Host for a space on GitHub that this binary judges. The
// land check runs here against the exact merge of the write and the base tip,
// and that merge commit is pushed to the base branch without force, so a write
// that lost a race is rejected by the server and judged again on the new tip.
// The pull request stays as the record of the write; GitHub marks it merged
// when the push reaches the base branch.
//
// GitHub's merge API is deliberately not used: it guards only the head SHA, so
// GitHub would build the merge on whatever the base is by then, and a concurrent
// write would land a combination nobody judged.
//
// *GitHubHost is a named field and not embedded. An embedded one would promote
// EnableAutoMerge, MergePR and EnsureFork, and a shadowing method still satisfies
// the optional interface, so the write funnel would try to arm auto-merge or
// merge through the API. Those three stay unimplemented on purpose: what is
// forwarded below is what this host offers, and adding to it is a decision.
type ClientLandHost struct {
	// gitOps supplies PushBranch, ReadRemoteBranch and the recovery readers.
	gitOps

	github    *GitHubHost
	remoteURL string
	landCheck LandCheckFunc
	guard     LandGuardFunc
	exec      gitExecFunc

	loginMu sync.Mutex
	// logins is keyed by a digest, so this host never retains a token.
	logins map[[sha256.Size]byte]string
}

var (
	_ Host                 = (*ClientLandHost)(nil)
	_ OpenPRLister         = (*ClientLandHost)(nil)
	_ RefStatusReader      = (*ClientLandHost)(nil)
	_ RemoteBranchReader   = (*ClientLandHost)(nil)
	_ RepoVisibilityReader = (*ClientLandHost)(nil)
)

// newClientLandHost builds the host for the space at remoteURL. host.Open is its
// only production caller, and it refuses a nil check before getting here.
func newClientLandHost(github *GitHubHost, remoteURL string, check LandCheckFunc, guard LandGuardFunc) *ClientLandHost {
	return &ClientLandHost{
		github:    github,
		remoteURL: remoteURL,
		landCheck: check,
		guard:     guard,
		exec:      runRecoveryGitInput,
		logins:    map[[sha256.Size]byte]string{},
	}
}

// Capabilities implements Host.Capabilities: a write needs a credential, GitHub
// reviews nothing, owners are the logins GitHub authenticates, the write lands
// inside OpenPR, and the repository is not on this machine.
func (h *ClientLandHost) Capabilities() Capabilities {
	return Capabilities{
		NeedsCredential:           true,
		ReviewsWrites:             false,
		OwnerIdentitySelfAsserted: false,
		LandsSynchronously:        true,
		OnThisMachine:             false,
	}
}

// CheckStatus implements Host.CheckStatus with GitHub's read.
func (h *ClientLandHost) CheckStatus(ctx context.Context, req StatusRequest) (CheckStatusResult, error) {
	return h.github.CheckStatus(ctx, req)
}

// RefCheckStatus implements RefStatusReader with GitHub's read.
func (h *ClientLandHost) RefCheckStatus(ctx context.Context, req RefStatusRequest) (CheckStatusResult, error) {
	return h.github.RefCheckStatus(ctx, req)
}

// ReviewStatus implements Host.ReviewStatus with GitHub's read.
func (h *ClientLandHost) ReviewStatus(ctx context.Context, req StatusRequest) (ReviewStatusResult, error) {
	return h.github.ReviewStatus(ctx, req)
}

// TokenScopes implements Host.TokenScopes with GitHub's read.
func (h *ClientLandHost) TokenScopes(ctx context.Context, cred Credential) ([]string, bool, error) {
	return h.github.TokenScopes(ctx, cred)
}

// FindPRByHeadBranch implements Host.FindPRByHeadBranch with GitHub's read. A PR
// this host landed may read as open for a few seconds after OpenPR returned.
func (h *ClientLandHost) FindPRByHeadBranch(ctx context.Context, req FindPRRequest) (*PRInfo, error) {
	return h.github.FindPRByHeadBranch(ctx, req)
}

// ListOpenPRs implements OpenPRLister with GitHub's read.
func (h *ClientLandHost) ListOpenPRs(ctx context.Context, req ListOpenPRsRequest) ([]OpenPRSummary, error) {
	return h.github.ListOpenPRs(ctx, req)
}

// AutoMergeAllowed forwards doctor's repository-settings read.
func (h *ClientLandHost) AutoMergeAllowed(ctx context.Context, req RepoSettingsRequest) (bool, error) {
	return h.github.AutoMergeAllowed(ctx, req)
}

// CodeownersErrors forwards doctor's repository-settings read.
func (h *ClientLandHost) CodeownersErrors(ctx context.Context, req RepoSettingsRequest) ([]CodeownersError, error) {
	return h.github.CodeownersErrors(ctx, req)
}

// RepoPermissions forwards doctor's repository-settings read.
func (h *ClientLandHost) RepoPermissions(ctx context.Context, req RepoSettingsRequest) (RepoPermissions, error) {
	return h.github.RepoPermissions(ctx, req)
}

// RepoVisibility implements RepoVisibilityReader with GitHub's read.
func (h *ClientLandHost) RepoVisibility(ctx context.Context, req RepoSettingsRequest) (RepositoryVisibility, error) {
	return h.github.RepoVisibility(ctx, req)
}

// OpenPR implements Host.OpenPR by landing the write from this machine. It
// returns a PRInfo in state "merged" without waiting: GitHub closes the pull
// request a few seconds later, and until then a read through GitHub may still
// say open, so the caller trusts this return value.
//
// Judging comes before the pull request exists, so a refused write leaves none.
// Once the pull request does exist, every later error returns its handle in
// state "open" alongside the error: the handle is durable evidence of the write.
func (h *ClientLandHost) OpenPR(ctx context.Context, req OpenPRRequest) (info PRInfo, err error) {
	const op = "OpenPR"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.Head == "" || req.Base == "" || req.Head == req.Base ||
		req.ExpectedHeadSHA == "" || !utf8.ValidString(req.Title) || !utf8.ValidString(req.Body) {
		return PRInfo{}, &Error{Op: op, Err: ErrInvalidRequest}
	}
	login, err := h.tokenLogin(ctx, op, req.Credential)
	if err != nil {
		return PRInfo{}, err
	}
	dir, err := os.MkdirTemp("", "a2a-client-land-")
	if err != nil {
		return PRInfo{}, &Error{Op: op, Input: req.Head, Err: fmt.Errorf("%w: create the land directory: %w", errLocalGit, err)}
	}
	defer func() {
		// reason: by now the write has landed or been refused, and that outcome
		// is the answer; a directory left in the temp root costs only disk.
		_ = os.RemoveAll(dir) // reason: a leftover temp directory cannot change the land's outcome
	}()

	land := &clientLand{host: h, req: req, login: login, dir: dir, repo: landRepo{exec: h.exec, gitDir: dir + "/.git"}}
	if err := land.prepare(ctx, op); err != nil {
		return PRInfo{}, err
	}
	for attempt := 1; attempt <= maxLandAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return land.opened(), recoveryGitError(op, "land the write", ctxErr)
		}
		landed, done, attemptErr := land.attempt(ctx, op)
		if attemptErr != nil {
			return land.opened(), attemptErr
		}
		if done {
			return landed, nil
		}
	}
	return land.opened(), &Error{Op: op, Input: req.Head, Err: fmt.Errorf(
		"%w: %s kept moving under the write for %d attempts; re-running resumes it", ErrLandContended, req.Base, maxLandAttempts)}
}

// clientLand is one OpenPR's work: a throwaway repository that survives its
// retries, so a retry fetches only what moved, and the pull request once found.
type clientLand struct {
	host  *ClientLandHost
	req   OpenPRRequest
	login string
	dir   string
	repo  landRepo
	pr    *PRInfo
}

// opened is the handle an error carries: the pull request when this call found
// or created one, and nothing before that.
func (l *clientLand) opened() PRInfo {
	if l.pr == nil {
		return PRInfo{}
	}
	return *l.pr
}

// prepare creates the throwaway repository and refuses a name git would not
// accept as a ref, which also keeps a fork-qualified owner:branch head out: this
// host pushes a merge of a branch in the space itself.
func (l *clientLand) prepare(ctx context.Context, op string) error {
	if res := l.host.exec(ctx, nil, 4096, "init", "--quiet", l.dir); res.err != nil || res.exitCode != 0 {
		return localGitError(op, "create the land repository", res)
	}
	for _, name := range []string{l.req.Head, l.req.Base} {
		res := l.host.exec(ctx, nil, 256, "check-ref-format", headRefPrefix+name)
		switch {
		case res.err == nil && res.exitCode == 0:
		case res.err == nil && res.exitCode == 1:
			return &Error{Op: op, Input: name, Err: fmt.Errorf("%w: not a valid ref name", ErrInvalidRequest)}
		default:
			return localGitError(op, "check the ref name", res)
		}
	}
	return nil
}

// network runs one git command that talks to the remote. The credential goes
// before the subcommand so it lasts for this invocation only (see GitAuthArgs).
func (l *clientLand) network(ctx context.Context, limit int, args ...string) recoveryGitResult {
	argv := append([]string{"-C", l.dir}, GitAuthArgs(l.req.Credential)...)
	argv = append(argv, "-c", "core.hooksPath=/dev/null")
	return l.host.exec(ctx, nil, limit, append(argv, args...)...)
}

// remoteTrackingRef is where the fetch stores a branch, the ref a clone of the
// space would hold it under.
func remoteTrackingRef(branch string) string { return "refs/remotes/origin/" + branch }

// fetch brings the base and the head into the throwaway repository, history
// included, and returns the commits they name.
func (l *clientLand) fetch(ctx context.Context, op string) (tip, head string, err error) {
	res := l.network(ctx, 4096, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--", l.host.remoteURL,
		"+"+headRefPrefix+l.req.Base+":"+remoteTrackingRef(l.req.Base),
		"+"+headRefPrefix+l.req.Head+":"+remoteTrackingRef(l.req.Head))
	if res.err != nil || res.exitCode != 0 {
		return "", "", recoveryGitError(op, "fetch the base and head branches", ctx.Err())
	}
	if tip, err = l.resolve(ctx, op, remoteTrackingRef(l.req.Base)); err != nil {
		return "", "", err
	}
	if head, err = l.resolve(ctx, op, remoteTrackingRef(l.req.Head)); err != nil {
		return "", "", err
	}
	return tip, head, nil
}

// resolve reads one fetched ref.
func (l *clientLand) resolve(ctx context.Context, op, ref string) (string, error) {
	res := l.repo.run(ctx, nil, 256, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if res.err != nil || res.exitCode != 0 {
		return "", localGitError(op, "read "+ref, res)
	}
	sha := strings.TrimSpace(string(res.stdout))
	if !validRecoveryGitOID(sha) {
		return "", localGitError(op, "malformed object id for "+ref, res)
	}
	return sha, nil
}

// checkout puts the throwaway work tree at exactly sha. One tree serves the
// guard, the check and every retry, so what an earlier reader wrote or modified
// is removed: the tree judged must be the tree of the commit pushed.
func (l *clientLand) checkout(ctx context.Context, op, sha string) error {
	for _, args := range [][]string{
		{"checkout", "--quiet", "--force", "--detach", sha},
		{"clean", "-ffdxq"},
	} {
		argv := append([]string{"-C", l.dir, "-c", "core.hooksPath=/dev/null"}, args...)
		if res := l.host.exec(ctx, nil, 4096, argv...); res.err != nil || res.exitCode != 0 {
			return localGitError(op, "prepare the work tree at "+sha, res)
		}
	}
	return nil
}

// alreadyOnBase reports whether head is in tip's history.
func (l *clientLand) alreadyOnBase(ctx context.Context, op, head, tip string) (bool, error) {
	res := l.repo.run(ctx, nil, 256, "merge-base", "--is-ancestor", head, tip)
	switch {
	case res.err == nil && res.exitCode == 0:
		return true, nil
	case res.err == nil && res.exitCode == 1:
		return false, nil
	default:
		return false, localGitError(op, "compare the head with the base", res)
	}
}

// attempt is one pass: fetch, guard, build, judge, ensure the pull request,
// push. done=false with no error means the base moved and the caller starts over
// against the new tip.
func (l *clientLand) attempt(ctx context.Context, op string) (info PRInfo, done bool, err error) {
	req := l.req
	tip, head, err := l.fetch(ctx, op)
	if err != nil {
		return PRInfo{}, false, err
	}
	if head != req.ExpectedHeadSHA {
		return PRInfo{}, false, &Error{Op: op, Input: req.Head, Err: ErrPRHeadMoved}
	}
	onBase, err := l.alreadyOnBase(ctx, op, head, tip)
	if err != nil {
		return PRInfo{}, false, err
	}
	if onBase {
		info, err = l.alreadyLanded(ctx, op, head)
		return info, err == nil, err
	}

	if l.host.guard != nil {
		if err := l.checkout(ctx, op, tip); err != nil {
			return PRInfo{}, false, err
		}
		if err := l.host.guard(ctx, l.dir, tip); err != nil {
			return PRInfo{}, false, err
		}
	}

	merge, headMessage, err := l.buildMerge(ctx, op, tip, head)
	if err != nil {
		return PRInfo{}, false, err
	}
	if err := l.judge(ctx, op, tip, merge, head, headMessage); err != nil {
		return PRInfo{}, false, err
	}
	if err := l.ensurePR(ctx, op, head); err != nil {
		return PRInfo{}, false, err
	}
	moved, err := l.push(ctx, op, merge, tip)
	if err != nil || moved {
		return PRInfo{}, false, err
	}

	info = *l.pr
	info.State, info.HeadSHA, info.MergeSHA, info.BaseBranch = "merged", head, merge, req.Base
	info.MergeMethod = MergeMethodMerge
	return info, true, nil
}

// alreadyLanded answers a re-run whose head is already on the base: an earlier
// call landed it and its answer was lost, or a concurrent identical call won.
// Nothing is judged, pushed or created. A read through GitHub may still say open
// for a few seconds; the head being on the base is what makes it merged.
func (l *clientLand) alreadyLanded(ctx context.Context, op, head string) (PRInfo, error) {
	req := l.req
	found, err := l.host.github.FindPRByHeadBranch(ctx, FindPRRequest{Repo: req.Repo, Branch: req.Head, Credential: req.Credential})
	if err != nil {
		return PRInfo{}, err
	}
	if found == nil || found.HeadSHA != head || found.BaseBranch != req.Base {
		return PRInfo{}, &Error{Op: op, Input: req.Head, Err: fmt.Errorf(
			"%w: the head is already on %s and no pull request records it", ErrInvalidRequest, req.Base)}
	}
	info := *found
	info.State, info.BaseBranch, info.MergeMethod = "merged", req.Base, MergeMethodMerge
	return info, nil
}

// buildMerge creates the merge commit of tip and head in the throwaway
// repository and returns it with the head commit's message. The identity is the
// token's login, which is also the author the land check is told.
func (l *clientLand) buildMerge(ctx context.Context, op, tip, head string) (merge, headMessage string, err error) {
	tree, conflict, err := l.repo.mergeTree(ctx, op, tip, head)
	if err != nil {
		return "", "", err
	}
	if conflict {
		return "", "", &Error{Op: op, Input: l.req.Head, Err: ErrLandConflict}
	}
	headMessage, err = l.repo.commitMessage(ctx, op, head)
	if err != nil {
		return "", "", err
	}
	merge, err = l.repo.commitMerge(ctx, op, tree, tip, head, clientLandMessage(l.req, headMessage),
		l.login, emailLocalPart(l.login)+clientLandEmailDomain)
	return merge, headMessage, err
}

// judge runs the land check over the work tree at the merge commit, in the
// shape LocalHost reports it.
func (l *clientLand) judge(ctx context.Context, op, tip, merge, head, headMessage string) error {
	if err := l.checkout(ctx, op, merge); err != nil {
		return err
	}
	verdict, err := l.host.landCheck(ctx, LandCandidate{
		CheckoutDir: l.dir, BaseBranch: l.req.Base, BaseSHA: tip, MergeSHA: merge, HeadSHA: head,
		HeadMessage: headMessage, Author: l.login,
	})
	if err != nil {
		return &Error{Op: op, Input: l.req.Head, Err: fmt.Errorf("land check could not judge the write: %w", err)}
	}
	if !verdict.OK {
		return &Error{Op: op, Input: l.req.Head, Err: landRefusedError(verdict)}
	}
	return nil
}

// ensurePR finds this head's open pull request or creates it, arming nothing. It
// looks once more after a failed create: a create GitHub answered with an error
// may still have opened the pull request, and so may an identical concurrent
// write.
func (l *clientLand) ensurePR(ctx context.Context, op, head string) error {
	if l.pr != nil {
		return nil
	}
	found, err := l.findOpenPR(ctx, head)
	if err != nil {
		return err
	}
	if found != nil {
		l.pr = found
		return nil
	}
	created, _, createErr := l.host.github.createPR(ctx, op, l.req, MergeMethodMerge)
	if createErr != nil {
		found, err := l.findOpenPR(ctx, head)
		if err != nil || found == nil {
			return createErr
		}
		l.pr = found
		return nil
	}
	l.pr = &created
	return nil
}

// findOpenPR is the open pull request for exactly this write, or nil. A merged
// one on a reused branch name is an earlier write, and an open one for another
// head or base is not this write's record.
func (l *clientLand) findOpenPR(ctx context.Context, head string) (*PRInfo, error) {
	req := l.req
	found, err := l.host.github.FindPRByHeadBranch(ctx, FindPRRequest{Repo: req.Repo, Branch: req.Head, Credential: req.Credential})
	if err != nil {
		return nil, err
	}
	if found == nil || found.State != "open" || found.HeadSHA != head || found.BaseBranch != req.Base {
		return nil, nil
	}
	return found, nil
}

// push moves the base to the merge commit, never by force. moved=true means the
// base was no longer tip when the push arrived. git's stderr stays out of every
// error, since it can carry the transient Basic header.
//
// A failure git did not attribute to the ref says nothing about whether the push
// took effect, so the remote is read once to find out.
func (l *clientLand) push(ctx context.Context, op, merge, tip string) (moved bool, err error) {
	baseRef := headRefPrefix + l.req.Base
	res := l.network(ctx, maxPushStatusBytes, "push", "--porcelain", "--", l.host.remoteURL, merge+":"+baseRef)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, recoveryGitError(op, "push the merge commit", ctxErr)
	}
	if res.err == nil && res.exitCode == 0 {
		return false, nil
	}
	status := ""
	if res.err == nil {
		status = pushRefStatus(string(res.stdout), baseRef)
	}
	if strings.Contains(status, "[rejected]") &&
		(strings.Contains(status, "fetch first") || strings.Contains(status, "non-fast-forward")) {
		return true, nil
	}

	remote, readErr := l.host.ReadRemoteBranch(ctx, RemoteBranchRequest{
		RepoDir: l.dir, RemoteURL: l.host.remoteURL, Branch: l.req.Base, Credential: l.req.Credential,
	})
	switch {
	case readErr == nil && remote.Exists && remote.SHA == merge:
		return false, nil
	case readErr == nil && remote.Exists && remote.SHA == tip:
		return false, l.pushRefused(op, status, string(res.stderr))
	case readErr == nil && remote.Exists && strings.Contains(status, "[remote rejected]"):
		return true, nil
	default:
		return false, &Error{Op: op, Input: l.req.Base, Err: fmt.Errorf(
			"%w: the push failed and the base branch could not be matched to it; re-running finds the write if it landed", ErrTransient)}
	}
}

// pushRefused is the error for a push the server refused while the base stayed
// where it was: no access, a protected branch, or a push that never arrived.
func (l *clientLand) pushRefused(op, status, stderr string) error {
	cause := fmt.Errorf("%w: the base branch did not move, so this push was refused or never arrived", ErrPushRejected)
	if pushForbidden(stderr) {
		cause = fmt.Errorf("%w: %w", ErrPushRejected, ErrPushForbidden)
	}
	if status != "" {
		cause = fmt.Errorf("%w (git reported %q)", cause, status)
	}
	return &Error{Op: op, Input: l.req.Base, Err: cause}
}

// pushRefStatus returns the summary git push --porcelain printed for a rejected
// ref, or "" when there is none. A rejected ref's line is
// "!\t<src>:<dst>\t<summary>"; an authentication or transport failure prints no
// ref line at all.
func pushRefStatus(stdout, ref string) string {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) == 3 && fields[0] == "!" && strings.HasSuffix(fields[1], ":"+ref) {
			return fields[2]
		}
	}
	return ""
}

// tokenLogin is the account the credential authenticates as, read from GitHub
// once per credential. A failed read is an error and never a guessed identity.
func (h *ClientLandHost) tokenLogin(ctx context.Context, op string, cred Credential) (string, error) {
	if cred.Token == "" {
		return "", &Error{Op: op, Err: fmt.Errorf("%w: a client land needs a credential", ErrInvalidRequest)}
	}
	key := sha256.Sum256([]byte(cred.Token))
	h.loginMu.Lock()
	login, cached := h.logins[key]
	h.loginMu.Unlock()
	if cached {
		return login, nil
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := h.github.restCall(ctx, op, http.MethodGet, "/user", cred, nil, &user); err != nil {
		return "", err
	}
	if user.Login == "" {
		return "", &Error{Op: op, Err: fmt.Errorf("%w: GET /user named no login", ErrRequestFailed)}
	}
	h.loginMu.Lock()
	h.logins[key] = user.Login
	h.loginMu.Unlock()
	return user.Login, nil
}

// clientLandMessage is the merge commit's message: a subject, the title, the
// head commit's build note, and its approval trailers as the final paragraph,
// so the merge commit reads the way the head commit does to the same parsers.
//
// The subject names no pull request number. The number does not exist until
// after the judgment, and writing it in then would change the commit that was
// judged.
func clientLandMessage(req OpenPRRequest, headMessage string) string {
	var b strings.Builder
	b.WriteString("Merge branch '" + req.Head + "' into " + req.Base + "\n")
	if req.Title != "" {
		b.WriteString("\n" + req.Title + "\n")
	}
	if note := buildNoteLine(headMessage); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	if lines := ApprovedGatesTrailers(headMessage); len(lines) > 0 {
		b.WriteString("\n" + strings.Join(lines, "\n") + "\n")
	}
	return b.String()
}

// buildNoteLine returns the head commit's build note, or "". The note is a
// paragraph of exactly one line, and the funnel writes it last or directly above
// the one trailer paragraph, so only those two paragraphs are read: a line in
// the message's body is never taken for it.
func buildNoteLine(message string) string {
	paragraphs := strings.Split(strings.TrimRight(message, "\n"), "\n\n")
	first := max(len(paragraphs)-2, 0)
	for i := len(paragraphs) - 1; i >= first; i-- {
		p := paragraphs[i]
		if strings.HasPrefix(p, trailerBuiltBy+": ") && !strings.Contains(p, "\n") {
			return p
		}
	}
	return ""
}
