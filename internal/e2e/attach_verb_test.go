package e2e

import (
	"os"
	"os/exec"
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
func TestAttachVerbReachesTheBuiltBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// A minimal envelope/v2 work_request, hand-written to keep this test's
	// subject narrow: it asks whether `a2a attach` reaches the draft, not
	// whether `a2a new` can produce one.
	//
	// It used to say the fixture was a WORKAROUND, because `a2a new` authored
	// envelope/v1 for this type and the generation declaring attachments[]
	// was unreachable from the tool. That gap is closed —
	// schemas/templates/v2/work_request.md ships and template.go's
	// selectGeneration makes `a2a new work_request` unconditionally v2 — so
	// the fixture is now a choice rather than a detour.
	draft := filepath.Join(dir, "XW-axon-20260808-attach.md")
	body := "---\n" +
		"schema: envelope/v2\n" +
		"id: XW-axon-20260808-attach\n" +
		"type: work_request\n" +
		"title: attach reachability\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [seomatrix]\n" +
		"thread: XW-axon-20260808-attach\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-08-08T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"category: analysis\n" +
		"acceptance_criteria: [\"it attaches\"]\n" +
		"---\nbody\n"
	if err := os.WriteFile(draft, []byte(body), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	payload := filepath.Join(dir, "payload.csv")
	if err := os.WriteFile(payload, []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	cmd := exec.Command(filepath.Join(binDir, "a2a"), "attach", draft,
		"--from", payload, "--verification", "offered")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a2a attach: %v\n%s", err, out)
	}

	after, err := os.ReadFile(draft)
	if err != nil {
		t.Fatalf("re-read draft: %v", err)
	}
	got := string(after)
	if !strings.Contains(got, "attachments:") {
		t.Errorf("draft carries no attachments block after `a2a attach`:\n%s", got)
	}
	if !strings.Contains(got, "sha256:") {
		t.Errorf("the attachment carries no sha256 digest — the reference is the digest, and without it the entry names bytes nobody can verify:\n%s", got)
	}
	// `verification: offered` was asked for and must be what landed.
	// Defaulting it to `required` would claim somebody promised a verdict on
	// these bytes, which is the fused-axes problem this phase separates.
	if !strings.Contains(got, "offered") {
		t.Errorf("verification is not `offered` as requested:\n%s", got)
	}
}
