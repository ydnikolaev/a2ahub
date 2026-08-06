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
// nothing watched: the canonical template is what `a2a new` renders, and
// `skill/a2ahub/reference/authoring/<type>.md` is a BYTE COPY of it that an
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
//
// # Why the BINARY, and not schemas/templates/v1/, is the comparand
//
// It used to read `schemas/templates/v1/<type>.md` directly, and that hard-coded
// directory is how this gate stayed green through the exact drift it exists to
// catch. On 2026-08-06 an external report (fb-20260806-3539ac) showed the
// shipped `contract` authoring page still documenting the envelope/v1 shape —
// no top-level `artifacts:` inventory — while `a2a new contract` had moved to
// envelope/v2. The page and templates/v1 agreed perfectly; they were simply no
// longer the document the tool renders. An author followed the page, and their
// contract validated, submitted, passed the space's own CI, merged to main, and
// could then never publish.
//
// WHICH GENERATION a type authors at is a decision the binary makes
// (internal/template.AuthoringEnvelopeSchema), so a gate that names a directory
// is asserting something it is not entitled to assert. Asking
// `a2a template show <type>` asks the product, which is the only comparand that
// cannot silently fall behind the product.
func TestAuthoringPagesMatchTheTemplatesTheyDocument(t *testing.T) {
	t.Parallel()

	root := repoRootForTest(t)
	bin := filepath.Join(binDir, "a2a")

	// The type list comes from the binary too, so a NEW envelope type cannot
	// ship without an authoring page: a hard-coded list would simply not know
	// to look for it.
	listCmd := exec.Command(bin, "template", "list")
	listCmd.Dir = root
	listed, err := listCmd.Output()
	if err != nil {
		t.Fatalf("a2a template list: %v", err)
	}

	var checked int
	for _, line := range strings.Split(strings.TrimSpace(string(listed)), "\n") {
		typ, generation, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if typ == "" {
			continue
		}
		showCmd := exec.Command(bin, "template", "show", typ)
		showCmd.Dir = root
		tpl, err := showCmd.Output()
		if err != nil {
			t.Errorf("a2a template show %s: %v", typ, err)
			continue
		}
		guidePath := filepath.Join(root, "skill", "a2ahub", "reference", "authoring", typ+".md")
		guide, err := os.ReadFile(guidePath)
		if err != nil {
			t.Errorf("type %s has no authoring page at %s: %v — every artifact type an agent can "+
				"draft needs the page that documents it", typ, guidePath, err)
			continue
		}
		checked++
		if !bytes.Equal(tpl, guide) {
			t.Errorf("skill/a2ahub/reference/authoring/%s.md has drifted from what `a2a template show %s` "+
				"renders (%s).\nThey are the same document: one is rendered by `a2a new`, the other is read "+
				"by an agent, and an agent that follows a stale page writes an artifact the tool refuses.\n"+
				"Re-sync it from the BINARY, never from a fixed templates directory:\n"+
				"    go run ./cmd/a2a template show %s > skill/a2ahub/reference/authoring/%s.md",
				typ, typ, generation, typ, typ)
		}
	}

	// A gate that found no pairs would pass silently forever — the same
	// vacuity that let a manifest schema sit wired to nothing.
	if checked == 0 {
		t.Fatal("no template/authoring pairs were compared — this gate is guarding nothing")
	}
	t.Logf("compared %d template/authoring pairs", checked)
}
