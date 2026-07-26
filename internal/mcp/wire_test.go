package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	manifest := "schema: space/v1\nspace: fixture-space\nmin_binary_version: \"0.0.0\"\nparticipants:\n" +
		"  - system: axon\n    status: active\n  - system: beta\n    status: active\n"
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
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test")
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}
	if server == nil {
		t.Fatal("expected a non-nil server")
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
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test")
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}
	names := server.registry.ToolNames()
	if len(names) != 6 {
		t.Fatalf("expected exactly the 6 read-only tools with no connected space, got %v", names)
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
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test")
	if err != nil {
		t.Fatalf("an unreachable space must not stop the server from starting: %v", err)
	}
	names := server.registry.ToolNames()
	if len(names) != 6 {
		t.Fatalf("expected exactly the 6 read tools when the space is unreachable, got %v", names)
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
