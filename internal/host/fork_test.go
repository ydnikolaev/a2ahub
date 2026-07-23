package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestEnsureForkCreatesAndWaits is P28 AC-904.1's host half: EnsureFork
// POSTs to the forks endpoint and does not return until GitHub reports the
// new repository readable (fork creation is asynchronous, so the first
// read can 404).
func TestEnsureForkCreatesAndWaits(t *testing.T) {
	t.Parallel()

	var forkCalls, readCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/ydnikolaev/a2ahub/forks":
			forkCalls++
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":      "a2ahub",
				"clone_url": "https://github.com/consumer/a2ahub.git",
				"owner":     map[string]any{"login": "consumer"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/consumer/a2ahub":
			readCalls++
			if readCalls == 1 { // the fork has not materialised yet
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "a2ahub"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	h.ForkReadyDelay = time.Millisecond

	fork, err := h.EnsureFork(context.Background(), EnsureForkRequest{
		Repo:       Repo{Owner: "ydnikolaev", Name: "a2ahub"},
		Credential: Credential{Token: "test-token"},
	})
	if err != nil {
		t.Fatalf("EnsureFork: %v", err)
	}
	if fork.Repo != (Repo{Owner: "consumer", Name: "a2ahub"}) {
		t.Errorf("fork repo = %+v, want consumer/a2ahub", fork.Repo)
	}
	if fork.RemoteURL != "https://github.com/consumer/a2ahub.git" {
		t.Errorf("fork remote = %q", fork.RemoteURL)
	}
	if forkCalls != 1 || readCalls != 2 {
		t.Errorf("forkCalls/readCalls = %d/%d, want 1/2 (one create, one 404 then one hit)", forkCalls, readCalls)
	}
}

// TestEnsureForkUnavailable is AC-904.4's host half: a fork that cannot be
// created is an error wrapping ErrForkUnavailable — never a silent
// degradation to "no fork, carry on".
func TestEnsureForkUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("create refused", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		h.ForkReadyDelay = time.Millisecond
		_, err := h.EnsureFork(context.Background(), EnsureForkRequest{Repo: Repo{Owner: "o", Name: "r"}})
		if !errors.Is(err, ErrForkUnavailable) {
			t.Fatalf("err = %v, want ErrForkUnavailable", err)
		}
	})

	t.Run("never becomes readable", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name": "r", "owner": map[string]any{"login": "consumer"},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		h.ForkReadyAttempts = 2
		h.ForkReadyDelay = time.Millisecond
		_, err := h.EnsureFork(context.Background(), EnsureForkRequest{Repo: Repo{Owner: "o", Name: "r"}})
		if !errors.Is(err, ErrForkUnavailable) {
			t.Fatalf("err = %v, want ErrForkUnavailable", err)
		}
	})

	t.Run("missing repo", func(t *testing.T) {
		t.Parallel()
		h := NewGitHubHost(nil, "https://example.invalid")
		_, err := h.EnsureFork(context.Background(), EnsureForkRequest{})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("err = %v, want ErrInvalidRequest", err)
		}
	})
}

// TestFindPRByHeadBranchQualifiesAForkHead pins the AC-904.3 lookup: a
// cross-fork PR is only visible under `<forkOwner>:<branch>`, so the
// request's HeadOwner must reach GitHub's head filter.
func TestFindPRByHeadBranchQualifiesAForkHead(t *testing.T) {
	t.Parallel()

	var gotHead string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHead = r.URL.Query().Get("head")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 7, "html_url": "https://example.invalid/pr/7", "state": "open"},
		})
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	pr, err := h.FindPRByHeadBranch(context.Background(), FindPRRequest{
		Repo:      Repo{Owner: "ydnikolaev", Name: "a2ahub"},
		Branch:    "a2a/feedback/fb-1",
		HeadOwner: "consumer",
	})
	if err != nil {
		t.Fatalf("FindPRByHeadBranch: %v", err)
	}
	if pr == nil || pr.Number != 7 {
		t.Fatalf("pr = %+v, want #7", pr)
	}
	if gotHead != "consumer:a2a/feedback/fb-1" {
		t.Errorf("head filter = %q, want consumer:a2a/feedback/fb-1", gotHead)
	}
}

// TestPushForbiddenClassification pins the ONE signal a refused push
// carries — git's stderr. An unrecognised refusal must stay a plain
// ErrPushRejected: forking does not fix a non-fast-forward or a protected
// branch, so the fallback must not fire there.
func TestPushForbiddenClassification(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		"remote: Permission to ydnikolaev/a2ahub.git denied to consumer.\nfatal: unable to access ...: The requested URL returned error: 403",
		"remote: Write access to repository not granted.",
		"git@github.com: Permission denied (publickey).",
	}
	for _, stderr := range forbidden {
		if !pushForbidden(stderr) {
			t.Errorf("pushForbidden(%q) = false, want true", stderr)
		}
	}

	notForbidden := []string{
		"! [rejected]        main -> main (non-fast-forward)",
		"remote: error: GH006: Protected branch update failed.",
		"fatal: repository 'https://example.invalid/o/r.git/' not found",
	}
	for _, stderr := range notForbidden {
		if pushForbidden(stderr) {
			t.Errorf("pushForbidden(%q) = true, want false", stderr)
		}
	}
}

// TestPushBranchNonPermissionFailureStaysRejected drives the classifier
// through the real command: a push to a directory that is not a repository
// fails, and must NOT be reported as a permission refusal.
func TestPushBranchNonPermissionFailureStaysRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	runGit(t, dir, nil, "init", "-b", "main")
	commitInDir(t, dir, "a.md", "hello\n")

	h := NewGitHubHost(nil, "")
	_, err := h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir: dir, LocalRef: "HEAD", Branch: "a2a/x/y",
		RemoteURL: filepath.Join(t.TempDir(), "not-a-repo"),
	})
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("err = %v, want ErrPushRejected", err)
	}
	if errors.Is(err, ErrPushForbidden) {
		t.Fatalf("err = %v, must not be classified as a permission refusal", err)
	}
}
