package livee2e

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestFoldTransitionCellsAgreeOnHEAD is universe 2's own live cell,
// production shape: the REAL fold vocabulary (fold.BuildVocabulary(), never
// a second hand-written transition/kind/state list here) crossed over
// fold.TransitionRows() vs fold.LegalNext. Green on HEAD: both readers are
// pure filters over the same underlying rows table, independently
// implemented (dedup by (Kind, From, Transition) vs. by (Transition, To,
// Role)) — a regression in either one's filtering logic reds this cell.
func TestFoldTransitionCellsAgreeOnHEAD(t *testing.T) {
	vocab := fold.BuildVocabulary()
	if len(vocab.Transitions) == 0 || len(vocab.States) == 0 {
		t.Fatal("fold.BuildVocabulary() published no transitions/states — nothing to cross")
	}
	evaluated, errs := foldTransitionCells(vocab)
	wantCells := 0
	for _, states := range vocab.States {
		wantCells += len(states) * len(vocab.Transitions)
	}
	if len(evaluated) != wantCells {
		t.Fatalf("evaluated %d cells, want %d (kinds x states x transitions)", len(evaluated), wantCells)
	}
	if len(errs) != 0 {
		t.Fatalf("foldTransitionCells disagreed on HEAD: %v", errs)
	}
	t.Logf("crossed %d (kind, state, transition) cells, TransitionRows and LegalNext agree on all of them", len(evaluated))
}

// TestFoldTransitionCellsAreDerivedFromVocabulary is AC-7/AC-14 at the
// mechanism level: foldTransitionCells reacts to whatever vocab carries — a
// FIXTURE Vocabulary here, never fold's own package edited, and never a
// second transition/kind/state list maintained inside internal/livee2e —
// proving the cross-product is TRANSITION-generic (not fixed to one
// transition, unlike the shipped advertiserLegalityNoteCells), kind-generic
// and state-generic, driven entirely by its PARAMETER.
//
// Both readers legitimately say "absent" for a fixture value the real fold
// tables do not carry, so the fixture cell agrees trivially — this test is
// about CELL CONSTRUCTION (the cross-product grows with no Go edit), not
// about exercising a live disagreement, exactly as the Testing requirements
// table's own "derivation" row asks for.
func TestFoldTransitionCellsAreDerivedFromVocabulary(t *testing.T) {
	fixture := fold.Vocabulary{
		States: map[string][]string{
			"a-kind-the-real-fold-package-does-not-publish-yet": {"a-state-the-real-fold-package-does-not-publish-yet"},
		},
		Transitions: []string{"a-transition-the-real-fold-package-does-not-publish-yet"},
	}
	evaluated, errs := foldTransitionCells(fixture)
	wantID := "a-kind-the-real-fold-package-does-not-publish-yet/a-state-the-real-fold-package-does-not-publish-yet/a-transition-the-real-fold-package-does-not-publish-yet"
	if len(evaluated) != 1 || evaluated[0] != wantID {
		t.Fatalf("evaluated %v, want exactly [%q] — the cross-product is not reacting to its own parameter", evaluated, wantID)
	}
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none — both readers agree (absent) on a value neither publishes", errs)
	}
}

// TestFoldTransitionCellsGrowsWithATransitionAddedToTheTable is AC-7's own
// literal wording ("a transition added to the fold model produces a new
// cell with no Go edit"): the SAME fixture kind/state, once with one
// transition in vocab.Transitions and once with two, must evaluate strictly
// more cells the second time — proving growth is driven by the vocabulary's
// own Transitions slice, not by anything this file enumerates.
func TestFoldTransitionCellsGrowsWithATransitionAddedToTheTable(t *testing.T) {
	before := fold.Vocabulary{
		States:      map[string][]string{"fixture-kind": {"fixture-state"}},
		Transitions: []string{"fixture-transition-one"},
	}
	after := fold.Vocabulary{
		States:      map[string][]string{"fixture-kind": {"fixture-state"}},
		Transitions: []string{"fixture-transition-one", "fixture-transition-two"},
	}
	beforeEvaluated, _ := foldTransitionCells(before)
	afterEvaluated, _ := foldTransitionCells(after)
	if len(afterEvaluated) <= len(beforeEvaluated) {
		t.Fatalf("adding a transition to vocab.Transitions did not grow the cross-product: before=%d after=%d", len(beforeEvaluated), len(afterEvaluated))
	}
	if !verbAgreementContainsString(afterEvaluated, "fixture-kind/fixture-state/fixture-transition-two") {
		t.Fatalf("evaluated %v does not carry a cell for the newly-added transition", afterEvaluated)
	}
}
