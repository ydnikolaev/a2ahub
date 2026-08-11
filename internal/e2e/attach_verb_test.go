package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachVerbReachesTheBuiltBinary is `attach`'s coverage-manifest
// evidence (coverage.go's own rule: every catalog verb owns a row, and a
// GoTest row resolves to a test in THIS package that drives the built
// binary).
//
// internal/cli/cmd_attach_test.go already covers the verb's behaviour in
// process. This proves the different thing that manifest is about: the verb
// is REACHABLE — registered in the dispatch table, present in the catalog,
// and able to do its job when invoked as `a2a attach` rather than as a Go
// constructor.
//
// The distinction is not academic. `a2a attach` was fully implemented and
// unreachable for one wave: `runAttach` existed, the command existed, and
// the registration line was commented out because registering it panicked
// the catalog. Every in-process test stayed green throughout.
//
// P10 (agent-exchange-2026-08) wave B/C: `a2a attach` stopped being a local
// draft edit and became a NETWORK WRITE (spec 10 §1's decided ordering — it
// lands the bytes into the space, its own commit, before the draft ever
// references them). A bare temp dir with no `.a2a/config.yaml` therefore no
// longer reaches attach's own logic at all — it refuses at `resolvePaths()`
// with "no project config (run `a2a init` first)", which is the correct
// consequence of the ordering decision, not a regression. This test now
// drives attach through hostRig (host_loop_test.go) — a real space, a real
// project/machine config, and the exec'd binary pointed at an in-process
// GitHub stand-in — the same rig this package's other network-writing tests
// already use, so the fixture matches what the verb actually is now.
func TestAttachVerbReachesTheBuiltBinary(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "seomatrix")

	// A minimal envelope/v2 work_request, hand-written to keep this test's
	// subject narrow: it asks whether `a2a attach` reaches the draft, not
	// whether `a2a new` can produce one.
	draftPath := r.stageAttachWorkRequest("XW-axon-20260808-attach", "seomatrix")

	payload := filepath.Join(r.projectDir, "payload.csv")
	mustWrite(t, payload, "a,b\n1,2\n")

	out := r.mustRun("attach", draftPath, "--from", payload, "--verification", "offered")
	if !strings.Contains(out, "written to the space") {
		t.Errorf("attach stdout does not report the network write: %q", out)
	}

	after := mustReadFile(t, draftPath)
	if !strings.Contains(after, "attachments:") {
		t.Errorf("draft carries no attachments block after `a2a attach`:\n%s", after)
	}
	// P10 spec 10 §2: ref carries a MINTED BL- id now, not a bare digest —
	// the reference is something the space resolves, not merely the digest
	// restated. Asserting the BL- shape (not only "sha256:", which the
	// `digest:` field still legitimately carries) is what now matters.
	if !strings.Contains(after, "ref: BL-axon-") {
		t.Errorf("the attachment's ref is not a minted BL- id:\n%s", after)
	}
	if !strings.Contains(after, "digest: sha256:") {
		t.Errorf("the attachment carries no sha256 digest — the digest is what verifies the bytes the ref names, and without it nobody can check them:\n%s", after)
	}
	// `verification: offered` was asked for and must be what landed.
	// Defaulting it to `required` would claim somebody promised a verdict on
	// these bytes, which is the fused-axes problem this phase separates.
	if !strings.Contains(after, "offered") {
		t.Errorf("verification is not `offered` as requested:\n%s", after)
	}

	// The blob really landed in the space as its own commit, BEFORE the
	// draft (still local/staged) references it — spec 10 §1's decided
	// ordering, and the reason attach costs a PR at all now.
	if got := len(r.gh.PRs()); got != 1 {
		t.Fatalf("PRs after one `a2a attach` = %d, want exactly 1 (the blob's own commit)", got)
	}
}
