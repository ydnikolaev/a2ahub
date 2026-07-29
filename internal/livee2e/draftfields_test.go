package livee2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftFieldArgsCoversEveryEnvelopeType(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{
		"announcement", "contract", "requirement", "question",
		"work_request", "decision", "handoff", "response",
	} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			args, ok := draftFieldArgs(typ, "livee2e", "alpha", "bravo")
			if !ok || len(args) == 0 {
				t.Fatalf("no public field inputs for %s", typ)
			}
			joined := strings.Join(args, "\n")
			for _, want := range []string{"--field", "title=matrix " + typ, "space=livee2e"} {
				if !strings.Contains(joined, want) {
					t.Errorf("%s inputs missing %q: %v", typ, want, args)
				}
			}
		})
	}
}

// The harness once reopened staged Markdown and regex-patched frontmatter.
// Keep the absence of that second authoring path structural: scenario data
// must enter through draftFieldArgs / `a2a new --field`.
func TestLiveScenariosHaveNoFrontmatterPatchPath(t *testing.T) {
	t.Parallel()
	entries, err := filepath.Glob("*_live.go")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"FillDraft(", "ToLinePattern", "boundaryProbeEnvelope(", `filepath.Join(c.Dir, ".a2a", "staging", id+".md")`}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range banned {
			if strings.Contains(string(raw), needle) {
				t.Errorf("%s reintroduces direct frontmatter authoring via %q", path, needle)
			}
		}
	}
	if _, err := os.Stat("draftfill.go"); !os.IsNotExist(err) {
		t.Errorf("draftfill.go exists again; live authoring must use the public command")
	}
}
