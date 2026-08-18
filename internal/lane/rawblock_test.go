package lane

import (
	"strings"
	"testing"
)

func TestFindLaneBlocksScoped(t *testing.T) {
	src := `#!/usr/bin/env bash
# check-readme.sh — keep it short.
#
# lane-inputs:
#   README.md
set -euo pipefail
`
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if b.Kind != KindScoped {
		t.Errorf("Kind = %v, want KindScoped", b.Kind)
	}
	if len(b.Inputs) != 1 || b.Inputs[0] != "README.md" {
		t.Errorf("Inputs = %v, want [README.md]", b.Inputs)
	}
	if b.Following != "set -euo pipefail" {
		t.Errorf("Following = %q, want %q", b.Following, "set -euo pipefail")
	}
	if b.StartLine != 4 {
		t.Errorf("StartLine = %d, want 4", b.StartLine)
	}
}

func TestFindLaneBlocksAlways(t *testing.T) {
	src := `#!/usr/bin/env bash
# lane-inputs: ALWAYS
# lane-reason: reads git ls-files over the whole tracked set
run_check() {
`
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if b.Kind != KindAlways {
		t.Errorf("Kind = %v, want KindAlways", b.Kind)
	}
	if b.Reason == "" {
		t.Errorf("Reason is empty")
	}
	if len(b.Inputs) != 0 {
		t.Errorf("Inputs = %v, want none", b.Inputs)
	}
}

func TestFindLaneBlocksAlwaysReasonContinuesAcrossLines(t *testing.T) {
	src := `if [ "$MODE" = test ]; then
  # lane-inputs: NEVER
  # lane-reason: parameterised by the caller (` + "`make test PKG=…`" + `). The derivation
  #   emits ` + "`go-test-scoped:<pkg>`" + ` from a package's OWN doc.go lane-inputs block;
  #   this bare phase is that mechanism's implementation, never a selectable one.
  run_phase go-test-scoped run_scoped_tests
fi
`
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if b.Kind != KindNever {
		t.Errorf("Kind = %v, want KindNever", b.Kind)
	}
	if !strings.Contains(b.Reason, "parameterised by the caller") || !strings.Contains(b.Reason, "never a selectable one.") {
		t.Errorf("Reason did not join continuation lines: %q", b.Reason)
	}
	if b.Following != `  run_phase go-test-scoped run_scoped_tests` {
		t.Errorf("Following = %q", b.Following)
	}
}

func TestFindLaneBlocksNeverRequiresReason(t *testing.T) {
	src := `#!/usr/bin/env bash
# lane-inputs: NEVER
run_check() {
`
	_, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err == nil {
		t.Fatal("expected an error for NEVER with no lane-reason line")
	}
}

func TestFindLaneBlocksScopedWithNoGlobsIsAnError(t *testing.T) {
	src := `# lane-inputs:
run_check() {
`
	_, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err == nil {
		t.Fatal("expected an error for lane-inputs: with no glob lines")
	}
}

func TestFindLaneBlocksGoPrefix(t *testing.T) {
	src := `// Package notes ...
//
// lane-inputs:
//   releasenotes/**
package notes
`
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "//")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Inputs[0] != "releasenotes/**" {
		t.Fatalf("got %+v", blocks)
	}
	if blocks[0].Following != "package notes" {
		t.Errorf("Following = %q", blocks[0].Following)
	}
}

func TestFindLaneBlocksGodocPreformattedList(t *testing.T) {
	src := "// Package notes ...\n" +
		"//\n" +
		"// lane-inputs:\n" +
		"//\n" +
		"//\treleasenotes/**\n" +
		"//\tinternal/notes/testdata/**\n" +
		"package notes\n"
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "//")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	want := []string{"releasenotes/**", "internal/notes/testdata/**"}
	got := blocks[0].Inputs
	if len(got) != len(want) {
		t.Fatalf("Inputs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Inputs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if blocks[0].Following != "package notes" {
		t.Errorf("Following = %q", blocks[0].Following)
	}
}

func TestFindLaneBlocksExclusion(t *testing.T) {
	src := `# lane-inputs:
#   internal/localserver/**/*.go
#   !internal/localserver/**/*_test.go
localserver-readonly-routes:
`
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	want := []string{"internal/localserver/**/*.go", "!internal/localserver/**/*_test.go"}
	got := blocks[0].Inputs
	if len(got) != len(want) {
		t.Fatalf("Inputs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Inputs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFindLaneBlocksAlwaysWithClaims(t *testing.T) {
	src := `#!/usr/bin/env bash
# lane-inputs: ALWAYS
# lane-reason: check A runs git log with no pathspec, judging commit subjects
# lane-claims:
#   docs/status.md
#   docs/features/**/tracker.yaml
run_check() {
`
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if b.Kind != KindAlways {
		t.Errorf("Kind = %v, want KindAlways", b.Kind)
	}
	if b.Reason == "" || strings.Contains(b.Reason, "lane-claims") {
		t.Errorf("Reason = %q, must not swallow the lane-claims: header", b.Reason)
	}
	want := []string{"docs/status.md", "docs/features/**/tracker.yaml"}
	if len(b.Claims) != len(want) {
		t.Fatalf("Claims = %v, want %v", b.Claims, want)
	}
	for i := range want {
		if b.Claims[i] != want[i] {
			t.Errorf("Claims[%d] = %q, want %q", i, b.Claims[i], want[i])
		}
	}
	if b.Following != "run_check() {" {
		t.Errorf("Following = %q", b.Following)
	}
}

func TestFindLaneBlocksClaimsIllegalOnNever(t *testing.T) {
	src := `# lane-inputs: NEVER
# lane-reason: needs two credentials, network and a real GitHub space
# lane-claims:
#   docs/status.md
run_check() {
`
	_, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err == nil {
		t.Fatal("expected an error: lane-claims: is illegal on a NEVER block")
	}
	if !strings.Contains(err.Error(), "lane-claims") {
		t.Errorf("error does not name lane-claims: %v", err)
	}
}

func TestFindLaneBlocksClaimsIllegalOnScoped(t *testing.T) {
	src := `# lane-inputs:
#   README.md
# lane-claims:
#   docs/status.md
run_check() {
`
	_, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err == nil {
		t.Fatal("expected an error: lane-claims: is illegal on a scoped block")
	}
	if !strings.Contains(err.Error(), "lane-claims") {
		t.Errorf("error does not name lane-claims: %v", err)
	}
}

func TestFindLaneBlocksClaimsHeaderWithNoGlobsIsAnError(t *testing.T) {
	src := `# lane-inputs: ALWAYS
# lane-reason: reads git ls-files over the whole tracked set
# lane-claims:
run_check() {
`
	_, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err == nil {
		t.Fatal("expected an error: lane-claims: header with no glob lines")
	}
}

// TestFindLaneBlocksScopedWithReadsOpaqueDoesNotAbsorbIt pins D-11: a
// trailing "lane-reads-opaque:" directive (with its inline reason text and
// indented continuation lines, the same shape lane-reason: uses) below a
// scoped block's globs must not be swallowed into Inputs as bogus glob
// entries — it is its own trailing sub-block, consumed into EndLine (so
// Following still resolves past it) but never stored as an Input.
func TestFindLaneBlocksScopedWithReadsOpaqueDoesNotAbsorbIt(t *testing.T) {
	src := `#!/usr/bin/env bash
if [ "$MODE" = logic-e2e ]; then
  # lane-inputs:
  #   **/*.go
  #   go.mod
  # lane-reads-opaque: trim_telemetry (line ~156) rotates
  #   "$VERIFY_ROOT"/telemetry.jsonl through "$tmp". Not repo content.
  run_phase logic-e2e run_logic_tests
fi
`
	lines := strings.Split(src, "\n")
	blocks, err := findLaneBlocks(lines, "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	want := []string{"**/*.go", "go.mod"}
	if len(b.Inputs) != len(want) {
		t.Fatalf("Inputs = %v, want %v (lane-reads-opaque: lines must not be absorbed as globs)", b.Inputs, want)
	}
	for i := range want {
		if b.Inputs[i] != want[i] {
			t.Errorf("Inputs[%d] = %q, want %q", i, b.Inputs[i], want[i])
		}
	}
	// EndLine (1-based) must cover the opaque directive's own two lines —
	// lines 6 and 7 — so Following resolves past them to the real
	// run_phase statement rather than to the directive comment itself.
	if want := 7; b.EndLine != want {
		t.Errorf("EndLine = %d, want %d (must consume the lane-reads-opaque: sub-block)", b.EndLine, want)
	}
	if want := "  run_phase logic-e2e run_logic_tests"; b.Following != want {
		t.Errorf("Following = %q, want %q", b.Following, want)
	}
}

// TestFindLaneBlocksAlwaysNeverWithReadsOpaqueDoesNotAbsorbIt pins the same
// D-11 guard on the ALWAYS/NEVER arm: a "lane-reads-opaque:" directive right
// after lane-reason: must not be joined into Reason as more continuation
// prose.
func TestFindLaneBlocksAlwaysNeverWithReadsOpaqueDoesNotAbsorbIt(t *testing.T) {
	src := `if [ "$MODE" = live ]; then
  # lane-inputs: NEVER
  # lane-reason: two credentials and a real throwaway GitHub space with
  #   Actions latency. No diff may select it.
  # lane-reads-opaque: trim_telemetry (line ~156) rotates
  #   "$VERIFY_ROOT"/telemetry.jsonl through "$tmp". Not repo content.
  run_phase live-e2e run_live_tests
fi
`
	lines := strings.Split(src, "\n")
	blocks, err := findLaneBlocks(lines, "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if b.Kind != KindNever {
		t.Errorf("Kind = %v, want KindNever", b.Kind)
	}
	if strings.Contains(b.Reason, "lane-reads-opaque") || strings.Contains(b.Reason, "trim_telemetry") {
		t.Errorf("Reason absorbed the lane-reads-opaque: directive: %q", b.Reason)
	}
	if !strings.Contains(b.Reason, "No diff may select it.") {
		t.Errorf("Reason lost its own continuation line: %q", b.Reason)
	}
	// EndLine (1-based) must cover both lane-reads-opaque: lines — lines 5
	// and 6, so Following resolves past them to the real run_phase line.
	if want := 6; b.EndLine != want {
		t.Errorf("EndLine = %d, want %d (must consume the lane-reads-opaque: sub-block)", b.EndLine, want)
	}
	if want := "  run_phase live-e2e run_live_tests"; b.Following != want {
		t.Errorf("Following = %q, want %q", b.Following, want)
	}
}

func TestFindLaneBlocksNone(t *testing.T) {
	src := `#!/usr/bin/env bash
# an ordinary comment, no lane-inputs block here
echo hi
`
	blocks, err := findLaneBlocks(strings.Split(src, "\n"), "#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("got %d blocks, want 0", len(blocks))
	}
}
