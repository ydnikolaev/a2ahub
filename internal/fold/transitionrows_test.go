package fold

import "testing"

// TestTransitionRowsUniverse pins the universe size the internal/livee2e
// path-coverage gate (W3) is checked against — every distinct (Kind, From,
// Transition) triple this package's rows admit, StateNone rows included
// (docs/features/active/agent-ops-2026-07/plans/11-*.plan.md W3/D6). A
// count drift here is a real signal (a table row added/removed/merged),
// not test noise; the livee2e coverage gate is what should red on a drift
// that changes the covered set, not this one silently tracking it.
func TestTransitionRowsUniverse(t *testing.T) {
	got := TransitionRows()
	if len(got) != 101 {
		t.Fatalf("TransitionRows(): want 101 triples, got %d: %+v", len(got), got)
	}
}

// TestTransitionRowsIncludesStateNone asserts the create-transition rows
// (StateNone fromState) ARE present — the one deliberate difference from
// SubjectStates' universe: a path can drive a create, so it must be in
// scope for coverage.
func TestTransitionRowsIncludesStateNone(t *testing.T) {
	for _, tk := range TransitionRows() {
		if tk.From == StateNone {
			return
		}
	}
	t.Fatal("TransitionRows() excludes every StateNone row; create transitions must be covered")
}

// TestTransitionRowsDeduplicatedAndSorted asserts no triple repeats (a
// Scenario-only difference, e.g. decision approve's quorum-reached vs
// quorum-not-reached rows, must collapse to one triple) and the result is
// sorted (Kind, From, Transition).
func TestTransitionRowsDeduplicatedAndSorted(t *testing.T) {
	got := TransitionRows()
	seen := make(map[TransitionKey]bool, len(got))
	for i, tk := range got {
		if seen[tk] {
			t.Fatalf("TransitionRows() returned a duplicate triple: %+v", tk)
		}
		seen[tk] = true
		if i > 0 {
			prev := got[i-1]
			less := prev.Kind < tk.Kind ||
				(prev.Kind == tk.Kind && prev.From < tk.From) ||
				(prev.Kind == tk.Kind && prev.From == tk.From && prev.Transition < tk.Transition)
			equal := prev == tk
			if !less && !equal {
				t.Fatalf("TransitionRows() not sorted at index %d: %+v before %+v", i, prev, tk)
			}
		}
	}
}

// TestTransitionRowsFreshSlice asserts a caller mutating one returned
// slice cannot corrupt what a later call returns.
func TestTransitionRowsFreshSlice(t *testing.T) {
	first := TransitionRows()
	if len(first) == 0 {
		t.Fatal("TransitionRows() returned no triples")
	}
	first[0] = TransitionKey{Kind: "corrupted", From: "corrupted", Transition: "corrupted"}

	second := TransitionRows()
	for _, tk := range second {
		if tk.Kind == "corrupted" {
			t.Fatal("TransitionRows() shares backing storage across calls")
		}
	}
}
