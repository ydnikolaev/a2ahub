package html

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/skill"
)

// TestDocsManifestParity is the silent-omission guard (mirrors the completion
// parity guard): every *.md in the embedded skill tree must be curated into a
// Documentation section, so a newly added skill doc can't ship in the binary
// yet be invisible in the Documentation tab. There is no skip-set today — every
// skill markdown file is a section. If a future doc is deliberately excluded,
// add it to an explicit skipDocs set here WITH a reason, never widen silently.
func TestDocsManifestParity(t *testing.T) {
	t.Parallel()

	skipDocs := map[string]bool{} // none — every skill *.md is a Documentation section

	inManifest := make(map[string]bool, len(docManifest))
	for _, e := range docManifest {
		inManifest[e.File] = true
	}

	err := fs.WalkDir(skill.Files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		if !inManifest[path] && !skipDocs[path] {
			t.Errorf("skill doc %q is in the binary but missing from docManifest (and not in skipDocs) — it would be invisible in the Documentation tab", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk skill.Files: %v", err)
	}

	// Reverse direction: a manifest entry pointing at a non-existent file.
	for _, e := range docManifest {
		if _, err := fs.Stat(skill.Files, e.File); err != nil {
			t.Errorf("docManifest entry %q (%s) has no file in skill.Files: %v", e.ID, e.File, err)
		}
	}
}

func TestDocs_RendersEverySection(t *testing.T) {
	t.Parallel()
	docs, err := Docs()
	if err != nil {
		t.Fatalf("Docs(): %v", err)
	}
	if len(docs) != len(docManifest) {
		t.Fatalf("got %d sections, want %d", len(docs), len(docManifest))
	}

	groupOK := make(map[string]bool, len(DocGroups))
	for _, g := range DocGroups {
		groupOK[g] = true
	}
	seenID := map[string]bool{}
	for _, d := range docs {
		if strings.TrimSpace(d.HTML) == "" {
			t.Errorf("section %q rendered empty HTML", d.ID)
		}
		if !strings.Contains(d.HTML, "<") {
			t.Errorf("section %q HTML doesn't look rendered (no tags): %q", d.ID, d.HTML)
		}
		if !groupOK[d.Group] {
			t.Errorf("section %q group %q not in DocGroups", d.ID, d.Group)
		}
		if d.ID == "" || d.Title == "" {
			t.Errorf("section has empty id/title: %+v", d)
		}
		if seenID[d.ID] {
			t.Errorf("duplicate section id %q", d.ID)
		}
		seenID[d.ID] = true
	}
}

// TestDocs_GFMTablesRender proves the GFM extension is wired: several skill docs
// use pipe tables, which PLAIN goldmark renders as literal "|"-text. The command
// reference is a bullet list, but onboarding/loops carry tables — assert at least
// one real <table> made it through.
func TestDocs_GFMTablesRender(t *testing.T) {
	t.Parallel()
	docs, err := Docs()
	if err != nil {
		t.Fatalf("Docs(): %v", err)
	}
	var anyTable bool
	for _, d := range docs {
		if strings.Contains(d.HTML, "<table>") {
			anyTable = true
			break
		}
	}
	if !anyTable {
		t.Error("no <table> in any section — GFM table extension likely not enabled")
	}
}

// TestSkillDeadlinePolicy keeps the agent-operating rule concrete. The schema
// cannot require needed_by on every artifact because one-way artifacts expect
// no response; the authoring skill is what makes response-bearing exchanges
// autonomous and AI-speed rather than silently borrowing human review SLAs.
func TestSkillDeadlinePolicy(t *testing.T) {
	t.Parallel()
	source, err := fs.ReadFile(skill.Files, "a2ahub/loops.md")
	if err != nil {
		t.Fatalf("read loops.md: %v", err)
	}
	policy := string(source)
	for _, required := range []string{
		"`created` calendar date +1 day",
		"`priority: p1`",
		"+2 days otherwise",
		"human to choose a routine deadline",
		"external, non-agent constraint",
	} {
		if !strings.Contains(policy, required) {
			t.Errorf("deadline policy is missing %q", required)
		}
	}
}

func TestSkillCorrectionPolicy(t *testing.T) {
	t.Parallel()
	source, err := fs.ReadFile(skill.Files, "a2ahub/loops.md")
	if err != nil {
		t.Fatalf("read loops.md: %v", err)
	}
	policy := string(source)
	for _, required := range []string{
		"A submitted artifact is immutable",
		"a2a note --note <clarification> <id>",
		"including correcting `needed_by`",
		"`supersedes: <old-id>`",
		// Subject first, flag after — matching `a2a withdraw <id>`,
		// `a2a cancel <id>` and every other lifecycle verb, and matching
		// loops.md's own three other supersede citations. This line used to
		// pin the reverse order, which was the one outlier of four in a
		// document whose whole job is teaching an agent how to invoke the
		// tool. `parseArgsAnyOrder` accepts both, so the inconsistency was
		// never a defect — it was just the document contradicting itself
		// where it can least afford to.
		"a2a supersede <old-id> --refs <new-id>",
	} {
		if !strings.Contains(policy, required) {
			t.Errorf("correction policy is missing %q", required)
		}
	}
}

func TestDocs_Deterministic(t *testing.T) {
	t.Parallel()
	a, err := Docs()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Docs()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("len mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("section %d not deterministic:\n%+v\nvs\n%+v", i, a[i], b[i])
		}
	}
}

func TestStripLeadingH1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"strips leading h1", "# Title\n\n## Body\ntext", "\n## Body\ntext"},
		{"skips blank lines first", "\n\n# Title\n## Body", "\n\n## Body"},
		{"no h1 unchanged", "## Section\ntext", "## Section\ntext"},
		{"h1-in-middle kept", "intro\n# Not first\n", "intro\n# Not first\n"},
	}
	for _, tt := range tests {
		if got := string(stripLeadingH1([]byte(tt.in))); got != tt.want {
			t.Errorf("%s: stripLeadingH1(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}
