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
// Its consumers read it through the go-foundation adoption unit
// (adoption.Load with internal/notes.CorpusPattern, adoption.LoadStanding with
// internal/notes.StandingPath), which parses every embedded *.yaml into an
// adoption.Release.
package releasenotes

import "embed"

//go:embed *.yaml current/*.yaml

// FS is the embedded release-notes corpus: one root *.yaml file per shipped
// a2a version plus current/known-issues.yaml for standing limitations.
// Both are authored by hand (not generated), never edited by this package.
var FS embed.FS
