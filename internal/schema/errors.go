// Package schema loads and compiles the embedded product schema corpus
// (schemas.FS: envelope/v1 base + 8 type extensions, event/v1, manifest/v1,
// consumes/v1) with santhosh-tekuri/jsonschema/v6, and loads the
// error-code registry (schemas/errors/v1/registry.yaml). It is the only
// package in this repo that imports jsonschema/v6 (go-conventions.md
// "Stack" table); internal/validate is its sole v1 consumer.
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

	// ErrUnsupportedVersion is returned when a requested envelope/event
	// schema version falls outside the one-cycle overlap window (§5.4
	// last bullet, CC-005): older than N-1, or newer than the binary
	// knows. Per CC-005 this is refuse-and-warn, never a silent
	// downgrade.
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
