package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestAuthoringPagesMatchTheTemplatesTheyDocument closes a drift channel that
// nothing watched: `schemas/templates/v1/<type>.md` is what `a2a new` renders,
// and `skill/a2ahub/reference/authoring/<type>.md` is a BYTE COPY of it that an
// agent reads. Two files, one document, nothing comparing them.
//
// Both have to ship — the templates are embedded for rendering, the skill tree
// is installed into consumer repos — so deduplicating them is not on the table.
// What is on the table is noticing when they disagree, and the copy an AGENT
// reads is the likelier of the two to rot: the one the binary renders is at least
// exercised by every draft anyone writes.
//
// Found while giving the contract body four named sections; both were edited
// together that time, which is precisely the accident this test removes the need
// for. Verified 2026-07-26: all eight pairs were byte-identical, so this gate
// starts from a clean corpus rather than grandfathering a difference.
//
// A gate rather than generation, and the earlier backlog note calling a gate
// "just moving the work" was wrong. Generation would be nicer to maintain; a gate
// makes drift unmergeable, which is the actual requirement. The cost it imposes —
// editing two files together — is the cost of the two files existing, not of
// checking them.
func TestAuthoringPagesMatchTheTemplatesTheyDocument(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	tplDir := filepath.Join(root, "schemas", "templates", "v1")
	entries, err := os.ReadDir(tplDir)
	if err != nil {
		t.Fatalf("read %s: %v", tplDir, err)
	}

	var checked int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		tplPath := filepath.Join(tplDir, e.Name())
		guidePath := filepath.Join(root, "skill", "a2ahub", "reference", "authoring", e.Name())

		tpl, err := os.ReadFile(tplPath)
		if err != nil {
			t.Errorf("read %s: %v", tplPath, err)
			continue
		}
		guide, err := os.ReadFile(guidePath)
		if err != nil {
			t.Errorf("template %s has no authoring page at %s: %v — every artifact type an agent can "+
				"draft needs the page that documents it", e.Name(), guidePath, err)
			continue
		}
		checked++
		if !bytes.Equal(tpl, guide) {
			t.Errorf("skill/a2ahub/reference/authoring/%s has drifted from schemas/templates/v1/%s.\n"+
				"They are the same document: one is rendered by `a2a new`, the other is read by an agent. "+
				"Copy the template over the page:\n"+
				"    cp schemas/templates/v1/%s skill/a2ahub/reference/authoring/%s",
				e.Name(), e.Name(), e.Name(), e.Name())
		}
	}

	// A gate that found no pairs would pass silently forever — the same
	// vacuity that let a manifest schema sit wired to nothing.
	if checked == 0 {
		t.Fatal("no template/authoring pairs were compared — this gate is guarding nothing")
	}
	t.Logf("compared %d template/authoring pairs", checked)
}
