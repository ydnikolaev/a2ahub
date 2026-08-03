// Package schemas embeds the product schema corpus — versioned envelope, event,
// manifest, consumes JSON schemas, the error-code registry, and the
// per-type templates — so it ships inside the `a2a` binary (§5.1, D-009:
// "schemas and validator travel as one artifact").
//
// The go:embed directive cannot traverse ".." (the embed patterns are rooted at this
// package's own directory), which is why this file lives at
// schemas/embed.go rather than inside internal/schema — a placement
// decision recorded in
// docs/features/archive/v1-min-2026-07/plans/03-validation-engine.plan.md.
//
// Every fixtures/ tree is deliberately EXCLUDED from FS: fixtures are test
// data, read straight from disk by tests (internal/schema, internal/
// validate), never compiled into the binary.
//
// internal/schema is this package's corpus consumer; P6's internal/template
// reuses the same FS later for template rendering (§5.6).
package schemas

import "embed"

//go:embed envelope/v1/*.schema.json
//go:embed envelope/v2/*.schema.json
//go:embed event/v1/*.schema.json
//go:embed event/v2/*.schema.json
//go:embed manifest/v1/*.schema.json
//go:embed consumes/v1/*.schema.json
//go:embed errors/v1/registry.yaml
//go:embed templates/v1/*.md
//go:embed templates/v2/*.md
//go:embed feedback/v1/*.schema.json
//go:embed feedback/v1/template.yaml
//go:embed feedback/v1/codes.yaml
//go:embed release-notes/v1/*.schema.json
//go:embed known-issues/v1/*.schema.json

// FS is the embedded, fixture-free slice of the schemas/ corpus: envelope/v1,
// the envelope/v2 base + contract descriptor + work announcement, event/v1 and event/v2,
// manifest and consumes schemas, the error-code registry data file, and the
// versioned envelope templates, plus (P25) the feedback family's 2 schemas + its own authoring template +
// its own feedback-local FB-### code table (schemas/feedback/v1/, I1: not
// an envelope, own code path), and (P31) the release-notes/v1 schema that
// validates the authored corpus embedded separately under releasenotes/
// (this package cannot embed it directly: go:embed cannot traverse ".."),
// plus the current known-issues schema used by the standing release-warning
// list.
var FS embed.FS
