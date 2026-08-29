package livee2e

import (
	"fmt"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// foldtransitions.go generalises universe 2 (answers-that-hold-2026-08 spec
// 10 §"The input universes", row 2; 2026-08-28 amendment) from the one
// FIXED transition/kind pair scenarios_verb_agreement.go's
// advertiserLegalityNoteCells already carries (fold.TNote /
// fold.KindResponse only) to every transition fold.BuildVocabulary()
// publishes — so a transition added to the fold model produces a new cell
// with no Go edit (AC-7), never a second hand-written transition list
// (AC-14).
//
// The two readers crossed here are fold.TransitionRows() (the raw table
// enumerator: "every distinct (Kind, From, Transition) triple appearing in
// this package's transition rows") and fold.LegalNext (the derived
// per-state accessor CheckLegality's own doc comment and internal/cache's
// thread view both build on) — DELIBERATELY NOT fold.CheckLegality/
// CheckCandidateWithSuccessor, whose own doc comments document that verify
// and dispute are looked up via a HARDCODED KindResponse table regardless
// of the caller's own kind, while LegalNext reports the dispute-reopens-
// the-exchange row (table.go, Kind: <exchange kind>, From: StateResponded)
// UNMODIFIED (legalnext.go's own doc comment: "LegalNext reports both,
// unmodified — a pure filter has no dispatch logic to apply here"). Any
// same-args comparison against CheckLegality would manufacture a
// disagreement out of that documented calling-convention split rather than
// finding a fold-model bug — see this wave's own deviations note.
// TransitionRows and LegalNext carry no such redirect (both are pure
// filters over the SAME `rows` table, independently implemented — dedup by
// (Kind, From, Transition) vs. dedup by (Transition, To, Role) — exactly
// the "two readers of one question" shape this epic's own mirror-decoder-
// vs-doctor pair already ships for path classification), so the pair is
// safe to cross generically over every transition, kind and state the
// vocabulary publishes.

// excludedTransitionCell is one cross-product cell EXCLUDED from the reader
// agreement assertion because it matches a declaredTransitionException — the
// cell's own record, carrying the reason forward rather than the cell being
// silently dropped or left green by accident (spec 10 §11, 2026-08-28
// amendment: "the cell must carry a written reason, not be quietly dropped
// or left green").
type excludedTransitionCell struct {
	ID     string
	Reason string
}

// foldTransitionCells is universe 2's own cross-product. vocab is a
// PARAMETER — the production caller passes fold.BuildVocabulary()'s real
// answer; a fixture test (AC-7/AC-14) passes a synthetic Vocabulary
// carrying a kind, state or transition the real fold package does not yet
// publish, to prove this function reacts to whatever the vocabulary
// carries with no edit to this file.
//
// exceptions is declaredTransitionExceptions()'s roster in production —
// this is the consumer that roster lacked (spec 10 §11, 2026-08-29
// amendment: "declaredTransitionExceptions() has no consumer... a
// declaration list that filters nothing is this epic's own class"). Every
// declared exception is checked against fold.TransitionRows() — the same
// raw table enumerator this cross-product already crosses against
// fold.LegalNext — because "legal by the table" (the 2026-08-28 amendment's
// own wording for why the P5 cell may not simply be deleted) is exactly
// what TransitionRows() answers.
//
// A component-wise check against vocab alone would be WEAKER, and the
// reason first written here for preferring the triple was wrong, so it is
// corrected rather than quietly deleted: it claimed vocab.States[kind]
// carries StateDraft for every kind because RestingStates() adds it
// unconditionally. It does not — postSubmissionState (internal/fold/fold.go)
// maps KindResponse to StateSubmitted, and only KindDecision to StateDraft.
// StateDraft reaches vocab.States[KindResponse] through SubjectStates
// reading table.go:364's own From, so deleting that row WOULD move the
// component too, and a component-wise check would have reded here.
//
// The triple is still the right check, on the honest argument: half of
// vocab.Transitions IS global (TSubmit is carried by other kinds' rows), so
// a component-wise test is vacuous on that axis, and only the (kind, state,
// transition) TRIPLE expresses what "legal by the table" means. A declared
// exception whose triple TransitionRows() no longer carries is STALE and
// reds by name.
func foldTransitionCells(vocab fold.Vocabulary, exceptions []declaredTransitionException) (evaluated []string, excluded []excludedTransitionCell, errs []error) {
	rows := fold.TransitionRows()
	inRows := make(map[[3]string]bool, len(rows))
	for _, tr := range rows {
		inRows[[3]string{string(tr.Kind), string(tr.From), tr.Transition}] = true
	}

	exceptionByCell := make(map[[3]string]declaredTransitionException, len(exceptions))
	for _, e := range exceptions {
		cell := [3]string{e.Kind, e.State, e.Transition}
		if !inRows[cell] {
			errs = append(errs, fmt.Errorf(
				"declared transition exception is STALE: %s/%s/%s is no longer legal by fold.TransitionRows() (declared reason: %q) — remove or re-target this exception",
				e.Kind, e.State, e.Transition, e.Reason))
			continue
		}
		exceptionByCell[cell] = e
	}

	kinds := make([]string, 0, len(vocab.States))
	for kind := range vocab.States {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)

	for _, kind := range kinds {
		for _, state := range vocab.States[kind] {
			legalNext := fold.LegalNext(fold.Kind(kind), fold.State(state))
			inLegalNext := make(map[string]bool, len(legalNext))
			for _, move := range legalNext {
				inLegalNext[move.Transition] = true
			}
			for _, transition := range vocab.Transitions {
				id := fmt.Sprintf("%s/%s/%s", kind, state, transition)
				evaluated = append(evaluated, id)
				if e, ok := exceptionByCell[[3]string{kind, state, transition}]; ok {
					excluded = append(excluded, excludedTransitionCell{ID: id, Reason: e.Reason})
					continue
				}
				verdicts := map[string]bool{
					"fold.TransitionRows (raw table enumerator)":  inRows[[3]string{kind, state, transition}],
					"fold.LegalNext (derived per-state accessor)": inLegalNext[transition],
				}
				if err := assertReaderAgreement(id, verdicts, nil); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return evaluated, excluded, errs
}
