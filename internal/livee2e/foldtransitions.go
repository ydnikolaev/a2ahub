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

// foldTransitionCells is universe 2's own cross-product. vocab is a
// PARAMETER — the production caller passes fold.BuildVocabulary()'s real
// answer; a fixture test (AC-7/AC-14) passes a synthetic Vocabulary
// carrying a kind, state or transition the real fold package does not yet
// publish, to prove this function reacts to whatever the vocabulary
// carries with no edit to this file.
func foldTransitionCells(vocab fold.Vocabulary) (evaluated []string, errs []error) {
	inRows := make(map[[3]string]bool)
	for _, tr := range fold.TransitionRows() {
		inRows[[3]string{string(tr.Kind), string(tr.From), tr.Transition}] = true
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
	return evaluated, errs
}
