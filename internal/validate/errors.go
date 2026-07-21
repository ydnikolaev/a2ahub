// Package validate is THE one validation engine (D-011): schema,
// referential, lifecycle, and policy classes, exposed through two
// invocation points — ValidateDraft (V1, authoring) and ValidateForSubmit
// (V2, pre-write). Both return the same §7 result shape so P6's `a2a new`/
// `a2a validate`/`a2a submit` (and later V3/V4 mounts of this same
// library, D-011) get identical results for identical content
// (AC-201.2).
//
// This package is pure core (go-conventions.md "Architecture": no I/O,
// never logs — every exported entry point returns a value). It imports
// internal/artifact (ID parse, frontmatter split, digest) and
// internal/schema (compiled corpus + registry); it does NOT import
// internal/fold — the lifecycle class calls out through the
// LegalityChecker interface (seam.go), a consumer-side ISP interface P4's
// concrete implementation is wired into at cmd/a2a (P6), per this epic's
// plan Amendment (2026-07-21).
package validate

import "errors"

// Sentinel errors, one per operational failure class (never used for a
// schema/referential/authz/lifecycle/policy VIOLATION — those are always
// reported as Violation values in a Result, never as a Go error; see
// engine.go's doc comment). Callers use errors.Is against these; a typed
// *Error carries the operation and offending input on top (idiom copied
// from internal/artifact/errors.go and internal/schema/errors.go).
var (
	// ErrNoFrontmatter / ErrMalformedFrontmatter mirror
	// internal/artifact's own sentinels — validate wraps them rather than
	// re-declaring new ones so errors.Is against the artifact package's
	// sentinels still works through this package's typed wrapper.

	// ErrUnknownEnvelopeType is returned when a parsed envelope's `type`
	// field is not one of the 8 §3.1 types (schema class would also
	// reject this via its enum, but referential/authz/lifecycle classes
	// need to dispatch on `type` before schema class runs, so this is
	// caught early too).
	ErrUnknownEnvelopeType = errors.New("validate: unknown envelope type")

	// ErrOversizedArtifact is CC-006: the artifact exceeds the bounded
	// read limit (MaxArtifactBytes).
	ErrOversizedArtifact = errors.New("validate: artifact exceeds size limit")

	// ErrNotUTF8 is CC-007: the artifact's raw bytes are not valid UTF-8.
	ErrNotUTF8 = errors.New("validate: artifact is not valid utf-8")
)

// Error is the small typed error every exported operation in this package
// returns on an OPERATIONAL failure (never a content violation — see
// Result/Violation in result.go for those).
type Error struct {
	// Op names the failing operation (e.g. "ValidateDraft",
	// "ValidateForSubmit").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel.
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "validate: " + e.Op + ": " + e.Err.Error()
	}
	return "validate: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
