// Package space implements the §4.2 space layout model and the D-002/
// D-026 write funnel (errors.go carries this package's own canonical
// doc comment; this file exists only for the declaration below).
//
// The declaration exists because one funnel guard's own verdict depends
// on repo files that are NOT Go source: funnel.go's parent-transition
// guard (answers-that-hold-2026-08 spec 05 §5/§8 AC-4/AC-5) reads a
// committed document's own `schema`+`type` fields, builds
// "<schema>/<type>.schema.json", and checks THAT file's own top-level
// `required` array for "parent" — never a hardcoded kind list, so a kind
// gaining `parent` in its own schema's `required` array is covered with
// no Go change. That means a schema-only edit under schemas/envelope/v1
// or v2 changes this guard's own meaning, and nothing about that fact is
// visible in a Go diff — the same hole internal/notes/doc.go closes for
// the release-notes corpus (read that file's own doc comment for the
// shape this one copies).
//
// lane-inputs:
//
//	schemas/envelope/v1/*.schema.json
//	schemas/envelope/v2/*.schema.json
package space
