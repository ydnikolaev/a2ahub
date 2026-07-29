package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// checkRunsHost serves a PR head SHA plus a fixed check-runs listing, and
// records the check-runs query it was asked for.
func checkRunsHost(t *testing.T, runs []map[string]any) (*GitHubHost, *string) {
	t.Helper()

	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/space/pulls/7":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"head": map[string]any{"sha": "deadbeef"},
			})
		case "/repos/acme/space/commits/deadbeef/check-runs":
			query = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": runs})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return NewGitHubHost(srv.Client(), srv.URL), &query
}

func checkStatus(t *testing.T, h *GitHubHost) CheckStatusResult {
	t.Helper()
	got, err := h.CheckStatus(context.Background(), StatusRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7,
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	return got
}

// AC-940.1 — a P33-migrated space's caller job emits the COMPOUND context.
func TestCheckStatusResolvesCompoundCheckRun(t *testing.T) {
	t.Parallel()

	h, query := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate / validate", "status": "completed", "conclusion": "success"},
	})
	got := checkStatus(t, h)

	if got.State != "completed" || got.Conclusion != "success" {
		t.Fatalf("CheckStatus = %+v, want completed/success", got)
	}
	if got.Name != "a2a-validate / validate" {
		t.Fatalf("CheckStatus.Name = %q, want the compound run's own name", got.Name)
	}
	// The server-side exact filter is what P33 broke — it must be gone, or
	// a real GitHub would answer with an empty listing regardless of the
	// selection logic below it.
	if strings.Contains(*query, "check_name=") {
		t.Fatalf("check-runs query = %q, want NO check_name= exact filter (spec 34 §2.1)", *query)
	}
}

// A conclusion alone is not a verdict: GitHub can serve a run computed against
// a stale merge commit after the base moved (observed twice during the
// 2026-07-24 getvisa migration). CheckStatus therefore reports WHICH REF the
// selected run executed against, so a caller comparing it to the PR's current
// head can tell a fresh green from a stale one.
func TestCheckStatusReportsTheExecutedRef(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate / validate", "status": "completed", "conclusion": "success", "head_sha": "deadbeef"},
	})
	got := checkStatus(t, h)

	if got.HeadSHA != "deadbeef" {
		t.Fatalf("CheckStatus.HeadSHA = %q, want the selected run's own head_sha — without it a stale run's green is indistinguishable from a current one", got.HeadSHA)
	}
}

// No matching run means no ref to report. Reporting the PR's head here would
// be the worst possible default: it would make every stale-ref comparison
// agree with itself.
func TestCheckStatusReportsNoExecutedRefWhenNoRunMatched(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-postmerge-audit / validate", "status": "completed", "conclusion": "success", "head_sha": "deadbeef"},
	})
	got := checkStatus(t, h)

	if got.HeadSHA != "" {
		t.Fatalf("CheckStatus.HeadSHA = %q, want empty when no required run matched", got.HeadSHA)
	}
}

// AC-940.2 — an un-migrated space still emits the FLAT name; the mixed fleet
// means one binary must resolve both without per-space configuration.
func TestCheckStatusResolvesFlatCheckRun(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate", "status": "completed", "conclusion": "success"},
	})
	got := checkStatus(t, h)

	if got.State != "completed" || got.Conclusion != "success" {
		t.Fatalf("CheckStatus = %+v, want completed/success", got)
	}
	if got.Name != "a2a-validate" {
		t.Fatalf("CheckStatus.Name = %q, want %q", got.Name, "a2a-validate")
	}
}

func TestCheckStatusFindsRequiredRunAfterFirstPage(t *testing.T) {
	t.Parallel()

	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/space/pulls/7":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"head": map[string]any{"sha": "deadbeef"},
			})
		case "/repos/acme/space/commits/deadbeef/check-runs":
			page := r.URL.Query().Get("page")
			pages = append(pages, page)
			if page == "1" {
				runs := make([]map[string]any, 100)
				for i := range runs {
					runs[i] = map[string]any{
						"name": "unrelated", "status": "completed", "conclusion": "success",
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": runs})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []map[string]any{
				{"name": "a2a-validate / validate", "status": "completed", "conclusion": "success"},
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	got := checkStatus(t, NewGitHubHost(srv.Client(), srv.URL))
	if got.Name != "a2a-validate / validate" || got.Conclusion != "success" {
		t.Fatalf("CheckStatus = %+v, want page-2 required run", got)
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Fatalf("pages = %v, want [1 2]", pages)
	}
}

// A space mid-migration can briefly emit BOTH; the compound one is the shape
// branch protection now requires, so it wins (spec 34 §2.2).
func TestCheckStatusPrefersCompoundOverFlat(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate", "status": "completed", "conclusion": "failure"},
		{"name": "a2a-validate / validate", "status": "completed", "conclusion": "success"},
	})
	got := checkStatus(t, h)

	if got.Name != "a2a-validate / validate" || got.Conclusion != "success" {
		t.Fatalf("CheckStatus = %+v, want the compound run to win", got)
	}
}

// AC-940.3 — the post-merge audit is push-triggered and is NEVER the required
// check (spec 09 §5.5). The prefix test is anchored on `a2a-validate`, so a
// name merely CONTAINING it must not match.
func TestCheckStatusNeverSelectsPostmergeAudit(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-postmerge-audit / validate", "status": "completed", "conclusion": "success"},
		// Anchored on BOTH sides: a leading near-miss must not match…
		{"name": "some-a2a-validate / validate", "status": "completed", "conclusion": "success"},
		// …and neither must a trailing one — this is the row that fails if
		// the separator is ever dropped from the prefix.
		{"name": "a2a-validate-extra / validate", "status": "completed", "conclusion": "success"},
	})
	got := checkStatus(t, h)

	if got.State != "queued" || got.Name != "" {
		t.Fatalf("CheckStatus = %+v, want the no-check result — neither run is the required check", got)
	}
}

// AC-940.4 — zero matching runs keeps the pre-existing "no check" result.
func TestCheckStatusNoMatchingRun(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{})
	if got := checkStatus(t, h); got.State != "queued" || got.Conclusion != "" {
		t.Fatalf("CheckStatus = %+v, want the queued/no-check result", got)
	}
}

// More than one compound candidate is resolved deterministically AND
// reported — a silent pick is what spec 34 §2.4 forbids.
func TestCheckStatusReportsAmbiguousCompoundRuns(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate / zeta", "status": "completed", "conclusion": "failure"},
		{"name": "a2a-validate / alpha", "status": "completed", "conclusion": "success"},
	})
	got := checkStatus(t, h)

	if got.Name != "a2a-validate / alpha" || got.Conclusion != "success" {
		t.Fatalf("CheckStatus = %+v, want the lexicographically first compound run", got)
	}
	if len(got.Ambiguous) != 2 {
		t.Fatalf("CheckStatus.Ambiguous = %v, want both compound candidates reported", got.Ambiguous)
	}
}

// A re-triggered check leaves TWO runs with the SAME name on one head SHA,
// and the current one must win.
//
// This is not hypothetical and not rare. Verified against real GitHub on
// 2026-07-24 (P36's live tier): re-triggering a PR's check made
// `/commits/{sha}/check-runs?filter=latest` return two completed runs both
// named `a2a-validate / validate` — `filter=latest` is per-check-SUITE, and a
// re-trigger creates another suite. An earlier version of the selector sorted
// by name alone, which compares equal here; `sort.Slice` is not stable, so the
// pick between the stale run and the current one was arbitrary.
//
// The fixture puts the STALE run first and gives it the "better" conclusion,
// so a selector that keeps input order, or that prefers success, fails.
func TestCheckStatusPrefersTheMostRecentRunOfTheSameName(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate / validate", "status": "completed", "conclusion": "success",
			"started_at": "2026-07-24T16:06:38Z", "id": 89529876709},
		{"name": "a2a-validate / validate", "status": "completed", "conclusion": "failure",
			"started_at": "2026-07-24T16:09:08Z", "id": 89530405168},
	})
	got := checkStatus(t, h)

	if got.Conclusion != "failure" {
		t.Fatalf("CheckStatus.Conclusion = %q, want the LATER run's %q — a stale conclusion read as current is the whole defect", got.Conclusion, "failure")
	}
	// Still reported: the caller should be able to see the SHA carried more
	// than one candidate, even though the pick is now meaningful.
	if len(got.Ambiguous) != 2 {
		t.Fatalf("CheckStatus.Ambiguous = %v, want both runs reported", got.Ambiguous)
	}
}

// Same-second runs still resolve deterministically, by id — GitHub's
// check-run ids increase over time, so the higher id is the later run.
func TestCheckStatusBreaksSameSecondTiesByID(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate / validate", "status": "completed", "conclusion": "success",
			"started_at": "2026-07-24T16:09:08Z", "id": 100},
		{"name": "a2a-validate / validate", "status": "completed", "conclusion": "failure",
			"started_at": "2026-07-24T16:09:08Z", "id": 101},
	})
	if got := checkStatus(t, h); got.Conclusion != "failure" {
		t.Fatalf("CheckStatus.Conclusion = %q, want the higher-id run's %q", got.Conclusion, "failure")
	}
}

// The unambiguous single-match path must NOT report ambiguity.
func TestCheckStatusSingleMatchIsNotAmbiguous(t *testing.T) {
	t.Parallel()

	h, _ := checkRunsHost(t, []map[string]any{
		{"name": "a2a-validate / validate", "status": "in_progress"},
		{"name": "a2a-postmerge-audit / validate", "status": "completed", "conclusion": "success"},
	})
	if got := checkStatus(t, h); len(got.Ambiguous) != 0 {
		t.Fatalf("CheckStatus.Ambiguous = %v, want empty for a single candidate", got.Ambiguous)
	}
}

func TestReviewStatusFoldsLatestPerReviewer(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"state": "CHANGES_REQUESTED", "user": map[string]any{"login": "alice"}},
			{"state": "APPROVED", "user": map[string]any{"login": "alice"}}, // supersedes above
			{"state": "APPROVED", "user": map[string]any{"login": "bob"}},
		})
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	got, err := h.ReviewStatus(context.Background(), StatusRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7,
	})
	if err != nil {
		t.Fatalf("ReviewStatus: %v", err)
	}
	if !got.Approved {
		t.Fatalf("ReviewStatus.Approved = false, want true (alice's latest review is APPROVED); pending=%v", got.Pending)
	}
}

func TestFindPRByHeadBranchReturnsOpenMatch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 9, "html_url": "https://example.invalid/pr/9", "state": "open", "merged": false},
		})
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	got, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, Branch: "a2a/axon/XQ-axon-1",
	})
	if err != nil {
		t.Fatalf("FindPRByHeadBranch: %v", err)
	}
	if got == nil || got.Number != 9 {
		t.Fatalf("FindPRByHeadBranch = %+v, want PR #9", got)
	}
}

func TestFindPRByHeadBranchRecognisesMergedAtFromListPulls(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":    10,
				"html_url":  "https://example.invalid/pr/10",
				"state":     "closed",
				"merged_at": "2026-07-29T17:50:55Z",
			},
		})
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	got, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, Branch: "a2a/axon/retire/XC-axon-widget",
	})
	if err != nil {
		t.Fatalf("FindPRByHeadBranch: %v", err)
	}
	if got == nil || got.Number != 10 || got.State != "merged" {
		t.Fatalf("FindPRByHeadBranch = %+v, want merged PR #10", got)
	}
}

func TestFindPRByHeadBranchNoneFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	got, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, Branch: "a2a/axon/XQ-axon-1",
	})
	if err != nil {
		t.Fatalf("FindPRByHeadBranch: %v", err)
	}
	if got != nil {
		t.Fatalf("FindPRByHeadBranch = %+v, want nil (no match)", got)
	}
}

func TestListOpenPRsReportsAutoMergeArmed(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"number":7,"html_url":"https://example.invalid/pull/7","auto_merge":{"merge_method":"SQUASH"}},
			{"number":8,"html_url":"https://example.invalid/pull/8","auto_merge":null}
		]`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	prs, err := h.ListOpenPRs(context.Background(), ListOpenPRsRequest{
		Repo: Repo{Owner: "acme", Name: "space"},
	})
	if err != nil {
		t.Fatalf("ListOpenPRs: %v", err)
	}
	if len(prs) != 2 || !prs[0].AutoMergeArmed || prs[1].AutoMergeArmed {
		t.Fatalf("ListOpenPRs = %+v, want armed then unarmed", prs)
	}
}

func TestGitHubHostRequestFailedOnNon2xx(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"revoked credential"}`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	_, err := h.CheckStatus(context.Background(), StatusRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 1,
	})
	if err == nil {
		t.Fatal("expected error on 403 response")
	}
	if !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("expected errors.Is(err, ErrRequestFailed), got %v", err)
	}
}

func TestInvalidRequestsRejected(t *testing.T) {
	t.Parallel()

	h := NewGitHubHost(nil, "")
	ctx := context.Background()

	if _, err := h.PushBranch(ctx, PushBranchRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("PushBranch({}) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := h.OpenPR(ctx, OpenPRRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("OpenPR({}) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := h.CheckStatus(ctx, StatusRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("CheckStatus({}) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := h.ReviewStatus(ctx, StatusRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("ReviewStatus({}) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := h.FindPRByHeadBranch(ctx, FindPRRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("FindPRByHeadBranch({}) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := h.ListOpenPRs(ctx, ListOpenPRsRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("ListOpenPRs({}) error = %v, want ErrInvalidRequest", err)
	}
}

// TestTokenScopesReportsAndDistinguishesSilence covers the whole point of the
// `reported` flag: a classic/OAuth token advertises its scopes in
// X-OAuth-Scopes, while a fine-grained PAT or App installation token omits the
// header entirely — and "omitted" must never be read as "holds none", or the
// most narrowly-scoped credentials would be the ones we refuse.
func TestTokenScopesReportsAndDistinguishesSilence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		setHeader    bool
		header       string
		wantScopes   []string
		wantReported bool
	}{
		{name: "oauth token advertises scopes", setHeader: true, header: "repo, read:org, workflow",
			wantScopes: []string{"repo", "read:org", "workflow"}, wantReported: true},
		{name: "classic token with no scopes is reported as empty", setHeader: true, header: "",
			wantScopes: nil, wantReported: true},
		{name: "fine-grained PAT omits the header entirely", setHeader: false,
			wantScopes: nil, wantReported: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/rate_limit" {
					t.Errorf("probed %s, want the rate_limit endpoint (it does not count against the limit)", r.URL.Path)
				}
				if tc.setHeader {
					w.Header().Set("X-OAuth-Scopes", tc.header)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			h := NewGitHubHost(srv.Client(), srv.URL)
			scopes, reported, err := h.TokenScopes(context.Background(), Credential{Token: "t"})
			if err != nil {
				t.Fatalf("TokenScopes: %v", err)
			}
			if reported != tc.wantReported {
				t.Fatalf("reported = %v, want %v", reported, tc.wantReported)
			}
			if len(scopes) != len(tc.wantScopes) {
				t.Fatalf("scopes = %v, want %v", scopes, tc.wantScopes)
			}
			for i := range scopes {
				if scopes[i] != tc.wantScopes[i] {
					t.Fatalf("scopes = %v, want %v", scopes, tc.wantScopes)
				}
			}
		})
	}
}

func TestTokenScopesRejectsAnEmptyCredential(t *testing.T) {
	t.Parallel()
	h := NewGitHubHost(nil, "")
	if _, _, err := h.TokenScopes(context.Background(), Credential{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("TokenScopes({}) error = %v, want ErrInvalidRequest", err)
	}
}

// EnableAutoMerge is the repair path for OpenPR's non-atomicity: creating a
// PR and arming auto-merge are two calls, so a failure between them leaves a
// PR that passes its checks and never merges.
//
// Observed live on 2026-07-24 (P36's matrix): a 504 on OpenPR left a PR open
// with a green required check and `auto_merge: null`.
func TestEnableAutoMergeResolvesTheNodeAndArms(t *testing.T) {
	t.Parallel()

	var gotPRPath, gotQuery, gotNodeID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			var body struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotQuery = body.Query
			gotNodeID, _ = body.Variables["id"].(string)
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		gotPRPath = r.URL.Path
		_, _ = w.Write([]byte(`{"node_id":"PR_kwDO"}`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	err := h.EnableAutoMerge(context.Background(), EnableAutoMergeRequest{
		Repo: Repo{Owner: "o", Name: "r"}, PRNumber: 2, Credential: Credential{Token: "t"},
	})
	if err != nil {
		t.Fatalf("EnableAutoMerge: %v", err)
	}
	if gotPRPath != "/repos/o/r/pulls/2" {
		t.Errorf("resolved the node id from %q", gotPRPath)
	}
	if !strings.Contains(gotQuery, "enablePullRequestAutoMerge") {
		t.Errorf("mutation = %q", gotQuery)
	}
	if gotNodeID != "PR_kwDO" {
		t.Errorf("armed node %q, want the one the PR reported", gotNodeID)
	}
}

func TestEnableAutoMergeRejectsAnIncompleteRequest(t *testing.T) {
	t.Parallel()

	h := NewGitHubHost(nil, "http://unused.invalid")
	for name, req := range map[string]EnableAutoMergeRequest{
		"no owner":  {Repo: Repo{Name: "r"}, PRNumber: 1},
		"no name":   {Repo: Repo{Owner: "o"}, PRNumber: 1},
		"no number": {Repo: Repo{Owner: "o", Name: "r"}},
	} {
		if err := h.EnableAutoMerge(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: want ErrInvalidRequest, got %v", name, err)
		}
	}
}

// GraphQL answers HTTP 200 for a FAILED operation and puts the failure in the
// body's `errors` array, so a call that checks only the status swallows every
// GraphQL failure silently.
//
// That was not theoretical: `a2a submit` arms auto-merge through this path, so
// a refused arming left the PR open forever while the command reported
// "opened PR … (pending-merge)". Verified against real GitHub on 2026-07-24
// (P36's matrix): HTTP 200, errors ["Pull request Pull request is in clean
// status"], data {enablePullRequestAutoMerge: null}.
func TestGraphQLErrorsSurfaceDespiteHTTP200(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			// The exact shape GitHub sends: success status, failed operation.
			_, _ = w.Write([]byte(`{"data":{"enablePullRequestAutoMerge":null},` +
				`"errors":[{"message":"Pull request Pull request is in clean status"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"node_id":"PR_1"}`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	err := h.EnableAutoMerge(context.Background(), EnableAutoMergeRequest{
		Repo: Repo{Owner: "o", Name: "r"}, PRNumber: 2,
	})
	if err == nil {
		t.Fatal("a GraphQL failure returned nil — the whole defect")
	}
	if !errors.Is(err, ErrGraphQLFailed) {
		t.Errorf("err = %v, want it to wrap ErrGraphQLFailed", err)
	}
	if !strings.Contains(err.Error(), "clean status") {
		t.Errorf("err = %v, want GitHub's own message carried through", err)
	}
	// The funnel's repair path depends on recognising THIS refusal, because
	// "the PR is already mergeable" is not a failed repair.
	if !IsAutoMergeAlreadyClean(err) {
		t.Error("IsAutoMergeAlreadyClean did not recognise GitHub's clean-status refusal")
	}
}

// A GraphQL call that genuinely succeeds must stay a success — the errors
// check must not turn every 200 into a failure.
func TestGraphQLSuccessStaysSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			_, _ = w.Write([]byte(`{"data":{"enablePullRequestAutoMerge":{"clientMutationId":null}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"node_id":"PR_1"}`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	if err := h.EnableAutoMerge(context.Background(), EnableAutoMergeRequest{
		Repo: Repo{Owner: "o", Name: "r"}, PRNumber: 2,
	}); err != nil {
		t.Fatalf("EnableAutoMerge on a clean success: %v", err)
	}
	if IsAutoMergeAlreadyClean(nil) {
		t.Error("IsAutoMergeAlreadyClean(nil) reported true")
	}
}

// A repository with auto-merge DISABLED must not make `a2a submit` fail. The
// write happened and the PR is real; only the unattended merge is
// unavailable, and the operator needs to be told which.
//
// This is the case that matters in practice: `allow_auto_merge` is OFF by
// default on a new repository, and the live getvisa space runs with it off
// (verified 2026-07-24). Before GraphQL errors were surfaced at all, every
// submit there quietly claimed "pending-merge" over a PR nobody would merge;
// making the error fatal instead would have broken submit outright.
func TestOpenPRReportsADisabledAutoMergeInsteadOfFailing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			// GitHub's exact wording, captured from the real API.
			_, _ = w.Write([]byte(`{"data":{"enablePullRequestAutoMerge":null},` +
				`"errors":[{"message":"Auto merge is not allowed for this repository"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"number":7,"html_url":"https://x/pull/7","node_id":"PR_7","state":"open"}`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	pr, err := h.OpenPR(context.Background(), OpenPRRequest{
		Repo: Repo{Owner: "o", Name: "r"}, Head: "a2a/x/y", Base: "main", Title: "t",
	})
	if err != nil {
		t.Fatalf("OpenPR failed on a repo with auto-merge disabled: %v", err)
	}
	if pr.Number != 7 {
		t.Fatalf("PRInfo = %+v, want the created PR", pr)
	}
	if pr.AutoMergeArmed {
		t.Error("AutoMergeArmed is true though GitHub refused")
	}
	if !strings.Contains(pr.AutoMergeNote, "Allow auto-merge") {
		t.Errorf("AutoMergeNote = %q, want it to name the remedy", pr.AutoMergeNote)
	}
}

// An UNRECOGNISED GraphQL failure stays fatal. A surprise must not be filed
// under "known limitation" and shipped as a PR nobody knows is stuck.
func TestOpenPRStillFailsOnAnUnknownGraphQLError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Resource not accessible by personal access token"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"number":8,"html_url":"https://x/pull/8","node_id":"PR_8","state":"open"}`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	_, err := h.OpenPR(context.Background(), OpenPRRequest{
		Repo: Repo{Owner: "o", Name: "r"}, Head: "a2a/x/y", Base: "main", Title: "t",
	})
	if err == nil {
		t.Fatal("an unrecognised GraphQL failure was swallowed as a known limitation")
	}
	if !errors.Is(err, ErrGraphQLFailed) {
		t.Errorf("err = %v, want ErrGraphQLFailed", err)
	}
}

// A successful arming reports armed and says nothing.
func TestOpenPRArmedReportsNoNote(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			_, _ = w.Write([]byte(`{"data":{"enablePullRequestAutoMerge":{"clientMutationId":null}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"number":9,"html_url":"https://x/pull/9","node_id":"PR_9","state":"open"}`))
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	pr, err := h.OpenPR(context.Background(), OpenPRRequest{
		Repo: Repo{Owner: "o", Name: "r"}, Head: "a2a/x/y", Base: "main", Title: "t",
	})
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if !pr.AutoMergeArmed || pr.AutoMergeNote != "" {
		t.Fatalf("PRInfo = %+v, want armed with no note", pr)
	}
}
