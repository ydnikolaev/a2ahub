package html

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

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

// docEntry curates one skill markdown file into a section: its stable id (also
// the deep-link anchor), its group, its nav title, and its path inside
// skill.Files. Slice order is the in-group section order.
type docEntry struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Title string `json:"title"`
	File  string `json:"file"`
}

type docsManifest struct {
	Schema   string     `json:"schema"`
	Groups   []string   `json:"groups"`
	Sections []docEntry `json:"sections"`
}

// docManifest and DocGroups are loaded from the embedded manifest that also
// drives the public Astro documentation build. The JSON file is now the curation
// SSOT; the parity test still proves every embedded Markdown document is listed.
var embeddedDocsManifest = mustLoadDocsManifest()
var docManifest = embeddedDocsManifest.Sections

// DocGroups lists the documentation navigation groups in manifest order.
var DocGroups = embeddedDocsManifest.Groups

func mustLoadDocsManifest() docsManifest {
	b, err := fs.ReadFile(skill.Files, "a2ahub/docs-manifest.json")
	if err != nil {
		panic(fmt.Sprintf("html: docs manifest: %v", err))
	}
	var manifest docsManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		panic(fmt.Sprintf("html: docs manifest decode: %v", err))
	}
	if manifest.Schema != "a2a-docs-manifest/v1" || len(manifest.Groups) == 0 || len(manifest.Sections) == 0 {
		panic("html: docs manifest: invalid or empty contract")
	}
	return manifest
}

// Docs renders the embedded skill tree into the ordered DocSection list. Pure:
// reads only skill.Files (no store, no network). GFM so the skill docs' tables
// / autolinks / strikethrough render (plain goldmark drops tables); a link
// rewriter retargets intra-skill `*.md` links to their in-tab section anchors.
// Deterministic — same embedded input, same output.
func Docs() ([]DocSection, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithASTTransformers(
			util.Prioritized(docLinkRewriter{}, 100),
		)),
	)
	out := make([]DocSection, 0, len(docManifest))
	for _, e := range docManifest {
		src, err := fs.ReadFile(skill.Files, e.File)
		if err != nil {
			return nil, fmt.Errorf("html: docs: read %s: %w", e.File, err)
		}
		var buf bytes.Buffer
		if err := md.Convert(prepareMarkdown(src), &buf); err != nil {
			return nil, fmt.Errorf("html: docs: render %s: %w", e.File, err)
		}
		out = append(out, DocSection{ID: e.ID, Group: e.Group, Title: e.Title, HTML: buf.String()})
	}
	return out, nil
}

// prepareMarkdown normalizes a skill doc for in-page display. Several docs
// (the authoring skeletons, SKILL.md) lead with a YAML frontmatter block that
// is meaningful CONTENT — the literal template a human/agent fills in — but
// goldmark would parse it as markdown (the `---` becomes an <hr>, the `#`
// comment lines become <h1> headings). We instead present the whole block as a
// fenced ```yaml code block so it renders as the literal skeleton it is. Then
// the body's own leading H1 is stripped (the nav supplies the section title,
// and the body should start at the first `##` for the on-page TOC).
func prepareMarkdown(src []byte) []byte {
	if fm, body, ok := splitFrontmatter(src); ok {
		var b strings.Builder
		b.WriteString("```yaml\n")
		b.Write(fm)
		b.WriteString("\n```\n\n")
		b.Write(stripLeadingH1(body))
		return []byte(b.String())
	}
	return stripLeadingH1(src)
}

// splitFrontmatter returns the YAML frontmatter body (without the fences) and
// the markdown after it, when src opens with a `---` … `---` block. ok is false
// when there is no frontmatter.
func splitFrontmatter(src []byte) (fm, body []byte, ok bool) {
	const fence = "---"
	s := string(src)
	if !strings.HasPrefix(s, fence+"\n") {
		return nil, nil, false
	}
	rest := s[len(fence)+1:]
	idx := strings.Index(rest, "\n"+fence)
	if idx < 0 {
		return nil, nil, false
	}
	fmStr := rest[:idx]
	after := rest[idx+1+len(fence):]        // skip "\n---"
	after = strings.TrimPrefix(after, "\n") // and the newline that ends the closing fence line
	return []byte(fmStr), []byte(after), true
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

// docLinkRewriter retargets intra-skill relative `*.md` (and `authoring/` dir)
// links to their in-tab section anchor (e.g. `loops.md` → `#work-loops`), so
// cross-references navigate within the Documentation tab instead of pointing at
// files that don't exist beside the generated page. External (scheme) and
// existing `#` links are left untouched; an unmapped relative link is left
// as-authored.
type docLinkRewriter struct{}

func (docLinkRewriter) Transform(node *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		if anchor, ok := docAnchorFor(string(link.Destination)); ok {
			link.Destination = []byte(anchor)
		}
		return ast.WalkContinue, nil
	})
}

// docAnchorFor maps a relative intra-skill link destination to its section
// anchor. Returns ok=false for scheme/`#`/unmapped links (left as-authored).
func docAnchorFor(dest string) (string, bool) {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.Contains(dest, "://") {
		return "", false
	}
	base := strings.TrimSuffix(dest, "/")
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:] // drop any ../ or dir prefix — anchors are basename-keyed
	}
	switch base {
	case "loops.md":
		return "#work-loops", true
	case "onboarding.md":
		return "#getting-started", true
	case "troubleshooting.md":
		return "#troubleshooting", true
	case "commands.md":
		return "#commands", true
	case "decompose-example.md":
		return "#decompose", true
	case "feedback.md":
		return "#feedback", true
	case "authoring": // a `reference/authoring/` directory link → the first authoring section
		return "#authoring-contract", true
	}
	// Per-type authoring file: `<type>.md` → `#authoring-<type>` when that is a
	// real section id.
	if typ, cut := strings.CutSuffix(base, ".md"); cut {
		id := "authoring-" + typ
		for _, e := range docManifest {
			if e.ID == id {
				return "#" + id, true
			}
		}
	}
	return "", false
}
