package livee2e

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestFoldTransitionCellsAgreeOnHEAD is universe 2's own live cell,
// production shape: the REAL fold vocabulary (fold.BuildVocabulary(), never
// a second hand-written transition/kind/state list here) crossed over
// fold.TransitionRows() vs fold.LegalNext, with declaredTransitionExceptions()
// wired in as the real production exception roster (spec 10 §11, 2026-08-29
// amendment: this is the consumer that roster lacked). Green on HEAD: both
// readers are pure filters over the same underlying rows table,
// independently implemented (dedup by (Kind, From, Transition) vs. by
// (Transition, To, Role)) — a regression in either one's filtering logic
// reds this cell, and a stale declared exception reds by name too.
func TestFoldTransitionCellsAgreeOnHEAD(t *testing.T) {
	vocab := fold.BuildVocabulary()
	if len(vocab.Transitions) == 0 || len(vocab.States) == 0 {
		t.Fatal("fold.BuildVocabulary() published no transitions/states — nothing to cross")
	}
	exceptions := declaredTransitionExceptions()
	evaluated, excluded, errs := foldTransitionCells(vocab, exceptions)
	wantCells := 0
	for _, states := range vocab.States {
		wantCells += len(states) * len(vocab.Transitions)
	}
	if len(evaluated) != wantCells {
		t.Fatalf("evaluated %d cells, want %d (kinds x states x transitions)", len(evaluated), wantCells)
	}
	if len(errs) != 0 {
		t.Fatalf("foldTransitionCells disagreed, or a declared exception is STALE: %v", errs)
	}
	// declaredTransitionExceptions()'s own (and, at HEAD, only) entry —
	// {KindResponse, StateDraft, TSubmit} — must actually be the thing
	// excluded, with its written reason carried into the cell's own
	// record, not merely passed in and ignored.
	wantID := string(fold.KindResponse) + "/" + string(fold.StateDraft) + "/" + fold.TSubmit
	found := false
	for _, ex := range excluded {
		if ex.ID == wantID {
			found = true
			if strings.TrimSpace(ex.Reason) == "" {
				t.Fatalf("excluded cell %q carries no reason — the reason must be carried into the cell's own record", wantID)
			}
		}
	}
	if !found {
		t.Fatalf("excluded = %+v, want the declared P5 funnel-guard cell %q among them", excluded, wantID)
	}
	t.Logf("crossed %d (kind, state, transition) cells, %d declared exception(s) excluded, TransitionRows and LegalNext agree on the rest", len(evaluated), len(excluded))
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
// table's own "derivation" row asks for. No exceptions are declared for this
// fixture, so none are excluded.
func TestFoldTransitionCellsAreDerivedFromVocabulary(t *testing.T) {
	fixture := fold.Vocabulary{
		States: map[string][]string{
			"a-kind-the-real-fold-package-does-not-publish-yet": {"a-state-the-real-fold-package-does-not-publish-yet"},
		},
		Transitions: []string{"a-transition-the-real-fold-package-does-not-publish-yet"},
	}
	evaluated, excluded, errs := foldTransitionCells(fixture, nil)
	wantID := "a-kind-the-real-fold-package-does-not-publish-yet/a-state-the-real-fold-package-does-not-publish-yet/a-transition-the-real-fold-package-does-not-publish-yet"
	if len(evaluated) != 1 || evaluated[0] != wantID {
		t.Fatalf("evaluated %v, want exactly [%q] — the cross-product is not reacting to its own parameter", evaluated, wantID)
	}
	if len(excluded) != 0 {
		t.Fatalf("excluded = %v, want none — no exceptions were declared", excluded)
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
	beforeEvaluated, _, _ := foldTransitionCells(before, nil)
	afterEvaluated, _, _ := foldTransitionCells(after, nil)
	if len(afterEvaluated) <= len(beforeEvaluated) {
		t.Fatalf("adding a transition to vocab.Transitions did not grow the cross-product: before=%d after=%d", len(beforeEvaluated), len(afterEvaluated))
	}
	if !verbAgreementContainsString(afterEvaluated, "fixture-kind/fixture-state/fixture-transition-two") {
		t.Fatalf("evaluated %v does not carry a cell for the newly-added transition", afterEvaluated)
	}
}

// TestFoldTransitionCellsExcludesADeclaredExceptionWithItsReason is
// wave-5-closure item 2 (spec 10 §11, 2026-08-29 amendment): a declared
// exception whose cell IS still legal by the table is excluded from the
// reader-agreement assertion, and its reason is carried into the excluded
// cell's own record rather than the cell being silently dropped.
func TestFoldTransitionCellsExcludesADeclaredExceptionWithItsReason(t *testing.T) {
	vocab := fold.Vocabulary{
		States:      map[string][]string{string(fold.KindResponse): {string(fold.StateDraft)}},
		Transitions: []string{fold.TSubmit},
	}
	declared := []declaredTransitionException{
		{
			Kind:       string(fold.KindResponse),
			State:      string(fold.StateDraft),
			Transition: fold.TSubmit,
			Reason:     "test fixture: this triple is legal by fold.TransitionRows() on HEAD",
		},
	}
	evaluated, excluded, errs := foldTransitionCells(vocab, declared)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none — the declared exception's cell is legal by fold.TransitionRows(), so it is not stale", errs)
	}
	wantID := string(fold.KindResponse) + "/" + string(fold.StateDraft) + "/" + fold.TSubmit
	if len(evaluated) != 1 || evaluated[0] != wantID {
		t.Fatalf("evaluated %v, want exactly [%q] — the cell is still evaluated even though excluded from the agreement assertion", evaluated, wantID)
	}
	if len(excluded) != 1 || excluded[0].ID != wantID || excluded[0].Reason != declared[0].Reason {
		t.Fatalf("excluded = %+v, want one record naming %q and carrying its declared reason", excluded, wantID)
	}
}

// TestFoldTransitionCellsRedsOnAStaleDeclaredException is wave-5-closure
// item 3, watched failing before being believed (spec 10 §11, 2026-08-29
// amendment): a declared exception naming a transition fold.TransitionRows()
// does not carry — the fixture kind/state exist in the vocab crossed here,
// but the transition is fictitious and no row anywhere in the real fold
// table uses it — is STALE, and foldTransitionCells REDS naming it, rather
// than silently accepting a declaration that no longer filters anything.
func TestFoldTransitionCellsRedsOnAStaleDeclaredException(t *testing.T) {
	vocab := fold.Vocabulary{
		States:      map[string][]string{"fixture-kind": {"fixture-state"}},
		Transitions: []string{"fixture-transition-that-exists-in-this-fixture-vocabulary"},
	}
	staleTransition := "fixture-transition-that-fold-transitionrows-does-not-carry"
	stale := []declaredTransitionException{
		{
			Kind:       "fixture-kind",
			State:      "fixture-state",
			Transition: staleTransition,
			Reason:     "test fixture: seeded stale exception, must red",
		},
	}
	_, excluded, errs := foldTransitionCells(vocab, stale)
	if len(excluded) != 0 {
		t.Fatalf("excluded = %+v, want none — a stale exception's cell must not be silently excluded from anything", excluded)
	}
	if len(errs) == 0 {
		t.Fatal("foldTransitionCells did not red on a declared exception naming a transition fold.TransitionRows() does not carry — a declaration that filters nothing must not stay silently green")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "STALE") && strings.Contains(err.Error(), staleTransition) {
			found = true
		}
	}
	if !found {
		t.Fatalf("errs = %v, want one naming the stale exception (%q) as STALE", errs, staleTransition)
	}
}
