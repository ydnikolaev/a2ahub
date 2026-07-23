// Package notes parses and range-queries the P31 release-notes corpus: an
// authored, version-keyed YAML file per shipped a2a version
// (releasenotes/*.yaml, schema release-notes/v1,
// schemas/release-notes/v1/release-notes.schema.json), embedded by the
// releasenotes package. It structurally parses (ParseReleaseNotes, the same
// syntax-only-decode idiom internal/space's ParseManifest uses), loads the
// whole embedded corpus in ascending version order (Load), and answers the
// two range questions `a2a whatsnew` needs: "everything newer than the
// version I last saw, up to the version I'm running" (Since) and "the
// entry for exactly this version" (Exactly).
//
// Schema/policy validity of the corpus is NOT this package's job (D-011,
// the same split internal/space draws for space.yaml) — that is
// internal/schema's ValidateReleaseNotes, consumed here only by this
// package's own test as the corpus-integrity gate: every embedded file
// must validate, or the gate reds before it ever ships.
package notes
