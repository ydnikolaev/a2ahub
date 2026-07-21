// Package schemas embeds the product schema corpus — envelope, event,
// manifest, consumes JSON schemas, the error-code registry, and the
// per-type templates — so it ships inside the `a2a` binary (§5.1, D-009:
// "schemas and validator travel as one artifact").
//
// The go:embed directive cannot traverse ".." (the embed patterns are rooted at this
// package's own directory), which is why this file lives at
// schemas/embed.go rather than inside internal/schema — a placement
// decision recorded in
// docs/features/v1-min-2026-07/plans/03-validation-engine.plan.md.
//
// Every fixtures/ tree is deliberately EXCLUDED from FS: fixtures are test
// data, read straight from disk by tests (internal/schema, internal/
// validate), never compiled into the binary.
//
// internal/schema is this package's v1 consumer; P6's internal/template
// reuses the same FS later for template rendering (§5.6).
package schemas

import "embed"

//go:embed envelope/v1/*.schema.json
//go:embed event/v1/*.schema.json
//go:embed manifest/v1/*.schema.json
//go:embed consumes/v1/*.schema.json
//go:embed errors/v1/registry.yaml
//go:embed templates/v1/*.md

// FS is the embedded, fixture-free slice of the schemas/ corpus: the 11
// product JSON schemas (base + 8 envelope extensions + event + manifest +
// consumes), the error-code registry data file, and the 8 per-type
// templates.
var FS embed.FS
