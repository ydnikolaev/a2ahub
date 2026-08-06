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
