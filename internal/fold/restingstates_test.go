package fold

import "testing"

// TestRestingStatesUniverse pins the universe size the internal/livee2e
// coverage split (W3d, exercised-vs-asserted) is checked against — 45
// distinct (Kind, State) pairs today: every ordinary row's To, unioned with
// every dynamic row's declared Outcomes, unioned with
// postSubmissionState(kind) for all eight kinds. A count drift here is a
// real signal (a table row's To changed, an Outcomes declaration changed,
// or a kind's zero-events fallback changed), not test noise.
//
// It was 43 until P0 and 44 until P1, and both increments are worth knowing.
//
// The 44th is {decision approved}: both approve rows carry the StateDynamic
// sentinel and no decision row carries StateApproved as a literal To, so the
// old "every To, sentinel skipped" derivation said a state a quorum-reached
// approve demonstrably produces could not be observed at rest.
//
// The 45th is {decision withdrawn}: P1 gave a proposer the exits their own
// decision lacked, and a new To is a new resting state. That this test reds
// when a row is added is the point — the universe is not something a phase
// gets to change quietly, and P1 changing it is what forced P0's outcome map
// to gain a meaning for the new state in the same commit.
//
// It briefly rose to 46 on 2026-08-08 with {response superseded}: a row
// meant to close the disputed sink (a disputed response was refused AND
// TERMINAL while its two siblings — decision/rejected and handoff/rejected —
// are refused and non-terminal). P8's tagged conformance matrix then proved
// no shipped verb ever reached that row — a response's closure state is
// sub-state on the PARENT's Result.Responses, and the only reader of that
// sub-state never dispatches a bare `supersede` to it. 2026-08-09 deleted
// the row (spec 06's amendment, epic-backlog B8) rather than widening the
// routing to reach it, because the exit it existed to provide already
// exists one level up: a dispute reopens the PARENT to in_progress, where
// the responder owes `respond`. Deleting the row's only producer of
// {response superseded} drops the universe back to 45.
func TestRestingStatesUniverse(t *testing.T) {
	got := RestingStates()
	if len(got) != 45 {
		t.Fatalf("RestingStates(): want 45 pairs, got %d: %+v", len(got), got)
	}
}

// TestRestingStatesContainsDecisionApproved names the pair the enumerator
// used to lose, so a regression reads as what it is rather than as an
// off-by-one in the count above. P1's own AC1 is unsatisfiable without it,
// and internal/livee2e's pathcoverage_test.go flagged the gap from the
// outside without being able to fix it.
func TestRestingStatesContainsDecisionApproved(t *testing.T) {
	want := KindRestingState{Kind: KindDecision, State: StateApproved}
	for _, krs := range RestingStates() {
		if krs == want {
			return
		}
	}
	t.Fatalf("RestingStates() does not contain %+v — the quorum-reached approve row declares it in Outcomes and RestingStates must union it in", want)
}

// TestRestingStatesExcludesDynamic asserts the StateDynamic sentinel never
// appears — unblock's pre-block recovery and decision approve's quorum
// arithmetic are resolved at apply time, never a state a subject is
// literally found holding at rest.
func TestRestingStatesExcludesDynamic(t *testing.T) {
	for _, krs := range RestingStates() {
		if krs.State == StateDynamic {
			t.Fatalf("RestingStates() returned a StateDynamic pair: %+v", krs)
		}
	}
}

// TestRestingStatesDecisionDraftIsObservable is the derivation's own load-
// bearing case (RestingStates' doc comment, spec §18a): a decision at rest
// CAN be `draft` — postSubmissionState(KindDecision) is StateDraft, because
// a decision's own first committed event is `propose`, not a submit/
// publish entry transition. No OTHER kind's rows land a subject back on
// `draft` (draft is a `From` for every kind's first row, never a `To`), so
// a hand-written "every kind's draft is pre-commit and unobservable" list
// would have gotten exactly this one wrong.
func TestRestingStatesDecisionDraftIsObservable(t *testing.T) {
	want := KindRestingState{Kind: KindDecision, State: StateDraft}
	for _, krs := range RestingStates() {
		if krs == want {
			return
		}
	}
	t.Fatalf("RestingStates() does not contain %+v — decision's zero-events fallback (postSubmissionState) is StateDraft, and RestingStates must union it in even though no decision row lands ON draft", want)
}

// TestRestingStatesOtherKindsDraftIsNotObservable is the negative half of
// the decision case above: every OTHER kind's postSubmissionState is
// published or submitted (fold.go), never draft, and no row of theirs lands
// a subject on draft either — so draft must be genuinely absent from their
// resting set, not merely absent from the test's own assertions.
func TestRestingStatesOtherKindsDraftIsNotObservable(t *testing.T) {
	for _, k := range kinds {
		if k == KindDecision {
			continue
		}
		for _, krs := range RestingStates() {
			if krs.Kind == k && krs.State == StateDraft {
				t.Fatalf("RestingStates() contains {%s draft} — only decision's zero-events fallback should ever produce a draft resting state", k)
			}
		}
	}
}

// TestRestingStatesDeduplicatedAndSorted asserts no pair repeats and the
// result is sorted (Kind, then State) — same determinism contract as
// SubjectStates/TransitionRows.
func TestRestingStatesDeduplicatedAndSorted(t *testing.T) {
	got := RestingStates()
	seen := make(map[KindRestingState]bool, len(got))
	for i, krs := range got {
		if seen[krs] {
			t.Fatalf("RestingStates() returned a duplicate pair: %+v", krs)
		}
		seen[krs] = true
		if i > 0 {
			prev := got[i-1]
			if krs.Kind < prev.Kind || (krs.Kind == prev.Kind && krs.State < prev.State) {
				t.Fatalf("RestingStates() not sorted at index %d: %+v before %+v", i, prev, krs)
			}
		}
	}
}

// TestRestingStatesFreshSlice asserts a caller mutating one returned slice
// cannot corrupt what a later call returns.
func TestRestingStatesFreshSlice(t *testing.T) {
	first := RestingStates()
	if len(first) == 0 {
		t.Fatal("RestingStates() returned no pairs")
	}
	first[0] = KindRestingState{Kind: "corrupted", State: "corrupted"}

	second := RestingStates()
	for _, krs := range second {
		if krs.Kind == "corrupted" {
			t.Fatal("RestingStates() shares backing storage across calls")
		}
	}
}
