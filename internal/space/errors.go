// Package space implements the §4.2 space layout model, space.yaml
// manifest load, project/machine configuration (§7.4), credential
// resolution (§7.4/§10.5), mirror clones, and the D-002/D-026 write funnel.
// internal/space orchestrates internal/host; it is the only a2ahub package
// that calls into it (ADR-001).
//
// Validate/schema checks are consumed through this package's own
// consumer-side interfaces (ManifestValidator, SubmitValidator) — never a
// direct import of internal/validate/internal/schema, which are built in
// parallel by another wave of this epic (spec 05 §11 Amendments,
// plan 05 Placement decisions). cmd/a2a (P6) wires the real engines.
package space

import "errors"

// Sentinel errors, one per failure class (P1 idiom: internal/artifact).
var (
	// ErrInvalidSystemID is returned when a system id fails
	// internal/artifact's id-grammar shape check (§4.2 layout builder
	// guard).
	ErrInvalidSystemID = errors.New("space: invalid system id")

	// ErrWrongSection is returned when a write-funnel file path is outside
	// the authoring system's own section (and not under decisions/, the
	// one funnel-level exception) — refused before any git action.
	ErrWrongSection = errors.New("space: file path outside authoring system's section")

	// ErrStaleBinaryVersion is returned by the write funnel when the
	// injected binary version is older than space.yaml's
	// min_binary_version pin (CC-085, §7.3): the funnel refuses to write,
	// stays read-only, and the caller must surface a loud warning.
	ErrStaleBinaryVersion = errors.New("space: local binary version older than space.yaml min_binary_version")

	// ErrInvalidVersion is returned when a version string (binary version
	// or min_binary_version) cannot be parsed as dotted-integer semver.
	// The CC-085 guard fails CLOSED on this (refuses the write) rather
	// than silently permitting an unverifiable version.
	ErrInvalidVersion = errors.New("space: invalid version string")

	// ErrNonGitTarget is returned by the mirror-clone step when the
	// target directory exists, is non-empty, and is not a git repository.
	ErrNonGitTarget = errors.New("space: mirror target exists and is not a git repository")

	// ErrInvalidCredentialReference is returned when a machine-config
	// credential value does not match the env:<VAR> or cmd:<argv...>
	// shape (§7.4 Placement decision) — a config-load-time guard that
	// structurally keeps literal secrets out of the file.
	ErrInvalidCredentialReference = errors.New("space: invalid credential reference")

	// ErrCredentialUnresolved is returned when neither the explicit
	// A2A_* env var nor the configured reference resolves to a secret
	// (Open Q1 RESOLVED precedence) — never falls back to a literal.
	ErrCredentialUnresolved = errors.New("space: credential unresolved")

	// ErrManifestInvalid is returned when space.yaml fails structural
	// YAML parse.
	ErrManifestInvalid = errors.New("space: manifest is not valid yaml")
)

// Error is the small typed error every exported operation in this package
// returns on failure. It always wraps one of the sentinels above so
// callers can use errors.Is/As; it never panics on bad input.
type Error struct {
	// Op names the failing operation (e.g. "NewLayout", "Submit").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel (see the vars above).
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "space: " + e.Op + ": " + e.Err.Error()
	}
	return "space: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
