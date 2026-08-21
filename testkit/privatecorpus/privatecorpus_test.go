package privatecorpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkipWithoutPrivateCorpusSkipsWhenAbsent is the public-checkout arm:
// the exact path passed does not exist, so the test must skip, announcing
// the path it looked for.
func TestSkipWithoutPrivateCorpusSkipsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "docs")

	var sub *testing.T
	reachedAfter := false
	t.Run("subject", func(st *testing.T) {
		sub = st
		SkipWithoutPrivateCorpus(st, missing)
		reachedAfter = true
	})

	if !sub.Skipped() {
		t.Fatalf("SkipWithoutPrivateCorpus did not skip for an absent path %s", missing)
	}
	if reachedAfter {
		t.Fatalf("code after SkipWithoutPrivateCorpus ran — Skip should have stopped the goroutine")
	}
}

// TestSkipMessageNamesThePath: the skip message is the only thing an
// operator sees in a CI log; it must name the exact path, not a generic
// "corpus missing". skipMessage is the pure formatter SkipWithoutPrivateCorpus
// calls, tested directly rather than by scraping *testing.T's own log.
func TestSkipMessageNamesThePath(t *testing.T) {
	got := skipMessage("docs/features/some-spec.md")
	if !strings.Contains(got, "docs/features/some-spec.md") {
		t.Fatalf("skipMessage(...) = %q, does not name the absent path", got)
	}
	if !strings.Contains(got, "public checkout") {
		t.Fatalf("skipMessage(...) = %q, does not say WHY (public checkout)", got)
	}
}

// TestSkipWithoutPrivateCorpusDoesNotSkipWhenPresent is the private-tree
// arm: the exact path passed exists, so the test must NOT skip and must run
// to completion. This is the half that keeps the helper from becoming a way
// for a test to vanish where it is supposed to run.
func TestSkipWithoutPrivateCorpusDoesNotSkipWhenPresent(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "docs")
	if err := os.Mkdir(present, 0o755); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	var sub *testing.T
	reachedAfter := false
	t.Run("subject", func(st *testing.T) {
		sub = st
		SkipWithoutPrivateCorpus(st, present)
		reachedAfter = true
	})

	if sub.Skipped() {
		t.Fatalf("SkipWithoutPrivateCorpus skipped for a PRESENT path %s — a present root must never be treated as the public-checkout case", present)
	}
	if !reachedAfter {
		t.Fatalf("code after SkipWithoutPrivateCorpus did not run for a present path — the test would vanish instead of judging the corpus")
	}
}

// TestSkipWithoutPrivateCorpusOnlyExaminesTheGivenPath is the asymmetry
// documented on SkipWithoutPrivateCorpus and the property the epic flags as
// the likeliest thing to get wrong, stated as what it actually is: this
// function's verdict depends ONLY on os.Stat(path) for the exact path
// passed — it never walks beneath it, so a caller that passes a PRESENT
// parent directory gets "do not skip" regardless of what is or is not
// inside that directory. A present parent with a missing LEAF file is
// therefore never this function's call to make; the leaf read is the
// caller's own subsequent code (os.ReadFile, in every real call site) and
// fails through Go's normal error path, which the "does not skip when
// present" case above already proves this function does not intercept.
// internal/html and internal/localserver's TestImportBoundaryMatchesADR001
// exercise the full chain (root present, leaf missing -> t.Fatalf, not
// t.Skip) against the real corpus; reproducing a *testing.T failure cleanly
// inside this package's own suite would need subprocess self-exec
// machinery this repo does not otherwise use for a property already
// covered by construction.
func TestSkipWithoutPrivateCorpusOnlyExaminesTheGivenPath(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "docs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	// A file beneath root that SkipWithoutPrivateCorpus is never told about.
	// If this function walked the directory, an empty one would look
	// suspicious; it must still not skip, because it only ever looks at the
	// exact path argument.
	if empty, err := os.ReadDir(root); err != nil || len(empty) != 0 {
		t.Fatalf("fixture setup: root is not an empty directory: %v %v", empty, err)
	}

	var sub *testing.T
	t.Run("subject", func(st *testing.T) {
		sub = st
		SkipWithoutPrivateCorpus(st, root)
	})

	if sub.Skipped() {
		t.Fatalf("SkipWithoutPrivateCorpus skipped for a present-but-empty directory — it must judge only os.Stat(path), never the directory's contents")
	}
}
