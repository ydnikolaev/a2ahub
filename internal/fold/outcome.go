package fold

// Outcome is what a state MEANS, once an artifact is resting in it.
// Distinct from State: many states map to one outcome, and the mapping is
// the domain's to make, not a renderer's.
//
// Before this existed, the dashboard component decided it from three
// kind-agnostic literal sets (SUCCESS_STATES / CANCELLED_STATES /
// REFUSED_STATES) and got two things wrong that only a Kind can settle:
// `retired` was filed as cancelled, when retiring a contract is how a
// contract's life is SUPPOSED to end; and handoff `accepted` — the state a
// passing verification produces, the end of that exchange — was treated as
// the same fact as question `accepted`, which is the middle of one.
type Outcome string

// The five outcomes. They are deliberately fewer than the states: a
// reader asks "how did this end", and twenty-one state names are not an
// answer to that question.
const (
	OutcomeOpen       Outcome = "open"       // the exchange is still live
	OutcomeSettled    Outcome = "settled"    // it concluded, and concluded well
	OutcomeRefused    Outcome = "refused"    // it concluded, against the asker
	OutcomeWithdrawn  Outcome = "withdrawn"  // the asker took it back
	OutcomeSuperseded Outcome = "superseded" // another artifact replaced it
)

// outcomes is the meaning of every (Kind, State) pair a subject can be
// found resting in. It is written out per kind rather than as one state
// map with per-kind exceptions ON PURPOSE: an exception table makes the
// kind-dependent pairs the quiet case, and the kind-dependent pairs are
// exactly the ones four renderers each got wrong.
//
// Its key set must equal RestingStates() EXACTLY — both directions,
// asserted in outcome_test.go. A missing pair is a state whose meaning
// nobody decided; a surplus pair is a claim about a state no subject can
// hold, which reads as coverage and is not.
//
// This map is NOT internal/cache's openStates and must not be conflated
// with it. openStates answers "does somebody owe a move from here", which
// is a different question with a different answer: handoff `rejected` is
// OPEN there (§3.4.5 puts the producer on the hook to resubmit) and
// REFUSED here (the verification did not pass). Both are true. Terminal is
// a third question again — see its own doc comment.
var outcomes = map[Kind]map[State]Outcome{
	KindContract: {
		StatePublished:  OutcomeOpen,
		StateDeprecated: OutcomeOpen, // still retirable, and still readable
		// A retired contract completed its life. The dashboard filed this
		// under CANCELLED, which told a reader an orderly retirement and
		// an abandoned artifact were the same event.
		StateRetired: OutcomeSettled,
	},
	KindRequirement: {
		StatePublished:    OutcomeOpen,
		StateAcknowledged: OutcomeOpen,
		StateSatisfied:    OutcomeSettled,
		StateDeclined:     OutcomeRefused,
		StateWithdrawn:    OutcomeWithdrawn,
		StateSuperseded:   OutcomeSuperseded,
	},
	KindQuestion:    exchangeOutcomes(),
	KindWorkRequest: exchangeOutcomes(),
	KindDecision: {
		// A decision with zero committed events folds to draft
		// (postSubmissionState), so draft IS observable here and nowhere
		// else — RestingStates' own load-bearing case.
		StateDraft:      OutcomeOpen,
		StateProposed:   OutcomeOpen,
		StateApproved:   OutcomeSettled,
		StateRejected:   OutcomeRefused,
		StateSuperseded: OutcomeSuperseded,
		// P1 gave the proposer a withdraw exit, so a decision can now rest
		// here. Same meaning as everywhere else: the asker took it back.
		StateWithdrawn: OutcomeWithdrawn,
	},
	KindHandoff: {
		StateSubmitted:    OutcomeOpen,
		StateAcknowledged: OutcomeOpen,
		// `accepted` is where a handoff ENDS: it is what verify-pass
		// produces, and no row leaves it. On a question or work_request
		// the same word means the target took the work on and has not
		// done it yet. One word, two meanings — which is why Outcome
		// takes a Kind and the retired literal sets could not have been
		// right for both.
		StateAccepted: OutcomeSettled,
		// Refused by the verification, and still non-terminal: the
		// producer may supersede it with a corrected handoff, and
		// internal/cache's openStates lists it as live for exactly that
		// reason.
		StateRejected:   OutcomeRefused,
		StateSuperseded: OutcomeSuperseded,
	},
	KindResponse: {
		StateSubmitted: OutcomeOpen,
		StateVerified:  OutcomeSettled,
		// The asker rejected the answer. D-024 additionally reopens the
		// parent, so the exchange continues — but THIS response
		// concluded against the party that gave it.
		//
		// 2026-08-08 gave the producer a `supersede` exit from here,
		// matching decision `rejected` and handoff `rejected`. 2026-08-09
		// deleted that row (spec 06's amendment, epic-backlog B8): no
		// shipped verb ever reached it, because a response's closure state
		// is sub-state on the PARENT's Result.Responses and only
		// applyResponseScoped's verify/dispute branch ever reads it. So
		// `disputed` is refused AND, once again, terminal — the row that
		// used to produce StateSuperseded for this kind is gone, and with
		// it this kind's only member of that outcome.
		StateDisputed: OutcomeRefused,
	},
	KindAnnouncement: {
		StatePublished:  OutcomeOpen,
		StateSuperseded: OutcomeSuperseded,
	},
}

// exchangeOutcomes is question and work_request's shared lifecycle, built
// per call so the two kinds cannot share a map a caller could mutate —
// the same discipline the row builders already use.
func exchangeOutcomes() map[State]Outcome {
	return map[State]Outcome{
		StateSubmitted:    OutcomeOpen,
		StateAcknowledged: OutcomeOpen,
		StateAccepted:     OutcomeOpen,
		StateInProgress:   OutcomeOpen,
		StateBlocked:      OutcomeOpen, // waiting on somebody, not concluded
		StateResponded:    OutcomeOpen, // answered, not yet closed or disputed
		StateClosed:       OutcomeSettled,
		StateDeclined:     OutcomeRefused,
		StateCancelled:    OutcomeWithdrawn,
		StateSuperseded:   OutcomeSuperseded,
	}
}

// OutcomeOf returns the meaning of (kind, state). It is TOTAL over
// RestingStates(): every pair that enumerator yields returns a non-empty
// Outcome, asserted by a test that iterates the enumerator rather than a
// literal list.
//
// seam.md §1 freezes this as `func Outcome(kind Kind, state State) Outcome`
// alongside `type Outcome string`. Go permits exactly one of those two
// names in a package block, so the frozen pair does not compile. The type
// keeps the name — it is what the read model's field and all five
// constants are spelled in terms of — and the accessor takes the ordinary
// Go suffix instead. Recorded as an amendment on the seam rather than
// silently substituted.
//
// A pair outside RestingStates() returns "" — the caller is asking about a
// state no artifact of that kind can rest in, and a zero value is the
// honest answer, never OutcomeOpen. Defaulting an unknown pair to `open`
// is how a renderer ends up promising a reader that a state it does not
// understand is still live.
func OutcomeOf(kind Kind, state State) Outcome {
	return outcomes[kind][state]
}

// Terminal reports whether an artifact of this kind, resting in this
// state, has no legal transition out of it for ANY role. It is derived
// from the transition rows, never from a literal set of state names, so a
// row added tomorrow changes the answer the day it lands.
//
// Terminal is NOT "Outcome != OutcomeOpen", and the gap is not an edge
// case: a rejected decision can still be superseded, so (decision,
// rejected) is refused AND non-terminal; so is (handoff, rejected), and
// every requirement state supersede can leave. Three questions, three
// answers — what a state means, whether anything can follow it, and
// whether somebody owes a move (internal/cache's openStates).
func Terminal(kind Kind, state State) bool {
	for _, r := range rows {
		if r.Kind == kind && r.From == state {
			return false
		}
	}
	return true
}
