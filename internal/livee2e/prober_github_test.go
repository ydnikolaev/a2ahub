package livee2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func proberFor(t *testing.T, h http.HandlerFunc) *GitHubProber {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &GitHubProber{APIRoot: srv.URL, Client: srv.Client()}
}

func TestGitHubProberLogin(t *testing.T) {
	t.Parallel()
	var gotAuth, gotPath string
	p := proberFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"login":"a2a-e2e-test-bot"}`))
	})

	login, err := p.Login(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if login != "a2a-e2e-test-bot" {
		t.Errorf("login = %q", login)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotPath != "/user" {
		t.Errorf("path = %q", gotPath)
	}
}

// GitHub reports permissions cumulatively — an admin also has push and pull —
// so the flags must be read most-privileged first. Reading them in any other
// order classifies an admin as a writer, which is exactly the
// misclassification the anti-vacuity guard exists to catch.
func TestGitHubProberRepoPermissionReadsMostPrivilegedFirst(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		want Permission
	}{
		{"admin implies push and pull", `{"permissions":{"admin":true,"push":true,"pull":true}}`, PermissionAdmin},
		{"write implies pull", `{"permissions":{"admin":false,"push":true,"pull":true}}`, PermissionWrite},
		{"read only", `{"permissions":{"admin":false,"push":false,"pull":true}}`, PermissionRead},
		{"no access", `{"permissions":{"admin":false,"push":false,"pull":false}}`, PermissionNone},
		{"absent block", `{}`, PermissionNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := proberFor(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			})
			got, err := p.RepoPermission(context.Background(), "org", "repo", "tok")
			if err != nil {
				t.Fatalf("RepoPermission: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGitHubProberRepoPermissionPath(t *testing.T) {
	t.Parallel()
	var gotPath string
	p := proberFor(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"permissions":{"push":true,"pull":true}}`))
	})
	if _, err := p.RepoPermission(context.Background(), "a2ahub-live-e2e", "live-e2e-space", "tok"); err != nil {
		t.Fatalf("RepoPermission: %v", err)
	}
	if gotPath != "/repos/a2ahub-live-e2e/live-e2e-space" {
		t.Errorf("path = %q", gotPath)
	}
}

// A probe that could not read the answer must never be mistaken for an
// answer. 403 in particular is what a missing permission looks like, and
// defaulting it to PermissionNone would let a boundary scenario run against
// an identity nobody actually verified.
func TestGitHubProberSurfacesHTTPErrors(t *testing.T) {
	t.Parallel()
	p := proberFor(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
	})

	if _, err := p.RepoPermission(context.Background(), "org", "repo", "tok"); err == nil {
		t.Fatal("403 did not produce an error")
	} else if !strings.Contains(err.Error(), "not accessible by personal access token") {
		t.Errorf("error loses GitHub's actionable message: %v", err)
	}

	if _, err := p.Login(context.Background(), "tok"); err == nil {
		t.Fatal("403 on /user did not produce an error")
	}
}

func TestGitHubProberSurfacesMalformedJSON(t *testing.T) {
	t.Parallel()
	p := proberFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})
	if _, err := p.Login(context.Background(), "tok"); err == nil {
		t.Fatal("malformed body did not produce an error")
	}
}

func TestGitHubProberDefaultsToPublicAPI(t *testing.T) {
	t.Parallel()
	var p GitHubProber
	if got := p.root(); got != defaultAPIRoot {
		t.Errorf("root() = %q, want %q", got, defaultAPIRoot)
	}
	if p.client().Timeout == 0 {
		t.Error("default client has no timeout — a hung probe would stall the run with no verdict to show")
	}
}
