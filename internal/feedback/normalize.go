package feedback

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// normalizeRecord returns src with every YAML comment excised and EVERY OTHER
// BYTE UNCHANGED (judge-the-thing-2026-08 P10 §T1.2).
//
// A draft is a form and may carry instructions; a submitted record is
// evidence and may not. `a2a feedback new` materializes a commented skeleton
// on purpose — the header states why `checks.*` is all-`false`, and the
// inline enums are the only place a closed vocabulary appears at the moment
// an author needs it — and `Drafter.Draft`'s yaml.Node walk preserves those
// comments across its round trip. Nothing stripped them afterwards, so
// `id: fb-20260818-76f29d # auto-filled: fb-YYYYMMDD-<6 hex>` landed in the
// hub of record and four separate readers of that corpus, comparing lines
// where they meant values, disagreed with the id the line carried.
//
// NON-REFLOW IS THE LOAD-BEARING PROPERTY AND IT DECIDES THE MECHANISM.
// The obvious implementation — clear the comment fields on the node tree and
// re-encode — is rejected on measurement: yaml.v3 re-emission is what gave
// the six scaffolded records their 4-space `context:` indentation against the
// template's 2, and it re-wraps folded scalars. The shell readers compare key
// BLOCKS byte-wise, so a full re-encode would make a normalized record differ
// from its pre-normalization twin in EVERY key — during a rollover window
// docs/runbooks/feedback-hub.md holds open until at least 2026-09-12, and
// against a publisher that ABORTs a release on exactly that. The strip and
// the reader repair must COMPOSE, not interact.
//
// So the parser is used to say WHERE a comment may be, and the excision is
// made in the ORIGINAL text:
//
//   - a multi-line scalar's interior is never scanned. yaml.v3 gives every
//     node a Line/Column (measured: a block scalar's Line is its INDICATOR
//     line, a quoted scalar's Line/Column is its opening quote), and that is
//     the authoritative `#`-is-data answer for a folded `summary` whose body
//     contains a markdown heading.
//   - on every other line, a `#` opens a comment only outside a quoted scalar
//     and only at the start of the line or after a space/tab. That rule is
//     not re-derived here: it is `scripts/check-feedback-corpus.sh`'s
//     `_record_field`, repaired 2026-08-20 in 6520097d, and
//     `.github/workflows/feedback-intake.yml`'s one-line `sed` spelling of
//     the same thing.
//   - a comment-only line is dropped ENTIRELY, line and all. Blanking it
//     would leave the skeleton's twelve-line header as twelve blank lines,
//     and the mid-document `# evidence is REQUIRED...` block as a blank line
//     inside the PRECEDING key's section — which is the very difference the
//     line-comparing readers refuse a record over.
//
// Two fail-closed guards then judge the result, in the discipline
// applyStatusResolution already applies to its own round trip (triage.go):
// the output must parse to a value deeply equal to the input's, and the
// output must carry NO comment according to the parser. On either failure it
// returns no bytes, and Submit writes nothing.
func normalizeRecord(src []byte) ([]byte, error) {
	const op = "normalize"
	if len(src) == 0 {
		return src, nil
	}

	var before any
	if err := yaml.Unmarshal(src, &before); err != nil {
		return nil, fmt.Errorf("feedback: %s: %w", op, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("feedback: %s: %w", op, err)
	}

	lines, trailingNewline := splitRecordLines(src)
	protected := make(map[int]bool)
	scanFrom := make(map[int]int)
	protectScalarInteriors(&doc, lines, protected, scanFrom)

	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		ln := i + 1
		if protected[ln] {
			kept = append(kept, line)
			continue
		}
		idx := commentIndex(line, scanFrom[ln])
		if idx < 0 {
			kept = append(kept, line)
			continue
		}
		if strings.TrimSpace(line[:idx]) == "" {
			continue // the line is a comment and nothing else
		}
		kept = append(kept, strings.TrimRight(line[:idx], " \t"))
	}

	out := []byte(strings.Join(kept, "\n"))
	if trailingNewline {
		out = append(out, '\n')
	}

	var after any
	if err := yaml.Unmarshal(out, &after); err != nil {
		return nil, fmt.Errorf("feedback: %s: the comment-free document no longer parses: %w", op, err)
	}
	if !reflect.DeepEqual(before, after) {
		return nil, fmt.Errorf("feedback: %s: refused — stripping the comments would have changed a VALUE in this record", op)
	}
	var check yaml.Node
	if err := yaml.Unmarshal(out, &check); err != nil {
		return nil, fmt.Errorf("feedback: %s: the comment-free document no longer parses: %w", op, err)
	}
	if c := firstComment(&check); c != "" {
		return nil, fmt.Errorf("feedback: %s: refused — a comment survived normalization: %q", op, c)
	}
	return out, nil
}

// splitRecordLines splits src into lines WITHOUT their terminators, and
// reports whether src ended with one, so the join can restore the file's own
// shape byte for byte.
func splitRecordLines(src []byte) ([]string, bool) {
	s := string(src)
	trailing := strings.HasSuffix(s, "\n")
	if trailing {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n"), trailing
}

// protectScalarInteriors marks every line that lies INSIDE a multi-line
// scalar — where a `#` is the reporter's own text and no scan may run — and,
// for the line on which a multi-line quoted scalar closes, the column after
// which a scan may resume.
func protectScalarInteriors(n *yaml.Node, lines []string, protected map[int]bool, scanFrom map[int]int) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode {
		switch n.Style {
		case yaml.LiteralStyle, yaml.FoldedStyle:
			// The node's Line is the indicator line (`summary: >-`), which may
			// itself carry a comment. Its content is every following line that
			// is blank or indented deeper than the indicator line — YAML's own
			// block-scalar termination rule, read off the text.
			base := indentOf(lines[clampLine(n.Line, lines)-1])
			for i := n.Line; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) != "" && indentOf(lines[i]) <= base {
					break
				}
				protected[i+1] = true
			}
		case yaml.DoubleQuotedStyle, yaml.SingleQuotedStyle:
			endLine, endCol := closingQuote(lines, n.Line, n.Column)
			if endLine > n.Line {
				for i := n.Line + 1; i < endLine; i++ {
					protected[i] = true
				}
				scanFrom[endLine] = endCol
			}
		}
	}
	for _, c := range n.Content {
		protectScalarInteriors(c, lines, protected, scanFrom)
	}
}

// closingQuote finds where a quoted scalar opening at (line, col) — both
// 1-based, col pointing at the quote character — closes. It returns the
// 1-based line and the 0-based byte index just past the closing quote. When
// the scalar is unterminated (which the parser would already have refused) it
// reports the opening position, leaving the line to the ordinary scan.
func closingQuote(lines []string, line, col int) (int, int) {
	if line < 1 || line > len(lines) {
		return line, 0
	}
	text := lines[line-1]
	if col < 1 || col > len(text) {
		return line, 0
	}
	quote := text[col-1]
	i := col // 0-based index of the character after the opening quote
	for l := line; l <= len(lines); l++ {
		text = lines[l-1]
		for ; i < len(text); i++ {
			switch text[i] {
			case '\\':
				if quote == '"' {
					i++
				}
			case quote:
				if quote == '\'' && i+1 < len(text) && text[i+1] == '\'' {
					i++
					continue
				}
				return l, i + 1
			}
		}
		i = 0
	}
	return line, 0
}

// commentIndex reports the byte index at which a YAML comment begins on line,
// scanning from `from`, or -1. A `#` opens a comment only OUTSIDE a quoted
// scalar and only at the start of the line or after a space/tab, so `bug#42`
// and `"a bug in the # channel"` stay data. A quote opens a scalar only where
// a scalar may START, otherwise the apostrophe in a plain `don't # note`
// would be read as an opening quote and the comment kept.
func commentIndex(line string, from int) int {
	if from < 0 || from > len(line) {
		return -1
	}
	var quote byte
	for i := from; i < len(line); i++ {
		c := line[i]
		atStart := i == 0 || line[i-1] == ' ' || line[i-1] == '\t'
		if quote == 0 {
			switch {
			case (c == '"' || c == '\'') && atStart:
				quote = c
			case c == '#' && atStart:
				return i
			}
			continue
		}
		switch c {
		case '\\':
			if quote == '"' {
				i++
			}
		case quote:
			if quote == '\'' && i+1 < len(line) && line[i+1] == '\'' {
				i++
				continue
			}
			quote = 0
		}
	}
	return -1
}

// firstComment returns the first head, line or foot comment anywhere in the
// tree, or "". This is the comment-free property asked of the PARSER rather
// than of the scan that produced the document — the half a regex cannot
// honestly claim.
func firstComment(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	for _, c := range []string{n.HeadComment, n.LineComment, n.FootComment} {
		if c != "" {
			return c
		}
	}
	for _, child := range n.Content {
		if c := firstComment(child); c != "" {
			return c
		}
	}
	return ""
}

func indentOf(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return i
		}
	}
	return len(line)
}

// clampLine keeps a node's reported line inside the text this package
// actually split, so a malformed position can never index out of range.
func clampLine(line int, lines []string) int {
	if line < 1 {
		return 1
	}
	if line > len(lines) {
		return len(lines)
	}
	return line
}
