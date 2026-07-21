package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
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
}

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
	args = append(args, "push", req.RemoteURL, req.LocalRef+":refs/heads/"+req.Branch)

	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return PushBranchResult{}, &Error{
			Op:    op,
			Input: req.Branch,
			Err:   fmt.Errorf("%w: %s", ErrPushRejected, strings.TrimSpace(stderr.String())),
		}
	}
	return PushBranchResult{Branch: req.Branch}, nil
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
	mutation := map[string]any{
		"query": "mutation($id: ID!) { enablePullRequestAutoMerge(input: {pullRequestId: $id}) { clientMutationId } }",
		"variables": map[string]any{
			"id": created.NodeID,
		},
	}
	if err := h.graphQLCall(ctx, op, req.Credential, mutation, nil); err != nil {
		return PRInfo{}, err
	}

	state := created.State
	if state == "" {
		state = "open"
	}
	return PRInfo{Number: created.Number, URL: created.HTMLURL, State: state}, nil
}

// CheckStatus implements Host.CheckStatus: reads the PR's head SHA, then
// the named `a2a-validate` check-run's state/conclusion.
func (h *GitHubHost) CheckStatus(ctx context.Context, req StatusRequest) (CheckStatusResult, error) {
	const op = "CheckStatus"
	if req.Repo.Owner == "" || req.Repo.Name == "" || req.PRNumber == 0 {
		return CheckStatusResult{}, &Error{Op: op, Err: ErrInvalidRequest}
	}

	headSHA, err := h.prHeadSHA(ctx, op, req)
	if err != nil {
		return CheckStatusResult{}, err
	}

	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?check_name=a2a-validate", req.Repo.Owner, req.Repo.Name, headSHA)
	var resp struct {
		CheckRuns []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &resp); err != nil {
		return CheckStatusResult{}, err
	}
	if len(resp.CheckRuns) == 0 {
		return CheckStatusResult{State: "queued"}, nil
	}
	run := resp.CheckRuns[0]
	return CheckStatusResult{State: run.Status, Conclusion: run.Conclusion}, nil
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

	path := fmt.Sprintf("/repos/%s/%s/pulls?state=all&head=%s:%s", req.Repo.Owner, req.Repo.Name, req.Repo.Owner, req.Branch)
	var results []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"` // "open" | "closed"
		Merged  bool   `json:"merged"`
	}
	if err := h.restCall(ctx, op, http.MethodGet, path, req.Credential, nil, &results); err != nil {
		return nil, err
	}
	for _, r := range results {
		switch {
		case r.State == "open":
			return &PRInfo{Number: r.Number, URL: r.HTMLURL, State: "open"}, nil
		case r.Merged:
			return &PRInfo{Number: r.Number, URL: r.HTMLURL, State: "merged"}, nil
		}
	}
	return nil, nil
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
func (h *GitHubHost) graphQLCall(ctx context.Context, op string, cred Credential, body any, out any) error {
	return h.doJSON(ctx, op, http.MethodPost, h.BaseURL+"/graphql", cred, body, out)
}

func (h *GitHubHost) doJSON(ctx context.Context, op, method, url string, cred Credential, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return &Error{Op: op, Err: fmt.Errorf("%w: encode request: %v", ErrRequestFailed, err)}
		}
		reqBody = bytes.NewReader(raw)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: build request: %v", ErrRequestFailed, err)}
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if cred.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+cred.Token)
	}

	resp, err := h.Client.Do(httpReq)
	if err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: %v", ErrRequestFailed, err)}
	}
	defer func() { _ = resp.Body.Close() }() // reason: response already fully read/discarded below

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: read response: %v", ErrRequestFailed, err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{
			Op:  op,
			Err: fmt.Errorf("%w: status %d: %s", ErrRequestFailed, resp.StatusCode, strings.TrimSpace(string(raw))),
		}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: decode response: %v", ErrRequestFailed, err)}
	}
	return nil
}
