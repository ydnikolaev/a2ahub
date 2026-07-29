//go:build livee2e

package livee2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ghClient is the harness's own GitHub REST client for create/reset/protect/
// rerun questions — deliberately NOT a widening of internal/host's
// 5-primitive contract (wave 3a's precedent, carried forward here): "who may
// administer this repo", "reset its protection", "re-run a workflow" are
// questions only a test harness asks, and growing the product's Host
// interface to answer them would push harness concerns into the product's
// own contract.
type ghClient struct {
	// Token authenticates every call this client makes — either the
	// provisioner or the participant credential, never both (Config keeps
	// them separate; a harness wires one ghClient per identity).
	Token string
	// APIRoot defaults to defaultAPIRoot (prober_github.go) when empty.
	APIRoot string
	// HTTP defaults to a 30s-timeout client when nil — bounded by default,
	// same rationale as GitHubProber.
	HTTP *http.Client
}

func (c *ghClient) root() string {
	if c.APIRoot == "" {
		return defaultAPIRoot
	}
	return strings.TrimSuffix(c.APIRoot, "/")
}

func (c *ghClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// attempt issues ONE HTTP round trip. transportErr is set only for a genuine
// transport-level failure (dial/timeout, or a body that could not be read) —
// never for a completed request that merely answered with a non-2xx status,
// which is reported through (status, respBody) instead. That split is what
// lets do's ClassifyStatus call see the real distinction §T6-d needs: a
// network failure and a decisive 4xx/5xx are different signals.
func (c *ghClient) attempt(ctx context.Context, method, path string, body []byte) (status int, respBody []byte, transportErr error) {
	var reqReader io.Reader
	if body != nil {
		reqReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.root()+path, reqReader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// max+1, then check — never a bare LimitReader. A LimitReader TRUNCATES
	// silently, and the 2026-07-25 run is what that costs: the space had
	// accumulated ~100 pull requests, `GET /pulls?state=all&per_page=100`
	// crossed the old 1 MiB ceiling, every response came back as valid JSON
	// cut off mid-object, and 17 rows reported an unparseable body. The
	// diagnosis only became possible once the error carried the body's
	// size — exactly 1048576 bytes, which is not a number GitHub would ever
	// pick. A bound that produces invalid data is worse than no bound: it
	// turns "too big" into "malformed", which looks like someone else's bug.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(raw)) > maxResponseBytes {
		return resp.StatusCode, nil, fmt.Errorf("response body exceeds the %d byte bound", maxResponseBytes)
	}
	return resp.StatusCode, raw, nil
}

// maxResponseBytes bounds a SINGLE response. A page of 100 pull requests
// runs to roughly a megabyte, so this leaves real headroom while still
// refusing a runaway; pagination, not a bigger ceiling, is what keeps a
// listing bounded as the space ages (see listAllPages).
const maxResponseBytes = 8 << 20

// bodyOrErr renders whichever of a response body or a transport error is
// available, for an error message — GitHub's error body usually names the
// actionable part (a missing permission), so it is preferred when present.
// The error is preferred over the body when both are present: when there IS
// an error it is the more specific diagnosis (a transport failure names its
// cause; a decode failure already embeds a bounded body prefix), whereas the
// raw body alone is what left the 2026-07-25 run's operator staring at
// `[{"number": 1, "head` with no indication that the problem was truncation.
// A fatal status carries no error, so that path is unchanged.
func bodyOrErr(body []byte, err error) string {
	if err != nil {
		return err.Error()
	}
	if len(body) > 0 {
		return strings.TrimSpace(string(body))
	}
	return "(no body)"
}

// bodyPrefix renders a bounded, single-line head of a response body for a
// diagnostic message. An unparseable body is exactly the case where the
// bytes matter and exactly the case where dumping all of them into a report
// row would be useless — 200 characters is enough to tell a truncated JSON
// array from an HTML error page from a rate-limit notice.
func bodyPrefix(body []byte) string {
	const max = 200
	s := strings.TrimSpace(string(body))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// do is the ONE path every method in this file goes through, and it is
// where §T6-d lives: classify the outcome with the sibling's ClassifyStatus,
// and on OutcomeUnknown sleep RetryDelay(attempt) and try again, up to
// MaxAttempts, before giving up with ErrUnknownOutcome (wrapped, naming the
// path) — never silently reporting the last transient failure as decisive.
//
// Idempotent GETs retry freely under this loop with no special casing.
// WRITES go through the identical loop; the contract that makes that safe is
// the caller's, not do's: a write that comes back Unknown may already have
// landed (a 504 on OpenPR that had, a 500 on a push that had not — both
// observed against real GitHub on 2026-07-24), so the caller must RE-READ
// state before concluding anything, never treat "do returned an error" as
// "the write did not happen". `a2a submit`'s own idempotent-retry path (look
// the PR up by its deterministic head branch) is the product-side precedent
// for this contract.
//
// RetryDelay's attempt argument is 1-based here (the first RETRY, i.e. the
// second overall try, passes 1) — a documented assumption about the
// sibling's RetryDelay, since the pinned signature does not state an
// indexing convention; flagged as a coordination point in the wave report.
func (c *ghClient) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("livee2e: encode %s %s: %w", method, path, err)
		}
	}

	var lastStatus int
	var lastRespBody []byte
	var lastTransportErr error
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return lastStatus, fmt.Errorf("livee2e: %s %s: %w", method, path, ctx.Err())
			case <-time.After(RetryDelay(attempt)):
			}
		}

		status, respBody, transportErr := c.attempt(ctx, method, path, reqBody)
		lastStatus, lastRespBody, lastTransportErr = status, respBody, transportErr

		switch ClassifyStatus(status, transportErr) {
		case OutcomeOK:
			if out != nil && len(respBody) > 0 {
				if decErr := json.Unmarshal(respBody, out); decErr != nil {
					// A body we could not parse tells us NOTHING about the
					// product, so it must not be decisive. This used to
					// return the decode error straight out, and the
					// 2026-07-25 run is what that costs: GitHub returned a
					// truncated body on `GET /pulls`, every family's very
					// first pullForBranch inherited an opaque "unexpected
					// end of JSON input", and the report showed **15 rows
					// as product failures** for a transport-level cause.
					// In a pre-release ritual that is the worst possible
					// error: it either blocks a good release or teaches the
					// operator to discount reds.
					//
					// It is the same class this loop already handles for a
					// 5xx — "we do not know" — so it takes the same route:
					// retry, and if every attempt comes back unparseable,
					// fail with ErrUnknownOutcome, which verdictForError
					// renders as TIMED-OUT rather than FAIL. The body's
					// length and a bounded prefix ride along, because
					// "could not decode" with no evidence is undiagnosable
					// the next time it happens.
					lastTransportErr = fmt.Errorf("decode %d-byte body: %w (body starts: %q)",
						len(respBody), decErr, bodyPrefix(respBody))
					continue
				}
			}
			return status, nil
		case OutcomeFatal:
			return status, fmt.Errorf("livee2e: %s %s: status %d: %s", method, path, status, bodyOrErr(respBody, transportErr))
		case OutcomeUnknown:
			continue
		}
	}
	return lastStatus, fmt.Errorf("livee2e: %s %s: %w after %d attempts: %s",
		method, path, ErrUnknownOutcome, MaxAttempts, bodyOrErr(lastRespBody, lastTransportErr))
}

// Repo reports whether owner/name exists — a 404 is a normal "no" here, not
// an error (the reset flow's own first question: create, or reset in
// place?).
func (c *ghClient) Repo(ctx context.Context, owner, name string) (bool, error) {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	status, err := c.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		if status == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateRepo creates a PUBLIC repo under org (spec 36 §9.5.1 — public is
// deliberate: unmetered Actions + working branch protection on a Free org).
func (c *ghClient) CreateRepo(ctx context.Context, org, name, description string) error {
	path := "/orgs/" + url.PathEscape(org) + "/repos"
	body := map[string]any{
		"name":        name,
		"private":     false,
		"auto_init":   true,
		"description": description,
	}
	_, err := c.do(ctx, http.MethodPost, path, body, nil)
	return err
}

// PatchRepo updates repo settings (e.g. allow_auto_merge, delete_branch_on_merge).
func (c *ghClient) PatchRepo(ctx context.Context, owner, name string, body any) error {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	_, err := c.do(ctx, http.MethodPatch, path, body, nil)
	return err
}

// PutProtection arms branch protection. body carries the FULL protection
// document (required_status_checks, enforce_admins, required_pull_request_
// reviews, restrictions, allow_force_pushes, allow_deletions) — GitHub's PUT
// replaces the whole document, so a partial body silently drops whatever it
// omits (restrictions in particular must be present-and-null, or GitHub
// answers 422).
func (c *ghClient) PutProtection(ctx context.Context, owner, name, branch string, body any) error {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/branches/" + url.PathEscape(branch) + "/protection"
	_, err := c.do(ctx, http.MethodPut, path, body, nil)
	return err
}

// DeleteProtection removes branch protection. A 404 is a no-op, not an
// error: the first-ever reset has no protection yet to remove.
func (c *ghClient) DeleteProtection(ctx context.Context, owner, name, branch string) error {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/branches/" + url.PathEscape(branch) + "/protection"
	status, err := c.do(ctx, http.MethodDelete, path, nil, nil)
	if err != nil && status == http.StatusNotFound {
		return nil
	}
	return err
}

// ListBranches lists every branch name on owner/name (first page, up to
// 100 — the reset space never approaches that many).
func (c *ghClient) ListBranches(ctx context.Context, owner, name string) ([]string, error) {
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/branches?per_page=100"

	// PAGINATED, for the reason ListPulls below already learned the hard way
	// — the lesson simply had not been applied to this sibling. ResetSpace
	// prunes exactly what this returns, so a truncated listing does not fail
	// loudly: it leaves the space PARTLY clean and reports success. The next
	// run then pushes onto a surviving branch and gets a non-fast-forward
	// rejection, or re-opens a PR GitHub says already exists — failures that
	// look like product defects and are not.
	//
	// A reached cap is an ERROR rather than a quiet stop, same as there: a
	// silently short list is the exact failure mode being fixed.
	const maxPages = 50
	var out []string
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("livee2e: listing branches for %s/%s exceeded %d pages — the space needs emptying, not a bigger cap", owner, name, maxPages)
		}
		var payload []struct {
			Name string `json:"name"`
		}
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s&page=%d", base, page), nil, &payload); err != nil {
			return nil, err
		}
		for _, b := range payload {
			out = append(out, b.Name)
		}
		if len(payload) < 100 { // a short page is the last page
			return out, nil
		}
	}
}

// DeleteRef deletes a git ref (e.g. "heads/probe/xsec"). ref is passed
// through verbatim rather than escaped per path segment: GitHub's git-refs
// path legitimately contains slashes (branch names commonly do), and
// escaping each one would double-encode a valid ref.
func (c *ghClient) DeleteRef(ctx context.Context, owner, name, ref string) error {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/git/refs/" + ref
	_, err := c.do(ctx, http.MethodDelete, path, nil, nil)
	return err
}

// PullState is the subset of GitHub's pull-request object the harness reads.
type PullState struct {
	Number int
	// HeadRef is the head BRANCH name. It is how a caller resolves "the PR
	// for this write" without scraping CLI output: the funnel's branch name
	// is deterministic (space.BranchName), which is also what its own
	// idempotent-retry path looks a PR up by.
	HeadRef        string
	HeadSHA        string
	Merged         bool
	MergeableState string
	State          string
}

// pullPayload is PullState's wire shape.
type pullPayload struct {
	Number int `json:"number"`
	Head   struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Merged         bool   `json:"merged"`
	MergeableState string `json:"mergeable_state"`
	State          string `json:"state"`
}

func (p pullPayload) toPullState() PullState {
	return PullState{
		Number: p.Number, HeadRef: p.Head.Ref, HeadSHA: p.Head.SHA, Merged: p.Merged,
		MergeableState: p.MergeableState, State: p.State,
	}
}

// OpenPull opens a PR from head into base, returning its number.
//
// prBody is not decoration. The one caller opens a pull request whose check
// is REQUIRED to go red, and that red run shows up in the space's Actions tab
// (and in the repository's failure mail) under this title. Left unexplained
// it is indistinguishable from a defect — so the title and body are where the
// probe says what it is, to the only reader who will ever see it there.
func (c *ghClient) OpenPull(ctx context.Context, owner, name, title, prBody, head, base string) (int, error) {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/pulls"
	body := map[string]any{"title": title, "body": prBody, "head": head, "base": base}
	var created struct {
		Number int `json:"number"`
	}
	if _, err := c.do(ctx, http.MethodPost, path, body, &created); err != nil {
		return 0, err
	}
	return created.Number, nil
}

// Pull reads a single PR's current state.
func (c *ghClient) Pull(ctx context.Context, owner, name string, number int) (PullState, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(name), number)
	var payload pullPayload
	if _, err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return PullState{}, err
	}
	return payload.toPullState(), nil
}

// ListPulls lists PRs in the given state ("open" | "closed" | "all"; ""
// defaults to "open", matching GitHub's own default).
func (c *ghClient) ListPulls(ctx context.Context, owner, name, state string) ([]PullState, error) {
	if state == "" {
		state = "open"
	}
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) +
		"/pulls?state=" + url.QueryEscape(state) + "&per_page=100"

	// PAGINATED, because this is not a bounded question. `state=all` over a
	// throwaway space that is reset but never emptied of pull requests grows
	// by ~20 PRs every run; the single unpaginated call this replaced was
	// fine on run 1 and broke the entire matrix by run ~5, in a way that
	// looked like a GitHub fault rather than an accumulating one.
	//
	// A page cap that is reached is an ERROR, not a quiet stop: a truncated
	// listing would make `pullForBranch` answer "no PR on that branch" for a
	// PR that exists, which is the same silent-wrong-answer class as the
	// truncating read above, one layer up.
	const maxPages = 50
	var out []PullState
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("livee2e: listing pulls for %s/%s exceeded %d pages — the space needs emptying, not a bigger cap", owner, name, maxPages)
		}
		var payloads []pullPayload
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s&page=%d", base, page), nil, &payloads); err != nil {
			return nil, err
		}
		for _, p := range payloads {
			out = append(out, p.toPullState())
		}
		if len(payloads) < 100 { // a short page is the last page
			return out, nil
		}
	}
}

// SetPullState closes or reopens a PR — the §T6-c re-trigger mechanism that
// needs no Actions permission at all: reopening fires a fresh pull_request
// run whose triggering_actor is whoever reopened it, which is precisely how
// the v0.6.4 GITHUB_ACTOR bypass class is exercised (AC-961.3).
func (c *ghClient) SetPullState(ctx context.Context, owner, name string, number int, state string) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(name), number)
	_, err := c.do(ctx, http.MethodPatch, path, map[string]any{"state": state}, nil)
	return err
}

// MergePull merges a PR via PUT .../merge. The returned STATUS is itself the
// assertion AC-961.4 needs: a merge blocked by branch protection answers 405
// Method Not Allowed, and an unmergeable/dirty PR answers 409 Conflict —
// both come back as a VALUE (merged=false, status, err=nil), never as a Go
// error, so a caller asserting "the merge was refused" reads the status, not
// a failed do() call. Any other non-2xx (including an exhausted retry loop)
// is a genuine error.
func (c *ghClient) MergePull(ctx context.Context, owner, name string, number int) (merged bool, status int, err error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", url.PathEscape(owner), url.PathEscape(name), number)
	var result struct {
		Merged bool `json:"merged"`
	}
	status, err = c.do(ctx, http.MethodPut, path, nil, &result)
	if err != nil {
		if status == http.StatusMethodNotAllowed || status == http.StatusConflict {
			return false, status, nil
		}
		return false, status, err
	}
	return result.Merged, status, nil
}

// UpdateBranch forces merge-commit recomputation (§T6-b): without this, a
// re-triggered check after main moved can serve a STALE merge commit, so a
// scenario that changes the base must call this before re-triggering, or
// assert on which ref actually executed instead.
//
// GitHub answers 422 when the branch is already up to date with its base
// (nothing to recompute) — that is OutcomeFatal under ClassifyStatus, so it
// surfaces here as an error. Fine for row 15's intended sequence (main moves
// first, so the branch is always behind when this is called); a caller that
// might invoke this defensively on an already-current branch must expect and
// tolerate a 422, which this method does not special-case.
func (c *ghClient) UpdateBranch(ctx context.Context, owner, name string, number int) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/update-branch", url.PathEscape(owner), url.PathEscape(name), number)
	_, err := c.do(ctx, http.MethodPut, path, nil, nil)
	return err
}

// CheckRuns lists the check runs GitHub recorded for sha, returning the
// sibling's untagged CheckRunRef type so refassert.go's comparisons can
// consume it directly.
//
// HeadSHA is reported exactly as GitHub sends it (the check run's own
// head_sha field), never normalized against the queried sha: for
// pull_request-triggered runs GitHub may report the MERGE commit rather than
// the PR's head, and whether that happens here is exactly the empirical
// question §T6-b exists to let the live run answer, not one to decide by
// silently coercing the two to look equal.
//
// Explicitly passes `filter=all` — the opposite of internal/host's
// CheckStatus (github.go), which explicitly passes `filter=latest` (one run
// per name, the most recent) because ITS selection logic depends on exactly
// that collapsing. This function wants the OPPOSITE: §T6-b's whole point is
// seeing the stale, superseded runs a re-trigger leaves behind, which
// `filter=latest` would hide.
//
// This param is NOT optional the way it might look. AC-984.1's own live-run
// diagnosis (scenarios_boundary_live.go's own top-of-file note) is that an
// EARLIER version of this function built its query string with no `filter`
// param at all, on the documented (here, wrongly) assumption that omitting
// it meant "no filtering". It does not: GitHub's own default for this
// endpoint, when the param is omitted, IS `filter=latest` — the exact fact
// internal/host/github.go's own CheckStatus states explicitly ("filter=latest
// is GitHub's own default"), one file over, previously verified against
// real GitHub for THAT function's own selection logic. An unfiltered call
// here therefore silently collapsed to the same "one run per name" view
// CheckStatus deliberately asks for on purpose — meaning a caller counting
// entries in the result to detect "did a re-trigger add a new run" could
// never see the count exceed 1, before OR after any re-trigger, regardless
// of how many runs GitHub actually executed. Explicit `filter=all` is the
// fix.
//
// Like CheckStatus, this reads only the first page (up to 100 runs), which is
// the same fail-safe ceiling github.go documents for the same reason: a
// space repo runs few enough checks that the ceiling is far off, and the
// failure mode is an incomplete list, never a false positive.
func (c *ghClient) CheckRuns(ctx context.Context, owner, name, sha string) ([]CheckRunRef, error) {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/commits/" + url.PathEscape(sha) + "/check-runs?filter=all&per_page=100"
	var payload struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if _, err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return nil, err
	}
	out := make([]CheckRunRef, 0, len(payload.CheckRuns))
	for _, r := range payload.CheckRuns {
		out = append(out, CheckRunRef{ID: r.ID, Name: r.Name, HeadSHA: r.HeadSHA, Status: r.Status, Conclusion: r.Conclusion})
	}
	return out, nil
}

type CheckAnnotation struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Level   string `json:"annotation_level"`
}

func (c *ghClient) CheckRunAnnotations(ctx context.Context, owner, name string, runID int64) ([]CheckAnnotation, error) {
	path := fmt.Sprintf("/repos/%s/%s/check-runs/%d/annotations?per_page=100",
		url.PathEscape(owner), url.PathEscape(name), runID)
	var out []CheckAnnotation
	if _, err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// WorkflowRun is the subset of GitHub's workflow-run object the §T6-c
// rerun-API path needs. Not part of the pinned cross-agent API (no sibling
// file references it), so it is defined here rather than guessed into
// refassert.go.
type WorkflowRun struct {
	ID         int64
	Name       string
	Status     string
	Conclusion string
	HeadSHA    string
}

// ListWorkflowRuns lists workflow runs for a head SHA.
func (c *ghClient) ListWorkflowRuns(ctx context.Context, owner, name, headSHA string) ([]WorkflowRun, error) {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/actions/runs?head_sha=" + url.QueryEscape(headSHA) + "&per_page=100"
	var payload struct {
		WorkflowRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HeadSHA    string `json:"head_sha"`
		} `json:"workflow_runs"`
	}
	if _, err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return nil, err
	}
	out := make([]WorkflowRun, 0, len(payload.WorkflowRuns))
	for _, r := range payload.WorkflowRuns {
		out = append(out, WorkflowRun{ID: r.ID, Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, HeadSHA: r.HeadSHA})
	}
	return out, nil
}

// RerunWorkflow re-runs a completed workflow run — the OTHER §T6-c
// mechanism: unlike SetPullState's close/reopen, this needs the
// provisioner's Actions:write permission (spec 36 §9.5.3).
func (c *ghClient) RerunWorkflow(ctx context.Context, owner, name string, runID int64) error {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/rerun", url.PathEscape(owner), url.PathEscape(name), runID)
	_, err := c.do(ctx, http.MethodPost, path, nil, nil)
	return err
}

// ListRunsSince lists the repository's workflow runs created at or after
// since (RFC3339), across every branch and event — NOT filtered to a head
// SHA the way ListWorkflowRuns is.
//
// The distinction is the point. ListWorkflowRuns answers "what happened to
// the PR I just opened", which is what every scenario needs. This answers
// "what happened in this repository while I was working", which is what
// nothing asked until a real failure on the space reached the operator by
// email while this tier reported green.
//
// JobCount is left zero here and filled in only for runs worth a second
// call — see FillJobCounts.
func (c *ghClient) ListRunsSince(ctx context.Context, owner, name, since string) ([]SpaceRun, error) {
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) +
		"/actions/runs?per_page=100&created=%3E%3D" + url.QueryEscape(since)

	// PAGINATED. One matrix run pushes ~50 times and opens ~40 pull
	// requests, so a single page of 100 is not a bound on the answer — it is
	// roughly the answer's size, which means a truncated read is the DEFAULT
	// rather than an edge case. The first live use of this call returned
	// exactly 100 runs, i.e. it had already hit the cap.
	const maxPages = 50
	var out []SpaceRun
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("livee2e: listing runs for %s/%s since %s exceeded %d pages", owner, name, since, maxPages)
		}
		var payload struct {
			WorkflowRuns []struct {
				ID         int64  `json:"id"`
				Name       string `json:"name"`
				Event      string `json:"event"`
				HeadBranch string `json:"head_branch"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			} `json:"workflow_runs"`
		}
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s&page=%d", base, page), nil, &payload); err != nil {
			return nil, err
		}
		for _, r := range payload.WorkflowRuns {
			out = append(out, SpaceRun{
				ID: r.ID, Name: r.Name, Event: r.Event, HeadBranch: r.HeadBranch,
				Status: r.Status, Conclusion: r.Conclusion,
			})
		}
		if len(payload.WorkflowRuns) < 100 { // a short page is the last page
			return out, nil
		}
	}
}

// CountJobs returns how many jobs a workflow run actually ran.
//
// Zero is the signal that matters: a run whose workflow could not be loaded
// (an unresolvable reusable-workflow ref, most often) completes as a FAILURE
// having run nothing, and GitHub surfaces no failing check for it at all —
// the required context simply never appears, so every waiter times out
// instead of failing. That fault is indistinguishable from an ordinary
// failed job by conclusion alone.
func (c *ghClient) CountJobs(ctx context.Context, owner, name string, runID int64) (int, error) {
	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs?per_page=100",
		url.PathEscape(owner), url.PathEscape(name), runID)
	var payload struct {
		TotalCount int `json:"total_count"`
	}
	if _, err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return 0, err
	}
	return payload.TotalCount, nil
}
