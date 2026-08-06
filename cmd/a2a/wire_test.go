package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestResolveFeedbackCredentialPrecedence(t *testing.T) {
	clear := func(t *testing.T) {
		t.Helper()
		for _, name := range []string{"A2A_FEEDBACK_TOKEN", "A2A_CONFIGURED_FEEDBACK", "GITHUB_TOKEN", "GH_TOKEN"} {
			t.Setenv(name, "")
		}
	}

	t.Run("explicit override wins", func(t *testing.T) {
		clear(t)
		t.Setenv("A2A_FEEDBACK_TOKEN", "explicit-secret")
		t.Setenv("A2A_CONFIGURED_FEEDBACK", "configured-secret")
		t.Setenv("GITHUB_TOKEN", "fallback-secret")
		got, err := resolveFeedbackCredential(context.Background(), space.MachineConfig{
			Credentials: map[string]string{"feedback": "env:A2A_CONFIGURED_FEEDBACK"},
		})
		if err != nil || got.Token != "explicit-secret" {
			t.Fatalf("credential = %#v, err=%v; want explicit override", got, err)
		}
	})

	t.Run("configured reference wins over compatibility fallback", func(t *testing.T) {
		clear(t)
		t.Setenv("A2A_CONFIGURED_FEEDBACK", "configured-secret")
		t.Setenv("GITHUB_TOKEN", "fallback-secret")
		got, err := resolveFeedbackCredential(context.Background(), space.MachineConfig{
			Credentials: map[string]string{"feedback": "env:A2A_CONFIGURED_FEEDBACK"},
		})
		if err != nil || got.Token != "configured-secret" {
			t.Fatalf("credential = %#v, err=%v; want configured reference", got, err)
		}
	})

	t.Run("canonical compatibility fallback remains", func(t *testing.T) {
		clear(t)
		t.Setenv("GH_TOKEN", "gh-secret")
		got, err := resolveFeedbackCredential(context.Background(), space.MachineConfig{})
		if err != nil || got.Token != "gh-secret" {
			t.Fatalf("credential = %#v, err=%v; want GH_TOKEN fallback", got, err)
		}
	})

	t.Run("a gh login is the last fallback, ahead of refusing", func(t *testing.T) {
		clear(t)
		withGHLogin(t, "gh-cli-secret", true)
		got, err := resolveFeedbackCredential(context.Background(), space.MachineConfig{})
		if err != nil || got.Token != "gh-cli-secret" {
			t.Fatalf("credential = %#v, err=%v; want the gh login fallback", got, err)
		}
	})

	t.Run("an env token still wins over the gh login", func(t *testing.T) {
		clear(t)
		withGHLogin(t, "gh-cli-secret", true)
		t.Setenv("A2A_FEEDBACK_TOKEN", "explicit-secret")
		got, err := resolveFeedbackCredential(context.Background(), space.MachineConfig{})
		if err != nil || got.Token != "explicit-secret" {
			t.Fatalf("credential = %#v, err=%v; want the explicit override", got, err)
		}
	})

	t.Run("missing result names every checked seam", func(t *testing.T) {
		clear(t)
		withGHLogin(t, "", false)
		_, err := resolveFeedbackCredential(context.Background(), space.MachineConfig{
			Credentials: map[string]string{"feedback": "env:A2A_CONFIGURED_FEEDBACK"},
		})
		if err == nil {
			t.Fatal("missing feedback credential succeeded")
		}
		for _, name := range []string{"A2A_FEEDBACK_TOKEN", "A2A_CONFIGURED_FEEDBACK", "GITHUB_TOKEN", "GH_TOKEN"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not name %s", err, name)
			}
		}
	})
}

// withGHLogin states which machine a subtest is describing: one with a logged
// in GitHub CLI, or one without. Every developer machine has the former and
// every CI runner the latter, so a test that reads the real `gh` would pass in
// one place and fail in the other for reasons that have nothing to do with the
// code under test.
func withGHLogin(t *testing.T, token string, available bool) {
	t.Helper()
	previous := feedbackGHLoginToken
	feedbackGHLoginToken = func(context.Context) (string, bool) { return token, available }
	t.Cleanup(func() { feedbackGHLoginToken = previous })
}

func TestFeedbackCredentialBoundary(t *testing.T) {
	t.Parallel()

	for _, remote := range []string{
		"https://github.com/ydnikolaev/a2ahub",
		"git@github.com:ydnikolaev/a2ahub.git",
	} {
		if !feedbackGitHubRemote(remote) {
			t.Errorf("feedbackGitHubRemote(%q) = false, want true", remote)
		}
	}
	for _, remote := range []string{
		"/tmp/a2ahub-feedback.git",
		"file:///tmp/a2ahub-feedback.git",
		"https://git.example.test/acme/a2ahub",
	} {
		if feedbackGitHubRemote(remote) {
			t.Errorf("feedbackGitHubRemote(%q) = true, want false", remote)
		}
	}
}

// TestFunnelBinaryVersionIsBare guards the wiring-regression class the P11
// smoke test surfaced (docs/backlog.md): the value handed to
// space.NewWriteFunnel must be the BARE dotted version, not versionStamp()
// ("a2a x.y.z (sha)"), or every write against a version-pinned space fails to
// parse. funnelBinaryVersion() is the single seam both write paths (submit +
// lifecycle/contract) call; if a future edit points it at versionStamp() this
// test reds. The funnel-side contract is guarded by
// TestFunnelStampShapedVersionFailsClosed in internal/space.
func TestFunnelBinaryVersionIsBare(t *testing.T) {
	t.Parallel()

	got := funnelBinaryVersion()

	// Identity with the bare package var — the exact intent that regressed.
	if got != version {
		t.Errorf("funnelBinaryVersion() = %q, want the bare version var %q (not versionStamp())", got, version)
	}
	// Structural: the stamp carries a space and an "a2a " prefix; a bare
	// version carries neither. This catches versionStamp() even if the bare
	// version var were ever itself set stamp-shaped.
	if got != versionStamp() && (strings.Contains(got, " ") || strings.HasPrefix(got, "a2a ")) {
		t.Errorf("funnelBinaryVersion() = %q looks stamp-shaped (space / \"a2a \" prefix); the funnel needs a bare major.minor.patch", got)
	}
	if got == versionStamp() {
		t.Errorf("funnelBinaryVersion() returned the full stamp %q — the funnel's CC-085 guard cannot parse it", got)
	}
}

func TestParseGitHubRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url, owner, name string
		wantErr          bool
	}{
		{"https://github.com/getvisa/getvisa", "getvisa", "getvisa", false},
		{"https://github.com/getvisa/getvisa.git", "getvisa", "getvisa", false},
		{"git@github.com:r22d222/a2a.git", "r22d222", "a2a", false},
		{"https://github.com/org/repo", "org", "repo", false},
		{"not-a-url", "", "", true},
		{"https://github.com/onlyowner", "", "", true},
	}
	for _, tc := range cases {
		owner, name, err := parseGitHubRepo(tc.url)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseGitHubRepo(%q): want error, got %s/%s", tc.url, owner, name)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGitHubRepo(%q): unexpected error %v", tc.url, err)
			continue
		}
		if owner != tc.owner || name != tc.name {
			t.Errorf("parseGitHubRepo(%q) = %s/%s, want %s/%s", tc.url, owner, name, tc.owner, tc.name)
		}
	}
}

func TestFindSpace(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{
		System: "axon",
		Spaces: []space.Ref{
			{ID: "getvisa", RepoURL: "https://github.com/getvisa/getvisa"},
			{ID: "other", RepoURL: "https://github.com/o/other"},
		},
	}
	if r, ok := findSpace(cfg, "getvisa"); !ok || r.RepoURL != "https://github.com/getvisa/getvisa" {
		t.Errorf("findSpace(getvisa) = %+v, %v", r, ok)
	}
	if _, ok := findSpace(cfg, "nope"); ok {
		t.Errorf("findSpace(nope): want not found")
	}
}

func TestReadEnvelopeFacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "XQ-axon-20260721-abcd.md")
	if err := os.WriteFile(good, []byte("---\nschema: envelope/v1\nid: XQ-axon-20260721-abcd\nfrom: axon\nspace: getvisa\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := readEnvelopeFacts(good)
	if err != nil {
		t.Fatalf("readEnvelopeFacts: %v", err)
	}
	if f.from != "axon" || f.space != "getvisa" {
		t.Errorf("readEnvelopeFacts = %+v, want from=axon space=getvisa", f)
	}

	// Missing `from`/`space` is an error, not a silent empty.
	bad := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(bad, []byte("---\nschema: envelope/v1\nid: x\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvelopeFacts(bad); err == nil {
		t.Errorf("readEnvelopeFacts(missing fields): want error")
	}
}

// TestBuildStoreConfigTolerance guards the wave-4 MED-4 fix: buildStore
// TOLERATES an absent config (pre-onboarding, CC-092) but SURFACES a
// malformed one (bad YAML / bad credential ref) rather than silently
// degrading to zero connected spaces.
func TestBuildStoreConfigTolerance(t *testing.T) {
	t.Parallel()

	// pathsIn builds a paths set rooted at dir with the standard layout.
	pathsIn := func(dir string) paths {
		return paths{
			projectConfig: filepath.Join(dir, ".a2a", "config.yaml"),
			machineConfig: filepath.Join(dir, "machine.yaml"),
			projectRoot:   dir,
			staging:       filepath.Join(dir, ".a2a", "staging"),
		}
	}

	t.Run("absent config is tolerated (empty store, no error)", func(t *testing.T) {
		t.Parallel()
		store, err := buildStore(t.Context(), pathsIn(t.TempDir()))
		if err != nil {
			t.Fatalf("buildStore(absent): unexpected error %v", err)
		}
		if store == nil {
			t.Fatal("buildStore(absent): want non-nil store over zero mirrors")
		}
	})

	t.Run("malformed project config surfaces loudly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".a2a"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Unterminated flow sequence — a real YAML parse error, not an
		// absent file.
		if err := os.WriteFile(filepath.Join(dir, ".a2a", "config.yaml"), []byte("system: [axon\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := buildStore(t.Context(), pathsIn(dir)); err == nil {
			t.Error("buildStore(malformed project config): want error, got nil")
		}
	})

	t.Run("malformed machine config surfaces loudly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Absent project config (tolerated) but a machine config carrying a
		// credential reference that is neither env: nor cmd: — LoadMachineConfig
		// rejects it at load, and buildStore must NOT swallow that.
		if err := os.WriteFile(filepath.Join(dir, "machine.yaml"), []byte("credentials:\n  getvisa: \"literal-secret-not-a-ref\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := buildStore(t.Context(), pathsIn(dir)); err == nil {
			t.Error("buildStore(malformed machine config): want error, got nil")
		}
	})
}
