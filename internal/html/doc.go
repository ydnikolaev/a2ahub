// Package html renders the self-contained local dashboard and the
// Documentation tab, both assembled from the embedded skill tree.
//
// The declaration below exists because this package's verdict depends on repo
// files that are NOT Go source. `Docs()` reads `skill/a2ahub/docs-manifest.json`
// and every page it names, `TestDocsManifestParity` fails when a skill page
// exists with no manifest section, and two policy tests assert that specific
// agent-operating rules are still written somewhere in the shipped tree. All of
// that judges `skill/a2ahub/**`, and none of it is selected by a diff that
// touches only prose — `make check-validators` runs no Go tests at all.
//
// It was missing until 2026-08-12, and the cost was immediate rather than
// theoretical: P13 split `skill/a2ahub/loops.md` into eight pages, both policy
// tests read that file by fixed path, and the lane derived for a docs-only
// commit selected no Go phase that could see it. The same hole was closed in
// internal/e2e/doc.go the same day, for the same reason, one package over —
// which is the argument for declaring the read rather than remembering it.
//
// lane-inputs:
//
//	skill/a2ahub/**
package html
