package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAPITokenFallsBackToTheGitHubCLILogin pins the step whose absence shipped:
// with no A2A_UPDATE_TOKEN / GH_TOKEN / GITHUB_TOKEN set — the normal state of
// a developer machine — the releases-list read went out anonymous, onto
// GitHub's 60-per-hour per-IP budget, and `a2a update` died with
// "403 API rate limit exceeded" while a working `gh` login sat unasked.
func TestAPITokenFallsBackToTheGitHubCLILogin(t *testing.T) {
	t.Setenv("A2A_UPDATE_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	restore := lookupGitHubCLIToken
	t.Cleanup(func() { lookupGitHubCLIToken = restore })
	lookupGitHubCLIToken = func(context.Context) (string, bool) { return "from-gh-cli", true }

	s := &GitHubSource{Token: ResolveToken()}
	if got := s.apiToken(); got != "from-gh-cli" {
		t.Fatalf("apiToken() = %q, want the gh CLI token", got)
	}
}

// TestAPITokenPrefersAnExplicitToken keeps the fallback from overriding a
// deliberately-set identity.
func TestAPITokenPrefersAnExplicitToken(t *testing.T) {
	restore := lookupGitHubCLIToken
	t.Cleanup(func() { lookupGitHubCLIToken = restore })
	lookupGitHubCLIToken = func(context.Context) (string, bool) {
		t.Error("the gh CLI must not be consulted when an explicit token is set")
		return "from-gh-cli", true
	}

	s := &GitHubSource{Token: "explicit"}
	if got := s.apiToken(); got != "explicit" {
		t.Fatalf("apiToken() = %q, want the explicit token", got)
	}
}

// TestResolveTokenStaysEnvOnly is the guard on the distinction the update
// tests taught, and it is the one worth stating loudly.
//
// ResolveToken feeds fetchAsset, which SELECTS A DOWNLOAD ROUTE on it: empty
// means the public browser_download_url (CDN, unmetered), non-empty means the
// private release-asset API (metered). A gh fallback here would move every
// machine with gh installed off the CDN and onto the rate-limited API for its
// downloads — strictly worse than the problem being fixed, and invisible until
// somebody read why 11 update tests went red.
func TestResolveTokenStaysEnvOnly(t *testing.T) {
	t.Setenv("A2A_UPDATE_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	restore := lookupGitHubCLIToken
	t.Cleanup(func() { lookupGitHubCLIToken = restore })
	lookupGitHubCLIToken = func(context.Context) (string, bool) { return "from-gh-cli", true }

	if got := ResolveToken(); got != "" {
		t.Fatalf("ResolveToken() = %q, want empty — a gh login must not switch the asset download off the public CDN", got)
	}
}

// TestLatestSendsTheFallbackTokenOnTheWire proves the fallback reaches the
// actual request rather than only the helper — the assertion that would have
// caught this being wired in the wrong place.
func TestLatestSendsTheFallbackTokenOnTheWire(t *testing.T) {
	t.Setenv("A2A_UPDATE_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	restore := lookupGitHubCLIToken
	t.Cleanup(func() { lookupGitHubCLIToken = restore })
	lookupGitHubCLIToken = func(context.Context) (string, bool) { return "from-gh-cli", true }

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	s := &GitHubSource{Client: srv.Client(), BaseURL: srv.URL, Repo: "o/r", Token: ResolveToken()}
	_, _ = s.Latest(context.Background())

	if gotAuth != "Bearer from-gh-cli" {
		t.Fatalf("Authorization = %q, want the gh CLI token on the releases read", gotAuth)
	}
}
