package livee2e

import (
	"os"
	"testing"
)

// skipWithoutPrivateCorpus skips a test whose subject is a corpus under
// `docs/`, when that corpus is not present.
//
// `docs/` is STRIPPED from the public projection, so a release candidate
// carries none of it. Five tests in this package and one in internal/e2e read
// spec files, incident evidence and the corner-case plan to hold the shipped
// catalogue against the corpus that declares it — and in a candidate only the
// catalogue exists. A tree that does not carry the corpus cannot have drifted
// from it, so the honest verdict there is "not judged here", exactly the
// answer the Makefile already gives for five presence-gated private gates
// ("skip — absent (public checkout)").
//
// WHY THIS EXISTS AT ALL, since it looks like a weakening: these reads were
// added between 2026-08-09 and 2026-08-11 (ba3d8ff5, bc66ec27, 8272f590) and
// NO candidate was cut in that window, so no filtered tree ever executed them
// until 2026-08-12. They would have failed the v0.19.10 candidate's own
// `make check` in the Go tier, after its repo gates passed — the same class as
// the three gate-side blockers fixed the same day, and the fifth instance of
// it found that week.
//
// It is deliberately NOT a general "skip if anything is missing". It takes the
// exact path the caller is about to read, skips ONLY when that path does not
// exist, and names it in the skip message. In the private tree every one of
// these paths is present, so every one of these tests runs — which is what
// `make check` on this repo proves on each run, and what makes the skip
// observable rather than silent.
func skipWithoutPrivateCorpus(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("skip — %s absent (public checkout): this test judges a corpus that is stripped from the candidate", path)
	}
}
