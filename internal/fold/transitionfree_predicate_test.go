package fold

import "testing"

// TestTransitionFreeAgreesWithTheTable is the derivation stated as an
// invariant: a transition that carries a ROW moves state by definition, so
// it can never be transition-free. This is what stops the predicate from
// drifting away from the table it is supposed to describe.
func TestTransitionFreeAgreesWithTheTable(t *testing.T) {
	t.Parallel()

	for _, r := range rows {
		if TransitionFree(r.Kind, r.Transition) {
			t.Errorf("TransitionFree(%s, %s) is true, but the table carries a row %s/%s/%s that moves state to %q", r.Kind, r.Transition, r.Kind, r.From, r.Transition, r.To)
		}
	}
}

// TestTransitionFreeKindIsLoadBearing is the case that decided the
// signature. Spec 00 originally proposed TransitionFree(t) bool; a review
// break established that `acknowledge` moves a requirement's state and
// moves nothing on a broadcast, so a name-only predicate is wrong for one
// of the two.
func TestTransitionFreeKindIsLoadBearing(t *testing.T) {
	t.Parallel()

	if !TransitionFree(KindAnnouncement, TAcknowledge) {
		t.Error("TransitionFree(announcement, acknowledge) must be true — D-025 exempts a broadcast ack from the table entirely; it accumulates into the recipient set and never changes State")
	}
	if TransitionFree(KindRequirement, TAcknowledge) {
		t.Error("TransitionFree(requirement, acknowledge) must be false — it moves published -> acknowledged, and calling it free would hide a real protocol move")
	}
}

// TestNoteIsTransitionFreeForEveryKind covers the any-kind half of the
// registry: D-025 makes `note` exempt everywhere, not on the kinds
// somebody happened to test.
func TestNoteIsTransitionFreeForEveryKind(t *testing.T) {
	t.Parallel()

	for _, k := range kinds {
		if !TransitionFree(k, TNote) {
			t.Errorf("TransitionFree(%s, note) must be true — D-025 exempts `note` for every kind", k)
		}
	}
}

// TestTransitionFreeEventsNeverChangeState drives the real Apply path for
// every registered transition-free pair rather than reading the registry
// back. A predicate that agrees only with its own map would pass every
// structural check above and still be a lie about what Apply does.
func TestTransitionFreeEventsNeverChangeState(t *testing.T) {
	t.Parallel()

	for key := range transitionFreeHandlers {
		for _, k := range kinds {
			if key.Kind != "" && key.Kind != k {
				continue
			}
			if !TransitionFree(k, key.Transition) {
				continue
			}
			t.Run(string(k)+"/"+key.Transition, func(t *testing.T) {
				t.Parallel()
				env := rowEnv(k)
				prior := Result{Kind: k, State: postSubmissionState(k)}
				ev := Event{ULID: "01FREE000000000000000001", CommitSeq: 1, Subject: env.ID, Transition: key.Transition, Actor: Actor{System: env.From}}

				got := Apply(k, env, prior, ev, alwaysMember)

				if got.State != prior.State {
					t.Fatalf("Apply(%s, %s) moved state %q -> %q, but TransitionFree says it changes nothing", k, key.Transition, prior.State, got.State)
				}
			})
		}
	}
}

// TestTransitionFreeUnknownTransition guards the fallback: an unmodelled
// name is not free, it is unmodelled. Answering true here would exempt
// every typo from the illegal-transition flag.
func TestTransitionFreeUnknownTransition(t *testing.T) {
	t.Parallel()

	if TransitionFree(KindQuestion, "no_such_transition") {
		t.Error("TransitionFree must not call an unmodelled transition free — the generic path flags it illegal, and that is the point")
	}
}
