package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
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
// in GitHub CLI, or one without.
//
// It works by putting a fake `gh` on PATH (or emptying PATH), NOT by swapping a
// package var. Both surfaces reach the login through internal/ghauth's real
// exec.LookPath + `gh auth token`, so this exercises the actual code path and
// needs no test-only seam in production API — and there is nothing for a second
// seam to drift away from.
//
// Without this, the refusal cases are untestable anywhere `gh` happens to be
// logged in: that is every developer machine and no CI runner, the exact split
// that lets a test pass in CI and fail on the desk of whoever has to fix it.
func withGHLogin(t *testing.T, token string, available bool) {
	t.Helper()
	dir := t.TempDir()
	if !available {
		// An empty directory as the whole PATH: exec.LookPath("gh") fails, which
		// internal/ghauth treats as an ordinary "no token available".
		t.Setenv("PATH", dir)
		return
	}
	script := "#!/bin/sh\nif [ \"$1\" = auth ] && [ \"$2\" = token ]; then echo " + token + "; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", dir)
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

// TestResolveCredentialAndFeedbackShareTheGHLoginFallback pins the two chains
// to one behaviour on one machine state.
//
// They had drifted: `a2a feedback submit` gained the gh-login fallback while
// every REAL space write kept refusing on a machine that was perfectly well
// authenticated. `cmd:gh auth token` is seeded into the machine config ONCE, at
// connect time, so a space connected before gh existed — or a config copied
// between machines — carries a plain env reference and never sees the login.
// Fixing the smaller surface and leaving the larger one is worse than fixing
// neither: it makes the remaining refusal look space-specific.
func TestResolveCredentialAndFeedbackShareTheGHLoginFallback(t *testing.T) {
	clear := func(t *testing.T) {
		t.Helper()
		for _, name := range []string{"A2A_FEEDBACK_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", "A2A_TOKEN_GETVISA"} {
			t.Setenv(name, "")
		}
	}

	t.Run("a space write falls through to the gh login", func(t *testing.T) {
		clear(t)
		withGHLogin(t, "gh-cli-secret", true)
		// The machine config names the documented env var rather than
		// `cmd:gh auth token` — the shape a space connected before gh was set
		// up actually has on disk.
		got, err := resolveCredential(context.Background(), "getvisa", space.MachineConfig{
			Credentials: map[string]string{"getvisa": "env:A2A_TOKEN_GETVISA"},
		})
		if err != nil || got.Token != "gh-cli-secret" {
			t.Fatalf("credential = %#v, err=%v; want the gh login fallback", got, err)
		}
	})

	t.Run("the explicit per-space override still wins", func(t *testing.T) {
		clear(t)
		withGHLogin(t, "gh-cli-secret", true)
		t.Setenv("A2A_TOKEN_GETVISA", "space-secret")
		got, err := resolveCredential(context.Background(), "getvisa", space.MachineConfig{})
		if err != nil || got.Token != "space-secret" {
			t.Fatalf("credential = %#v, err=%v; want the explicit per-space override", got, err)
		}
	})

	t.Run("without a gh login the refusal still names what it checked", func(t *testing.T) {
		clear(t)
		withGHLogin(t, "", false)
		_, err := resolveCredential(context.Background(), "getvisa", space.MachineConfig{
			Credentials: map[string]string{"getvisa": "env:A2A_TOKEN_GETVISA"},
		})
		if err == nil {
			t.Fatal("a space write with no credential anywhere succeeded")
		}
		if !strings.Contains(err.Error(), "A2A_TOKEN_GETVISA") {
			t.Errorf("refusal does not name the checked env var: %v", err)
		}
	})

	// The two surfaces must agree on the SAME machine, which is the property
	// that actually broke — each being individually correct is not enough.
	t.Run("both surfaces agree on one machine", func(t *testing.T) {
		clear(t)
		withGHLogin(t, "gh-cli-secret", true)
		space1, err1 := resolveCredential(context.Background(), "getvisa", space.MachineConfig{
			Credentials: map[string]string{"getvisa": "env:A2A_TOKEN_GETVISA"},
		})
		feedback, err2 := resolveFeedbackCredential(context.Background(), space.MachineConfig{})
		if err1 != nil || err2 != nil {
			t.Fatalf("one surface refused where the other resolved: space=%v feedback=%v", err1, err2)
		}
		if space1.Token != feedback.Token {
			t.Fatalf("surfaces disagree on the same machine: space=%q feedback=%q", space1.Token, feedback.Token)
		}
	})
}

// seedTargetSpaceArtifact writes an "<id>.md" placeholder into ref's own
// resolved mirror directory (space.ResolveMirrorLocation's default,
// projectRoot/.a2a/cache/mirrors/<space-id>) — the on-disk shape
// resolveTargetSpaceRef's walk (via cache.ResolveArtifactSpace) looks for.
func seedTargetSpaceArtifact(t *testing.T, projectRoot string, ref space.Ref, id string) {
	t.Helper()
	dir := filepath.Join(space.ResolveMirrorLocation(projectRoot, ref, space.MachineConfig{}), "exchanges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveTargetSpaceRefFindsSecondSpace is the exact defect REF-025
// (no-silent-yes-2026-08 P2a) exists for: an id held by the SECOND
// connected space must resolve to THAT space, never silently to the first.
func TestResolveTargetSpaceRefFindsSecondSpace(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one"}, {ID: "space-two"},
	}}
	seedTargetSpaceArtifact(t, projectRoot, cfg.Spaces[1], "XQ-beta-20260827-aaaa")

	ref, err := resolveTargetSpaceRef(cfg, space.MachineConfig{}, projectRoot, "XQ-beta-20260827-aaaa")
	if err != nil {
		t.Fatalf("resolveTargetSpaceRef: %v", err)
	}
	if ref.ID != "space-two" {
		t.Fatalf("resolveTargetSpaceRef resolved %q, want space-two", ref.ID)
	}
}

// TestResolveTargetSpaceRefRefusesUnknownIDAcrossMultipleSpaces asserts
// REF-025's own contract: an id in NEITHER connected space refuses by
// name, naming the id AND both spaces searched — never a silent
// cfg.Spaces[0] resolution.
func TestResolveTargetSpaceRefRefusesUnknownIDAcrossMultipleSpaces(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one"}, {ID: "space-two"},
	}}

	_, err := resolveTargetSpaceRef(cfg, space.MachineConfig{}, projectRoot, "XQ-beta-20260827-zzzz")
	if err == nil {
		t.Fatal("expected a REF-025 refusal for an id in no connected space")
	}
	for _, want := range []string{"REF-025", "XQ-beta-20260827-zzzz", "space-one", "space-two"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// TestResolveTargetSpaceRefSingleSpaceNeverRefuses is
// TestWireMirrorIsFreshAfterResolution's (wire_tier_test.go) unit-level
// pin: with exactly ONE connected space there is no "second space" for
// REF-025's own defect to describe, so an id the walk cannot currently see
// (never seeded, or not yet synced into the mirror) must still resolve to
// the one connected space rather than refuse — resolveLifecycleDepsWithPolicy
// syncs the RESOLVED space's mirror only AFTER this call returns, so a
// walk that refused here would make a freshly-published id unreachable
// even though the space that will hold it is completely unambiguous.
func TestResolveTargetSpaceRefSingleSpaceNeverRefuses(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{{ID: "only-space"}}}

	ref, err := resolveTargetSpaceRef(cfg, space.MachineConfig{}, projectRoot, "XQ-beta-20260827-unsynced")
	if err != nil {
		t.Fatalf("resolveTargetSpaceRef: %v", err)
	}
	if ref.ID != "only-space" {
		t.Fatalf("resolveTargetSpaceRef resolved %q, want only-space", ref.ID)
	}
}

// TestResolveTargetSpaceRefContractAndDataPackageIDsBypassTheWalk pins the
// two id families cache.ResolveArtifactSpace's own doc comment names as
// never matching "<id>.md" — runContract and `data fetch`/`data verify`
// reach resolveTargetSpaceRef with one of these even in a multi-space
// project, and the walk must not refuse REF-025 for either.
func TestResolveTargetSpaceRefContractAndDataPackageIDsBypassTheWalk(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one"}, {ID: "space-two"},
	}}

	for _, id := range []string{"XC-axon-orders", "DP-axon-20260827-ab12"} {
		ref, err := resolveTargetSpaceRef(cfg, space.MachineConfig{}, projectRoot, id)
		if err != nil {
			t.Fatalf("resolveTargetSpaceRef(%q): %v", id, err)
		}
		if ref.ID != "space-one" {
			t.Fatalf("resolveTargetSpaceRef(%q) = %q, want space-one (cfg.Spaces[0])", id, ref.ID)
		}
	}
}

// localSpaceRig is a bare space made a local space the way `space init --local`
// does, whose manifest names "local:<system>" as each system's owner, the login
// a local land is attributed to.
type localSpaceRig struct {
	origin string
	clones map[string]string
}

func newLocalSpaceRig(t *testing.T) localSpaceRig {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	clone := fx.Clones["axon"]
	manifestPath := filepath.Join(clone, "space.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	owned := strings.NewReplacer("axon-bot", "local:axon", "beta-bot", "local:beta").Replace(string(raw))
	if owned == string(raw) {
		t.Fatal("the fixture manifest changed: no owner to rename")
	}
	if err := os.WriteFile(manifestPath, []byte(owned), 0o644); err != nil {
		t.Fatal(err)
	}
	wiringGit(t, clone, "add", "space.yaml")
	wiringGit(t, clone, "commit", "-q", "-m", "owners are local logins")
	// Before PrepareLocalSpace installs the hook that refuses a push to main.
	wiringGit(t, clone, "push", "-q", "origin", "HEAD:refs/heads/main")
	if err := host.PrepareLocalSpace(context.Background(), fx.OriginDir, "main"); err != nil {
		t.Fatalf("PrepareLocalSpace: %v", err)
	}
	return localSpaceRig{origin: fx.OriginDir, clones: fx.Clones}
}

func wiringGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a2a", "GIT_AUTHOR_EMAIL=a@invalid", "GIT_COMMITTER_NAME=a2a", "GIT_COMMITTER_EMAIL=a@invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// push commits file on a fresh branch of the system's clone and pushes it to
// the local space through the host.
func (r localSpaceRig) push(t *testing.T, h host.Host, system, branch, file string) {
	t.Helper()
	clone := r.clones[system]
	wiringGit(t, clone, "checkout", "-q", "-B", branch, "main")
	full := filepath.Join(clone, file)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wiringGit(t, clone, "add", file)
	wiringGit(t, clone, "commit", "-q", "-m", "write "+file)
	if _, err := h.PushBranch(context.Background(), host.PushBranchRequest{RepoDir: clone, LocalRef: "HEAD", Branch: branch, RemoteURL: r.origin}); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}
}

func (r localSpaceRig) mainTip(t *testing.T) string {
	t.Helper()
	return wiringGit(t, r.origin, "rev-parse", "refs/heads/main")
}

// TestNewHostOpenerFor_LocalLandRunsTheRealCheck proves the production wiring
// of a local land end to end: the opener built for a system attributes the land
// to "local:<system>", the lazy check builds the real engine and runs the
// required check's body, and a write into the caller's own section lands while
// a write into another system's section is refused with AUTHZ-001.
func TestNewHostOpenerFor_LocalLandRunsTheRealCheck(t *testing.T) {
	t.Setenv("A2A_GITHUB_API", "")
	r := newLocalSpaceRig(t)
	repo := host.Repo{Owner: "local", Name: "space"}
	open := newHostOpenerFor("axon")
	h, _, err := open(r.origin)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, ok := h.(*host.LocalHost); !ok {
		t.Fatalf("host = %T, want *host.LocalHost for a marked path", h)
	}

	t.Run("own section lands", func(t *testing.T) {
		before := r.mainTip(t)
		r.push(t, h, "axon", "a2a/axon/submit/own", "axon/docs/own.txt")
		pr, err := h.OpenPR(context.Background(), host.OpenPRRequest{Repo: repo, Head: "a2a/axon/submit/own", Base: "main", Title: "own", Body: "b"})
		if err != nil {
			t.Fatalf("OpenPR: %v", err)
		}
		if pr.State != "merged" {
			t.Fatalf("State = %q, want merged", pr.State)
		}
		if after := r.mainTip(t); after == before {
			t.Fatal("main did not move")
		}
		author := wiringGit(t, r.origin, "log", "-1", "--format=%an", "refs/heads/main")
		if author != "local:axon" {
			t.Fatalf("merge author = %q, want local:axon", author)
		}
	})

	t.Run("another system's section is refused with AUTHZ-001", func(t *testing.T) {
		before := r.mainTip(t)
		r.push(t, h, "axon", "a2a/axon/submit/cross", "beta/docs/cross.txt")
		_, err := h.OpenPR(context.Background(), host.OpenPRRequest{Repo: repo, Head: "a2a/axon/submit/cross", Base: "main", Title: "cross", Body: "b"})
		var refusal *host.LandRefusal
		if !errors.Is(err, host.ErrLandRefused) || !errors.As(err, &refusal) {
			t.Fatalf("OpenPR error = %v, want ErrLandRefused carrying a LandRefusal", err)
		}
		if len(refusal.Codes) != 1 || refusal.Codes[0] != "AUTHZ-001" {
			t.Fatalf("refusal codes = %v, want [AUTHZ-001]", refusal.Codes)
		}
		if after := r.mainTip(t); after != before {
			t.Fatal("main moved on a refused write")
		}
	})
}

// TestNewHostOpener_HasNoSystemToLandUnder pins the two callers that keep the
// argument-free opener (the doctor and the feedback hub): they can open a local
// space, but a land through it is refused before anything is written.
func TestNewHostOpener_HasNoSystemToLandUnder(t *testing.T) {
	t.Setenv("A2A_GITHUB_API", "")
	r := newLocalSpaceRig(t)
	h, _, err := newHostOpener()(r.origin)
	if err != nil {
		t.Fatalf("a marked path must open for the doctor: %v", err)
	}
	r.push(t, h, "axon", "a2a/axon/submit/nosys", "axon/docs/nosys.txt")
	before := r.mainTip(t)
	_, err = h.OpenPR(context.Background(), host.OpenPRRequest{Repo: host.Repo{Owner: "local", Name: "space"}, Head: "a2a/axon/submit/nosys", Base: "main", Title: "t", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "no system is configured") {
		t.Fatalf("OpenPR = %v, want an error naming the missing system", err)
	}
	if after := r.mainTip(t); after != before {
		t.Fatal("main moved on a land with no system")
	}
}
