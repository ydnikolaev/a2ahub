package cache

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

func provenanceEnv() fold.Envelope {
	return fold.Envelope{ID: "XQ-provenance", Kind: fold.KindQuestion, From: "axon", To: []string{"beta"}}
}

func alwaysMemberView(string) fold.MembershipStatus { return fold.MembershipMember }

// TestStateOriginNamesTheEventThatProducedTheState is spec 00's AC4 at the
// level the fact is computed: a folded artifact reports the event and the
// actor that put it in its current state, rather than a timestamp with no
// author.
func TestStateOriginNamesTheEventThatProducedTheState(t *testing.T) {
	t.Parallel()

	env := provenanceEnv()
	events := []fold.Event{
		{ULID: "01SUBMIT", CommitSeq: 1, Subject: env.ID, Transition: fold.TSubmit, Actor: fold.Actor{System: "axon"}},
		{ULID: "02ACK", CommitSeq: 2, Subject: env.ID, Transition: fold.TAcknowledge, Actor: fold.Actor{System: "beta"}},
	}

	result, _, origin := foldWithReceiptEvidence(fold.KindQuestion, env, events, alwaysMemberView, nil)

	if result.State != fold.StateAcknowledged {
		t.Fatalf("state = %q, want acknowledged", result.State)
	}
	if origin.EventULID != "02ACK" {
		t.Errorf("origin event = %q, want the acknowledge that produced the state", origin.EventULID)
	}
	if origin.By != "beta" {
		t.Errorf("origin actor = %q, want the system whose event moved it", origin.By)
	}
}

// TestNoteMovesTheActivityClockAndNotTheStateClock is the defect this whole
// distinction exists for. The dashboard rendered the newest event of ANY kind
// under a "moved" label, so a `note` made an artifact look like it had moved
// when nothing had.
//
// D-025 makes `note` transition-free, and fold.TransitionFree now says so
// out loud — but this asserts the CONSEQUENCE rather than the predicate:
// after a note, the state origin still names the acknowledge.
func TestNoteMovesTheActivityClockAndNotTheStateClock(t *testing.T) {
	t.Parallel()

	env := provenanceEnv()
	events := []fold.Event{
		{ULID: "01SUBMIT", CommitSeq: 1, Subject: env.ID, Transition: fold.TSubmit, Actor: fold.Actor{System: "axon"}},
		{ULID: "02ACK", CommitSeq: 2, Subject: env.ID, Transition: fold.TAcknowledge, Actor: fold.Actor{System: "beta"}},
		{ULID: "03NOTE", CommitSeq: 3, Subject: env.ID, Transition: fold.TNote, Actor: fold.Actor{System: "axon"}},
	}

	result, _, origin := foldWithReceiptEvidence(fold.KindQuestion, env, events, alwaysMemberView, nil)

	if result.State != fold.StateAcknowledged {
		t.Fatalf("a note must not change state: got %q", result.State)
	}
	if origin.EventULID != "02ACK" {
		t.Errorf("origin event = %q — the note is the newest event but produced no state, so it must not claim authorship", origin.EventULID)
	}
	if origin.By != "beta" {
		t.Errorf("origin actor = %q, want beta — the note's author did not move anything", origin.By)
	}
}

// TestIllegalEventClaimsNoAuthorship guards the reason the origin is observed
// (state before vs state after) rather than derived from the transition name.
// An `accept` on a question that was never acknowledged is flagged and moves
// nothing; asking fold.TransitionFree would call it state-moving and let it
// claim a state it never produced.
func TestIllegalEventClaimsNoAuthorship(t *testing.T) {
	t.Parallel()

	env := provenanceEnv()
	events := []fold.Event{
		{ULID: "01SUBMIT", CommitSeq: 1, Subject: env.ID, Transition: fold.TSubmit, Actor: fold.Actor{System: "axon"}},
		{ULID: "02ACCEPT", CommitSeq: 2, Subject: env.ID, Transition: fold.TAccept, Actor: fold.Actor{System: "beta"}},
	}

	result, _, origin := foldWithReceiptEvidence(fold.KindQuestion, env, events, alwaysMemberView, nil)

	if result.State != fold.StateSubmitted {
		t.Fatalf("accept from submitted is illegal and must not move state: got %q", result.State)
	}
	if origin.EventULID != "01SUBMIT" {
		t.Errorf("origin event = %q — the flagged accept changed nothing and must not be credited with the state", origin.EventULID)
	}
}

// TestZeroEventsHasNoStateOrigin pins the honest answer for the zero-events
// fallback: postSubmissionState is a state NOTHING produced, so there is no
// event, no actor and no instant to name. Inventing one from the artifact's
// own file would put an author on a state no author chose.
func TestZeroEventsHasNoStateOrigin(t *testing.T) {
	t.Parallel()

	env := provenanceEnv()
	result, _, origin := foldWithReceiptEvidence(fold.KindQuestion, env, nil, alwaysMemberView, nil)

	if result.State != fold.StateSubmitted {
		t.Fatalf("zero-events fallback for a question is submitted, got %q", result.State)
	}
	if origin.EventULID != "" || origin.By != "" {
		t.Errorf("origin = %+v, want empty — no event produced this state", origin)
	}
}

// TestOutcomeAndTerminalReachTheItem closes the loop the projection gate
// cannot: reflection proves the FIELD exists, never that toItem fills it.
func TestOutcomeAndTerminalReachTheItem(t *testing.T) {
	t.Parallel()

	fa := foldedArtifact{
		SpaceID: "axon",
		Env:     envelopeProbe{ID: "XQ-1", Type: string(fold.KindQuestion), From: "axon"},
		Result:  fold.Result{Kind: fold.KindQuestion, State: fold.StateClosed},
		StateBy: "beta", StateEventID: "02ACK",
	}

	item := toItem(fa, false, false)

	if item.Outcome != fold.OutcomeSettled {
		t.Errorf("Item.Outcome = %q, want settled — a closed exchange concluded well", item.Outcome)
	}
	if !item.Terminal {
		t.Error("Item.Terminal = false — no row leaves a closed exchange")
	}
	if item.StateBy != "beta" || item.StateEvent != "02ACK" {
		t.Errorf("Item state provenance = (%q, %q), want (beta, 02ACK)", item.StateBy, item.StateEvent)
	}
}
