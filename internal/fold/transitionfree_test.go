package fold

import "testing"

// TestTransitionFreeKinds is spec 04 §8 AC 7 (D-025): `note` and
// broadcast-`acknowledge` never appear in the illegal-transition flag
// stream regardless of current state, and never change `state`; ack
// accumulates per-recipient (deduplicated), not globally.
func TestTransitionFreeKinds(t *testing.T) {
	t.Parallel()

	t.Run("note_on_a_closed_exchange_is_still_legal", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateClosed}
		note := Event{ULID: "01NOTE00000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TNote, Actor: Actor{System: env.From}}

		got := Apply(KindQuestion, env, prior, note, alwaysMember)

		if got.State != StateClosed {
			t.Fatalf("note must never change state: %q", got.State)
		}
		for _, f := range got.Flags {
			if f.Kind == FlagIllegalTransition {
				t.Fatalf("note must never be flagged illegal, regardless of state: %+v", got.Flags)
			}
		}
	})

	t.Run("note_authorized_for_either_party", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateInProgress}
		for _, actor := range []string{env.From, env.To0()} {
			actor := actor
			t.Run(actor, func(t *testing.T) {
				t.Parallel()
				note := Event{ULID: "01NOTE00000000000000002", CommitSeq: 1, Subject: env.ID, Transition: TNote, Actor: Actor{System: actor}}
				got := Apply(KindQuestion, env, prior, note, alwaysMember)
				if len(got.Flags) != 0 {
					t.Fatalf("either party's note should be unflagged: %+v", got.Flags)
				}
			})
		}
	})

	t.Run("note_from_a_third_party_is_unauthorized_not_illegal", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateInProgress}
		note := Event{ULID: "01NOTE00000000000000003", CommitSeq: 1, Subject: env.ID, Transition: TNote, Actor: Actor{System: "outsider"}}
		got := Apply(KindQuestion, env, prior, note, alwaysMember)
		if got.State != StateInProgress {
			t.Fatalf("note must never change state: %q", got.State)
		}
		assertFlag(t, got, FlagUnauthorizedActor, note.ULID)
		for _, f := range got.Flags {
			if f.Kind == FlagIllegalTransition {
				t.Fatalf("note must never be flagged illegal: %+v", got.Flags)
			}
		}
	})

	t.Run("broadcast_ack_never_illegal_regardless_of_state_never_changes_state", func(t *testing.T) {
		t.Parallel()
		env := Envelope{ID: "XA-acme-fixture", Kind: KindAnnouncement, From: "acme"}
		for _, from := range []State{StateDraft, StatePublished, StateSuperseded} {
			from := from
			t.Run(string(from), func(t *testing.T) {
				t.Parallel()
				prior := Result{Kind: KindAnnouncement, State: from}
				ack := Event{ULID: "01ACK000000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: "consumer-a"}}
				got := Apply(KindAnnouncement, env, prior, ack, alwaysMember)
				if got.State != from {
					t.Fatalf("broadcast ack must never change state: %q, want %q", got.State, from)
				}
				for _, f := range got.Flags {
					if f.Kind == FlagIllegalTransition {
						t.Fatalf("broadcast ack must never be flagged illegal: %+v", got.Flags)
					}
				}
				if !got.Acks["consumer-a"] {
					t.Fatalf("ack should be recorded for consumer-a: %+v", got.Acks)
				}
			})
		}
	})

	t.Run("ack_accumulates_per_recipient_not_globally", func(t *testing.T) {
		t.Parallel()
		env := Envelope{ID: "XA-acme-fixture", Kind: KindAnnouncement, From: "acme"}
		prior := Result{Kind: KindAnnouncement, State: StatePublished}
		ackA := Event{ULID: "01ACK000000000000000002", CommitSeq: 1, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: "consumer-a"}}
		ackB := Event{ULID: "01ACK000000000000000003", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: "consumer-b"}}

		got := Apply(KindAnnouncement, env, prior, ackA, alwaysMember)
		got = Apply(KindAnnouncement, env, got, ackB, alwaysMember)

		if !got.Acks["consumer-a"] || !got.Acks["consumer-b"] {
			t.Fatalf("both recipients should be tracked independently: %+v", got.Acks)
		}
		if len(got.Acks) != 2 {
			t.Fatalf("ack set should have exactly 2 recipients, got %d: %+v", len(got.Acks), got.Acks)
		}
	})

	t.Run("duplicate_ack_from_same_recipient_is_a_no_op", func(t *testing.T) {
		t.Parallel()
		env := Envelope{ID: "XA-acme-fixture", Kind: KindAnnouncement, From: "acme"}
		prior := Result{Kind: KindAnnouncement, State: StatePublished}
		ack1 := Event{ULID: "01ACK000000000000000004", CommitSeq: 1, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: "consumer-a"}}
		ack2 := Event{ULID: "01ACK000000000000000005", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: "consumer-a"}}

		got := Apply(KindAnnouncement, env, prior, ack1, alwaysMember)
		got = Apply(KindAnnouncement, env, got, ack2, alwaysMember)

		if len(got.Acks) != 1 {
			t.Fatalf("duplicate ack from the same recipient must not grow the set: %+v", got.Acks)
		}
	})
}
