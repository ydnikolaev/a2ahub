package artifact

import (
	"regexp"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the structural split of a raw `.md` artifact's bytes into
// its YAML frontmatter block and Markdown body (§5.1, D-009). This is a
// structural split only — it does not decode frontmatter fields into a
// typed envelope; JSON-Schema validation of envelope field content (§5.2)
// is internal/schema/internal/validate's concern (P2/P3), out of this
// phase's footprint.
type Frontmatter struct {
	// YAML holds the raw bytes between the two `---` delimiter lines,
	// verbatim — never re-encoded, so SerializeFrontmatter can
	// reconstruct the original bytes exactly.
	YAML []byte
	// Body holds the raw bytes after the closing delimiter line,
	// verbatim.
	Body []byte
}

var (
	// openDelimiter matches the leading `---` delimiter line (LF or
	// CRLF) at the very start of the input.
	openDelimiter = regexp.MustCompile(`^---\r?\n`)
	// closeDelimiter matches a `---` delimiter line (LF or CRLF)
	// starting at any line boundary.
	closeDelimiter = regexp.MustCompile(`(?m)^---\r?\n`)
)

// ParseFrontmatter splits raw into its frontmatter block and body. It
// requires the exact `---\n<yaml>\n---\n<body>` delimiter pair (CRLF
// tolerated on the delimiter lines themselves); a missing or malformed
// delimiter pair is ErrNoFrontmatter.
//
// As a guard beyond the literal delimiter split, the extracted YAML block
// must itself be syntactically valid YAML — a garbage block between valid
// delimiters is ErrMalformedFrontmatter. This does not decode the block
// into typed fields; it is a validity probe only, and the raw bytes
// (never a re-encoded form) are what YAML holds and what Serialize
// reproduces.
func ParseFrontmatter(raw []byte) (Frontmatter, error) {
	const op = "ParseFrontmatter"
	loc := openDelimiter.FindIndex(raw)
	if loc == nil {
		return Frontmatter{}, &Error{Op: op, Err: ErrNoFrontmatter}
	}
	rest := raw[loc[1]:]
	closeLoc := closeDelimiter.FindIndex(rest)
	if closeLoc == nil {
		return Frontmatter{}, &Error{Op: op, Err: ErrNoFrontmatter}
	}
	yamlBlock := rest[:closeLoc[0]]
	body := rest[closeLoc[1]:]

	var probe map[string]any
	if err := yaml.Unmarshal(yamlBlock, &probe); err != nil {
		return Frontmatter{}, &Error{Op: op, Err: ErrMalformedFrontmatter}
	}

	return Frontmatter{YAML: yamlBlock, Body: body}, nil
}

// SerializeFrontmatter produces the exact byte layout
// `---\n<yaml>\n---\n<body>` from fm's raw bytes.
func SerializeFrontmatter(fm Frontmatter) []byte {
	out := make([]byte, 0, len(fm.YAML)+len(fm.Body)+8)
	out = append(out, "---\n"...)
	out = append(out, fm.YAML...)
	out = append(out, "---\n"...)
	out = append(out, fm.Body...)
	return out
}
