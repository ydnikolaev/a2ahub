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
// ADR-001 import row, unchanged in substance by ADR-023 (docs/decisions.md):
// this package's import set is wider than any other presentation package's,
// and that width is accepted, not incidental. contract, datapackage, and
// fold supply canonical types and pure helpers; notes, releasenotes, and
// skill supply embedded authored read content; workreport supplies pure
// read-model/demo types; space supplies read-only mirror/history helpers
// plus the injected historical validator. This package still receives or
// delegates every domain decision and gains no network or write authority.
// space-notify-2026-08 P2 pulled two pure fact sources out of this package
// into agentprompt and viewvocab below it — the agent-prompt protocol
// tables and the RU/EN dashboard vocabulary — so a caller outside the
// presentation layer (a chat notifier, the CI plane) can read the same
// facts without importing this package; that shrank the argument for the
// wide import set, rather than growing it, because two more of this
// package's former responsibilities now sit below it as pure data.
//
// lane-inputs:
//
//	skill/a2ahub/**
package html
