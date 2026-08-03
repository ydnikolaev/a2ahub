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
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/space":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"allow_merge_commit": true})
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
		Repo:            Repo{Owner: "acme", Name: "space"},
		Head:            "a2a/axon/XQ-axon-1",
		Base:            "main",
		Title:           "a2a(question): XQ-axon-1",
		ExpectedHeadSHA: "green-sha",
		Credential:      Credential{Token: "test-token"},
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

func TestPushBranchForceWithExactLeaseReplacesOrphanAndRejectsStaleLease(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "first", "retry", "stale")
	branch := "a2a/feedback/submit/fb-20260728-abc123"
	firstDir := fx.Clone("first")
	commitInDir(t, firstDir, "feedback/inbox/old.yaml", "orphan")
	h := NewGitHubHost(nil, "")
	if _, err := h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir: firstDir, LocalRef: "HEAD", Branch: branch, RemoteURL: fx.RemoteURL(),
	}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	firstHead := fx.HeadSHA(firstDir, "HEAD")

	observed, err := h.ReadRemoteBranch(context.Background(), RemoteBranchRequest{
		RepoDir: firstDir, RemoteURL: fx.RemoteURL(), Branch: branch,
	})
	if err != nil {
		t.Fatalf("ReadRemoteBranch: %v", err)
	}
	if !observed.Exists || observed.SHA != firstHead {
		t.Fatalf("observed = %+v, want existing %s", observed, firstHead)
	}

	retryDir := fx.Clone("retry")
	commitInDir(t, retryDir, "feedback/inbox/new.yaml", "retry")
	if _, err := h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir: retryDir, LocalRef: "HEAD", Branch: branch, RemoteURL: fx.RemoteURL(),
		ForceWithLeaseSHA: observed.SHA,
	}); err != nil {
		t.Fatalf("replace orphan with exact lease: %v", err)
	}
	retryHead := fx.HeadSHA(retryDir, "HEAD")

	staleDir := fx.Clone("stale")
	commitInDir(t, staleDir, "feedback/inbox/stale.yaml", "stale")
	_, err = h.PushBranch(context.Background(), PushBranchRequest{
		RepoDir: staleDir, LocalRef: "HEAD", Branch: branch, RemoteURL: fx.RemoteURL(),
		ForceWithLeaseSHA: firstHead,
	})
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("stale lease err = %v, want ErrPushRejected", err)
	}
	checkDir := t.TempDir()
	runGitClone(t, fx.RemoteURL(), checkDir)
	if got := fx.HeadSHA(checkDir, "refs/remotes/origin/"+branch); got != retryHead {
		t.Fatalf("stale lease changed remote to %s, want %s", got, retryHead)
	}
}

// TestAutoMergeAllowed is WAVE M2 / spec 45 AC-1050.5's host-layer half: the
// repo-settings read reports `allow_auto_merge` verbatim on success, and a
// failed read (403 here — the "no permission" case AC-1050.5/spec 42 §6
// name explicitly) returns a non-nil error rather than a false "off" — the
// distinction doctorCheckAutoMerge depends on to never render a failed read
// as a PASS or a plain "off".
func TestAutoMergeAllowed(t *testing.T) {
	t.Parallel()

	t.Run("on", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/space" {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"allow_auto_merge": true})
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		allowed, err := h.AutoMergeAllowed(context.Background(), RepoSettingsRequest{
			Repo: Repo{Owner: "acme", Name: "space"}, Credential: Credential{Token: "tok"},
		})
		if err != nil {
			t.Fatalf("AutoMergeAllowed: %v", err)
		}
		if !allowed {
			t.Fatal("allowed = false, want true")
		}
	})

	t.Run("off", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"allow_auto_merge": false})
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		allowed, err := h.AutoMergeAllowed(context.Background(), RepoSettingsRequest{
			Repo: Repo{Owner: "acme", Name: "space"}, Credential: Credential{Token: "tok"},
		})
		if err != nil {
			t.Fatalf("AutoMergeAllowed: %v", err)
		}
		if allowed {
			t.Fatal("allowed = true, want false")
		}
	})

	t.Run("read fails, distinct from off", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"message":"Not Found"}`, http.StatusForbidden)
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		allowed, err := h.AutoMergeAllowed(context.Background(), RepoSettingsRequest{
			Repo: Repo{Owner: "acme", Name: "space"}, Credential: Credential{Token: "tok"},
		})
		if err == nil {
			t.Fatal("expected a non-nil error on a failed read, got nil")
		}
		if allowed {
			t.Fatal("allowed = true on a failed read, want false (never claim ON on a failed read)")
		}
		if !errors.Is(err, ErrRequestFailed) {
			t.Fatalf("expected errors.Is(err, ErrRequestFailed), got %v", err)
		}
	})

	t.Run("invalid request", func(t *testing.T) {
		t.Parallel()
		h := NewGitHubHost(nil, "")
		if _, err := h.AutoMergeAllowed(context.Background(), RepoSettingsRequest{}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected errors.Is(err, ErrInvalidRequest), got %v", err)
		}
	})
}

// TestRepoPermissions is this phase's (doctor "space scaffolding current"
// row) host-layer half: RepoPermissions reads the SAME GET
// /repos/{owner}/{repo} endpoint AutoMergeAllowed reads and decodes its
// `permissions` object, rather than issuing a second request against a
// different endpoint.
func TestRepoPermissions(t *testing.T) {
	t.Parallel()

	t.Run("push access", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/space" {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"allow_auto_merge": true,
				"permissions":      map[string]any{"admin": false, "push": true, "pull": true},
			})
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		perm, err := h.RepoPermissions(context.Background(), RepoSettingsRequest{
			Repo: Repo{Owner: "acme", Name: "space"}, Credential: Credential{Token: "tok"},
		})
		if err != nil {
			t.Fatalf("RepoPermissions: %v", err)
		}
		if !perm.Push || perm.Admin || !perm.Pull {
			t.Fatalf("perm = %+v, want push=true admin=false pull=true", perm)
		}
	})

	t.Run("read-only access", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"permissions": map[string]any{"admin": false, "push": false, "pull": true},
			})
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		perm, err := h.RepoPermissions(context.Background(), RepoSettingsRequest{
			Repo: Repo{Owner: "acme", Name: "space"}, Credential: Credential{Token: "tok"},
		})
		if err != nil {
			t.Fatalf("RepoPermissions: %v", err)
		}
		if perm.Push || perm.Admin {
			t.Fatalf("perm = %+v, want no push/admin", perm)
		}
	})

	t.Run("read fails", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"message":"Forbidden"}`, http.StatusForbidden)
		}))
		defer srv.Close()

		h := NewGitHubHost(srv.Client(), srv.URL)
		if _, err := h.RepoPermissions(context.Background(), RepoSettingsRequest{
			Repo: Repo{Owner: "acme", Name: "space"}, Credential: Credential{Token: "tok"},
		}); !errors.Is(err, ErrRequestFailed) {
			t.Fatalf("expected errors.Is(err, ErrRequestFailed), got %v", err)
		}
	})

	t.Run("invalid request", func(t *testing.T) {
		t.Parallel()
		h := NewGitHubHost(nil, "")
		if _, err := h.RepoPermissions(context.Background(), RepoSettingsRequest{}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected errors.Is(err, ErrInvalidRequest), got %v", err)
		}
	})
}

// TestMergePRSendsExplicitMergeMethod is WAVE M4's host-layer half of
// AC-1050.12: MergePR PUTs /repos/{o}/{r}/pulls/{n}/merge with an EXPLICIT
// merge_method chosen from the repository's own allowed set — never left to
// GitHub's implicit repository default, the way armAutoMerge's call is (see
// MergePR's doc for why the two paths must not diverge).
func TestMergePRSendsExplicitMergeMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		allowMergeCommit bool
		allowSquash      bool
		allowRebase      bool
		wantMethod       string
	}{
		{name: "merge commit preferred when allowed", allowMergeCommit: true, allowSquash: true, allowRebase: true, wantMethod: "merge"},
		{name: "falls back to squash when merge commit disallowed", allowMergeCommit: false, allowSquash: true, allowRebase: true, wantMethod: "squash"},
		{name: "falls back to rebase when only rebase allowed", allowMergeCommit: false, allowSquash: false, allowRebase: true, wantMethod: "rebase"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var sawMethod, sawSHA string
			var sawPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/space":
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"allow_merge_commit": tc.allowMergeCommit,
						"allow_squash_merge": tc.allowSquash,
						"allow_rebase_merge": tc.allowRebase,
					})
				case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/space/pulls/7/merge":
					sawPath = r.URL.Path
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatalf("decode merge body: %v", err)
					}
					sawMethod, _ = body["merge_method"].(string)
					sawSHA, _ = body["sha"].(string)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"merged": true})
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			}))
			defer srv.Close()

			h := NewGitHubHost(srv.Client(), srv.URL)
			method, err := h.MergePR(context.Background(), MergePRRequest{
				Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha", Credential: Credential{Token: "tok"},
			})
			if err != nil {
				t.Fatalf("MergePR: %v", err)
			}
			if sawPath != "/repos/acme/space/pulls/7/merge" {
				t.Fatalf("merge request path = %q, want /repos/acme/space/pulls/7/merge", sawPath)
			}
			if sawMethod != tc.wantMethod {
				t.Fatalf("merge_method = %q, want %q", sawMethod, tc.wantMethod)
			}
			if method != MergeMethod(tc.wantMethod) || sawSHA != "green-sha" {
				t.Fatalf("result/body = method %q sha %q, want %q/green-sha", method, sawSHA, tc.wantMethod)
			}
		})
	}
}

// TestMergePRNoMethodAllowed is the "guess nothing" half: a repository that
// disallows every merge method gets a typed, actionable error instead of a
// request GitHub would refuse anyway.
func TestMergePRNoMethodAllowed(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s — MergePR must not attempt a merge with no allowed method", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"allow_merge_commit": false, "allow_squash_merge": false, "allow_rebase_merge": false,
		})
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	_, err := h.MergePR(context.Background(), MergePRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha", Credential: Credential{Token: "tok"},
	})
	if !errors.Is(err, ErrMergeMethodUnavailable) {
		t.Fatalf("expected errors.Is(err, ErrMergeMethodUnavailable), got %v", err)
	}
}

// TestMergePRStatusOutcomes is WAVE M4's 405/409 distinguishability
// requirement: a head that moved under us (409) must never be confused with
// "not mergeable right now" (405), because a caller (the funnel) must NEVER
// report a 409 as a landed write — the commit that would actually be merged
// is not the one it verified green.
func TestMergePRStatusOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		wantErr    error
		wantOthers []error // must NOT match
	}{
		{name: "405 not mergeable", status: http.StatusMethodNotAllowed, wantErr: ErrPRNotMergeable, wantOthers: []error{ErrPRHeadMoved}},
		{name: "409 head moved", status: http.StatusConflict, wantErr: ErrPRHeadMoved, wantOthers: []error{ErrPRNotMergeable}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"allow_merge_commit": true})
				case http.MethodPut:
					http.Error(w, `{"message":"refused"}`, tc.status)
				}
			}))
			defer srv.Close()

			h := NewGitHubHost(srv.Client(), srv.URL)
			method, err := h.MergePR(context.Background(), MergePRRequest{
				Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha", Credential: Credential{Token: "tok"},
			})
			if method != MergeMethodMerge {
				t.Fatalf("MergePR method = %q, want merge even on provider refusal", method)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("errors.Is(err, %v) = false, err = %v", tc.wantErr, err)
			}
			for _, other := range tc.wantOthers {
				if errors.Is(err, other) {
					t.Fatalf("err unexpectedly also matches %v (must be distinguishable): %v", other, err)
				}
			}
		})
	}
}

// TestMergePRInvalidRequest guards the missing-field precondition every
// other host operation enforces.
func TestMergePRInvalidRequest(t *testing.T) {
	t.Parallel()
	h := NewGitHubHost(nil, "")
	if _, err := h.MergePR(context.Background(), MergePRRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected errors.Is(err, ErrInvalidRequest), got %v", err)
	}
}
