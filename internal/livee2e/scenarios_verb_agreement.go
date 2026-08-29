package livee2e

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// scenarios_verb_agreement.go wires this phase's own declared pairs
// (answers-that-hold-2026-08 spec 10) to the two assertion shapes
// (verbclasses.go) and to universe 2 — transitions × kind × state, derived
// from fold.BuildVocabulary(), never a hand list (spec 10 §"The input
// universes", row 2).

// declaredTransitionException is the classify-or-declare vocabulary AC-11
// already defines for a catalogue VERB, reused here for a universe-2
// TRANSITION this phase's own cross-product would otherwise construct a
// cell for, but that this phase declares out of reach with a written
// reason rather than silently dropping the row or leaving it green by
// accident (spec 10 §11, 2026-08-28 amendment: "the reason is structural
// and available... an exception is announced").
type declaredTransitionException struct {
	Kind, State, Transition, Reason string
}

// declaredTransitionExceptions is this phase's own first (and, at HEAD,
// only) universe-2 exception: this epic itself creates a transition the
// derivation cannot judge honestly, per the 2026-08-28 amendment.
func declaredTransitionExceptions() []declaredTransitionException {
	return []declaredTransitionException{
		{
			Kind:       string(fold.KindResponse),
			State:      string(fold.StateDraft),
			Transition: fold.TSubmit,
			Reason: "P5 installs a funnel guard refusing a write of a parent-requiring kind with no " +
				"parent transition in the same commit. {KindResponse, StateDraft, TSubmit, " +
				"StateSubmitted} (internal/fold/table.go:364) is still legal BY THE TABLE — it is " +
				"`a2a respond`'s own move, and four declared paths drive it — but is no longer " +
				"reachable via `a2a submit` after that guard. The transition has exactly one legal " +
				"producer, and the pair this cell would test is a verb the funnel now refuses by " +
				"design. NOT resolved by deleting the row: table.go:364 is `a2a respond`'s own legal " +
				"move and deleting it breaks that verb (spec 10 §11, 2026-08-28 amendment).",
		},
	}
}

// advertiserLegalityNoteCells is the advertiser/legality pair's (spec 10
// §11's 2026-08-29 amendment; fb-20260801-457629) own LIVE, currently-green
// universe-2 cross-product: every state a KindResponse subject can occupy,
// derived from vocab (pass fold.BuildVocabulary() in production; a test may
// pass a fixture Vocabulary carrying a state the domain does not yet
// publish, to prove this function reacts with no Go edit here — AC-7/AC-14,
// never a second hand-written state list).
//
// leftAccepted is always true: `a2a note`'s advertiser (NoteCommand.Run,
// internal/cli/cmd_lifecycle.go, read but not edited by this wave) probes
// only that the envelope exists and submits unconditionally — no fold call
// at all, for any state — exactly the mechanism
// scenarios_incidents.go's own "advertiser-legality-note-submitted" replay
// entry establishes for the pre-fix tree. rightRefused is computed for
// real, for every state, by calling the SAME fold.CheckLegality the space's
// required check runs: the fix that closed 457629
// (internal/fold/legality.go's TNote branch) does not condition on state
// either, which is WHY this cross-product is green today rather than only
// at the one state the incident happened to report — the fix generalised
// across the whole derived universe, not merely the reported cell.
//
// evaluated echoes back exactly the states this call actually crossed —
// vocab.States[...] itself, unchanged — so a caller (or a test standing in
// for AC-7/AC-14) can confirm this function reacts to whatever vocab
// carries, including a fixture state the real fold package does not yet
// publish, with no edit to this file.
func advertiserLegalityNoteCells(vocab fold.Vocabulary) (evaluated []string, errs []error) {
	states := vocab.States[string(fold.KindResponse)]
	env := fold.Envelope{From: "system-a", To: []string{"system-b"}}
	actor := fold.Actor{System: "system-a"}

	for _, s := range states {
		verdict := fold.CheckLegality(fold.KindResponse, fold.State(s), fold.TNote, env, actor, fold.MembershipMember)
		rightRefused := verdict == fold.VerdictIllegalTransition
		evaluated = append(evaluated, s)
		if err := assertDirectionalCell(pairPreviewingActing, "a2a note (advertiser)", true,
			"fold.CheckLegality (space's required V2 lifecycle check)", rightRefused); err != nil {
			errs = append(errs, fmt.Errorf("state=%s verdict=%s: %w", s, verdict, err))
		}
	}
	return evaluated, errs
}

// declaredPairVerbs collects every verb name this phase's own declared
// pairs and replay entries actually cite — the classify-or-declare
// obligation's own "usedVerbs" input (US-5), so a verb referenced by a live
// or documentary cell but absent from verbCatalogue() is caught rather than
// silently treated as covered.
func declaredPairVerbs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, r := range verbAgreementReplays() {
		add(r.PreFixLeftVerb)
		add(r.PreFixRightVerb)
		for reader := range r.PreFixReaderVerdicts {
			add(reader)
		}
	}
	return out
}
