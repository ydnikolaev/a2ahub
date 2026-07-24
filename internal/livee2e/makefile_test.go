package livee2e

import (
	"os"
	"strings"
	"testing"
)

// makefilePath is repo-relative from this package's directory.
const makefilePath = "../../Makefile"

// buildTagDirective is assembled rather than written literally so this file's
// own source can never satisfy the check that looks for it.
const buildTagDirective = "//go:" + "build livee2e"

// preamble returns everything before a Go file's package clause — the only
// place a build constraint may legally appear. Scanning whole files instead
// would let THIS file match on the directive it mentions in its own strings,
// which is how the first version of this guard passed with the tagged file
// deleted.
func preamble(src string) string {
	if strings.HasPrefix(src, "package ") {
		return ""
	}
	if idx := strings.Index(src, "\npackage "); idx >= 0 {
		return src[:idx]
	}
	return src
}

func readMakefile(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read %s: %v", makefilePath, err)
	}
	return strings.Split(string(b), "\n")
}

// targetRecipe returns the prerequisite text of `name:` and the recipe lines
// that follow it (tab-indented), which is enough to assert what a target
// depends on and what it actually runs.
func targetRecipe(lines []string, name string) (prereqs string, recipe []string, found bool) {
	for i, line := range lines {
		if !strings.HasPrefix(line, name+":") {
			continue
		}
		prereqs = strings.TrimPrefix(line, name+":")
		if idx := strings.Index(prereqs, "##"); idx >= 0 {
			prereqs = prereqs[:idx]
		}
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "\t") {
				recipe = append(recipe, next)
				continue
			}
			if strings.TrimSpace(next) == "" {
				continue
			}
			break
		}
		return prereqs, recipe, true
	}
	return "", nil, false
}

// TestLiveTierIsNotAMergeGate is AC-962.1. `make check` must neither run nor
// depend on the live tier: it needs network, two credentials and Actions
// latency, so a `check` that reached it would be flaky and expensive on every
// commit.
//
// This is asserted against the Makefile rather than trusted, because the
// failure mode is silent — a live target wired into REPO_GATES does not
// announce itself, it just makes the merge gate start failing for reasons
// that have nothing to do with the diff.
func TestLiveTierIsNotAMergeGate(t *testing.T) {
	t.Parallel()
	lines := readMakefile(t)

	// (a) The target must EXIST, or every assertion below is vacuous —
	// "live-e2e is absent from the merge gate" is trivially true when
	// live-e2e is absent from the Makefile entirely.
	_, liveRecipe, ok := targetRecipe(lines, "live-e2e")
	if !ok {
		t.Fatal("no `live-e2e` target in the Makefile — the guards below would pass vacuously")
	}

	// (b) It must run the tier behind its build tag. Without the tag a
	// plain `go test ./...` would compile (and could execute) the live
	// scenarios, which is the separation this whole test defends.
	if !strings.Contains(strings.Join(liveRecipe, "\n"), "-tags=livee2e") {
		t.Errorf("`live-e2e` does not pass -tags=livee2e; recipe:\n%s", strings.Join(liveRecipe, "\n"))
	}

	// (b2) …and something must actually be behind that tag. With no
	// `//go:build livee2e` file, -tags=livee2e is decorative: the target
	// would run only the hermetic tests in this package and exit 0 with no
	// credentials configured — precisely the silent green AC-962.2 forbids.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	tagged := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		b, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatalf("read %s: %v", entry.Name(), readErr)
		}
		// Only the preamble counts. A build constraint must precede the
		// package clause, and scanning the whole file would let THIS test
		// match its own directive literal below — a guard that is satisfied
		// by its own source is no guard at all.
		if strings.Contains(preamble(string(b)), buildTagDirective) {
			tagged = true
			break
		}
	}
	if !tagged {
		t.Errorf("no file carries the %q constraint — the build tag guards nothing and `make live-e2e` would exit 0 unconfigured", buildTagDirective)
	}

	// (c) REPO_GATES is the single list both `check` and `check-validators`
	// consume — the one place a live target would leak into both at once.
	var gates string
	for _, line := range lines {
		if strings.HasPrefix(line, "REPO_GATES") {
			gates = line
			break
		}
	}
	if gates == "" {
		t.Fatal("no REPO_GATES assignment found in the Makefile")
	}
	if strings.Contains(gates, "live-e2e") {
		t.Errorf("live-e2e is in REPO_GATES: %q", gates)
	}

	// (d) …and neither `check` nor `check-validators` may reach it by any
	// other route: not as a prerequisite, not from inside the recipe.
	for _, target := range []string{"check", "check-validators"} {
		prereqs, recipe, found := targetRecipe(lines, target)
		if !found {
			t.Fatalf("no `%s` target in the Makefile", target)
		}
		if strings.Contains(prereqs, "live-e2e") {
			t.Errorf("`%s` lists live-e2e as a prerequisite: %q", target, prereqs)
		}
		if body := strings.Join(recipe, "\n"); strings.Contains(body, "live-e2e") {
			t.Errorf("`%s` recipe references live-e2e:\n%s", target, body)
		}
	}
}
