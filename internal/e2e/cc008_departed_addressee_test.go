package e2e

import (
	"strings"
	"testing"
)

// cc008DepartedManifestYAML is properManifestYAML's own shape
// (helpers_test.go), except that one participant — "beta" — carries
// `status: left` instead of `status: active`. helpers_test.go is off
// limits to this file, so this is a deliberate, narrow duplicate rather
// than a call into fixOriginManifest's own all-active seed: CC-008's
// `left` branch (internal/validate/authz.go's checkAddressees) has no
// other way to observe a departed system through the real manifest-load
// path (cmd/a2a/wire.go's loadManifest -> cli.NewMirrorResolver), which is
// exactly the path this test exists to pin.
func cc008DepartedManifestYAML(spaceID string) string {
	var b strings.Builder
	b.WriteString("schema: space/v1\n")
	b.WriteString("space: " + spaceID + "\n")
	b.WriteString("min_binary_version: \"0.0.0\"\n")
	b.WriteString("participants:\n")
	for _, p := range []struct{ system, status string }{
		{"axon", "active"},
		{"beta", "left"},
		{"gamma", "active"},
	} {
		b.WriteString("  - system: " + p.system + "\n")
		b.WriteString("    org: fixture\n")
		b.WriteString("    section: " + p.system + "\n")
		b.WriteString("    owners: [" + p.system + "-bot]\n")
		b.WriteString("    status: " + p.status + "\n")
		b.WriteString("    joined: \"2026-01-01\"\n")
	}
	return b.String()
}

// departManifestParticipant overwrites originDir's main-branch space.yaml
// with cc008DepartedManifestYAML, the same throwaway-clone+commit+push
// idiom fixOriginManifest (helpers_test.go) uses for its own all-active
// seed — reused here, on THIS file's own manifest content, because
// newHostRig already calls fixOriginManifest once (leaving every
// participant active) and this test needs to land a SECOND, later commit
// that departs one of them before the rig's project ever clones the
// mirror.
func departManifestParticipant(t *testing.T, originDir, spaceID string) {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, "", "clone", originDir, dir)
	writeMirrorFile(t, dir, "space.yaml", cc008DepartedManifestYAML(spaceID))
	if !gitHasChanges(t, dir) {
		t.Fatal("departManifestParticipant: space.yaml already carries the departed-beta manifest — the fixture seed changed under this test")
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "fix: depart beta from space.yaml")
	gitRun(t, dir, "push", "origin", "main")
}

// TestCC008DepartedAddresseeIsRefused pins CC-008's `left`-system branch at
// the real-binary tier: submitting an envelope addressed to a system the
// manifest marks `status: left` must be REFUSED, naming REF-006 and the
// `left` condition specifically — not merely REF-006, which
// checkAddressees (internal/validate/authz.go) also emits for an unknown
// system, a DIFFERENT branch with a different message. A companion submit
// addressed to an ACTIVE participant is the negative control: without it,
// a test that refuses every submit for any reason would still pass.
func TestCC008DepartedAddresseeIsRefused(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	departManifestParticipant(t, r.fx.RemoteURL(), r.spaceID)
	// The rig's project has not cloned the mirror yet (submit below will be
	// its first git operation against origin), but `sync` first makes the
	// rig's own dependency on a real, already-departed manifest explicit
	// rather than incidental to submit's own clone-on-first-use behavior.
	r.mustRun("sync")

	departedID := "XQ-axon-20260811-c008"
	departedPath := r.stageQuestion(departedID, "beta")
	stdout, stderr, code := r.run("submit", departedPath)
	if code == 0 {
		t.Fatalf("submit to a departed system succeeded: exit 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "REF-006") {
		t.Fatalf("refusal does not name REF-006:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(combined, "marked `left`") {
		t.Fatalf("refusal does not name the `left` condition — got:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if strings.Contains(combined, "unknown system") {
		t.Fatalf("refusal fired the UNKNOWN-system branch, not the LEFT branch:\nstdout: %s\nstderr: %s", stdout, stderr)
	}

	// Negative control: the same submit, addressed to an ACTIVE
	// participant, must not be refused for CC-008 at all.
	activeID := "XQ-axon-20260811-c00b"
	activePath := r.stageQuestion(activeID, "gamma")
	out := r.mustRun("submit", activePath)
	if strings.Contains(out, "REF-006") {
		t.Fatalf("submit to an ACTIVE participant was refused with REF-006:\nstdout: %s", out)
	}
}
