package host

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
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

// reason: mutates process env (t.Setenv) — not run in parallel.
func TestOpen_MarkedPathIsLocal(t *testing.T) {
	t.Setenv("A2A_GITHUB_API", "http://env-value.invalid")
	ctx := context.Background()
	fx := spacefixture.New(t, "axon")
	check := func(context.Context, LandCandidate) (LandVerdict, error) { return LandVerdict{OK: true}, nil }

	// Unmarked: a bare repository is a GitHub-kind path, whatever the config says.
	loc, err := ParseLocator(fx.OriginDir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(loc, OpenConfig{GitHubAPIBase: "http://cfg-value.invalid", LandCheck: check, LandAuthor: "local:axon"})
	if err != nil {
		t.Fatalf("Open(unmarked): %v", err)
	}
	if _, ok := h.(*GitHubHost); !ok {
		t.Fatalf("Open(unmarked) = %T, want *GitHubHost", h)
	}

	if err := PrepareLocalSpace(ctx, fx.OriginDir, "main"); err != nil {
		t.Fatal(err)
	}
	// Marked, with A2A_GITHUB_API set in the environment: still local.
	for _, raw := range []string{fx.OriginDir, "file://" + fx.OriginDir} {
		loc, err := ParseLocator(raw)
		if err != nil {
			t.Fatal(err)
		}
		h, err := Open(loc, OpenConfig{GitHubAPIBase: "http://cfg-value.invalid", LandCheck: check, LandAuthor: "local:axon"})
		if err != nil {
			t.Fatalf("Open(marked %q): %v", raw, err)
		}
		lh, ok := h.(*LocalHost)
		if !ok {
			t.Fatalf("Open(marked %q) = %T, want *LocalHost", raw, h)
		}
		if lh.author != "local:axon" || lh.dir != fx.OriginDir {
			t.Errorf("LocalHost{author:%q dir:%q}", lh.author, lh.dir)
		}
	}

	// Marked with no land check: an error, never an unvalidated host.
	if h, err := Open(loc, OpenConfig{LandAuthor: "local:axon"}); err == nil || h != nil {
		t.Fatalf("Open(marked, nil LandCheck) = %v, %v; want an error and no host", h, err)
	} else if !strings.Contains(err.Error(), "land check") {
		t.Errorf("the error does not name the missing land check: %v", err)
	}

	// Marked with a land check and no author: opens, reads, and OpenPR refuses.
	h, err = Open(loc, OpenConfig{LandCheck: check})
	if err != nil {
		t.Fatalf("Open(marked, empty author): %v", err)
	}
	lh, ok := h.(*LocalHost)
	if !ok {
		t.Fatalf("Open = %T", h)
	}
	if caps := lh.Capabilities(); !caps.LandsSynchronously || !caps.OnThisMachine {
		t.Errorf("Capabilities = %+v", caps)
	}
	if _, err := lh.OpenPR(ctx, OpenPRRequest{Head: "a2a/axon/submit/x", Base: "main"}); err == nil {
		t.Error("OpenPR with no author succeeded")
	}

	// A URL-class locator never probes: it is GitHub whatever a path may say.
	urlLoc, _ := ParseLocator("https://github.com/o/r")
	if h, err := Open(urlLoc, OpenConfig{LandAuthor: "local:axon"}); err != nil {
		t.Fatal(err)
	} else if _, ok := h.(*GitHubHost); !ok {
		t.Errorf("Open(url) = %T", h)
	}
}

func TestOpen_AnUnmarkedOrMissingPathStaysGitHub(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{t.TempDir() + "/o/r", "/definitely/not/here/o/r"} {
		loc, err := ParseLocator(raw)
		if err != nil {
			t.Fatal(err)
		}
		h, err := Open(loc, OpenConfig{})
		if err != nil {
			t.Fatalf("Open(%q): %v", raw, err)
		}
		if _, ok := h.(*GitHubHost); !ok {
			t.Errorf("Open(%q) = %T, want *GitHubHost", raw, h)
		}
	}
}
