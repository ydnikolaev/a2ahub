package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// defaultAPIBaseURL is GitHub's production REST/GraphQL API host. Tests
// override GitHubHost.BaseURL with an httptest server — no live network
// call ever happens in this package's unit tests.
const defaultAPIBaseURL = "https://api.github.com"

// maxResponseBytes bounds every GitHub API response body read (rails:
// "bounded reads everywhere").
const maxResponseBytes = 1 << 20 // 1 MiB

// GitHubHost is the v1 Host implementation. PushBranch shells out to the
// system `git` binary with explicit argv (D-019: core speaks plain git —
// pushing a branch has no GitHub-specific mechanics); OpenPR/CheckStatus/
// ReviewStatus/FindPRByHeadBranch call the GitHub REST (and, for enabling
// auto-merge, GraphQL — GitHub's auto-merge toggle has no REST endpoint)
// API over net/http.
type GitHubHost struct {
	// Client performs every HTTP call. Required; NewGitHubHost defaults it
	// to http.DefaultClient for ergonomics (not a hidden dependency —
	// there is no domain reason a nil client should be an error here, any
	// more than it would be for stdlib's own http helpers).
	Client *http.Client
	// BaseURL is the REST API root (default "https://api.github.com").
	// GraphQL requests go to BaseURL+"/graphql" so a single override
	// (httptest server URL) captures both surfaces in tests.
	BaseURL string
	// ForkReadyAttempts/ForkReadyDelay bound EnsureFork's readiness poll
	// (fork creation is asynchronous). Zero means the defaults below;
	// tests shrink them so no suite ever waits on a real clock.
	ForkReadyAttempts int
	ForkReadyDelay    time.Duration
	// RetryAttempts/RetryDelayFn bound httpDo's retry of a transient
	// failure. Zero/nil means MaxAttempts and RetryDelay (retry.go); tests
	// shrink them, the same seam and for the same reason as ForkReady*
	// above — a suite that waits on RetryDelay's real 14-second worst case
	// is a suite people stop running.
	RetryAttempts int
	RetryDelayFn  func(attempt int) time.Duration
}

// Fork-readiness poll defaults: ~10s of patience in one-second steps,
// which is generously above the fork latency GitHub actually exhibits and
// still bounded (rails: no unbounded wait).
const (
	defaultForkReadyAttempts = 10
	defaultForkReadyDelay    = time.Second
)

// NewGitHubHost constructs a GitHubHost. client may be nil (defaults to
// http.DefaultClient); baseURL may be "" (defaults to the real GitHub API).
func NewGitHubHost(client *http.Client, baseURL string) *GitHubHost {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	return &GitHubHost{Client: client, BaseURL: baseURL}
}

// PushBranch implements Host.PushBranch via `git push` with explicit argv
// (never sh -c). The credential, when present, is injected only as a
// per-invocation `-c http.extraheader=...` git config override — never
// written to any file, never part of the remote URL that git might echo
// back in an error message.
func (h *GitHubHost) PushBranch(ctx context.Context, req PushBranchRequest) (PushBranchResult, error) {
	const op = "PushBranch"
	if req.RepoDir == "" || req.LocalRef == "" || req.Branch == "" || req.RemoteURL == "" {
		return PushBranchResult{}, &Error{Op: op, Err: ErrInvalidRequest}
	}

	args := []string{"-C", req.RepoDir}
	if req.Credential.Token != "" {
		basicAuth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + req.Credential.Token))
		args = append(args, "-c", "http.extraheader=AUTHORIZATION: basic "+basicAuth)
	}
	args = append(args, "push")
	if req.ForceWithLeaseSHA != "" {
		args = append(args, "--force-with-lease=refs/heads/"+req.Branch+":"+req.ForceWithLeaseSHA)
	}
	args = append(args, req.RemoteURL, req.LocalRef+":refs/heads/"+req.Branch)

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if pushForbidden(detail) {
			return PushBranchResult{}, &Error{
				Op:    op,
				Input: req.Branch,
				Err:   fmt.Errorf("%w: %w: %s", ErrPushRejected, ErrPushForbidden, detail),
			}
		}
		return PushBranchResult{}, &Error{
			Op:    op,
			Input: req.Branch,
			Err:   fmt.Errorf("%w: %s", ErrPushRejected, detail),
		}
	}
	return PushBranchResult{Branch: req.Branch}, nil
}

// ReadRemoteBranch implements RemoteBranchReader through `git ls-remote`.
// It observes exactly one fully-qualified head and returns the SHA later
// supplied to --force-with-lease; a concurrent remote update therefore makes
// the subsequent push fail instead of being overwritten.
func (h *GitHubHost) ReadRemoteBranch(ctx context.Context, req RemoteBranchRequest) (RemoteBranchHead, error) {
	const op = "ReadRemoteBranch"
	if req.RepoDir == "" || req.RemoteURL == "" || req.Branch == "" {
		return RemoteBranchHead{}, &Error{Op: op, Err: ErrInvalidRequest}
	}
	args := []string{"-C", req.RepoDir}
	if req.Credential.Token != "" {
		basicAuth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + req.Credential.Token))
		args = append(args, "-c", "http.extraheader=AUTHORIZATION: basic "+basicAuth)
	}
	args = append(args, "ls-remote", "--heads", req.RemoteURL, "refs/heads/"+req.Branch)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return RemoteBranchHead{}, &Error{
			Op: op, Input: req.Branch,
			Err: fmt.Errorf("%w: %s", ErrPushRejected, strings.TrimSpace(stderr.String())),
		}
	}
	fields := strings.Fields(stdout.String())
	if len(fields) == 0 {
		return RemoteBranchHead{}, nil
	}
	if len(fields) != 2 || fields[1] != "refs/heads/"+req.Branch {
		return RemoteBranchHead{}, &Error{
			Op: op, Input: req.Branch,
			Err: fmt.Errorf("%w: malformed git ls-remote output", ErrRequestFailed),
		}
	}
	return RemoteBranchHead{SHA: fields[0], Exists: true}, nil
}

// pushForbiddenMarkers are the phrases git/GitHub emit when a push is
// refused for ACCESS rather than for ref state. git's stderr is the only
// signal a push carries — there is no exit code that distinguishes the two
// — so the vocabulary is pinned here, next to the command that produces
// it, and tested against the real strings GitHub sends.
var pushForbiddenMarkers = []string{
	"denied to",                              // "remote: Permission to o/r.git denied to <login>"
	"permission denied",                      // ssh remotes
	"write access to repository not granted", // fine-grained PAT without contents:write
	"403",                                    // "The requested URL returned error: 403"
}

// pushForbidden reports whether stderr describes an ACCESS refusal.
// Deliberately conservative: an unrecognised refusal stays a plain
// ErrPushRejected, so the fork fallback never fires on a non-fast-forward
// or a protected-branch rejection (which forking would not fix).
func pushForbidden(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, marker := range pushForbiddenMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// EnsureFork implements the optional Forker capability: POST
// /repos/{owner}/{repo}/forks, which GitHub defines as idempotent (an
// existing fork is returned, not duplicated) and ASYNCHRONOUS — the repo
// record can lag the 202, so the fork is polled until the host reports it
// readable before any push is attempted.
func (h *GitHubHost) EnsureFork(ctx context.Context, req EnsureForkRequest) (ForkInfo, error) {
	const op = "EnsureFork"
	if req.Repo.Owner == "" || req.Repo.Name == "" {
		return ForkInfo{}, &Error{Op: op, Err: ErrInvalidRequest}
	}
	target := req.Repo.Owner + "/" + req.Repo.Name

	path := fmt.Sprintf("/repos/%s/%s/forks", req.Repo.Owner, req.Repo.Name)
	var forked repoPayload
	if err := h.restCall(ctx, op, http.MethodPost, path, req.Credential, nil, &forked); err != nil {
		return ForkInfo{}, &Error{Op: op, Input: target, Err: fmt.Errorf("%w: %w", ErrForkUnavailable, err)}
	}
	if forked.Owner.Login == "" || forked.Name == "" {
		return ForkInfo{}, &Error{Op: op, Input: target, Err: fmt.Errorf("%w: fork response named no repository", ErrForkUnavailable)}
	}

	fork := ForkInfo{
		Repo:      Repo{Owner: forked.Owner.Login, Name: forked.Name},
		RemoteURL: forked.CloneURL,
	}
	if fork.RemoteURL == "" {
		fork.RemoteURL = fmt.Sprintf("https://github.com/%s/%s.git", fork.Repo.Owner, fork.Repo.Name)
	}

	if err := h.waitForRepo(ctx, op, fork.Repo, req.Credential); err != nil {
		return ForkInfo{}, err
	}
	return fork, nil
}

// repoPayload is the subset of GitHub's repository object EnsureFork reads.
type repoPayload struct {
	Name     string `json:"name"`
	CloneURL string `json:"clone_url"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// waitForRepo polls GET /repos/{owner}/{name} until it answers, bounding
// both the attempt count and the wait so a fork that never materialises
// fails with an actionable error instead of hanging.
func (h *GitHubHost) waitForRepo(ctx context.Context, op string, repo Repo, cred Credential) error {
	attempts := h.ForkReadyAttempts
	if attempts <= 0 {
		attempts = defaultForkReadyAttempts
	}
	delay := h.ForkReadyDelay
	if delay <= 0 {
		delay = defaultForkReadyDelay
	}

	path := fmt.Sprintf("/repos/%s/%s", repo.Owner, repo.Name)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return &Error{Op: op, Input: repo.Owner + "/" + repo.Name, Err: fmt.Errorf("%w: %w", ErrForkUnavailable, ctx.Err())}
			case <-time.After(delay):
			}
		}
		lastErr = h.restCall(ctx, op, http.MethodGet, path, cred, nil, nil)
		if lastErr == nil {
			return nil
		}
	}
	return &Error{
		Op:    op,
		Input: repo.Owner + "/" + repo.Name,
		Err:   fmt.Errorf("%w: not readable after %d attempts: %w", ErrForkUnavailable, attempts, lastErr),
	}
}

// OpenPR implements Host.OpenPR: create the PR via REST, then enable
// auto-merge via the GraphQL mutation GitHub requires for that toggle
// (there is no REST field for it). Both calls target h.BaseURL so tests
// exercise the full sequence against one httptest server.
func (h *GitHubHost) OpenPR(ctx context.Context, req OpenPRRequest) (PRInfo, error) {
	const op = "OpenPR"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.Head == "" || req.Base == "" {
		return PRInfo{}, &Error{Op: op, Err: ErrInvalidRequest}
	}

	createBody := map[string]any{
		"title": req.Title,
		"head":  req.Head,
		"base":  req.Base,
		"body":  req.Body,
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls", req.Repo.Owner, req.Repo.Name)
	var created struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		NodeID  string `json:"node_id"`
		State   string `json:"state"`
	}
	if err := h.restCall(ctx, op, http.MethodPost, path, req.Credential, createBody, &created); err != nil {
		return PRInfo{}, err
	}

	// Auto-merge is UNIFORM (spec 05 §T1) — always requested, never
	// gated by a parameter here.
	//
	// NOTE this is a SECOND call: the PR already exists by the time it runs,
	// so a failure here leaves a real PR that will never merge on its own.
	// That is why AutoMerger exists and why the funnel re-arms on its
	// idempotent-retry path — see EnableAutoMerge.
	armed, note := true, ""
	if err := h.armAutoMerge(ctx, op, req.Credential, created.NodeID); err != nil {
		note = autoMergeRefusal(err)
		if note == "" {
			// An unrecognised failure is a surprise, not a known limitation:
			// surface it rather than shipping a PR nobody knows is stuck.
			return PRInfo{}, err
		}
		armed = false
	}

	state := created.State
	if state == "" {
		state = "open"
	}
	return PRInfo{
		Number: created.Number, URL: created.HTMLURL, State: state,
		AutoMergeArmed: armed, AutoMergeNote: note,
	}, nil
}

// requiredCheckName is the space CI's gate job name — frozen by spec 33 §11
// on both sides of the P33 migration. An un-migrated space emits it flat; a
// migrated space's caller job of the same name emits the compound context
// `a2a-validate / <reusable-job>`.
const requiredCheckName = "a2a-validate"

// compoundSeparator is GitHub's rendering of a called workflow's run name:
// `<caller-job> / <reusable-job>`. Matching on the prefix INCLUDING this
// separator is what keeps `a2a-postmerge-audit / validate` — push-triggered,
// never the required check (spec 09 §5.5) — out of the candidate set, and
// what makes the match anchored rather than a substring search.
const compoundSeparator = " / "

// checkRun is one entry of the head SHA's check-runs listing.
type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	// StartedAt and ID break ties between runs sharing a name (see
	// selectRequiredCheckRun). StartedAt is RFC-3339 and therefore sorts
	// correctly as a plain string; ID is the stable fallback for the
	// same-second case, since GitHub's check-run ids increase over time.
	StartedAt string `json:"started_at"`
	ID        int64  `json:"id"`
	// HeadSHA is the commit this run executed against. Surfaced through
	// CheckStatusResult because a conclusion read without it can be a stale
	// merge commit's verdict — see CheckStatusResult.HeadSHA.
	HeadSHA string `json:"head_sha"`
}

// selectRequiredCheckRun picks the V3 required check run out of a head SHA's
// runs (spec 34 §2): compound `a2a-validate / …` wins over flat
// `a2a-validate` — a space mid-migration can briefly emit both, and the
// compound one is the shape branch protection now requires. Zero candidates →
// ok=false, which the caller renders as the pre-existing "no check" result.
//
// Ordering is MOST RECENT FIRST (StartedAt desc, then ID desc), with the name
// only grouping equal-recency runs. This corrects an assumption an earlier
// version of this function documented and relied on — that `filter=latest`
// returns one run per name, so candidate names are distinct and sorting by
// name is total.
//
// That is false, and routinely so. Verified against real GitHub on
// 2026-07-24 (P36's live tier): after a PR's check was re-triggered,
// `/commits/{sha}/check-runs?filter=latest` returned TWO completed runs both
// named `a2a-validate / validate` for one head SHA — `filter=latest` is
// per-CHECK-SUITE, and a re-trigger creates another suite. Sorting by name
// alone then compares equal, `sort.Slice` is not stable, and the pick between
// the stale run and the current one is arbitrary. A caller would read the OLD
// conclusion — reporting failure on a PR that has since gone green, or the
// reverse. That is precisely the defect class spec 34 exists to close, one
// layer deeper, and no fake produces it: a fake never re-triggers.
//
// Ambiguity is still REPORTED (never a silent pick) whenever more than one
// compound candidate exists, so a caller can see that the head SHA carried
// several — but the pick itself is now the meaningful one rather than a
// coin flip.
func selectRequiredCheckRun(runs []checkRun) (run checkRun, ambiguous []string, ok bool) {
	var compound, flat []checkRun
	for _, r := range runs {
		switch {
		case strings.HasPrefix(r.Name, requiredCheckName+compoundSeparator):
			compound = append(compound, r)
		case r.Name == requiredCheckName:
			flat = append(flat, r)
		}
	}

	candidates := compound
	if len(candidates) == 0 {
		candidates = flat
	}
	if len(candidates) == 0 {
		return checkRun{}, nil, false
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.StartedAt != b.StartedAt {
			// RFC-3339, so lexicographic IS chronological. Descending:
			// the newest run answers for the head SHA.
			return a.StartedAt > b.StartedAt
		}
		if a.ID != b.ID {
			return a.ID > b.ID
		}
		return a.Name < b.Name
	})
	if len(compound) > 1 {
		ambiguous = make([]string, 0, len(compound))
		for _, r := range compound {
			ambiguous = append(ambiguous, r.Name)
		}
	}
	return candidates[0], ambiguous, true
}

// CheckStatus implements Host.CheckStatus: reads the PR's head SHA, then the
// required check run's state/conclusion.
//
// The listing is NOT filtered by `check_name=` server-side: that filter is an
// exact match, and P33 turned a migrated space's run name into the compound
// `a2a-validate / <reusable-job>`, which such a filter misses entirely
// (reporting "no check" instead of the real conclusion). Selection therefore
// happens here, over both shapes — see selectRequiredCheckRun.
func (h *GitHubHost) CheckStatus(ctx context.Context, req StatusRequest) (CheckStatusResult, error) {
	const op = "CheckStatus"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.PRNumber == 0 {
		return CheckStatusResult{}, &Error{Op: op, Err: ErrInvalidRequest}
	}

	headSHA, err := h.prHeadSHA(ctx, op, req)
	if err != nil {
		return CheckStatusResult{}, err
	}

	// filter=latest is GitHub's own default (one run per name, the most
	// recent) — stated explicitly because the selection below depends on it.
	// The name filter cannot be used (compound P33 names), so page through the
	// widened result instead of silently treating a page-2 required check as
	// absent. The bound prevents a hostile/misbehaving endpoint looping forever.
	const (
		checkRunsPerPage = 100
		maxCheckRunPages = 100
	)
	var runs []checkRun
	for page := 1; page <= maxCheckRunPages; page++ {
		path := fmt.Sprintf(
			"/repos/%s/%s/commits/%s/check-runs?filter=latest&per_page=%d&page=%d",
			req.Repo.Owner, req.Repo.Name, headSHA, checkRunsPerPage, page,
		)
		var resp struct {
			CheckRuns []checkRun `json:"check_runs"`
		}
		if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &resp); err != nil {
			return CheckStatusResult{}, err
		}
		runs = append(runs, resp.CheckRuns...)
		if len(resp.CheckRuns) < checkRunsPerPage {
			break
		}
		if page == maxCheckRunPages {
			return CheckStatusResult{}, &Error{
				Op: op, Input: headSHA,
				Err: fmt.Errorf("%w: check-runs listing exceeded %d pages", ErrRequestFailed, maxCheckRunPages),
			}
		}
	}
	run, ambiguous, ok := selectRequiredCheckRun(runs)
	if !ok {
		return CheckStatusResult{State: "queued"}, nil
	}
	return CheckStatusResult{State: run.Status, Conclusion: run.Conclusion, Name: run.Name, Ambiguous: ambiguous, HeadSHA: run.HeadSHA}, nil
}

// ReviewStatus implements Host.ReviewStatus: reads the PR's reviews and
// folds them to each reviewer's LATEST state.
func (h *GitHubHost) ReviewStatus(ctx context.Context, req StatusRequest) (ReviewStatusResult, error) {
	const op = "ReviewStatus"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.PRNumber == 0 {
		return ReviewStatusResult{}, &Error{Op: op, Err: ErrInvalidRequest}
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", req.Repo.Owner, req.Repo.Name, req.PRNumber)
	var reviews []struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &reviews); err != nil {
		return ReviewStatusResult{}, err
	}

	latest := make(map[string]string, len(reviews))
	order := make([]string, 0, len(reviews))
	for _, r := range reviews {
		if _, seen := latest[r.User.Login]; !seen {
			order = append(order, r.User.Login)
		}
		latest[r.User.Login] = r.State // reviews arrive oldest-first; last write wins
	}

	if len(order) == 0 {
		return ReviewStatusResult{Approved: false}, nil
	}
	var pending []string
	for _, login := range order {
		if latest[login] != "APPROVED" {
			pending = append(pending, login)
		}
	}
	return ReviewStatusResult{Approved: len(pending) == 0, Pending: pending}, nil
}

// FindPRByHeadBranch implements Host.FindPRByHeadBranch: lists PRs whose
// head matches owner:branch across all states, returning the first
// open-or-merged match (nil, nil if none).
func (h *GitHubHost) FindPRByHeadBranch(ctx context.Context, req FindPRRequest) (*PRInfo, error) {
	const op = "FindPRByHeadBranch"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.Branch == "" {
		return nil, &Error{Op: op, Err: ErrInvalidRequest}
	}

	headOwner := req.HeadOwner
	if headOwner == "" {
		headOwner = req.Repo.Owner
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=all&head=%s:%s", req.Repo.Owner, req.Repo.Name, headOwner, req.Branch)
	var results []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"` // "open" | "closed"
		Merged  bool   `json:"merged"`
		Body    string `json:"body"`
	}
	if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &results); err != nil {
		return nil, err
	}
	for _, r := range results {
		switch {
		case r.State == "open":
			return &PRInfo{Number: r.Number, URL: r.HTMLURL, State: "open", Body: r.Body}, nil
		case r.Merged:
			return &PRInfo{Number: r.Number, URL: r.HTMLURL, State: "merged", Body: r.Body}, nil
		}
	}
	return nil, nil
}

// ListOpenPRs implements OpenPRLister with bounded pagination. GitHub's
// auto_merge field is null when no merge queue is armed and an object when it
// is; the diagnostic needs only that distinction.
func (h *GitHubHost) ListOpenPRs(ctx context.Context, req ListOpenPRsRequest) ([]OpenPRSummary, error) {
	const op = "ListOpenPRs"
	if req.Repo.Owner == "" || req.Repo.Name == "" {
		return nil, &Error{Op: op, Err: ErrInvalidRequest}
	}
	const (
		perPage  = 100
		maxPages = 100
	)
	var out []OpenPRSummary
	for page := 1; page <= maxPages; page++ {
		path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&per_page=%d&page=%d",
			req.Repo.Owner, req.Repo.Name, perPage, page)
		var results []struct {
			Number    int    `json:"number"`
			HTMLURL   string `json:"html_url"`
			AutoMerge any    `json:"auto_merge"`
		}
		if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &results); err != nil {
			return nil, err
		}
		for _, result := range results {
			out = append(out, OpenPRSummary{
				Number: result.Number, URL: result.HTMLURL, AutoMergeArmed: result.AutoMerge != nil,
			})
		}
		if len(results) < perPage {
			return out, nil
		}
	}
	return nil, &Error{Op: op, Err: fmt.Errorf("%w: open PR listing exceeded %d pages", ErrRequestFailed, maxPages)}
}

// prHeadSHA fetches a PR's head commit SHA (used by CheckStatus, which
// needs it to look up check-runs).
func (h *GitHubHost) prHeadSHA(ctx context.Context, op string, req StatusRequest) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", req.Repo.Owner, req.Repo.Name, req.PRNumber)
	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &pr); err != nil {
		return "", err
	}
	return pr.Head.SHA, nil
}

// restCall issues one REST call against h.BaseURL+path, authenticated with
// cred (Authorization: Bearer <token>, when set), and decodes a
// bounded-size JSON response into out (nil to discard the body).
func (h *GitHubHost) restCall(ctx context.Context, op, method, path string, cred Credential, body any, out any) error {
	return h.doJSON(ctx, op, method, h.BaseURL+path, cred, body, out)
}

// graphQLCall issues one GraphQL POST against h.BaseURL+"/graphql".
// graphQLCall posts a GraphQL request and — unlike a REST call — must inspect
// the RESPONSE BODY to learn whether it worked.
//
// GraphQL answers HTTP 200 for a failed operation and reports the failure in
// the body's `errors` array. Checking only the status therefore swallows every
// GraphQL failure silently. That is not theoretical: `a2a submit` arms
// auto-merge through this path, so a refused arming left the PR open forever
// while the command reported "opened PR … (pending-merge)". Verified against
// real GitHub on 2026-07-24 (P36's matrix) — the mutation returned
// `HTTP 200` with `errors: ["Pull request Pull request is in clean status"]`
// and `data: {enablePullRequestAutoMerge: null}`, and the caller saw success.
func (h *GitHubHost) graphQLCall(ctx context.Context, op string, cred Credential, body any, out any) error {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	if err := h.doJSON(ctx, op, http.MethodPost, h.BaseURL+"/graphql", cred, body, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			messages = append(messages, e.Message)
		}
		return &Error{Op: op, Err: fmt.Errorf("%w: %s", ErrGraphQLFailed, strings.Join(messages, "; "))}
	}
	if out == nil || len(envelope.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: decode graphql data: %w", ErrRequestFailed, err)}
	}
	return nil
}

// autoMergeCleanMarker is GitHub's refusal when auto-merge is requested on a
// pull request that is ALREADY mergeable: there is nothing to wait for, so it
// declines and expects a plain merge instead.
//
// Matched on the message because GraphQL gives no stable code for it. This is
// not a failure of the write — it means the PR is green and ready — so the
// funnel's repair path reports it as such rather than as an error.
const autoMergeCleanMarker = "clean status"

// AutoMergeAlreadyCleanNote is the exact PRInfo.AutoMergeNote text emitted
// for the "clean status" refusal — pinned here as an exported const, rather
// than only inline in autoMergeRefusal's switch, so AutoMergeNoteIsAlreadyClean
// can compare against the SAME string a caller actually received instead of
// re-deriving the classification from scratch, and so a caller building a
// test double (a fake OpenPR response) can reproduce this exact note without
// duplicating GitHub's own wording by hand.
const AutoMergeAlreadyCleanNote = "the PR is already mergeable, so GitHub declined to arm auto-merge — merge it"

// autoMergeDisabledMarker is GitHub's refusal when the REPOSITORY has
// auto-merge turned off (Settings -> Allow auto-merge). Verified against real
// GitHub on 2026-07-24: "Auto merge is not allowed for this repository".
//
// This is the reason that matters most in practice: `allow_auto_merge` is
// OFF by default on a new repository, and the live getvisa space runs with it
// off — so before GraphQL errors were surfaced at all, every `a2a submit`
// there quietly opened a PR that needed a manual merge while reporting
// "pending-merge".
const autoMergeDisabledMarker = "auto merge is not allowed"

// autoMergeRefusal classifies a failed arming attempt into a NON-FATAL note,
// or returns "" when the failure is a genuine surprise the caller must not
// paper over.
//
// The two known refusals both leave a real, open PR: the repository forbids
// auto-merge, or the PR is already mergeable so there is nothing to wait for.
// Failing the whole submit for either would be wrong — the write happened.
// Reporting nothing would be worse, and is precisely what swallowing the
// GraphQL error used to do.
func autoMergeRefusal(err error) string {
	if !errors.Is(err, ErrGraphQLFailed) {
		return ""
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, autoMergeDisabledMarker):
		return "auto-merge is disabled on this repository — merge the PR yourself, " +
			"or enable Settings -> General -> Allow auto-merge so a2a can arm it"
	case strings.Contains(lower, autoMergeCleanMarker):
		return AutoMergeAlreadyCleanNote
	}
	return ""
}

// IsAutoMergeAlreadyClean reports whether err is GitHub declining to arm
// auto-merge because the PR can be merged right now.
func IsAutoMergeAlreadyClean(err error) bool {
	return errors.Is(err, ErrGraphQLFailed) && strings.Contains(strings.ToLower(err.Error()), autoMergeCleanMarker)
}

// AutoMergeNoteIsAlreadyClean reports whether note — a PRInfo.AutoMergeNote
// value — is the "already mergeable" refusal specifically, as opposed to
// "auto-merge disabled on this repository" (the other known refusal
// autoMergeRefusal classifies). It exists because OpenPR's caller (the write
// funnel) only ever sees the rendered note, not the underlying GraphQL
// error IsAutoMergeAlreadyClean matches against — a direct-merge attempt is
// worth trying for the first case (nothing but arming failed) and pointless
// for the second (the repository forbids it outright, and MergePR would
// merely repeat that refusal at the REST layer).
func AutoMergeNoteIsAlreadyClean(note string) bool {
	return note == AutoMergeAlreadyCleanNote
}

// RepoSettingsRequest identifies the repository whose GitHub-side repo
// SETTINGS (as opposed to any one PR's state) are read. Today's sole reader
// is AutoMergeAllowed.
type RepoSettingsRequest struct {
	Repo       Repo
	Credential Credential
}

// AutoMergeAllowed reads GET /repos/{owner}/{repo} and reports the repo's
// `allow_auto_merge` setting — OFF by default on a freshly created GitHub
// repository, which is exactly what `a2a space init` produces (WAVE M2 / spec
// 45 §T1, AC-1050.5). This is a READ only: a2a mutates no repo setting and no
// branch protection (spec 35's boundary; spec 42 §T3's coupling note).
//
// The two outcomes are deliberately NOT collapsed into one: a successful read
// reporting `false` (err == nil, allowed == false) means auto-merge really is
// off, while a non-nil error means the READ ITSELF failed (no token, no
// permission, network) and nothing was learned about the setting. A caller
// (doctor) that rendered the second case as "off" would reproduce this
// phase's own defect — the funnel opens a PR and arms auto-merge on every
// write, and with the repo setting off every write stalls behind a PR
// nothing will merge — with a green check next to it.
func (h *GitHubHost) AutoMergeAllowed(ctx context.Context, req RepoSettingsRequest) (bool, error) {
	const op = "AutoMergeAllowed"
	if req.Repo.Owner == "" || req.Repo.Name == "" {
		return false, &Error{Op: op, Err: ErrInvalidRequest}
	}
	settings, err := h.readRepoSettings(ctx, op, req.Repo, req.Credential)
	if err != nil {
		return false, err
	}
	return settings.AllowAutoMerge, nil
}

// CodeownersError is one entry from GitHub's own CODEOWNERS validation: the
// line, what kind of problem it is, and GitHub's suggested fix.
//
// Suggestion is carried rather than re-derived because GitHub's own wording
// names all three conditions an owner must satisfy ("make sure the team X
// exists, is publicly visible, and has write access to the repository"), and
// a paraphrase would drop whichever one the reader's case actually is.
type CodeownersError struct {
	Line       int    `json:"line"`
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
	Path       string `json:"path"`
}

// CodeownersErrors reads GET /repos/{owner}/{repo}/codeowners/errors and
// reports what GitHub thinks is wrong with the space's CODEOWNERS file on its
// default branch. An empty slice means every owner named there resolves.
//
// This exists because an unknown owner is IGNORED, not rejected. A CODEOWNERS
// naming a team nobody created looks like it gates `/space.yaml` and gates
// nothing — and code-owner review is the entire mechanism behind the G4 safety
// argument, so an inert file is an ungated trust root with no error anywhere at
// merge time to say so.
//
// The template used to tell an operator to check GitHub's CODEOWNERS view by
// eye, calling that "the only feedback you will get". That was wrong in a
// useful direction: GitHub exposes the same thing through this endpoint, with
// line numbers. Verified against a real repo on 2026-07-26, where both
// `@org/some-login` (the literal both-halves placeholder replacement, which
// reads as a team) and `@org/space-admins` came back as "Unknown owner" with
// the line and the caret position.
//
// A READ only. a2a mutates no CODEOWNERS file — this is `a2a doctor`'s
// evidence, not a repair.
func (h *GitHubHost) CodeownersErrors(ctx context.Context, req RepoSettingsRequest) ([]CodeownersError, error) {
	const op = "CodeownersErrors"
	if req.Repo.Owner == "" || req.Repo.Name == "" {
		return nil, &Error{Op: op, Err: ErrInvalidRequest}
	}
	path := fmt.Sprintf("/repos/%s/%s/codeowners/errors", req.Repo.Owner, req.Repo.Name)
	var payload struct {
		Errors []CodeownersError `json:"errors"`
	}
	if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &payload); err != nil {
		return nil, err
	}
	return payload.Errors, nil
}

// repoMergeSettings is the subset of GET /repos/{owner}/{repo} both
// AutoMergeAllowed and MergePR read — ONE response body, two callers, kept
// as one struct so the fields can never drift apart (WAVE M4: MergePR needs
// exactly the sibling fields AutoMergeAllowed already reads, per the same
// GitHub response). RepoPermissions reads the SAME response's `permissions`
// object — a third caller, still no second GET.
type repoMergeSettings struct {
	AllowAutoMerge   bool            `json:"allow_auto_merge"`
	AllowMergeCommit bool            `json:"allow_merge_commit"`
	AllowSquashMerge bool            `json:"allow_squash_merge"`
	AllowRebaseMerge bool            `json:"allow_rebase_merge"`
	Permissions      RepoPermissions `json:"permissions"`
}

// RepoPermissions is the subset of GET /repos/{owner}/{repo}'s `permissions`
// object doctor's "who can fix it" note needs (this phase's brief §3): what
// the CREDENTIAL that read this repo can actually DO on it, never a config
// field. GitHub nests these (admin implies push implies pull), so a caller
// deciding "can this credential write here" checks Admin || Push.
type RepoPermissions struct {
	Admin bool `json:"admin"`
	Push  bool `json:"push"`
	Pull  bool `json:"pull"`
}

// RepoPermissions reads GET /repos/{owner}/{repo} — the SAME call
// AutoMergeAllowed and MergePR already issue — and reports the resolved
// credential's own permissions on the repository, straight off that
// response's `permissions` object. No second GitHub endpoint: this extends
// the one existing read rather than adding a new one.
func (h *GitHubHost) RepoPermissions(ctx context.Context, req RepoSettingsRequest) (RepoPermissions, error) {
	const op = "RepoPermissions"
	if req.Repo.Owner == "" || req.Repo.Name == "" {
		return RepoPermissions{}, &Error{Op: op, Err: ErrInvalidRequest}
	}
	settings, err := h.readRepoSettings(ctx, op, req.Repo, req.Credential)
	if err != nil {
		return RepoPermissions{}, err
	}
	return settings.Permissions, nil
}

// readRepoSettings issues the single GET /repos/{owner}/{repo} call both
// AutoMergeAllowed and MergePR need.
func (h *GitHubHost) readRepoSettings(ctx context.Context, op string, repo Repo, cred Credential) (repoMergeSettings, error) {
	path := fmt.Sprintf("/repos/%s/%s", repo.Owner, repo.Name)
	var settings repoMergeSettings
	if err := h.restCall(ctx, op, http.MethodGet, path, cred, nil, &settings); err != nil {
		return repoMergeSettings{}, err
	}
	return settings, nil
}

// mergeMethodPreference is the order MergePR tries direct-merge methods in.
// GitHub exposes no "repository default merge method" field over REST — only
// which methods are ALLOWED — so a direct merge cannot literally read the
// same default armAutoMerge implicitly uses; it instead picks the first
// allowed method in this order, which is both GitHub's own new-repository
// default (merge commit) and the order `a2a space init`'s repos ship with all
// three enabled. Pinning this (rather than leaving it to chance) is what
// keeps a direct merge from landing a DIFFERENT commit shape than auto-merge
// would have, and from failing outright on a repo that disallows merge
// commits specifically.
var mergeMethodPreference = []string{"merge", "squash", "rebase"}

// pickMergeMethod selects the first allowed method in mergeMethodPreference.
// ok=false means the repository allows none — MergePR must not guess.
func (s repoMergeSettings) pickMergeMethod() (method string, ok bool) {
	for _, m := range mergeMethodPreference {
		switch m {
		case "merge":
			if s.AllowMergeCommit {
				return m, true
			}
		case "squash":
			if s.AllowSquashMerge {
				return m, true
			}
		case "rebase":
			if s.AllowRebaseMerge {
				return m, true
			}
		}
	}
	return "", false
}

// MergePR implements the optional Merger capability: PUT
// /repos/{owner}/{repo}/pulls/{number}/merge — a DIRECT merge, for the case
// GitHub's own auto-merge toggle refused to arm because the PR is already
// mergeable (see Merger's doc; WAVE M4).
//
// The merge method is PINNED, never left implicit: armAutoMerge sends no
// merge_method (auto-merge falls back to the repository's own default), so a
// direct merge that guessed a DIFFERENT method could land a different commit
// shape than auto-merge would have, or fail outright on a repo that disallows
// that specific method. MergePR reads the same GET /repos/{owner}/{repo}
// response AutoMergeAllowed reads and picks the first method the repository
// allows, in mergeMethodPreference's order. No method allowed at all is
// ErrMergeMethodUnavailable — a typed, actionable error, never a guess.
//
// 405 and 409 are returned as distinguishable typed errors (ErrPRNotMergeable,
// ErrPRHeadMoved) rather than the generic ErrRequestFailed every other status
// collapses to: a 409 in particular means the head moved since the caller
// last verified it, so the commit that would actually be merged is not the
// one that was checked green — a caller MUST NOT report that as a landed
// write.
func (h *GitHubHost) MergePR(ctx context.Context, req MergePRRequest) error {
	const op = "MergePR"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.PRNumber == 0 {
		return &Error{Op: op, Err: ErrInvalidRequest}
	}

	settings, err := h.readRepoSettings(ctx, op, req.Repo, req.Credential)
	if err != nil {
		return err
	}
	method, ok := settings.pickMergeMethod()
	if !ok {
		return &Error{
			Op:    op,
			Input: req.Repo.Owner + "/" + req.Repo.Name,
			Err:   fmt.Errorf("%w: merge commit, squash, and rebase are all disabled — enable at least one in Settings -> General -> Pull Requests", ErrMergeMethodUnavailable),
		}
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", req.Repo.Owner, req.Repo.Name, req.PRNumber)
	status, _, raw, err := h.httpDo(ctx, op, http.MethodPut, h.BaseURL+path, req.Credential, map[string]any{"merge_method": method})
	if err != nil {
		return err
	}

	input := fmt.Sprintf("%s/%s#%d", req.Repo.Owner, req.Repo.Name, req.PRNumber)
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusMethodNotAllowed: // 405: not mergeable right now
		return &Error{Op: op, Input: input, Err: fmt.Errorf("%w: %s", ErrPRNotMergeable, strings.TrimSpace(string(raw)))}
	case status == http.StatusConflict: // 409: head moved under us
		return &Error{Op: op, Input: input, Err: fmt.Errorf("%w: %s", ErrPRHeadMoved, strings.TrimSpace(string(raw)))}
	default:
		return &Error{Op: op, Input: input, Err: fmt.Errorf("%w: status %d: %s", ErrRequestFailed, status, strings.TrimSpace(string(raw)))}
	}
}

func (h *GitHubHost) doJSON(ctx context.Context, op, method, url string, cred Credential, body any, out any) error {
	_, raw, err := h.send(ctx, op, method, url, cred, body)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: decode response: %w", ErrRequestFailed, err)}
	}
	return nil
}

// scopeHeader is the response header GitHub uses to advertise a token's
// OAuth scopes. Only OAuth-app and classic-PAT credentials carry it;
// fine-grained PATs and GitHub App installation tokens do not use scopes at
// all and omit it entirely.
const scopeHeader = "X-OAuth-Scopes"

// TokenScopes implements Host.TokenScopes by reading scopeHeader off a
// /rate_limit response — the one authenticated endpoint GitHub documents as
// not counting against the rate limit, so a capability probe never costs the
// caller a request they needed.
func (h *GitHubHost) TokenScopes(ctx context.Context, cred Credential) ([]string, bool, error) {
	const op = "TokenScopes"
	if cred.Token == "" {
		return nil, false, &Error{Op: op, Err: ErrInvalidRequest}
	}
	hdr, _, err := h.send(ctx, op, http.MethodGet, h.BaseURL+"/rate_limit", cred, nil)
	if err != nil {
		return nil, false, err
	}
	// Absent header vs present-but-empty is the whole distinction: absent
	// means "this credential type does not do scopes" (reported=false, the
	// caller must not conclude anything), present-but-empty means a classic
	// token that genuinely holds none (reported=true, scopes empty).
	values, ok := hdr[http.CanonicalHeaderKey(scopeHeader)]
	if !ok {
		return nil, false, nil
	}
	var scopes []string
	for _, field := range strings.Split(strings.Join(values, ","), ",") {
		if s := strings.TrimSpace(field); s != "" {
			scopes = append(scopes, s)
		}
	}
	return scopes, true, nil
}

// send issues one request and returns the response HEADER alongside the
// bounded body, translating any non-2xx status into ErrRequestFailed. doJSON
// is a thin wrapper over it; TokenScopes needs the header, which is why this
// exists as its own step rather than a second hand-rolled HTTP path.
//
// MergePR uses httpDo directly instead: it needs to tell 405/409 apart from
// every other status, which collapsing everything non-2xx into one sentinel
// (what this function does) would lose.
func (h *GitHubHost) send(ctx context.Context, op, method, url string, cred Credential, body any) (http.Header, []byte, error) {
	status, header, raw, err := h.httpDo(ctx, op, method, url, cred, body)
	if err != nil {
		return nil, nil, err
	}
	if status < 200 || status >= 300 {
		return nil, nil, &Error{
			Op:  op,
			Err: fmt.Errorf("%w: status %d: %s", ErrRequestFailed, status, strings.TrimSpace(string(raw))),
		}
	}
	return header, raw, nil
}

// httpDo performs one HTTP request and returns the raw status code, response
// header, and bounded body — WITHOUT interpreting the status code. Every
// caller that wants a non-2xx status turned into an error uses send; MergePR
// calls this directly because it must distinguish 405/409 from a generic
// failure, and from each other.
//
// # A transient GitHub is retried here, once, for every caller
//
// This is the single transport chokepoint every REST and GraphQL call passes
// through, which is why the retry lives here rather than in each verb. An
// attempt classified OutcomeUnknown (retry.go: a 5xx, a rate limit, a
// transport error) is retried on a bounded, deterministic schedule; anything
// Fatal fails on the FIRST response, exactly as fast as before, because
// retrying a 403 or a 422 wastes the budget on an answer that will not change.
//
// The reason is not tidiness. During GitHub's Pull Requests outage on
// 2026-07-24, every `a2a submit` died instantly on a bare `status 500:` with
// an empty body: the push had landed and only the PR had not, and nothing said
// whether that was a platform problem or a refusal.
//
// # Retrying a POST, honestly
//
// This retries non-idempotent requests too, and that is a deliberate call
// rather than an oversight. If a POST /pulls answered 5xx having in fact
// created the pull request, the retry is refused as a duplicate — so the
// caller sees ErrRetriedWriteWasDuplicate, which NAMES that the first attempt
// landed, instead of the bare 500 it used to see. The recovery is identical
// either way (re-run; the write funnel adopts an already-open PR by head
// branch), so the retry can only improve the message, never the outcome.
//
// The whole window is capped by RetryTotalCap, including a server-supplied
// Retry-After: a command that hangs is worse than one that reports a transient
// failure and says re-running is safe.
func (h *GitHubHost) httpDo(ctx context.Context, op, method, url string, cred Credential, body any) (status int, header http.Header, raw []byte, err error) {
	// Encoded ONCE, outside the loop: an io.Reader is consumed by the first
	// attempt, so a retry that reused it would send an empty body — a
	// silently different request, which is worse than no retry at all.
	var encoded []byte
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return 0, nil, nil, &Error{Op: op, Err: fmt.Errorf("%w: encode request: %w", ErrRequestFailed, err)}
		}
	}

	// A request this method/url cannot even BUILD fails deterministically, so
	// it is caught once, here, rather than inside the loop: classified as a
	// transport error it would be read as "unknown" and retried four times
	// before surfacing as transient, which is the wrong word for a malformed
	// URL and the wrong number of attempts for an answer that cannot change.
	if _, buildErr := http.NewRequestWithContext(ctx, method, url, nil); buildErr != nil {
		return 0, nil, nil, &Error{Op: op, Err: fmt.Errorf("%w: build request: %w", ErrRequestFailed, buildErr)}
	}

	attempts := h.RetryAttempts
	if attempts <= 0 {
		attempts = MaxAttempts
	}
	delayFor := h.RetryDelayFn
	if delayFor == nil {
		delayFor = RetryDelay
	}

	start := time.Now()
	var retried bool
	var lastStatus int
	var lastRaw []byte
	var lastErr error

	for attempt := 1; ; attempt++ {
		status, header, raw, err = h.httpAttempt(ctx, op, method, url, cred, encoded, body != nil)

		outcome := ClassifyStatus(status, err)
		// The one case ClassifyStatus's own doc comment defers to a caller
		// holding the headers: a 403 that is really a rate limit. Status code
		// alone cannot tell it from a permission refusal, and guessing wrong
		// in the permissive direction would burn the budget on a problem
		// retrying cannot fix.
		if outcome == OutcomeFatal && status == http.StatusForbidden && rateLimited(header) {
			outcome = OutcomeUnknown
		}

		if outcome != OutcomeUnknown {
			// A duplicate refusal AFTER a retry is evidence, not noise: the
			// first attempt landed and GitHub failed only to say so.
			if retried && status == http.StatusUnprocessableEntity {
				return status, header, raw, &Error{Op: op, Err: fmt.Errorf("%w: status %d: %s",
					ErrRetriedWriteWasDuplicate, status, strings.TrimSpace(string(raw)))}
			}
			return status, header, raw, err
		}

		lastStatus, lastRaw, lastErr = status, raw, err

		if attempt >= attempts {
			break
		}
		// A server-supplied Retry-After is honoured, but only as a request:
		// GitHub asking for two minutes and this CLI hanging for two minutes
		// are different products.
		delay := delayFor(attempt)
		if ra, ok := retryAfterDelay(header); ok && ra > delay {
			delay = ra
		}
		if time.Since(start)+delay > RetryTotalCap {
			break
		}
		select {
		case <-ctx.Done():
			// The caller's own deadline wins over the retry budget, and the
			// last observed failure is what explains the call — not the bare
			// cancellation, which would hide a 500 behind "context canceled".
			return lastStatus, header, lastRaw, h.transientError(op, method, attempt, lastStatus, lastRaw, lastErr)
		case <-time.After(delay):
		}
		retried = true
	}

	return lastStatus, header, lastRaw, h.transientError(op, method, attempts, lastStatus, lastRaw, lastErr)
}

// transientError builds the error a caller sees when every attempt classified
// as unknown. It names the class, the evidence, and the next move — the three
// things `status 500:` with an empty body did not.
func (h *GitHubHost) transientError(op, method string, attempts, status int, raw []byte, transportErr error) error {
	observed := fmt.Sprintf("status %d", status)
	if transportErr != nil {
		observed = transportErr.Error()
	}
	if detail := strings.TrimSpace(string(raw)); detail != "" {
		observed += ": " + detail
	}
	return &Error{Op: op, Err: fmt.Errorf("%w [%s, %d attempts, last: %s]",
		ErrTransient, method, attempts, observed)}
}

// httpAttempt performs ONE round trip and reports what came back without
// interpreting it. Split out of httpDo so each attempt closes its own response
// body — a retry loop with a deferred close leaks every attempt but the last.
func (h *GitHubHost) httpAttempt(ctx context.Context, op, method, url string, cred Credential, encoded []byte, hasBody bool) (status int, header http.Header, raw []byte, err error) {
	var reqBody io.Reader
	if hasBody {
		reqBody = bytes.NewReader(encoded)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return 0, nil, nil, &Error{Op: op, Err: fmt.Errorf("%w: build request: %w", ErrRequestFailed, err)}
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	if hasBody {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if cred.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+cred.Token)
	}

	resp, err := h.Client.Do(httpReq)
	if err != nil {
		return 0, nil, nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrRequestFailed, err)}
	}
	defer func() { _ = resp.Body.Close() }() // reason: response already fully read/discarded below

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	raw, err = io.ReadAll(limited)
	if err != nil {
		return 0, nil, nil, &Error{Op: op, Err: fmt.Errorf("%w: read response: %w", ErrRequestFailed, err)}
	}
	return resp.StatusCode, resp.Header, raw, nil
}

// armAutoMerge runs the enable-auto-merge mutation for an already-created PR
// node. One implementation, shared by OpenPR's create path and by
// EnableAutoMerge's repair path, so the two can never drift.
func (h *GitHubHost) armAutoMerge(ctx context.Context, op string, cred Credential, nodeID string) error {
	if nodeID == "" {
		return &Error{Op: op, Err: ErrInvalidRequest}
	}
	mutation := map[string]any{
		"query": "mutation($id: ID!) { enablePullRequestAutoMerge(input: {pullRequestId: $id}) { clientMutationId } }",
		"variables": map[string]any{
			"id": nodeID,
		},
	}
	return h.graphQLCall(ctx, op, cred, mutation, nil)
}

// EnableAutoMerge implements AutoMerger: arm auto-merge on an EXISTING PR.
//
// This is the repair path for OpenPR's non-atomicity (see AutoMerger's doc).
// It resolves the PR's node id itself rather than taking one, so any caller
// holding only a PR number — which is all FindPRByHeadBranch returns — can
// use it.
//
// Idempotent by construction: GitHub treats enabling auto-merge on a PR that
// already has it as a no-op success, which is what the retry path needs,
// since it cannot know whether the original OpenPR got this far.
func (h *GitHubHost) EnableAutoMerge(ctx context.Context, req EnableAutoMergeRequest) error {
	const op = "EnableAutoMerge"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.PRNumber == 0 {
		return &Error{Op: op, Err: ErrInvalidRequest}
	}

	var pr struct {
		NodeID string `json:"node_id"`
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", req.Repo.Owner, req.Repo.Name, req.PRNumber)
	if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &pr); err != nil {
		return err
	}
	return h.armAutoMerge(ctx, op, req.Credential, pr.NodeID)
}
