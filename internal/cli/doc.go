// Package cli is the OP-2xx verb surface: a thin frontend over the core
// packages, holding argument parsing, refusal construction and rendering, and
// no domain rules of its own (ADR-001).
//
// The declaration below exists because this package's tests read a NON-GO
// corpus, and until 2026-08-29 nothing said so. `a2a html --demo` renders the
// embedded release-note corpus, and cmd_html_test.go byte-compares the result
// against a committed golden — so editing `releasenotes/**` reds a test in
// THIS package while `make lane` selected only the static release gates for
// that path. A release-note-only commit was green in the derived lane and red
// in the ceiling, which is the one failure mode the lane derivation exists to
// prevent.
//
// It was found by running the golden test by hand after a notes edit the lane
// had just called clean. The test itself had already noticed: its failure
// message says "this test has now gone stale twice for the same reason".
// Twice, with nothing connecting the cause to the gate that should have
// caught it.
//
// This is the `internal/notes` ← `releasenotes/**` shape the check convention
// names for exactly this case, applied to the second package that reads the
// same corpus. It costs a scoped `internal/cli` test run on a notes edit,
// which is the honest price of the golden being real evidence.
//
// lane-inputs:
//
//	releasenotes/*.yaml
//	releasenotes/current/*.yaml
package cli
