package fold

import "testing"

// TestTransitionRowsUniverse pins the universe size the internal/livee2e
// path-coverage gate (W3) is checked against — every distinct (Kind, From,
// Transition) triple this package's rows admit
// (docs/features/archive/agent-ops-2026-07/plans/11-*.plan.md W3/D6). A
// count drift here is a real signal (a table row added/removed/merged),
// not test noise; the livee2e coverage gate is what should red on a drift
// that changes the covered set, not this one silently tracking it.
//
// 93, not 101: spec §18a (2026-08-06 operator decision, W3d) removed the
// eight per-Kind `create` rows (StateNone fromState) from table.go. They
// were never reachable — Fold always starts a kind at NewResult(kind) =
// StateDraft, so a committed create event's own fromState (StateNone) is
// a state the fold never occupies, and the generic table lookup already
// flagged it illegal-transition with or without a dedicated row (pinned
// by a before/after probe in this phase's own report, not by a test here
// — the row's removal is behaviour-identical by construction, so there is
// nothing to regression-pin beyond this count).
//
// It briefly rose to 100 on 2026-08-08 ("Q3 is no longer an argument, it is
// a measurement") with (response, disputed, supersede) — the producer's
// exit out of a disputed response, matching decision `rejected` and handoff
// `rejected`'s own supersede exits. 2026-08-09 deleted that row (spec 06's
// amendment, epic-backlog B8): P8's tagged conformance matrix proved no
// shipped verb ever reached it, and the exit it existed to provide already
// exists one level up — a dispute reopens the PARENT to in_progress, where
// the responder owes `respond`. Deleting the triple drops the count back
// to 99.
func TestTransitionRowsUniverse(t *testing.T) {
	got := TransitionRows()
	if len(got) != 99 {
		t.Fatalf("TransitionRows(): want 99 triples, got %d: %+v", len(got), got)
	}
}

// TestTransitionRowsExcludesStateNone asserts the create-transition rows
// (StateNone fromState) are ABSENT — inverted from this test's own
// pre-W3d shape (TestTransitionRowsIncludesStateNone) now that table.go
// carries no StateNone row at all (spec §18a): TransitionRows() and
// SubjectStates() agree on excluding StateNone for the first time, since
// neither accessor has anything left to disagree about.
func TestTransitionRowsExcludesStateNone(t *testing.T) {
	for _, tk := range TransitionRows() {
		if tk.From == StateNone {
			t.Fatalf("TransitionRows() returned a StateNone triple: %+v — table.go should carry no create row (spec §18a)", tk)
		}
	}
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
