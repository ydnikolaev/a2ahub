package fold

import "testing"

// TestStateClaimMismatch is 3.5 rule 5 (§T1.2 "Flag-set reconciliation":
// state-claim-mismatch is fold-only): the event's resulting-state claim
// is informational; the fold's computed state always wins, and a
// mismatch is flagged, never honored, never fatal. Not one of the nine
// numbered ACs, but a first-class member of the flag set this package
// exports and easy to leave silently untested (no dedicated AC row names
// it).
func TestStateClaimMismatch(t *testing.T) {
	t.Parallel()

	t.Run("mismatch_is_flagged_but_computed_state_wins", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateSubmitted}
		event := Event{
			ULID: "01CLAIM00000000000000001", CommitSeq: 1, Subject: env.ID,
			Transition: TAcknowledge, Actor: Actor{System: env.To0()},
			ClaimedState: StateInProgress, // wrong: the transition table says acknowledged
		}

		got := Apply(KindQuestion, env, prior, event, alwaysMember)

		if got.State != StateAcknowledged {
			t.Fatalf("computed state must win over the claim: %q, want acknowledged", got.State)
		}
		assertFlag(t, got, FlagStateClaimMismatch, event.ULID)
	})

	t.Run("matching_claim_is_not_flagged", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateSubmitted}
		event := Event{
			ULID: "01CLAIM00000000000000002", CommitSeq: 1, Subject: env.ID,
			Transition: TAcknowledge, Actor: Actor{System: env.To0()},
			ClaimedState: StateAcknowledged,
		}

		got := Apply(KindQuestion, env, prior, event, alwaysMember)

		if len(got.Flags) != 0 {
			t.Fatalf("a correct claim must not be flagged: %+v", got.Flags)
		}
	})

	t.Run("absent_claim_is_not_flagged", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateSubmitted}
		event := Event{ULID: "01CLAIM00000000000000003", CommitSeq: 1, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}}

		got := Apply(KindQuestion, env, prior, event, alwaysMember)

		if len(got.Flags) != 0 {
			t.Fatalf("an omitted (optional) claim must not be flagged: %+v", got.Flags)
		}
	})

	t.Run("mismatch_never_fatal_never_crashes", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateSubmitted}
		event := Event{
			ULID: "01CLAIM00000000000000004", CommitSeq: 1, Subject: env.ID,
			Transition: TAcknowledge, Actor: Actor{System: env.To0()},
			ClaimedState: StateClosed,
		}
		mustNotPanic(t, func() Result { return Apply(KindQuestion, env, prior, event, alwaysMember) })
	})
}

// TestFoldHappyPathNoSpuriousFlags folds a realistic, fully legal
// event stream (starting with the real committed entry event — submit,
// publish or propose — as it would land in an actual space, never a
// hand-set starting state) for every kind that has one, and asserts zero
// flags. This is the regression net for a bug class where the
// per-event starting state and the entry event's expected fromState
// silently disagree (caught during pre-report review: Fold's starting
// state must be `draft`, not each kind's post-submission state, or the
// very first real committed event mis-flags as illegal-transition).
func TestFoldHappyPathNoSpuriousFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		kind      Kind
		env       Envelope
		events    func(env Envelope) []Event
		wantState State
	}{
		{
			name: "contract_first_publish_then_new_version",
			kind: KindContract,
			env:  Envelope{ID: "XC-acme-fixture", Kind: KindContract, From: "acme"},
			events: func(env Envelope) []Event {
				return []Event{
					{ULID: "01HAPPY0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Actor: Actor{System: env.From}},
					{ULID: "01HAPPY0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TPublish, Actor: Actor{System: env.From}},
				}
			},
			wantState: StatePublished,
		},
		{
			name: "requirement_publish_acknowledge_satisfy",
			kind: KindRequirement,
			env:  rowEnv(KindRequirement),
			events: func(env Envelope) []Event {
				return []Event{
					{ULID: "01HAPPY0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Actor: Actor{System: env.From}},
					{ULID: "01HAPPY0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}},
					{ULID: "01HAPPY0000000000000003", CommitSeq: 3, Subject: env.ID, Transition: TSatisfy, Actor: Actor{System: env.From}},
				}
			},
			wantState: StateSatisfied,
		},
		{
			name: "question_full_lifecycle_to_in_progress",
			kind: KindQuestion,
			env:  rowEnv(KindQuestion),
			events: func(env Envelope) []Event {
				return []Event{
					{ULID: "01HAPPY0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TSubmit, Actor: Actor{System: env.From}},
					{ULID: "01HAPPY0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}},
					{ULID: "01HAPPY0000000000000003", CommitSeq: 3, Subject: env.ID, Transition: TAccept, Actor: Actor{System: env.To0()}},
					{ULID: "01HAPPY0000000000000004", CommitSeq: 4, Subject: env.ID, Transition: TStart, Actor: Actor{System: env.To0()}},
				}
			},
			wantState: StateInProgress,
		},
		{
			name: "work_request_full_lifecycle_to_in_progress",
			kind: KindWorkRequest,
			env:  rowEnv(KindWorkRequest),
			events: func(env Envelope) []Event {
				return []Event{
					{ULID: "01HAPPY0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TSubmit, Actor: Actor{System: env.From}},
					{ULID: "01HAPPY0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}},
					{ULID: "01HAPPY0000000000000003", CommitSeq: 3, Subject: env.ID, Transition: TAccept, Actor: Actor{System: env.To0()}},
				}
			},
			wantState: StateAccepted,
		},
		{
			name: "handoff_submit_acknowledge_verify_pass",
			kind: KindHandoff,
			env:  rowEnv(KindHandoff),
			events: func(env Envelope) []Event {
				return []Event{
					{ULID: "01HAPPY0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TSubmit, Actor: Actor{System: env.From}},
					{ULID: "01HAPPY0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}},
					{ULID: "01HAPPY0000000000000003", CommitSeq: 3, Subject: env.ID, Transition: TVerifyPass, Actor: Actor{System: env.To0()}},
				}
			},
			wantState: StateAccepted,
		},
		{
			name: "announcement_publish_then_supersede",
			kind: KindAnnouncement,
			env:  Envelope{ID: "XA-acme-fixture", Kind: KindAnnouncement, From: "acme"},
			events: func(env Envelope) []Event {
				return []Event{
					{ULID: "01HAPPY0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Actor: Actor{System: env.From}},
					{ULID: "01HAPPY0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TSupersede, Actor: Actor{System: env.From}},
				}
			},
			wantState: StateSuperseded,
		},
		{
			name: "decision_propose_and_full_quorum_approval",
			kind: KindDecision,
			env:  rowEnv(KindDecision),
			events: func(env Envelope) []Event {
				return []Event{
					{ULID: "01HAPPY0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPropose, Actor: Actor{System: env.From}},
					{ULID: "01HAPPY0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TApprove, Actor: Actor{System: env.RequiredApprovers[0]}},
					{ULID: "01HAPPY0000000000000003", CommitSeq: 3, Subject: env.ID, Transition: TApprove, Actor: Actor{System: env.RequiredApprovers[1]}},
				}
			},
			wantState: StateApproved,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			events := tc.events(tc.env)
			got := Fold(tc.kind, tc.env, events, alwaysMember)
			if len(got.Flags) != 0 {
				t.Fatalf("unexpected flags on an all-legal happy path: %+v", got.Flags)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
		})
	}
}
