// Package privatecorpus is the ONE home for the announced-skip shape every
// Go test in this repo needs when its subject is a private-corpus path
// under `docs/` — spec files, incident evidence, ADR-001's decision table,
// the corner-case plan. `docs/` is STRIPPED from the public projection (the
// STRIP list in docs/runbooks/publish-to-public.sh), so a release candidate
// carries none of it. A test that judges a candidate against that corpus
// cannot possibly hold — the corpus it would compare against is not there —
// and the honest verdict is "not judged here", the same answer the Makefile
// already gives for its own presence-gated private gates
// ("skip — absent (public checkout)").
//
// release-loop-2026-08 P5 (spec 05-derive-not-enumerate.md §11 Amendment
// A1) moved this here from internal/livee2e/privatecorpus_test.go, where it
// had been since 2026-08-12 as skipWithoutPrivateCorpus. It could not stay
// there: a `_test.go` file is not importable across packages, so when
// internal/html and internal/localserver each grew a test reading
// ADR-001's table out of docs/decisions.md (commit 838b47fb — the SAME day
// this epic was filed over exactly this class of duplication), neither
// could reuse the existing helper. Both hand-copied the identical 15 lines
// instead — sixth and seventh instance of a class the epic's own filing
// commit was itself adding a fifth and sixth instance of. The duplication
// was FORCED by Go, not by carelessness: the fix is placement, moving the
// helper DOWN into something every surface can import, never copying it
// ACROSS (ADR-004's shape, applied to test support).
//
// internal/livee2e keeps a local, unqualified `skipWithoutPrivateCorpus`
// name that delegates straight to SkipWithoutPrivateCorpus below — its six
// call sites live in that package's own `_test.go` files, which this
// package cannot edit its way into (an unqualified name inside the same
// package is what those call sites already spell), so the delegation is the
// seam rather than a second implementation: the only `os.Stat` / `t.Skipf`
// pair in the whole repo for this concern lives here.
package privatecorpus

import (
	"fmt"
	"os"
	"testing"
)

// SkipWithoutPrivateCorpus skips the running test when path does not exist,
// with an announced message naming the exact path and why.
//
// It checks EXACTLY the path the caller passes — typically `docs/` itself,
// or a specific subdirectory the test's subsequent reads live under — and
// skips ONLY on that path's absence. This is deliberately NOT a general
// "skip if anything about the corpus looks wrong": a caller passes the ROOT
// its reads depend on, and if that root is present but a specific file
// beneath it is missing (a moved spec, a renamed doc, a deleted citation),
// this function is never consulted for that failure at all — the caller's
// own subsequent read (os.ReadFile, os.Stat on the leaf, a directory walk)
// fails through its own normal, FATAL error path. A present parent
// directory with a missing file must still fail the test; the day this
// function starts swallowing that case is the day an absent corpus stops
// being distinguishable from a broken one, and the day a real drift can
// disappear behind an announced skip instead of failing where it belongs.
//
// Every read error other than "path does not exist" is left for the caller
// to handle — a permission error or a symlink loop is not "the public
// checkout", and silently skipping on it would hide a real, local problem
// behind the same message that means "this is fine, just not here".
func SkipWithoutPrivateCorpus(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip(skipMessage(path))
	}
}

// skipMessage is the announcement's text, factored out of
// SkipWithoutPrivateCorpus so its content is unit-testable directly rather
// than through *testing.T's own unexported log buffer.
func skipMessage(path string) string {
	return fmt.Sprintf("skip — %s absent (public checkout): this test judges a corpus that is stripped from the candidate", path)
}
