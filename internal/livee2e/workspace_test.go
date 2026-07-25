//go:build livee2e

package livee2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// seedProjectConfig writes a `.a2a/config.yaml` under projectDir carrying one
// space ref, matching the shape `a2a connect` persists after repairing
// MirrorLocation (cmd_init.go's ConnectCommand.Run): ID is the MANIFEST id,
// mirrorLocation is the URL id `connect` cloned into (empty reproduces the
// pre-fix shape, where no location key was ever persisted).
func seedProjectConfig(t *testing.T, projectDir, spaceID, mirrorLocation string) {
	t.Helper()
	dir := filepath.Join(projectDir, ".a2a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "system: alpha\nspaces:\n  - id: " + spaceID + "\n    repo_url: https://github.com/org/live-e2e-space\n"
	if mirrorLocation != "" {
		body += "    mirror_location: " + mirrorLocation + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
}

// seedMachineConfig writes `~/.config/a2a/config.yaml` under home,
// optionally carrying a mirror_root.
func seedMachineConfig(t *testing.T, home, mirrorRoot string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "a2a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := ""
	if mirrorRoot != "" {
		body = "mirror_root: " + mirrorRoot + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write machine config: %v", err)
	}
}

// TestMirrorDirAsksTheProductResolverWithMirrorRoot is AC: with a machine
// `mirror_root` configured, MirrorDir must return exactly what
// space.ResolveMirrorLocation resolves — <mirror_root>/<mirror_location> —
// never the harness's own hardcoded, slug-keyed, project-local guess.
func TestMirrorDirAsksTheProductResolverWithMirrorRoot(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	mirrorRoot := filepath.Join(t.TempDir(), "mirrors")
	seedProjectConfig(t, projectDir, "livee2e", "live-e2e-space")
	seedMachineConfig(t, home, mirrorRoot)

	c := &checkout{Dir: projectDir, SpaceSlug: "livee2e"}

	cfg, err := space.LoadProjectConfig(filepath.Join(projectDir, ".a2a", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	machine, err := space.LoadMachineConfig(machineConfigPath())
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	var ref space.Ref
	for _, r := range cfg.Spaces {
		if r.ID == "livee2e" {
			ref = r
		}
	}
	want := space.ResolveMirrorLocation(projectDir, ref, machine)

	if got := c.MirrorDir(); got != want {
		t.Errorf("MirrorDir() = %q, want %q (space.ResolveMirrorLocation's own answer)", got, want)
	}
	if wantLiteral := filepath.Join(mirrorRoot, "live-e2e-space"); want != wantLiteral {
		t.Fatalf("test setup drifted: space.ResolveMirrorLocation returned %q, expected %q", want, wantLiteral)
	}
}

// TestMirrorDirAsksTheProductResolverProjectLocal is the unset-mirror_root
// half: MirrorDir must still equal space.ResolveMirrorLocation's own answer,
// which falls back to the project-relative default keyed by the ref's
// MirrorLocation.
func TestMirrorDirAsksTheProductResolverProjectLocal(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	seedProjectConfig(t, projectDir, "livee2e", "live-e2e-space")
	// No machine config file at all — LoadMachineConfig on an absent path
	// must degrade to a zero-value MachineConfig, exactly like connect's own
	// `machine, _ := c.loadMachineConfig(...)` tolerance.

	c := &checkout{Dir: projectDir, SpaceSlug: "livee2e"}

	got := c.MirrorDir()
	want := filepath.Join(projectDir, ".a2a", "cache", "mirrors", "live-e2e-space")
	if got != want {
		t.Errorf("MirrorDir() = %q, want %q", got, want)
	}

	// Cross-check against the product's own resolver directly, so this test
	// fails the moment MirrorDir and ResolveMirrorLocation disagree, not
	// just when both happen to disagree with this test's own literal.
	ref := space.Ref{ID: "livee2e", MirrorLocation: "live-e2e-space"}
	if resolverWant := space.ResolveMirrorLocation(projectDir, ref, space.MachineConfig{}); got != resolverWant {
		t.Errorf("MirrorDir() = %q, space.ResolveMirrorLocation = %q — diverged", got, resolverWant)
	}
}

// TestMirrorDirPreFixRefWithNoMirrorLocationStillResolves guards the shape
// real machines carry today: a ref persisted by the OLD `a2a init` (no
// MirrorLocation key at all) must still resolve MirrorDir to something —
// never panic — even with a `mirror_root` configured, because
// ResolveMirrorLocation only applies mirror_root when MirrorLocation is set.
func TestMirrorDirPreFixRefWithNoMirrorLocationStillResolves(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	seedProjectConfig(t, projectDir, "livee2e", "") // no mirror_location key
	seedMachineConfig(t, home, filepath.Join(t.TempDir(), "mirrors"))

	c := &checkout{Dir: projectDir, SpaceSlug: "livee2e"}

	got := c.MirrorDir()
	want := filepath.Join(projectDir, ".a2a", "cache", "mirrors", "livee2e")
	if got != want {
		t.Errorf("MirrorDir() = %q, want %q (project-local, id-keyed fallback)", got, want)
	}
}

// TestMirrorDirUnmatchedSpaceIDFallsBackToLegacyPath pins the residual gap
// MirrorDir's own doc comment names: a config that HAS entries but none
// whose ID matches c.SpaceSlug cannot be reported (MirrorDir returns a bare
// string, its call site cannot be changed by this wave) — it silently
// degrades to the same project-local/id-keyed fallback as "no config at
// all", which is only correct if this never actually happens live. This
// test exists so that degradation is a named, asserted behavior rather than
// an unpinned surprise.
func TestMirrorDirUnmatchedSpaceIDFallsBackToLegacyPath(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A config that connected a DIFFERENT space id than this checkout's own
	// SpaceSlug — e.g. the harness's SpaceSlug disagreeing with what the
	// manifest/config actually holds.
	seedProjectConfig(t, projectDir, "some-other-space", "some-other-space")
	seedMachineConfig(t, home, filepath.Join(t.TempDir(), "mirrors"))

	c := &checkout{Dir: projectDir, SpaceSlug: "livee2e"}

	got := c.MirrorDir()
	want := filepath.Join(projectDir, ".a2a", "cache", "mirrors", "livee2e")
	if got != want {
		t.Errorf("MirrorDir() = %q, want %q (legacy fallback for an unmatched space id)", got, want)
	}
}

// TestMirrorDirNoConfigAtAllFallsBackToLegacyShape guards a brand-new
// checkout where nothing has been written yet (no project config, no
// machine config): MirrorDir must not panic and must reproduce the old
// hardcoded, slug-keyed, project-local path — the synthesized zero-value
// ref fallback's whole point.
func TestMirrorDirNoConfigAtAllFallsBackToLegacyShape(t *testing.T) {
	projectDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := &checkout{Dir: projectDir, SpaceSlug: "livee2e"}

	got := c.MirrorDir()
	want := filepath.Join(projectDir, ".a2a", "cache", "mirrors", "livee2e")
	if got != want {
		t.Errorf("MirrorDir() = %q, want %q", got, want)
	}
}
