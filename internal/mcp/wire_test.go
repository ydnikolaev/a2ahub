package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// fixValidManifest overwrites and pushes a structurally-valid space.yaml
// (space.Manifest's own []Participant shape) onto the fixture's origin —
// spacefixture.go (testkit, off this phase's allowlist) seeds a
// map-shaped `participants:` block that space.ParseManifest cannot decode
// into []Participant; this test-local fix pushes a corrected manifest
// rather than editing the shared fixture helper.
func fixValidManifest(t *testing.T, fx *spacefixture.Fixture, system string) {
	t.Helper()
	dir := fx.Clone(system)
	manifest := "schema: space/v1\nspace: fixture-space\nmin_binary_version: \"0.19.0\"\nparticipants:\n" +
		"  - system: axon\n    org: fixture\n    section: axon\n    owners: [axon-bot]\n    status: active\n    joined: \"2026-01-01\"\n" +
		"  - system: beta\n    org: fixture\n    section: beta\n    owners: [beta-bot]\n    status: active\n    joined: \"2026-01-01\"\n"
	if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", "space.yaml")
	runGitTest(t, dir, "-c", "user.name=fixture", "-c", "user.email=fixture@a2ahub.invalid", "commit", "-m", "fix manifest shape")
	runGitTest(t, dir, "push", "origin", "HEAD:main")
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
}

func TestParseGitHubRepoHTTPS(t *testing.T) {
	t.Parallel()
	owner, name, err := parseGitHubRepo("https://github.com/acme/space.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "acme" || name != "space" {
		t.Fatalf("got %q/%q", owner, name)
	}
}

func TestParseGitHubRepoSSH(t *testing.T) {
	t.Parallel()
	owner, name, err := parseGitHubRepo("git@github.com:acme/space")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "acme" || name != "space" {
		t.Fatalf("got %q/%q", owner, name)
	}
}

func TestParseGitHubRepoMalformed(t *testing.T) {
	t.Parallel()
	if _, _, err := parseGitHubRepo("not-a-url"); err == nil {
		t.Fatal("expected an error for a malformed repo URL")
	}
}

func TestResolvePaths(t *testing.T) {
	t.Parallel()
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.ProjectConfig == "" || p.MachineConfig == "" || p.ProjectRoot == "" || p.Staging == "" {
		t.Fatalf("expected all Paths fields populated, got %+v", p)
	}
}

// reason: mutates process env (t.Setenv) — not run in parallel.
func TestNewServerFromConfigFullHappyPath(t *testing.T) {
	fx := spacefixture.New(t, "axon", "beta")
	fixValidManifest(t, fx, "beta")
	t.Setenv("A2A_TOKEN_FIXTURE_SPACE", "test-token")

	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte(
		"system: beta\nspaces:\n  - id: fixture-space\n    repo_url: "+fx.RemoteURL()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte(
		"credentials:\n  fixture-space: \"env:A2A_TOKEN_FIXTURE_SPACE\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}
	if server == nil {
		t.Fatal("expected a non-nil server")
	}
	if server.preCall == nil {
		t.Fatal("expected connected server pre-call hook")
	}
	offlineOrigin := fx.OriginDir + ".offline"
	if err := os.Rename(fx.OriginDir, offlineOrigin); err != nil {
		t.Fatal(err)
	}
	if err := server.preCall(context.Background(), WorkToolName); err != nil {
		t.Fatalf("a2a_work invoked generic mirror refresh: %v", err)
	}
	if err := server.preCall(context.Background(), "a2a_read"); err == nil {
		t.Fatal("control tool unexpectedly skipped unavailable mirror refresh")
	}
}

// reason: mutates process env indirectly via machine config credential
// lookup failure path — kept sequential alongside the happy-path test.
func TestNewServerFromConfigNoConnectedSpaces(t *testing.T) {
	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte("system: beta\nspaces: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}
	assertSpaceFreeSurface(t, server.registry.ToolNames())
}

// spaceFreeToolNames is what a session with no usable space must offer:
// `a2a_read` over the local cache, and `a2a_whatsnew` over the embedded
// corpus. Sorted, because ToolNames() is.
//
// It used to be six names — `a2a_inbox`, `a2a_outbox`, `a2a_show`,
// `a2a_thread`, `a2a_search`, `a2a_contracts` — the pre-P15 per-verb read
// tools, kept alive by wire.go's own copy of the registration long after
// BuildRegistry folded them into `a2a_read`'s `view` enum. tools_test's
// `removed` list asserts those exact names are ABSENT from the connected
// surface, so the two registries disagreed on what the read tools are
// CALLED, and the catalogue an agent reads documented only one of them.
var spaceFreeToolNames = []string{"a2a_read", "a2a_whatsnew"}

// assertSpaceFreeSurface checks what a degraded session offers, TWICE, and the
// second check is the one that catches a new fork.
//
// The literal comparison is the readable half: it says out loud which two tools
// a session with no usable space serves. On its own it is weak — a literal can
// always be edited to match whatever the code now does, which is how the six
// retired names survived every release since v0.1.0 here.
//
// So the second half resolves the connected surface from BuildRegistry and
// requires every offered name to exist there too. A degraded session must offer
// FEWER tools than a healthy one, never DIFFERENT ones: an agent that learns a
// tool name with no space connected must still find it after connecting, and
// `skill/a2ahub/reference/commands.md` — the skill-drift-gated catalogue an
// agent actually reads — is generated from the connected registry alone, so a
// name only the degraded path serves is a name nothing documents.
//
// It matters that this runs on `names` from a server built by
// NewServerFromConfig, not on a registry the test assembles itself. Asserting
// the subset against a registry built by calling registerSpaceFree would be a
// TAUTOLOGY, since BuildRegistry composes that same function — it could never
// fail. Reading the real wire path is what makes a second registration added
// there visible, and that was verified by planting one.
//
// The ceiling, stated so nobody credits this with more than it does: for the
// same compositional reason, the subset half cannot see a wrong name added
// INSIDE registerSpaceFree — BuildRegistry would then serve it too. That case
// is the literal's, which is why both halves are here.
func assertSpaceFreeSurface(t *testing.T, names []string) {
	t.Helper()

	if len(names) != len(spaceFreeToolNames) {
		t.Fatalf("space-free surface = %v, want exactly %v", names, spaceFreeToolNames)
	}
	for i, want := range spaceFreeToolNames {
		if names[i] != want {
			t.Fatalf("space-free surface = %v, want exactly %v", names, spaceFreeToolNames)
		}
	}

	connected := BuildRegistry(nil, WriteDeps{}, "", nil, NewDeps{})
	for _, name := range names {
		if _, ok := connected.Get(name); !ok {
			t.Errorf("this session offers %q and a CONNECTED session does not — a degraded surface "+
				"must be a subset of the healthy one, never a different set of names. That is the "+
				"defect P15 left behind: wire.go kept registering a2a_inbox/outbox/show/thread/"+
				"search/contracts after BuildRegistry folded them into a2a_read's view enum, so the "+
				"onboarding session was served six tool names nothing documents.", name)
		}
	}
}

func TestNewServerFromConfigNoProjectConfig(t *testing.T) {
	t.Parallel()
	p := Paths{
		ProjectConfig: t.TempDir() + "/does-not-exist/config.yaml",
		MachineConfig: t.TempDir() + "/machine.yaml",
		ProjectRoot:   t.TempDir(),
		Staging:       t.TempDir(),
	}
	_, err := NewServerFromConfig(context.Background(), p, "0.0.1-test")
	if err == nil {
		t.Fatal("expected an error when no project config exists")
	}
}

// TestNewServerFromConfigUnreachableSpaceStillServesReads is the regression
// for a full outage where a partial degradation belongs.
//
// buildWriteDeps clones or fetches the mirror, so a network blip, an expired
// credential, or a laptop on a plane made `a2a mcp` fail to START — while
// every read tool needs nothing but the local mirror already on disk. The
// CLI's read path has made the opposite choice since CC-092 (buildStore is
// explicitly tolerant of absent config, absent spaces, absent machine
// config); this brings the second surface in line.
//
// A session that can still tell you what your counterparty sent beats a
// session that will not open.
func TestNewServerFromConfigUnreachableSpaceStillServesReads(t *testing.T) {
	t.Setenv("A2A_TOKEN_FIXTURE_SPACE", "test-token")

	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	// A repo_url that resolves to nothing — the shape of an unreachable
	// space without needing the network to be down.
	unreachable := filepath.Join(t.TempDir(), "no-such-origin.git")
	if err := os.WriteFile(projectConfig, []byte(
		"system: beta\nspaces:\n  - id: fixture-space\n    repo_url: "+unreachable+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte(
		"credentials:\n  fixture-space: \"env:A2A_TOKEN_FIXTURE_SPACE\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("an unreachable space must not stop the server from starting: %v", err)
	}
	names := server.registry.ToolNames()
	wantDegraded := []string{"a2a_read", "a2a_whatsnew", "a2a_work"}
	if !slices.Equal(names, wantDegraded) {
		t.Fatalf("unreachable-space surface = %v, want %v", names, wantDegraded)
	}
	// Exact names, not substrings: `a2a_contracts` (plural) is a READ tool
	// and `a2a_contract` (singular) is the write one — a substring match
	// cannot tell them apart and would fail on a correct registry.
	writeTools := map[string]bool{
		"a2a_submit": true, "a2a_lifecycle": true, "a2a_exchange": true,
		"a2a_contract": true, "a2a_new": true,
	}
	for _, n := range names {
		if writeTools[n] {
			t.Errorf("write tool %q was registered over an unreachable space — a write that cannot "+
				"reach the space would fail deep inside the funnel instead of not being offered", n)
		}
	}
}
