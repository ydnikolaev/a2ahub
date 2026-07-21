package space

import (
	"path"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// Layout resolves the §4.2 normative tree's path constructors for one
// system's section within a space repo. Paths are space-relative (no
// leading slash, forward slashes always — safe to join onto any repo
// root with filepath.Join at the call site).
type Layout struct {
	// System is this layout's owning section (§4.2: "the subtree of a
	// space owned and writable by exactly one system").
	System string
}

// NewLayout constructs a Layout for system, rejecting an invalid system id
// via internal/artifact's own id-grammar shape check (reused, not
// re-implemented, per the anti-duplication rule) — a real §3.3 artifact
// id is round-tripped through ParseID with system as its <system> token;
// a hyphenated or otherwise malformed system id fails that shape check.
func NewLayout(system string) (Layout, error) {
	const op = "NewLayout"
	if !validSystemID(system) {
		return Layout{}, &Error{Op: op, Input: system, Err: ErrInvalidSystemID}
	}
	return Layout{System: system}, nil
}

// validSystemID reuses internal/artifact.ParseID's <system> token shape
// check by round-tripping system through a synthetic probe id, instead of
// duplicating the regex here (rails: one validation surface, D-011's
// spirit extended to id-shape reuse).
func validSystemID(system string) bool {
	if system == "" {
		return false
	}
	probe := "X-" + system + "-y"
	parsed, err := artifact.ParseID(probe)
	return err == nil && parsed.System == system
}

// ProvidesContract returns <system>/provides/<slug>/contract.md (the XC
// contract descriptor).
func (l Layout) ProvidesContract(slug string) string {
	return path.Join(l.System, "provides", slug, "contract.md")
}

// ProvidesSchemaDir returns <system>/provides/<slug>/schema/.
func (l Layout) ProvidesSchemaDir(slug string) string {
	return path.Join(l.System, "provides", slug, "schema")
}

// ProvidesFixturesValidDir returns <system>/provides/<slug>/fixtures/valid/.
func (l Layout) ProvidesFixturesValidDir(slug string) string {
	return path.Join(l.System, "provides", slug, "fixtures", "valid")
}

// ProvidesFixturesInvalidDir returns
// <system>/provides/<slug>/fixtures/invalid/.
func (l Layout) ProvidesFixturesInvalidDir(slug string) string {
	return path.Join(l.System, "provides", slug, "fixtures", "invalid")
}

// Requires returns <system>/requires/<id>.md (an XR requirement).
func (l Layout) Requires(id string) string {
	return path.Join(l.System, "requires", id+".md")
}

// ConsumesYAML returns <system>/consumes.yaml (the registered-consumer
// registry, D-022).
func (l Layout) ConsumesYAML() string {
	return path.Join(l.System, "consumes.yaml")
}

// Exchange returns <system>/exchanges/<id>.md — the shared location for
// every exchange/broadcast/response type authored by this system (XQ, XW,
// XH, XA, XS; §4.2 groups them under one directory).
func (l Layout) Exchange(id string) string {
	return path.Join(l.System, "exchanges", id+".md")
}

// EventFile returns <system>/events/<year>/<ulid>.yaml (a lifecycle
// event, sharded by year to keep directories bounded).
func (l Layout) EventFile(year, ulid string) string {
	return path.Join(l.System, "events", year, ulid+".yaml")
}

// DocsDir returns <system>/docs/ (free-form, non-normative).
func (l Layout) DocsDir() string {
	return path.Join(l.System, "docs")
}

// Decision returns decisions/<id>.md — a space-LEVEL location (not under
// any one system's section; XD decisions are multi-party, §4.2).
func Decision(id string) string {
	return path.Join("decisions", id+".md")
}

// VendoredDir returns vendored/<vendor>/ — a space-level, read-only
// mirror location (§4.4).
func VendoredDir(vendor string) string {
	return path.Join("vendored", vendor)
}
