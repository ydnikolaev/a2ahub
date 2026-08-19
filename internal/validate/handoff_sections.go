// P9 "a required section that says nothing" (spec
// docs/features/active/defects-fix-2026-08/specs/09-a-required-section-
// that-says-nothing.md, plan .../plans/09-a-required-section-that-says-
// nothing.plan.md): docs/the-plan/plan/16-handoff-directive.md §16.2 lists
// five handoff BODY sections as REQUIRED CONTENT with stated purposes
// ("written for a reader with NO access to the producer's private repo,
// chats, or history"), not a template's suggestion. The measurement
// (docs/inbox/defects/06-the-handoff-body-is-an-empty-skeleton.md): all
// four real getvisa handoffs carry all five headings and zero
// non-whitespace body content beneath any of them, and all four were
// verify-pass'ed.
//
// ADR-011 D2/D3 (docs/decisions.md) is why this refuses rather than warns,
// and why it must be silent at v3-full-repo:
//
//   - "Is this section adequately answered" is a judgement (D2: warns).
//   - "This required section carries zero non-whitespace characters" is
//     arithmetic over the body — a checkable fact (D2: refuses). The
//     distinction is the whole rule: this phase measures EMPTINESS, never
//     quality, and must never grow a length floor — a floor turns an
//     empty handoff into a padded one, which reads as answered. There is
//     deliberately no minimum byte/word count anywhere below.
//   - D3: the four getvisa handoffs are merged, and no verb rewrites a
//     committed artifact's body — a permanently red audit is a broken
//     instrument. This code is therefore scoped v3-pr only (the write
//     gate, where refusing still prevents the merge); the v3-full-repo
//     post-merge audit must return no verdict for it at all. The mode
//     split itself lives at internal/cli/cmd_validate_ci.go's call site
//     (mirroring REF-017's own Result.SuppressingCode("REF-017") call,
//     guarded by mode == "v3-full-repo") — this file stays mode-agnostic
//     by design, exactly as possession.go's own header records for
//     REF-017.
//
// The required section list is DERIVED from schemas/templates/v1/
// handoff.md's own body headings, never a hardcoded list in Go: a sixth
// section added to the template is required the day it lands, with no Go
// edit — see requiredHandoffSections/requiredSectionsFromTemplate.
//
// Only `handoff` is judged; every other type produces zero violations.
//
// checkPossession (possession.go) and checkDeclarationBlock
// (declaration_block.go) are the only two existing checks that read
// fm.Body, and both are NEGATIVE checks (refusing a specific wrong thing).
// This is the first POSITIVE one (requiring a right thing present) — it
// does not add a third parsing pass over the artifact's own body at the
// engine's hook site: like checkDeclarationBlock, it takes exactly the
// body bytes the caller (runCommonEnvelope) already holds, plus the
// already-decoded type.
//
// # Where the registry row and the two lead-reserved call sites live
//
// schemas/errors/v1/registry.yaml, schemas/errors/v1/history.tsv and
// internal/validate/engine.go are lead-reserved for this wave's
// allowlist. The registry row text and the exact hook lines for
// engine.go's runCommonEnvelope and internal/cli/cmd_validate_ci.go's
// mode-scope call are handed to the lead verbatim (see this brief's
// lead_actions_needed) rather than drafted here, following
// declaration_block.go's own precedent for POL-018.
package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/schemas"
)

// handoffTemplatePath is the SSOT §16.2's five sections are rendered from —
// never a hardcoded section list. schemas.FS is the same embedded corpus
// internal/template.Render already reads templates through (schemas/
// embed.go's own doc comment: "internal/template reuses the same FS later
// for template rendering").
const handoffTemplatePath = "templates/v1/handoff.md"

// markdownHeading is one ATX ("## Text") heading's level (count of `#`)
// and trimmed text, deliberately carrying no content — requiredHandoffSections
// only needs the template's heading SHAPE, never its (always-empty) body.
type markdownHeading struct {
	Level int
	Text  string
}

// markdownSection is one heading plus everything between it and the next
// heading whose level is <= its own (or the end of the document) — the
// section's own content, exactly as authored, fences and all.
type markdownSection struct {
	markdownHeading
	Content string
}

// headingLinePattern matches an ATX heading line (CommonMark: up to 3
// leading spaces, 1-6 `#`, then either a space-delimited text run or
// nothing but trailing whitespace). A bare `#`-prefixed token with no
// following space or end-of-line (`#!/bin/bash`, a hashtag mid-prose) does
// NOT match — CommonMark's own rule, which this pattern mirrors rather
// than approximates, so an unfenced shell shebang or a literal "#3" in
// prose is never mistaken for a heading.
var headingLinePattern = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$`)

// trailingHashRunPattern strips an ATX heading's optional closing sequence
// of `#`s (CommonMark: "must be preceded by a space"), e.g. "## Context ##"
// -> "Context".
var trailingHashRunPattern = regexp.MustCompile(`[ \t]+#+[ \t]*$`)

// fenceMarkerPattern matches a fenced-code-block delimiter line (three or
// more backticks or tildes, up to 3 leading spaces) — matched WITHOUT
// regard to any language tag or the opening fence's exact length, which is
// deliberately looser than CommonMark's matching-length-and-character rule:
// this package only needs "are we inside a fence", never to render the
// block, and a looser toggle can only ever make MORE of the body count as
// fence-interior (never less), which is the safe direction for a check
// whose entire job is refusing to misread fenced content as a heading.
var fenceMarkerPattern = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// parseHeadingLine reports whether line is an ATX heading line, returning
// its level and trimmed text (with any closing `#` run removed) when it is.
func parseHeadingLine(line string) (level int, text string, ok bool) {
	m := headingLinePattern.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	text = trailingHashRunPattern.ReplaceAllString(m[2], "")
	return len(m[1]), strings.TrimSpace(text), true
}

// parseMarkdownSections walks body once, fence-aware (a line inside a
// ```/~~~ fence — e.g. a shell comment beginning `#` in a "How to verify"
// block — is never read as a heading, and never mints a phantom section
// boundary), and returns every ATX heading found outside a fence together
// with the byte span up to the next heading of level <= its own (or the
// document's end). A heading's Content includes everything in that span
// verbatim: a sub-heading nested one level deeper (`### Subcomponent`
// inside a `## What was built` section) does NOT end the parent section —
// only a same-or-shallower heading does — so a well-authored section using
// sub-headings is never misread as empty, and content that is only a
// fenced code block, only a list, or only a link is still non-whitespace
// content by construction (this function does no interpretation of what
// KIND of content a span holds, only whether the span is there).
func parseMarkdownSections(body []byte) []markdownSection {
	lines := strings.Split(string(body), "\n")
	var headings []markdownHeading
	var starts []int
	inFence := false
	for i, line := range lines {
		if fenceMarkerPattern.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if level, text, ok := parseHeadingLine(line); ok {
			headings = append(headings, markdownHeading{Level: level, Text: text})
			starts = append(starts, i)
		}
	}
	sections := make([]markdownSection, len(headings))
	for idx, h := range headings {
		startLine := starts[idx] + 1
		endLine := len(lines)
		for j := idx + 1; j < len(headings); j++ {
			if headings[j].Level <= h.Level {
				endLine = starts[j]
				break
			}
		}
		content := ""
		if startLine < endLine {
			content = strings.Join(lines[startLine:endLine], "\n")
		}
		sections[idx] = markdownSection{markdownHeading: h, Content: content}
	}
	return sections
}

// requiredHandoffSections derives §16.2's required section list from the
// PRODUCTION embedded template (schemas.FS, handoffTemplatePath) — the
// live path checkHandoffSections calls. requiredSectionsFromTemplate does
// the actual derivation over caller-supplied bytes, so a test can prove
// the derivation itself (a mutated, six-section template) without needing
// to touch schemas/templates/v1/handoff.md, which is off this package's
// allowlist.
func requiredHandoffSections() ([]markdownHeading, error) {
	raw, err := schemas.FS.ReadFile(handoffTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("validate: requiredHandoffSections: reading %s: %w", handoffTemplatePath, err)
	}
	return requiredSectionsFromTemplate(raw)
}

// requiredSectionsFromTemplate parses raw (a complete handoff-template
// document: frontmatter + body) and returns its body's own ATX headings,
// in file order, as the required section list — this is the ONE seat that
// answers "what does §16.2 require", so a sixth `## Foo` heading added to
// the template is required the day it ships, with no second list to keep
// in sync and no Go edit.
func requiredSectionsFromTemplate(raw []byte) ([]markdownHeading, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("validate: requiredSectionsFromTemplate: %w", err)
	}
	sections := parseMarkdownSections(fm.Body)
	out := make([]markdownHeading, len(sections))
	for i, s := range sections {
		out[i] = s.markdownHeading
	}
	return out, nil
}

// sectionContentFor returns want's own content among sections (matched on
// BOTH level and exact heading text — never a fuzzy or case-insensitive
// match: the template's own text is the contract) and whether a heading of
// that shape was found at all — a missing heading and a present-but-empty
// one are two different facts checkHandoffSections must be able to tell
// apart in its own reasoning, even though both are named the same way in
// the violation it emits (T2: "a missing heading is named the same way an
// empty one is").
func sectionContentFor(sections []markdownSection, want markdownHeading) (content string, found bool) {
	for _, s := range sections {
		if s.Level == want.Level && s.Text == want.Text {
			return s.Content, true
		}
	}
	return "", false
}

// checkHandoffSections is POL-022: a `handoff` body carrying one of
// §16.2's required section headings (derived from schemas/templates/v1/
// handoff.md) with zero non-whitespace content beneath it, or omitting the
// heading entirely, is refused — once per artifact, naming every empty or
// missing section in a single violation (Subjects carries the machine-
// readable list; Message is the bounded human sentence, the same shape
// POL-006's construction site uses). typ is the already-decoded envelope
// type (env.Type, exactly as checkDeclarationBlock's own typ parameter);
// every type other than "handoff" produces zero violations without even
// reading the template.
func checkHandoffSections(body []byte, typ string) ([]Violation, error) {
	if typ != "handoff" {
		return nil, nil
	}
	required, err := requiredHandoffSections()
	if err != nil {
		return nil, fmt.Errorf("validate: checkHandoffSections: %w", err)
	}
	present := parseMarkdownSections(body)

	var empty []string
	for _, want := range required {
		content, found := sectionContentFor(present, want)
		if !found || strings.TrimSpace(content) == "" {
			empty = append(empty, want.Text)
		}
	}
	if len(empty) == 0 {
		return nil, nil
	}

	message := fmt.Sprintf(
		"handoff body carries %d of %d §16.2 required section(s) with no content — each is required content the receiver reads with NO access to the producer's private repo, chats, or history (docs/the-plan/plan/16-handoff-directive.md §16.2), not a template placeholder: %s",
		len(empty), len(required), strings.Join(empty, ", "))
	return []Violation{{
		Code:     "POL-022",
		Class:    ClassPolicy,
		Message:  message,
		Severity: SeverityReject,
		Subjects: append([]string(nil), empty...),
	}}, nil
}
