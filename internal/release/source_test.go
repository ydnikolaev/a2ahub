package release

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubSource_Latest(t *testing.T) {
	t.Parallel()

	releases := []ghRelease{
		{TagName: "v0.4.0", Draft: false, Prerelease: true, TargetCommitish: "main"}, // skipped: pre-release
		{TagName: "not-a-version", Draft: false, Prerelease: false},                  // skipped: malformed tag
		{TagName: "v0.3.0", Draft: true, Prerelease: false},                          // skipped: draft
		{
			TagName:         "v0.2.0",
			TargetCommitish: "abc123",
			Assets: []ghAsset{
				{Name: "a2a-linux-amd64", URL: "https://api.example/asset/1", BrowserDownloadURL: "https://dl.example/asset/1", Size: 42},
			},
		},
		{TagName: "v0.1.0"}, // would also be usable, but v0.2.0 comes first
	}

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/repos/ydnikolaev/a2ahub/releases" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(releases)
	}))
	defer srv.Close()

	t.Run("tokened path sends Authorization", func(t *testing.T) {
		src := &GitHubSource{Client: srv.Client(), BaseURL: srv.URL, Repo: "ydnikolaev/a2ahub", Token: "tok-123"}
		rel, err := src.Latest(context.Background())
		if err != nil {
			t.Fatalf("Latest: %v", err)
		}
		if gotAuth != "Bearer tok-123" {
			t.Fatalf("Authorization header = %q, want Bearer tok-123", gotAuth)
		}
		if rel.Tag != "v0.2.0" || rel.Version != "0.2.0" || rel.Commit != "abc123" {
			t.Fatalf("Latest() = %+v, want tag v0.2.0/version 0.2.0/commit abc123", rel)
		}
		if len(rel.Assets) != 1 || rel.Assets[0].Name != "a2a-linux-amd64" {
			t.Fatalf("Latest().Assets = %+v, want one a2a-linux-amd64 asset", rel.Assets)
		}
	})

	t.Run("tokenless path sends no Authorization", func(t *testing.T) {
		src := &GitHubSource{Client: srv.Client(), BaseURL: srv.URL, Repo: "ydnikolaev/a2ahub"}
		if _, err := src.Latest(context.Background()); err != nil {
			t.Fatalf("Latest: %v", err)
		}
		if gotAuth != "" {
			t.Fatalf("Authorization header = %q, want empty (tokenless)", gotAuth)
		}
	})
}

func TestGitHubSource_Latest_NoUsableRelease(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body []ghRelease
	}{
		{name: "empty list", body: []ghRelease{}},
		{name: "only prerelease/draft/malformed", body: []ghRelease{
			{TagName: "v1.0.0", Prerelease: true},
			{TagName: "v1.0.1", Draft: true},
			{TagName: "garbage"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()

			src := &GitHubSource{Client: srv.Client(), BaseURL: srv.URL, Repo: "ydnikolaev/a2ahub"}
			_, err := src.Latest(context.Background())
			if !errors.Is(err, ErrNoRelease) {
				t.Fatalf("Latest() error = %v, want ErrNoRelease", err)
			}
		})
	}
}

func TestGitHubSource_Latest_404(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	src := &GitHubSource{Client: srv.Client(), BaseURL: srv.URL, Repo: "ydnikolaev/a2ahub"}
	_, err := src.Latest(context.Background())
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("Latest() error = %v, want ErrNoRelease", err)
	}
}

// TestResolveToken_EnvOrder asserts the T3 token order
// A2A_UPDATE_TOKEN -> GH_TOKEN -> GITHUB_TOKEN -> "". Runs non-parallel
// (t.Setenv panics under t.Parallel).
func TestResolveToken_EnvOrder(t *testing.T) {
	cases := []struct {
		name                            string
		a2aUpdate, ghToken, githubToken string
		want                            string
	}{
		{name: "none set", want: ""},
		{name: "GITHUB_TOKEN only", githubToken: "gh-tok", want: "gh-tok"},
		{name: "GH_TOKEN wins over GITHUB_TOKEN", ghToken: "gh2", githubToken: "gh3", want: "gh2"},
		{name: "A2A_UPDATE_TOKEN wins over both", a2aUpdate: "a2a-tok", ghToken: "gh2", githubToken: "gh3", want: "a2a-tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("A2A_UPDATE_TOKEN", tc.a2aUpdate)
			t.Setenv("GH_TOKEN", tc.ghToken)
			t.Setenv("GITHUB_TOKEN", tc.githubToken)
			if got := ResolveToken(); got != tc.want {
				t.Fatalf("ResolveToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewGitHubSource_Defaults(t *testing.T) {
	t.Parallel()
	src := NewGitHubSource(nil, "", "ydnikolaev/a2ahub")
	if src.Client == nil {
		t.Fatal("NewGitHubSource: Client not defaulted")
	}
	if src.BaseURL != defaultAPIBaseURL {
		t.Fatalf("NewGitHubSource: BaseURL = %q, want %q", src.BaseURL, defaultAPIBaseURL)
	}
	if src.Name() != "github" {
		t.Fatalf("Name() = %q, want github", src.Name())
	}
}
