// Package artifact implements the artifact model shared by every a2ahub
// surface: the two §3.3 ID classes (standing, exchange/broadcast), ULID
// lifecycle-event IDs, structural frontmatter parse/serialize, and digests.
//
// This package is stdlib + ADR-002 deps only (gopkg.in/yaml.v3,
// github.com/oklog/ulid/v2) — see docs/decisions.md ADR-001/ADR-002. It
// never validates envelope field content (types, category enums); that is
// internal/schema and internal/validate's concern (P2/P3).
package artifact

import "errors"

// Sentinel errors, one per failure class. Callers use errors.Is against
// these; a typed *Error carries the operation and offending input on top.
var (
	// ErrMalformedID is returned when an ID string does not match either
	// §3.3 grammar (standing or exchange/broadcast).
	ErrMalformedID = errors.New("artifact: malformed id")

	// ErrEmptyField is returned when a mint input (prefix, system, slug)
	// that MUST be non-empty is empty.
	ErrEmptyField = errors.New("artifact: empty field")

	// ErrIDMismatch is returned by Validate when the filename stem does
	// not match the ID exactly.
	ErrIDMismatch = errors.New("artifact: filename does not match id")

	// ErrSectionMismatch is returned by Validate when the ID's <system>
	// component does not match the artifact's owning section.
	ErrSectionMismatch = errors.New("artifact: id system does not match owning section")

	// ErrNoFrontmatter is returned when raw bytes lack the well-formed
	// `---\n...\n---\n` frontmatter delimiter pair.
	ErrNoFrontmatter = errors.New("artifact: missing or malformed frontmatter delimiters")

	// ErrMalformedFrontmatter is returned when the extracted frontmatter
	// block is not syntactically valid YAML.
	ErrMalformedFrontmatter = errors.New("artifact: frontmatter block is not valid yaml")

	// ErrMalformedULID is returned when a ULID string fails to parse.
	ErrMalformedULID = errors.New("artifact: malformed ulid")
)

// Error is the small typed error every exported operation in this package
// returns on failure. It always wraps one of the sentinels above so callers
// can use errors.Is/As; it never panics on bad input.
type Error struct {
	// Op names the failing operation (e.g. "MintStandingID", "ParseID",
	// "Validate", "ParseFrontmatter", "ParseULID").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel (see the vars above).
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "artifact: " + e.Op + ": " + e.Err.Error()
	}
	return "artifact: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
