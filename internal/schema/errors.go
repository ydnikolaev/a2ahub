// Package schema loads and compiles the embedded, version-keyed product
// schema corpus (schemas.FS: envelope bases and registered type extensions,
// event, manifest, consumes, release notes, and known issues) with
// santhosh-tekuri/jsonschema/v6, and loads the error-code registry
// (schemas/errors/v1/registry.yaml).
//
// It is the home for jsonschema/v6 (go-conventions.md "Stack" table), and
// the one place a schema OUTSIDE the embedded corpus may be compiled —
// CompileExternal (external.go), which a contract's own producer-authored
// schema goes through. internal/validate is its sole v1 consumer and does
// no I/O of its own, so the compat core is fed bytes and hands them here.
//
// This doc used to claim it was "the only package in this repo that
// imports jsonschema/v6". That has not been true since P25:
// internal/feedback/validate.go imports it directly for the feedback/v1
// family, which is its own corpus with its own FB-### code domain. The
// claim is corrected rather than the import removed — consolidating a
// second embedded corpus into this package is a real design question, not
// a docs fix, and stating a boundary the code does not hold is exactly the
// class of thing P37/AC-974.1 exists to stop.
package schema

import "errors"

// Sentinel errors, one per failure class. Callers use errors.Is against
// these; a typed *Error carries the operation and offending input on top
// (idiom copied from internal/artifact/errors.go).
var (
	// ErrCorpusLoad is returned when the embedded schema corpus fails to
	// parse or compile (a build-time defect, never expected at runtime
	// against the shipped binary).
	ErrCorpusLoad = errors.New("schema: corpus failed to load")

	// ErrUnknownType is returned when EnvelopeSchema is asked for an
	// artifact type outside the 8 §3.1 types.
	ErrUnknownType = errors.New("schema: unknown envelope type")

	// ErrUnsupportedVersion is returned when no decoder is registered for a
	// requested family/version. The N/N-1 authoring decision is made by the
	// Accepts*Version seams; Corpus retains older registered decoders for
	// historical replay instead of coupling lookup to that moving window.
	ErrUnsupportedVersion = errors.New("schema: unsupported schema version")

	// ErrRegistryLoad is returned when schemas/errors/v1/registry.yaml
	// fails to parse.
	ErrRegistryLoad = errors.New("schema: registry failed to load")
)

// Error is the small typed error every exported operation in this package
// returns on failure. It always wraps one of the sentinels above so
// callers can use errors.Is/As; it never panics on bad input.
type Error struct {
	// Op names the failing operation (e.g. "Load", "EnvelopeSchema").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel (see the vars above).
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "schema: " + e.Op + ": " + e.Err.Error()
	}
	return "schema: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
