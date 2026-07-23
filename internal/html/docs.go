package html

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/ydnikolaev/a2ahub/skill"
)

// docs.go assembles the Documentation tab's content: the committed skill tree
// (skill/a2ahub/**, the same SSOT `a2a skill install` ships and the drift gate
// guards) rendered to HTML and injected as the page's DOCS global. Distinct
// from DATA: DOCS[].html is OUR content, so the template sets it via innerHTML;
// DATA is artifact-controlled and stays textContent-only. Rendering is
// server-side (goldmark) — no client markdown lib, keeping the page
// self-contained.

// DocSection is one Documentation section = the page's DOCS[] entry shape.
type DocSection struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Title string `json:"title"`
	HTML  string `json:"html"`
}

// DocGroups is the section-group vocabulary, matching the template's
// GROUP_ORDER (Start → Concepts → Reference → Authoring → Help). A section
// whose Group is outside this set still renders (the template appends unknown
// groups after the ordered ones), but the manifest keeps to these five.
var DocGroups = []string{"Start", "Concepts", "Reference", "Authoring", "Help"}

// docEntry curates one skill markdown file into a section: its stable id (also
// the deep-link anchor), its group, its nav title, and its path inside
// skill.Files. Slice order is the in-group section order.
type docEntry struct {
	ID, Group, Title, File string
}

// docManifest is the ordered curation of skill/a2ahub/** into Documentation
// sections. EVERY *.md in skill.Files must appear here (there is no skip-set) —
// TestDocsManifestParity gates that, so a newly added skill doc can't silently
// miss the Documentation tab (the same silent-omission guard as completion).
var docManifest = []docEntry{
	{"getting-started", "Start", "Getting started", "a2ahub/onboarding.md"},
	{"overview", "Concepts", "Overview", "a2ahub/SKILL.md"},
	{"work-loops", "Concepts", "The work loops", "a2ahub/loops.md"},
	{"commands", "Reference", "Command reference", "a2ahub/reference/commands.md"},
	{"decompose", "Reference", "Decompose example", "a2ahub/reference/decompose-example.md"},
	{"authoring-contract", "Authoring", "Authoring: Contract", "a2ahub/reference/authoring/contract.md"},
	{"authoring-requirement", "Authoring", "Authoring: Requirement", "a2ahub/reference/authoring/requirement.md"},
	{"authoring-question", "Authoring", "Authoring: Question", "a2ahub/reference/authoring/question.md"},
	{"authoring-work_request", "Authoring", "Authoring: Work request", "a2ahub/reference/authoring/work_request.md"},
	{"authoring-decision", "Authoring", "Authoring: Decision", "a2ahub/reference/authoring/decision.md"},
	{"authoring-handoff", "Authoring", "Authoring: Handoff", "a2ahub/reference/authoring/handoff.md"},
	{"authoring-response", "Authoring", "Authoring: Response", "a2ahub/reference/authoring/response.md"},
	{"authoring-announcement", "Authoring", "Authoring: Announcement", "a2ahub/reference/authoring/announcement.md"},
	{"troubleshooting", "Help", "Troubleshooting", "a2ahub/troubleshooting.md"},
}

// Docs renders the embedded skill tree into the ordered DocSection list. Pure:
// reads only skill.Files (no store, no network). GFM so the skill docs' tables
// / autolinks / strikethrough render (plain goldmark drops tables). Deterministic
// — same embedded input, same output, so the page is byte-stable across renders.
func Docs() ([]DocSection, error) {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	out := make([]DocSection, 0, len(docManifest))
	for _, e := range docManifest {
		src, err := fs.ReadFile(skill.Files, e.File)
		if err != nil {
			return nil, fmt.Errorf("html: docs: read %s: %w", e.File, err)
		}
		var buf bytes.Buffer
		if err := md.Convert(stripLeadingH1(src), &buf); err != nil {
			return nil, fmt.Errorf("html: docs: render %s: %w", e.File, err)
		}
		out = append(out, DocSection{ID: e.ID, Group: e.Group, Title: e.Title, HTML: buf.String()})
	}
	return out, nil
}

// stripLeadingH1 drops the file's first ATX H1 line: the section title comes
// from the manifest and shows in the nav, so the body should start at the first
// real content and the page's h2/h3-based on-page TOC stays clean. A file that
// doesn't open with `# ` (after any blank lines) is returned unchanged.
func stripLeadingH1(src []byte) []byte {
	lines := strings.Split(string(src), "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue // skip leading blank lines
		}
		if strings.HasPrefix(ln, "# ") {
			kept := make([]string, 0, len(lines)-1)
			kept = append(kept, lines[:i]...)
			kept = append(kept, lines[i+1:]...)
			return []byte(strings.Join(kept, "\n"))
		}
		break // first content line is not an H1 — leave the file as-is
	}
	return src
}
