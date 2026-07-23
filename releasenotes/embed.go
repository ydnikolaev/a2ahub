// Package releasenotes embeds the authored, version-keyed release-notes
// corpus (P31: one file per shipped a2a version, schema release-notes/v1,
// schemas/release-notes/v1/release-notes.schema.json) so it ships inside
// the `a2a` binary and is surfaced by `a2a whatsnew` without a network
// call — the same "schema and payload travel as one artifact" rationale
// schemas/embed.go documents for the product schema corpus.
//
// The go:embed directive cannot traverse ".." (patterns are rooted at this
// package's own directory), which is why this corpus lives at
// releasenotes/embed.go rather than being embedded by the schemas package
// itself, exactly the placement constraint schemas/embed.go's own doc
// comment records for the same reason.
//
// internal/notes is this package's consumer: it parses every embedded
// *.yaml into a notes.ReleaseNotes and range-queries the result.
package releasenotes

import "embed"

//go:embed *.yaml

// FS is the embedded release-notes corpus: one *.yaml file per shipped
// a2a version, authored by hand (not generated), never edited by this
// package.
var FS embed.FS
