package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestOpenPRAutoMerge is spec 05 §8 AC row 2: the GitHub implementation
// pushes a2a/<system>/<id> and opens a PR with auto-merge enabled,
// returning PR number/URL, against a controlled httptest server (no live
// GitHub call).
func TestOpenPRAutoMerge(t *testing.T) {
	t.Parallel()

	var sawCreate, sawAutoMergeMutation bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/space/pulls":
			sawCreate = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body["head"] != "a2a/axon/XQ-axon-1" {
				t.Errorf("head = %v, want a2a/axon/XQ-axon-1", body["head"])
			}
			if body["base"] != "main" {
				t.Errorf("base = %v, want main", body["base"])
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   42,
				"html_url": "https://example.invalid/pr/42",
				"node_id":  "PR_kwabc",
				"state":    "open",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			sawAutoMergeMutation = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql body: %v", err)
			}
			query, _ := body["query"].(string)
			if !strings.Contains(query, "enablePullRequestAutoMerge") {
				t.Errorf("graphql query missing enablePullRequestAutoMerge mutation: %s", query)
			}
			vars, _ := body["variables"].(map[string]any)
			if vars["id"] != "PR_kwabc" {
				t.Errorf("mutation variables[id] = %v, want PR_kwabc", vars["id"])
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	got, err := h.OpenPR(context.Background(), OpenPRRequest{
		Repo:       Repo{Owner: "acme", Name: "space"},
		Head:       "a2a/axon/XQ-axon-1",
		Base:       "main",
		Title:      "a2a(question): XQ-axon-1",
		Credential: Credential{Token: "test-token"},
	})
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}
	if !sawCreate || !sawAutoMergeMutation {
		t.Fatalf("expected both PR-create REST call and auto-merge GraphQL mutation, got create=%v mutation=%v", sawCreate, sawAutoMergeMutation)
	}
	if got.Number != 42 || got.URL != "https://example.invalid/pr/42" {
		t.Fatalf("OpenPR result = %+v, want number=42 url=https://example.invalid/pr/42", got)
	}
}

// TestPushBranchPushesToRemote exercises PushBranch's git plumbing against
// a real (local, no-network) bare repo built by testkit/spacefixture —
// rails pre-flight #6, no hand-rolled git plumbing in this test.
func TestPushBranchPushesToRemote(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	repoDir := fx.Clone("axon")
	commitInDir(t, repoDir, "axon/exchanges/XQ-axon-1.md", "content")

	h := NewGitHubHost(nil, "")
	res, err := h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir:   repoDir,
		LocalRef:  "HEAD",
		Branch:    "a2a/axon/XQ-axon-1",
		RemoteURL: fx.RemoteURL(),
	})
	if err != nil {
		t.Fatalf("PushBranch: %v", err)
	}
	if res.Branch != "a2a/axon/XQ-axon-1" {
		t.Fatalf("PushBranchResult.Branch = %q, want a2a/axon/XQ-axon-1", res.Branch)
	}

	// Confirm the branch actually landed on origin (not partial/local-only)
	// by cloning origin fresh and checking the pushed branch exists there.
	otherDir := t.TempDir()
	runGitClone(t, fx.RemoteURL(), otherDir)
	got := fx.HeadSHA(otherDir, "refs/remotes/origin/a2a/axon/XQ-axon-1")
	want := fx.HeadSHA(repoDir, "HEAD")
	if got == "" || got != want {
		t.Fatalf("pushed branch head = %q, want %q (local HEAD)", got, want)
	}
}

// TestPushBranchRejected exercises the CC-061 push-rejection error path: a
// non-fast-forward push to a branch that already has diverging history on
// the remote is rejected atomically (no partial state) and surfaces as a
// typed error wrapping ErrPushRejected. (Live credential-revocation is
// P10/P11's live-GitHub concern; this fixture exercises the same rejection
// class without network.)
func TestPushBranchRejected(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	repoDir := fx.Clone("axon")
	branch := "a2a/axon/XQ-axon-2"

	// First push establishes the branch on origin.
	commitInDir(t, repoDir, "axon/exchanges/XQ-axon-2.md", "v1")
	h := NewGitHubHost(nil, "")
	if _, err := h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir: repoDir, LocalRef: "HEAD", Branch: branch, RemoteURL: fx.RemoteURL(),
	}); err != nil {
		t.Fatalf("first PushBranch: %v", err)
	}

	// A second, independent clone (branched off the ORIGINAL main tip,
	// before the first push) commits DIVERGING history and tries to push
	// it to the same branch name: origin's branch already moved past this
	// clone's ancestry, so the push is a non-fast-forward and must be
	// rejected atomically (no partial state) as a typed error.
	otherDir := t.TempDir()
	runGitClone(t, fx.RemoteURL(), otherDir)
	commitInDir(t, otherDir, "axon/exchanges/XQ-axon-2.md", "v2-from-other-clone")
	_, err := h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir: otherDir, LocalRef: "HEAD", Branch: branch, RemoteURL: fx.RemoteURL(),
	})
	if err == nil {
		t.Fatal("expected diverging push to be rejected, got nil error")
	}
	var hostErr *Error
	if !errors.As(err, &hostErr) {
		t.Fatalf("expected *host.Error, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("expected errors.Is(err, ErrPushRejected), got %v", err)
	}

	// No partial state: the remote branch still points at the first
	// push's commit, unchanged by the rejected attempt.
	checkDir := t.TempDir()
	runGitClone(t, fx.RemoteURL(), checkDir)
	remoteHead := fx.HeadSHA(checkDir, "refs/remotes/origin/"+branch)
	firstPushHead := fx.HeadSHA(repoDir, "HEAD")
	if remoteHead != firstPushHead {
		t.Fatalf("remote branch head = %q after rejected push, want unchanged %q", remoteHead, firstPushHead)
	}
}
