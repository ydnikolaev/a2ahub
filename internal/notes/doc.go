// Package notes is a2a's own glue around the P31 release-notes corpus: an
// authored, version-keyed YAML file per shipped a2a version plus one standing
// current-known-issues document (releasenotes/*.yaml and
// releasenotes/current/known-issues.yaml), embedded by the releasenotes
// package.
//
// Parsing, range queries, the pending-obligation projection, the detect
// runner, the adoption notice and the clock-file probe are NOT here: they are
// the go-foundation adoption unit (github.com/ydnikolaev/go-foundation/
// units/adoption, D-047), which was lifted from this package and is called
// directly by every surface. What stays is what only a2a means: where the
// corpus lives in releasenotes.FS (CorpusPattern, StandingPath), a2a's
// adoption Policy and Verbs, the GitHub Release body renderer
// (RenderMarkdown), and the schema-validating adapter for P49's verified
// future-detail cache (NewDetailDecoder).
//
// Schema/policy validity of the corpus is NOT the unit's job either (D-011,
// the same split internal/space draws for space.yaml) — that is
// internal/schema's ValidateReleaseNotes and ValidateKnownIssues, consumed
// here by NewDetailDecoder and by this package's own tests as corpus-integrity
// gates: every embedded document must validate, or the gate reds before it
// ever ships. a2a's frozen release-notes/v1 schema stays the gate; the unit's
// Release.Raw carries the exact bytes to it.
//
// lane-inputs:
//
//	releasenotes/*.yaml
//	releasenotes/current/*.yaml
package notes
