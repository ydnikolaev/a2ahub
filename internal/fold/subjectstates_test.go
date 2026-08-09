package fold

import "testing"

// TestSubjectStatesUniverse pins the universe size internal/pendency's I8
// totality gate is checked against — 35 distinct (Kind, From) pairs today
// (docs/features/active/agent-ops-2026-07/plans/11-*.plan.md W1). A count
// drift here is a real signal (a table row added/removed), not test noise;
// internal/pendency's gate is what should red, not this one silently
// tracking it.
// The 36th is {response disputed}, added when P6 gave it a supersede exit:
// a state nothing departed was in no From position, so it was not a subject
// anybody could owe a move from. Giving it an exit makes it one, and
// internal/pendency's I8 gate is what then requires the row that says who.
func TestSubjectStatesUniverse(t *testing.T) {
	got := SubjectStates()
	if len(got) != 36 {
		t.Fatalf("SubjectStates(): want 36 pairs, got %d: %+v", len(got), got)
	}
}

// TestSubjectStatesExcludesNone asserts the sentinel "artifact does not
// exist yet" fromState never appears — SubjectStates' one explicit
// exclusion.
func TestSubjectStatesExcludesNone(t *testing.T) {
	for _, ks := range SubjectStates() {
		if ks.State == StateNone {
			t.Fatalf("SubjectStates() returned a StateNone pair: %+v", ks)
		}
	}
}

// TestSubjectStatesDeduplicatedAndSorted asserts no pair repeats and the
// result is sorted (Kind, then State) — determinism is the accessor's
// entire contract for a caller building a lookup universe off it.
func TestSubjectStatesDeduplicatedAndSorted(t *testing.T) {
	got := SubjectStates()
	seen := make(map[KindState]bool, len(got))
	for i, ks := range got {
		if seen[ks] {
			t.Fatalf("SubjectStates() returned a duplicate pair: %+v", ks)
		}
		seen[ks] = true
		if i > 0 {
			prev := got[i-1]
			if ks.Kind < prev.Kind || (ks.Kind == prev.Kind && ks.State < prev.State) {
				t.Fatalf("SubjectStates() not sorted at index %d: %+v before %+v", i, prev, ks)
			}
		}
	}
}

// TestSubjectStatesFreshSlice asserts a caller mutating one returned
// slice cannot corrupt what a later call returns.
func TestSubjectStatesFreshSlice(t *testing.T) {
	first := SubjectStates()
	if len(first) == 0 {
		t.Fatal("SubjectStates() returned no pairs")
	}
	first[0] = KindState{Kind: "corrupted", State: "corrupted"}

	second := SubjectStates()
	for _, ks := range second {
		if ks.Kind == "corrupted" {
			t.Fatal("SubjectStates() shares backing storage across calls")
		}
	}
}
