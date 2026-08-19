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

// TestDraftBodyHandoffFillsEverySectionThePolicyRequires pins the matrix's
// handoff body against the rule that refused the old one. POL-022
// (defects-fix-2026-08 P9) refuses a §16.2-required section carrying no
// content, and the driver used to submit a template-rendered handoff — five
// headings and nothing under them.
//
// It asserts CONTENT, not length: the rule measures emptiness and so does
// this, because a test asserting a word count would license padding, which is
// the one answer the rule's own spec forbids.
func TestDraftBodyHandoffFillsEverySectionThePolicyRequires(t *testing.T) {
	t.Parallel()

	body, ok := draftBody("handoff")
	if !ok {
		t.Fatal("draftBody(handoff) reported no body — the matrix would submit a template-rendered handoff and POL-022 would refuse it")
	}
	sections := []string{"## Context", "## What was built", "## How to verify", "## How to operate", "## Limitations & next steps"}
	lines := strings.Split(body, "\n")
	for _, heading := range sections {
		idx := -1
		for i, l := range lines {
			if strings.TrimSpace(l) == heading {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Errorf("draftBody(handoff) omits the §16.2 section %q", heading)
			continue
		}
		content := false
		for _, l := range lines[idx+1:] {
			if strings.HasPrefix(strings.TrimSpace(l), "## ") {
				break
			}
			if strings.TrimSpace(l) != "" {
				content = true
				break
			}
		}
		if !content {
			t.Errorf("draftBody(handoff) leaves %q empty — that is the exact shape POL-022 refuses", heading)
		}
	}

	if _, ok := draftBody("question"); ok {
		t.Error("draftBody claims a body for a type whose template carries no body invariant")
	}
}
