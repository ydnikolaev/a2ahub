package fold

import "testing"

// TestSubjectStatesUniverse pins the universe size internal/pendency's I8
// totality gate is checked against — 35 distinct (Kind, From) pairs today
// (docs/features/archive/agent-ops-2026-07/plans/11-*.plan.md W1). A count
// drift here is a real signal (a table row added/removed), not test noise;
// internal/pendency's gate is what should red, not this one silently
// tracking it.
//
// It briefly rose to 36 on 2026-08-08 with {response disputed}, added when
// a row gave it a supersede exit: a state nothing departed was in no From
// position, so it was not a subject anybody could owe a move from. P8's
// tagged conformance matrix then proved no shipped verb ever reached that
// exit (epic-backlog B8), and 2026-08-09 deleted the row rather than
// widening the routing to reach it — the exit it existed to provide already
// exists one level up, on the PARENT the dispute reopens. Deleting the
// row's only departure from {response disputed} drops the universe back to
// 35; the state itself still exists (the row above it, {response submitted}
// --dispute--> disputed, is what CREATES it, and that row was not touched).
func TestSubjectStatesUniverse(t *testing.T) {
	got := SubjectStates()
	if len(got) != 35 {
		t.Fatalf("SubjectStates(): want 35 pairs, got %d: %+v", len(got), got)
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
