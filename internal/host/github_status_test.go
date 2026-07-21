package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckStatusReadsNamedCheckRun(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/space/pulls/7":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"head": map[string]any{"sha": "deadbeef"},
			})
		case "/repos/acme/space/commits/deadbeef/check-runs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"check_runs": []map[string]any{
					{"status": "completed", "conclusion": "success"},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	got, err := h.CheckStatus(context.Background(), StatusRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7,
	})
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if got.State != "completed" || got.Conclusion != "success" {
		t.Fatalf("CheckStatus = %+v, want completed/success", got)
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
