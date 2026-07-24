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
		{"name": "some-a2a-validate / validate", "status": "completed", "conclusion": "success"},
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
}
