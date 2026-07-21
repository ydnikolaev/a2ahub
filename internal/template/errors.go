// Package template embeds and renders the P2 per-type canonical templates
// (schemas/templates/v1/*.md, §5.6) for `a2a new`/`a2a template
// list|show` (P6). It imports only internal/artifact (frontmatter split,
// ID/digest reuse) and internal/schema (the canonical type list + embedded
// schema property sets for its own field-subset self-check) per ADR-001's
// grant for this package; it never imports internal/cli or internal/
// validate — rendering is a pure projection of the schema corpus, with zero
// knowledge of CLI flags or the validation engine (§9 "Coupling: soft").
//
// Render never calls time.Now() or mints an ID itself: every value that
// must be genuinely fresh per invocation (the minted ID, the resolved
// actor, "now") is supplied by the caller (cmd_new.go, P6's own file) so
// this package stays deterministic and unit-testable without a clock or
// entropy source.
package template

import "errors"

// Sentinel errors, one per operational failure class (P1 idiom, mirrored
// from internal/artifact/errors.go).
var (
	// ErrUnknownType is returned when the requested type is not one of the
	// 8 §3.1 envelope types this package has an embedded template for.
	ErrUnknownType = errors.New("template: unknown envelope type")

	// ErrMalformedTemplate is returned when an embedded template's own
	// frontmatter block fails to parse as a YAML mapping — a build-time
	// bug in the embedded corpus, never expected at runtime (guarded by
	// this package's own tests, AC row 7).
	ErrMalformedTemplate = errors.New("template: embedded template frontmatter is not a YAML mapping")
)

// Error is the small typed error every exported operation in this package
// returns on failure, mirroring internal/artifact's own idiom so
// errors.Is/As works uniformly across both packages.
type Error struct {
	// Op names the failing operation ("Render", "Show").
	Op string
	// Input is the offending input (typically the type name), kept for
	// diagnostics.
	Input string
	// Err is the wrapped sentinel.
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "template: " + e.Op + ": " + e.Err.Error()
	}
	return "template: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
