package fold

import "github.com/ydnikolaev/a2ahub/internal/artifact"

// ParseSubject parses and validates an artifact ID string (a candidate
// `subject`, `parent`, `refs` entry, or response id) by reusing
// internal/artifact's §3.3 ID parser — the one dependency this
// otherwise-stdlib-only package is allowed (ADR-001; spec 04 footprint).
// Callers translating a validated event/v1 document into fold's own
// Event/Envelope input shapes use this instead of re-implementing ID
// parsing (spec §5 anti-duplication rule).
//
// Fold's own Fold/Apply/CheckLegality never call this internally: they
// operate on the plain ID strings their input structs already carry
// regardless of whether those strings happen to parse — an
// already-committed event with a malformed subject still folds (it will
// simply never match a known response, surfacing as an
// illegal-transition flag, never a parse error). ParseSubject exists for
// callers who want to validate shape before handing fold its inputs, and
// mirrors internal/artifact's own error idiom (a malformed id wraps
// artifact.ErrMalformedID) for genuinely-invalid input, as distinct from
// fold's own non-fatal flag semantics for illegal/unauthorized
// already-committed events.
func ParseSubject(id string) (artifact.ID, error) {
	return artifact.ParseID(id)
}
