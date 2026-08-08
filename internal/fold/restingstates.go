package fold

import "sort"

// KindRestingState pairs a Kind with a state a subject of that kind may
// actually be OBSERVED at rest — RestingStates' sole element type. Distinct
// from KindState (subjectstates.go), which pairs a Kind with a fromState a
// transition ROW departs from; this type is the complementary "where a
// subject can be found sitting still" question.
//
// Landed ahead of its first caller (same precedent pathgrammar.go's own
// grammar-before-driver split already sets — plan W3, "declaring the
// intended coverage in plain code means it is visible, diffable and
// unit-testable BEFORE any driver exists"). Spec §18a's own text names two
// intended consumers: (1) "the path catalogue... demands predicates only
// where a state can be observed at rest" — a grammar-level gate over
// pathcatalogue_paths.go's own FoldedState predicates that does not exist
// yet, and is NOT this wave's to build (W3d is serialized on that same
// file with W3e); (2) the coverage split's own credit rule — evaluated and
// deliberately NOT used that way (internal/livee2e's pathcoverage_test.go,
// assertedTriple's own doc comment).
//
// That doc comment carried TWO cases where RestingStates() membership gave
// the wrong answer. The second — decision's quorum-reached approve — was a
// defect in this enumerator and agent-exchange-2026-08 P0 fixed it here:
// dynamic rows now declare their Outcomes and RestingStates unions them.
// The first — decision's create step — is about what a CLI verb can
// commit, not about what Fold can compute, and stands.
type KindRestingState struct {
	Kind  Kind
	State State
}

// kinds is every Kind this package folds (types.go's own §3.1 enumeration),
// listed here because RestingStates needs to walk all eight to apply the
// zero-events fallback per kind — there is no other exported enumerator of
// the Kind set in this package, and inventing one for a single caller would
// be the same over-generalization SubjectStates/TransitionRows already
// avoid by staying narrow.
var kinds = []Kind{
	KindContract,
	KindRequirement,
	KindQuestion,
	KindWorkRequest,
	KindDecision,
	KindHandoff,
	KindResponse,
	KindAnnouncement,
}

// RestingStates returns every (Kind, State) pair a subject of that kind may
// actually be found in when nothing is mid-transition — the union of two
// sources, both real:
//
//  1. Every distinct To across this package's transition rows (a state some
//     transition lands a subject in), PLUS every state a dynamic row
//     declares in its own Outcomes. StateDynamic itself never appears — it
//     is a table-row sentinel resolved at apply time, never a state a
//     subject is literally found holding (see StateDynamic's own doc
//     comment) — but the states it resolves TO are ordinary resting states
//     and belong here.
//
//     This half was wrong until P0. Skipping the sentinel outright dropped
//     {decision approved} from the universe entirely: both approve rows
//     carry StateDynamic and no decision row carries StateApproved as a
//     literal To, so a quorum-reached approve landed a subject in a state
//     this enumerator said could not exist. internal/livee2e's
//     pathcoverage_test.go had flagged exactly that, as a false negative it
//     could not fix from the outside.
//
//  2. postSubmissionState(kind) for every kind — 03-domain.md §3.4's
//     explicit zero-events fallback (fold.go), the state an artifact with
//     no committed events at all folds to. This half is load-bearing and
//     NOT derivable from (1) alone: postSubmissionState(KindDecision) is
//     StateDraft, so a decision at rest CAN be `draft` (its own first
//     committed event is `propose`, decisionRows() carries no row landing
//     ON draft) — while `draft` is a To for no other kind's rows either, so
//     a hand-written list built only from "every row's To" would have
//     called EVERY kind's draft unobservable and gotten decision wrong.
//
// This is spec §18a's own "third narrow enumerator" (agent-ops-2026-07
// P11 W3d) — same discipline as SubjectStates and TransitionRows: a fresh,
// deduplicated, sorted (Kind, then State) slice on every call, no exported
// accessor over the raw rows a caller could re-derive the wrong answer
// from instead.
func RestingStates() []KindRestingState {
	seen := make(map[KindRestingState]bool)
	for _, r := range rows {
		// A dynamic row contributes its declared outcomes and never its
		// sentinel To; an ordinary row contributes its To. No branch on a
		// transition name, and no state a row can produce is lost.
		for _, s := range r.Outcomes {
			seen[KindRestingState{Kind: r.Kind, State: s}] = true
		}
		if r.To == StateDynamic {
			continue
		}
		seen[KindRestingState{Kind: r.Kind, State: r.To}] = true
	}
	for _, k := range kinds {
		seen[KindRestingState{Kind: k, State: postSubmissionState(k)}] = true
	}

	out := make([]KindRestingState, 0, len(seen))
	for krs := range seen {
		out = append(out, krs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].State < out[j].State
	})
	return out
}
