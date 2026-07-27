package livee2e

import (
	"os"
	"strings"
	"testing"
)

// TestMissingCoveredFamiliesFindsAGap is missingCoveredFamilies' own unit
// test — a SYNTHETIC gap, matching completeness.go's
// TestMissingCoveredKindsFindsAGap exactly: stays green permanently while
// still proving the comparison logic reds on an undispatched family.
func TestMissingCoveredFamiliesFindsAGap(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "a", Family: "happy"},
		{Name: "b", Family: "boundary"},
	}
	got := missingCoveredFamilies([]string{"happy", "boundary", "refusal"}, scenarios)
	if len(got) != 1 || got[0] != "refusal" {
		t.Fatalf("missingCoveredFamilies = %v, want [refusal]", got)
	}
}

func TestMissingCoveredFamiliesEmptyWhenFullyCovered(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{{Name: "a", Family: "happy"}, {Name: "b", Family: "boundary"}}
	got := missingCoveredFamilies([]string{"happy", "boundary"}, scenarios)
	if len(got) != 0 {
		t.Fatalf("missingCoveredFamilies = %v, want empty", got)
	}
}

// TestUnknownDeclaredFamiliesFindsATypo is unknownDeclaredFamilies' own unit
// test, matching completeness.go's TestUnknownDeclaredKindsFindsATypo:
// a Family value outside the canonical list — a typo, a blank field, or a
// family renamed/removed in driveFamilies — must be caught.
func TestUnknownDeclaredFamiliesFindsATypo(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "good-row", Family: "happy"},
		{Name: "typo-row", Family: "Happy"},   // capitalized — not the real value
		{Name: "blank-row", Family: ""},       // never filled in at all
		{Name: "stale-row", Family: "widget"}, // never a real family
	}
	got := unknownDeclaredFamilies([]string{"happy", "boundary"}, scenarios)
	want := []string{`blank-row: ""`, `stale-row: "widget"`, `typo-row: "Happy"`}
	if len(got) != len(want) {
		t.Fatalf("unknownDeclaredFamilies = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("unknownDeclaredFamilies[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestUnknownDeclaredFamiliesEmptyWhenAllValid(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "a", Family: "happy"},
		{Name: "b", Family: FamilySpaceCIHealth},
	}
	got := unknownDeclaredFamilies([]string{"happy", "boundary"}, scenarios)
	if len(got) != 0 {
		t.Fatalf("unknownDeclaredFamilies = %v, want empty (FamilySpaceCIHealth is the one documented exception)", got)
	}
}

// TestEveryCatalogueRowDeclaresAKnownFamily is the gate itself (spec 46
// postmortem, 2026-07-27): the matrix is dispatchable only if every
// Catalogue() row names a Family that CanonicalFamilies() (or the one
// documented exception, FamilySpaceCIHealth) actually covers, AND every
// canonical family has at least one row claiming it. A row whose family was
// declared in catalogue.go but never added to driveFamilies' dispatch table
// (runner_live_test.go) is exactly the defect this test exists to catch —
// in milliseconds, in the suite `make check` runs, rather than at the end
// of a fifty-nine-minute live run.
func TestEveryCatalogueRowDeclaresAKnownFamily(t *testing.T) {
	t.Parallel()
	families := CanonicalFamilies()
	catalogue := Catalogue()

	if missing := missingCoveredFamilies(families, catalogue); len(missing) > 0 {
		t.Errorf("these canonical families have no Catalogue() row declaring them: %s — a family driveFamilies dispatches but the catalogue never claims",
			strings.Join(missing, ", "))
	}
	if unknown := unknownDeclaredFamilies(families, catalogue); len(unknown) > 0 {
		t.Errorf("these Scenario.Family values are not a canonical family (or the FamilySpaceCIHealth exception): %s — a row whose family driveFamilies never dispatches stays not-run for the full run before anyone notices (spec 46 postmortem, 2026-07-27)",
			strings.Join(unknown, ", "))
	}
}

// TestFamilySpaceCIHealthIsExactlyOneRow guards the one documented exception
// itself: it must name EXACTLY the space-ci-has-no-unexplained-failures row,
// never silently grow to cover a second row that actually belongs in
// CanonicalFamilies() (which would defeat the dispatch-coverage check this
// file exists for) and never shrink to zero (which would mean the row that
// motivated the exception lost its Family value entirely).
func TestFamilySpaceCIHealthIsExactlyOneRow(t *testing.T) {
	t.Parallel()
	var matched []string
	for _, sc := range Catalogue() {
		if sc.Family == FamilySpaceCIHealth {
			matched = append(matched, sc.Name)
		}
	}
	want := []string{"space-ci-has-no-unexplained-failures"}
	if len(matched) != len(want) {
		t.Fatalf("rows declaring Family == FamilySpaceCIHealth = %v, want %v", matched, want)
	}
	for i := range want {
		if matched[i] != want[i] {
			t.Errorf("matched[%d] = %q, want %q", i, matched[i], want[i])
		}
	}
}

// TestCanonicalFamiliesHasNoDuplicatesOrBlanks is a sanity check on the list
// itself: a duplicate or blank entry would make missingCoveredFamilies and
// unknownDeclaredFamilies both silently misbehave.
func TestCanonicalFamiliesHasNoDuplicatesOrBlanks(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, f := range CanonicalFamilies() {
		if f == "" {
			t.Error("CanonicalFamilies() holds a blank entry")
		}
		if seen[f] {
			t.Errorf("CanonicalFamilies() declares %q twice", f)
		}
		seen[f] = true
	}
}

// TestSpaceCIHealthRowIsStillDispatched closes the one hole the family
// pre-flight assertion structurally cannot cover.
//
// Every other catalogue row is protected by an equality check: driveFamilies'
// dispatch table must name exactly CanonicalFamilies(), so a row whose family
// nobody dispatches fails in milliseconds instead of surfacing as `not-run` an
// hour later — which is the whole defect this file was written for. The
// `space-ci-has-no-unexplained-failures` row is the documented exception
// (FamilySpaceCIHealth): TestLiveMatrix calls runSpaceCIHealth DIRECTLY, after
// driveFamilies returns, because it has to observe the whole run's window. It
// is therefore outside the equality check by construction, and deleting or
// commenting out that one call would put its row straight back into the
// hour-long-silence failure mode the exception was carved out of.
//
// So this asserts the call site itself, from the source text. That is a coarse
// instrument, and deliberately so: the alternative is no gate at all, because
// runSpaceCIHealth and TestLiveMatrix both live behind //go:build livee2e and
// an untagged test cannot reference either symbol.
func TestSpaceCIHealthRowIsStillDispatched(t *testing.T) {
	t.Parallel()

	const runner = "runner_live_test.go"
	raw, err := os.ReadFile(runner)
	if err != nil {
		t.Fatalf("read %s: %v", runner, err)
	}
	if !strings.Contains(string(raw), "runSpaceCIHealth(") {
		t.Fatalf("%s no longer calls runSpaceCIHealth, but the catalogue still declares "+
			"%q with Family %q — that row is dispatched OUTSIDE driveFamilies, so the family "+
			"pre-flight equality check cannot see it, and it would go `not-run` at the END of a "+
			"full live run with nothing explaining why. Either restore the call, or remove the "+
			"row and its FamilySpaceCIHealth exception together.",
			runner, "space-ci-has-no-unexplained-failures", FamilySpaceCIHealth)
	}
}
