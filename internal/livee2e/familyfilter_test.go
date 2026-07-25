package livee2e

import (
	"reflect"
	"strings"
	"testing"
)

var filterDeclared = []string{"happy", "contract-integrity", "submitted-family", "draft-family", "boundary", "refusal", "space"}

func TestSelectedFamiliesUnsetRunsEverything(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "   ", "\t\n"} {
		got, err := selectedFamilies(raw, filterDeclared)
		if err != nil {
			t.Fatalf("raw %q: %v", raw, err)
		}
		if len(got) != len(filterDeclared) {
			t.Fatalf("raw %q selected %d families, want all %d — an unset variable must be the full matrix", raw, len(got), len(filterDeclared))
		}
	}
}

func TestSelectedFamiliesNarrows(t *testing.T) {
	t.Parallel()

	got, err := selectedFamilies(" happy , space ,", filterDeclared)
	if err != nil {
		t.Fatalf("selectedFamilies: %v", err)
	}
	if want := []string{"happy", "space"}; !reflect.DeepEqual(sortedKeys(got), want) {
		t.Fatalf("selected %v, want %v — surrounding space and a trailing comma are typos, not requests", sortedKeys(got), want)
	}
}

// An unknown name must REFUSE, not select nothing. Selecting nothing runs
// zero families, and because every row then keeps its declared not-run
// verdict, the report is indistinguishable from a catastrophic matrix
// failure — the operator debugs the product instead of their own typo.
//
// TEETH: make the unknown branch `continue` instead of collecting, and this
// reds — selectedFamilies returns an empty set with a nil error.
func TestSelectedFamiliesRefusesAnUnknownName(t *testing.T) {
	t.Parallel()

	got, err := selectedFamilies("happy,happpy", filterDeclared)
	if err == nil {
		t.Fatalf("expected a refusal, got selection %v", sortedKeys(got))
	}
	if !strings.Contains(err.Error(), "happpy") {
		t.Fatalf("the refusal must name the unknown value; got %v", err)
	}
	// Naming the legal set matters: "invalid family" without it is a second
	// round-trip for something the program already knows.
	for _, name := range filterDeclared {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("the refusal must list every declared family (missing %q); got %v", name, err)
		}
	}
}

func TestSelectedFamiliesRefusesAValueThatNamesNothing(t *testing.T) {
	t.Parallel()

	if _, err := selectedFamilies(",,, ,", filterDeclared); err == nil {
		t.Fatal("expected a refusal: a value made only of separators names no family, and must not silently mean `everything`")
	}
}

// The honesty property the filter rests on, asserted rather than assumed: a
// narrowed run cannot exit 0, because the families it skipped keep their
// declared not-run verdict and ExitCode() is non-zero for any verdict that
// is not a pass. This is why the filter needs no guard of its own.
func TestANarrowedRunCannotReportSuccess(t *testing.T) {
	t.Parallel()

	r := Report{Results: []Result{
		{Scenario: "ran", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass},
		{Scenario: "skipped", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictNotRun},
	}}
	if code := r.ExitCode(); code == 0 {
		t.Fatal("a report carrying a not-run row must not exit 0 — the whole filter design rests on this")
	}
}
