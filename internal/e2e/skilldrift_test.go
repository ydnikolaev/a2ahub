package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestSkillReferenceIsByteCurrent moves the `skill-drift` guarantee from
// CI-only into `make check`.
//
// Why this test exists at all. `skill/a2ahub/reference/commands.md` and
// `reference/authoring/*.md` are GENERATED from the binary, and SKILL.md tells
// every agent they are "the source of truth for invocation syntax". A CI job
// (.github/workflows/ci.yml, `skill-drift`) regenerates and byte-diffs them —
// but that job is not in `REPO_GATES` and not in `make check`, so the documented
// ceiling for "done" passed while the branch was red. That is filed in
// docs/validator-backlog.md (2026-07-24, "gate reachability") with the exact
// consequence: "a lead can honestly declare a phase done on a red branch." It
// happened: P35 shipped a new sub-verb and the committed reference went stale.
//
// A Go test is the right shape rather than a new script — it rides
// `go test ./...` inside `make check` with no new gate surface, the same
// reasoning the epic recorded for the AC-401.2 schema-pairing gate. The CI job
// stays: it is the belt for a checkout that cannot build.
//
// What this does NOT catch, stated because the limit is the interesting part:
// this proves the committed file matches a REGENERATION, not that the
// regeneration is TRUE. A stale sentence inside a Synopsis() string regenerates
// to the same stale bytes and stays green forever — which is exactly how
// `doctor`'s synopsis claimed five checks for two releases while it ran nine.
// Only a human reading the release checklist catches that class.
func TestSkillReferenceIsByteCurrent(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	tmp := t.TempDir()

	// The binary TestMain already built is the one to ask — building a second
	// copy here would double this package's slowest step.
	cmd := exec.Command(filepath.Join(binDir, "a2a"), "__catalog")
	cmd.Dir = root
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("a2a __catalog: %v", err)
	}
	regen := filepath.Join(tmp, "commands.md")
	if err := os.WriteFile(regen, got, 0o644); err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile(filepath.Join(root, "skill", "a2ahub", "reference", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		// A diff is far more useful than "bytes differ" when the cause is one
		// renamed flag among 42 commands.
		d := exec.Command("diff", "-u",
			filepath.Join(root, "skill", "a2ahub", "reference", "commands.md"), regen)
		diffOut, _ := d.CombinedOutput()
		t.Fatalf("skill/a2ahub/reference/commands.md is not byte-current with this binary's `a2a __catalog`.\n"+
			"Regenerate it:  go run ./cmd/a2a __catalog > skill/a2ahub/reference/commands.md\n"+
			"An agent is told this file is the source of truth for invocation syntax, so a stale copy "+
			"teaches it a command surface that does not exist.\n\n%s", diffOut)
	}
	// gitfixture is imported so this package's own hygiene gate sees the
	// reference; this test spawns no git of its own.
	_ = gitfixture.Args
}
