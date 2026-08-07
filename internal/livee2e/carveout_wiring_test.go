package livee2e

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// guardCall is one AssertionJudgeableBy(Catalogue(), …) call site found in a
// scenario body, with the literal assertion name it guards.
type guardCall struct {
	file      string
	assertion string
}

// assertionGuardRe matches the shipped guard shape. The row name and branch
// are deliberately NOT captured: at two of the three call sites the row name
// is the body's own `scenario` const rather than a literal, so a regex that
// demanded a literal there would silently match nothing and the gate would go
// green by finding no work — the exact failure mode it exists to prevent.
// The ASSERTION is always a literal (it names a fact, not a variable), and
// that is the half this gate can check honestly.
var assertionGuardRe = regexp.MustCompile(`AssertionJudgeableBy\(Catalogue\(\),[^)]*?"([a-z0-9-]+)",\s*[A-Za-z_.]*[Tt]ier\s*\)`)

// TestEveryCarveOutIsWiredBothWays is the gate a mutation exposed the absence
// of.
//
// A ProviderAssertions entry and the AssertionJudgeableBy guard that reads it
// are two halves of one fact, kept in two files, and NOTHING checked they
// agreed. Deleting `ProviderAssertions: []string{"protection-armed"}` from
// space-init passes every coherence gate in tier_test.go: those check that a
// carve-out's entries appear in Assertions, that provider rows carry none, and
// that none repeat — never that a row which is GUARDED is also CARVED.
//
// Both directions are real defects, and neither is theoretical:
//
//   - **Guarded but not carved** — the guard answers true for any assertion the
//     row does not carve, so it does nothing, and the logic tier goes on
//     asserting a fact only a provider can answer. That reds at run time, ~20
//     minutes in, with a 404 that names a URL rather than a tier decision.
//   - **Carved but not guarded** — the carve-out is inert in the other
//     direction: the tier's report claims the assertion was exempted while the
//     body asserted it anyway. tier.go's own doc says a carve-out nothing
//     consults produces "either a false red or a false green".
//
// The check is deliberately source-level. There is no run-time registry of
// which assertions a body guards — the guard is an `if` in a function — so the
// only place the two halves can be compared without executing the tier is the
// text. That makes this gate cheap and, unlike the tier run, able to fail in
// milliseconds.
func TestEveryCarveOutIsWiredBothWays(t *testing.T) {
	t.Parallel()

	guards, err := scenarioGuardCalls()
	if err != nil {
		t.Fatalf("reading scenario sources: %v", err)
	}
	if len(guards) == 0 {
		t.Fatal("no AssertionJudgeableBy guard found in any scenario body — either every carve-out lost its wiring, or this gate's own regex stopped matching the shipped shape. Both are failures; a green result here would mean nothing.")
	}

	carved := map[string]string{} // assertion -> row that carves it
	for _, s := range Catalogue() {
		for _, a := range s.ProviderAssertions {
			carved[a] = s.Name
		}
	}
	if len(carved) == 0 {
		t.Fatal("the catalogue declares no ProviderAssertions at all — with nothing carved, every guard below is inert")
	}

	var problems []string

	seen := map[string]bool{}
	for _, g := range guards {
		seen[g.assertion] = true
		if _, ok := carved[g.assertion]; !ok {
			problems = append(problems, g.file+": guards "+g.assertion+
				", which NO catalogue row carves — the guard answers true and exempts nothing, so the logic tier asserts a provider-only fact")
		}
	}
	for assertion, row := range carved {
		if !seen[assertion] {
			problems = append(problems, "catalogue row "+row+": carves "+assertion+
				", but no scenario body guards it — the report claims an exemption the body did not take")
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d carve-out(s) wired in only one direction:\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
}

// scenarioGuardCalls reads every scenario source in this package and returns
// the assertion literal of each AssertionJudgeableBy call site.
//
// It reads the FILES rather than importing anything, because the guards live
// behind `//go:build livee2e` and this gate must run in the ordinary untagged
// suite — a wiring gate that only runs in the tagged lane would first report
// the problem inside the 20-minute run it exists to spare.
func scenarioGuardCalls() ([]guardCall, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []guardCall
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "scenarios_") || !strings.HasSuffix(name, ".go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			return nil, err
		}
		for _, m := range assertionGuardRe.FindAllStringSubmatch(string(raw), -1) {
			out = append(out, guardCall{file: name, assertion: m[1]})
		}
	}
	return out, nil
}
