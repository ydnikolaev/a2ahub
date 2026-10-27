package host

import (
	"net/http"
	"testing"
)

// reason: mutates process env (t.Setenv) — not run in parallel.
func TestOpen_PathIsGitHubUnlessMarked(t *testing.T) {
	t.Setenv("A2A_GITHUB_API", "http://env-value.invalid")

	loc, err := ParseLocator("/tmp/x/some-org/some-hub")
	if err != nil {
		t.Fatal(err)
	}
	if loc.class != classPath {
		t.Fatalf("class = %v, want path", loc.class)
	}
	h, err := Open(loc, OpenConfig{GitHubAPIBase: "http://cfg-value.invalid"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	gh, ok := h.(*GitHubHost)
	if !ok {
		t.Fatalf("Open path = %T, want *GitHubHost", h)
	}
	if gh.BaseURL != "http://cfg-value.invalid" {
		t.Errorf("BaseURL = %q, want the cfg value (the environment must not participate)", gh.BaseURL)
	}
}

func TestOpen_URLIsGitHub(t *testing.T) {
	t.Parallel()
	client := &http.Client{}
	for _, raw := range []string{"https://github.com/o/r", "git@github.com:o/r", "https://ghe.corp/o/r"} {
		loc, err := ParseLocator(raw)
		if err != nil {
			t.Fatal(err)
		}
		h, err := Open(loc, OpenConfig{HTTPClient: client, GitHubAPIBase: "http://api.test"})
		if err != nil {
			t.Fatalf("Open(%q): %v", raw, err)
		}
		gh, ok := h.(*GitHubHost)
		if !ok {
			t.Fatalf("Open(%q) = %T, want *GitHubHost", raw, h)
		}
		if gh.BaseURL != "http://api.test" || gh.Client != client {
			t.Errorf("Open(%q) = base %q client %p, want cfg's", raw, gh.BaseURL, gh.Client)
		}
	}
}

func TestOpen_EmptyAPIBaseFallsToDefault(t *testing.T) {
	t.Parallel()
	loc, err := ParseLocator("https://github.com/o/r")
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(loc, OpenConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.(*GitHubHost).BaseURL; got != "https://api.github.com" {
		t.Errorf("BaseURL = %q, want the GitHub default", got)
	}
}

func TestOpenGitHub_UsesConfig(t *testing.T) {
	t.Parallel()
	gh := OpenGitHub(OpenConfig{GitHubAPIBase: "http://api.test"})
	if gh.BaseURL != "http://api.test" || gh.Client != http.DefaultClient {
		t.Errorf("OpenGitHub = base %q client %p", gh.BaseURL, gh.Client)
	}
}

func TestNewOpener_ReturnsHostAndLegacyIdentity(t *testing.T) {
	t.Parallel()
	open := NewOpener(OpenConfig{GitHubAPIBase: "http://api.test"})
	h, repo, err := open("https://github.com/acme/space.git")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.(*GitHubHost); !ok {
		t.Errorf("host = %T, want *GitHubHost", h)
	}
	if repo != (Repo{Owner: "acme", Name: "space"}) {
		t.Errorf("repo = %+v", repo)
	}
	if _, _, err := open("not-a-url"); err == nil {
		t.Error("want the locator's parse error")
	}
}

func TestFixedOpener_ReturnsTheHostForEveryURL(t *testing.T) {
	t.Parallel()
	f := NewFakeHost()
	open := FixedOpener(f)
	for _, raw := range []string{"https://github.com/a/b", "/tmp/x/o/r"} {
		h, repo, err := open(raw)
		if err != nil || h != Host(f) {
			t.Fatalf("FixedOpener(%q) = %v, %v", raw, h, err)
		}
		if repo.Name == "" {
			t.Errorf("repo identity empty for %q", raw)
		}
	}
	if _, _, err := open("not-a-url"); err == nil {
		t.Error("want the locator's parse error from FixedOpener")
	}
}

func TestCapabilities_GitHubDeclares(t *testing.T) {
	t.Parallel()
	want := Capabilities{NeedsCredential: true, ReviewsWrites: true}
	var h Host = NewGitHubHost(nil, "")
	if got := h.Capabilities(); got != want {
		t.Errorf("GitHubHost.Capabilities() = %+v, want %+v", got, want)
	}
	if got := NewFakeHost().Capabilities(); got != want {
		t.Errorf("FakeHost default = %+v, want %+v", got, want)
	}
	override := Capabilities{OnThisMachine: true, LandsSynchronously: true}
	f := NewFakeHost()
	f.Caps = &override
	if got := f.Capabilities(); got != override {
		t.Errorf("FakeHost override = %+v, want %+v", got, override)
	}
}
