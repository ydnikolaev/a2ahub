package fold

import "testing"

// TestIllegalAndUnauthorized is spec 04 §8 AC 2: CC-020 (illegal
// transition), CC-021 (unauthorized actor), CC-022 (conflicting events,
// commit-order tiebreak) — every scenario asserts no panic/error return,
// flags present, the event retained (in Flags, referencing its ULID).
func TestIllegalAndUnauthorized(t *testing.T) {
	t.Parallel()

	t.Run("CC-020_respond_on_closed_exchange", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prior := Result{Kind: KindQuestion, State: StateClosed}
		event := Event{ULID: "01CC020RESPOND00000000001", CommitSeq: 1, Subject: env.ID, Transition: TRespond, Actor: Actor{System: "beta"}}

		got := mustNotPanic(t, func() Result { return Apply(KindQuestion, env, prior, event, alwaysMember) })

		if got.State != StateClosed {
			t.Fatalf("state changed on illegal transition: %q", got.State)
		}
		assertFlag(t, got, FlagIllegalTransition, event.ULID)
	})

	t.Run("CC-020_any_transition_from_terminal_state", func(t *testing.T) {
		t.Parallel()
		for _, terminal := range []State{StateRetired, StateSuperseded, StateWithdrawn, StateCancelled} {
			terminal := terminal
			t.Run(string(terminal), func(t *testing.T) {
				t.Parallel()
				env := rowEnv(KindQuestion)
				prior := Result{Kind: KindQuestion, State: terminal}
				event := Event{ULID: "01CC020TERMINAL0000000001", CommitSeq: 1, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: "beta"}}
				got := Apply(KindQuestion, env, prior, event, alwaysMember)
				if got.State != terminal {
					t.Fatalf("state changed from terminal %q: %q", terminal, got.State)
				}
				assertFlag(t, got, FlagIllegalTransition, event.ULID)
			})
		}
	})

	t.Run("CC-021_wrong_system_entirely", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindDecision)
		prior := Result{Kind: KindDecision, State: StateProposed}
		event := Event{ULID: "01CC021WRONGSYS000000001", CommitSeq: 1, Subject: env.ID, Transition: TApprove, Actor: Actor{System: "mallory"}}

		got := mustNotPanic(t, func() Result { return Apply(KindDecision, env, prior, event, alwaysMember) })

		if got.State != StateProposed {
			t.Fatalf("state changed on unauthorized actor: %q", got.State)
		}
		assertFlag(t, got, FlagUnauthorizedActor, event.ULID)
	})

	t.Run("CC-021_right_system_wrong_role", func(t *testing.T) {
		t.Parallel()
		// Non-required-approver "approves" a decision (right system class
		// — agent/system actor — wrong role: not in RequiredApprovers).
		env := rowEnv(KindDecision)
		prior := Result{Kind: KindDecision, State: StateProposed}
		event := Event{ULID: "01CC021WRONGROLE00000001", CommitSeq: 1, Subject: env.ID, Transition: TApprove, Actor: Actor{System: "acme"}} // acme is env.From, not a required approver

		got := Apply(KindDecision, env, prior, event, alwaysMember)

		if got.State != StateProposed {
			t.Fatalf("state changed on unauthorized actor: %q", got.State)
		}
		assertFlag(t, got, FlagUnauthorizedActor, event.ULID)
	})

	t.Run("CC-021_left_system_per_manifest_as_of_commit", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindRequirement)
		prior := Result{Kind: KindRequirement, State: StatePublished}
		event := Event{ULID: "01CC021LEFTSYS0000000001", CommitSeq: 1, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}}
		leftView := func(system string) MembershipStatus {
			if system == env.To0() {
				return MembershipLeft
			}
			return MembershipMember
		}

		got := Apply(KindRequirement, env, prior, event, leftView)

		if got.State != StatePublished {
			t.Fatalf("state changed for a left-system actor: %q", got.State)
		}
		assertFlag(t, got, FlagUnauthorizedActor, event.ULID)
	})

	t.Run("CC-022_same_commit_ulid_tiebreak", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		// decline (fromState accepted, among others) and start (fromState
		// accepted ONLY — a single-state row) are genuinely exclusive
		// once one applies from `accepted`: whichever loses the ULID
		// tiebreak finds a fromState ("declined") the `start` row no
		// longer matches.
		prefix := acceptedPrefix(env)
		decline := Event{ULID: "01CC022AAAAAAAAAAAAAAAAAA", CommitSeq: 5, Subject: env.ID, Transition: TDecline, Actor: Actor{System: "beta"}}
		start := Event{ULID: "01CC022BBBBBBBBBBBBBBBBBB", CommitSeq: 5, Subject: env.ID, Transition: TStart, Actor: Actor{System: "beta"}}

		incremental := Fold(KindQuestion, env, prefix, alwaysMember)
		incremental = Apply(KindQuestion, env, incremental, decline, alwaysMember)
		incremental = Apply(KindQuestion, env, incremental, start, alwaysMember)
		// Fed intentionally out of ULID order; Fold re-sorts by
		// (CommitSeq, ULID) regardless of arrival order.
		fromFold := Fold(KindQuestion, env, append(append([]Event{}, prefix...), start, decline), alwaysMember)

		if incremental.State != fromFold.State {
			t.Fatalf("incremental (%q) and full fold (%q) diverged", incremental.State, fromFold.State)
		}
		if fromFold.State != StateDeclined {
			t.Fatalf("winner should be decline (lower ULID): state = %q, want declined", fromFold.State)
		}
		assertFlag(t, fromFold, FlagIllegalTransition, start.ULID)
	})

	t.Run("CC-022_cross_commit_ordering", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		prefix := acceptedPrefix(env)
		// decline lands in an EARLIER commit than start, despite a
		// lexically LARGER ULID within its own commit — commit order
		// wins over ULID across commits (D-017).
		decline := Event{ULID: "01CC022ZZZZZZZZZZZZZZZZZZ", CommitSeq: 4, Subject: env.ID, Transition: TDecline, Actor: Actor{System: "beta"}}
		start := Event{ULID: "01CC022AAAAAAAAAAAAAAAAAA", CommitSeq: 5, Subject: env.ID, Transition: TStart, Actor: Actor{System: "beta"}}

		got := Fold(KindQuestion, env, append(append([]Event{}, prefix...), start, decline), alwaysMember)

		if got.State != StateDeclined {
			t.Fatalf("earlier-commit decline should win despite a lexically smaller ULID in the later commit: state = %q, want declined", got.State)
		}
		assertFlag(t, got, FlagIllegalTransition, start.ULID)
	})
}

// acceptedPrefix is a minimal legal event prefix (submit, acknowledge,
// accept) landing a question/work_request at `accepted` via a real Fold
// call — Fold's per-event starting state is `draft` (NewResult), so
// CC-022 fixtures that need to start from `accepted` build it via actual
// events rather than hand-setting Result.State.
func acceptedPrefix(env Envelope) []Event {
	return []Event{
		{ULID: "01CC022PREFIX000000000001", CommitSeq: 1, Subject: env.ID, Transition: TSubmit, Actor: Actor{System: env.From}},
		{ULID: "01CC022PREFIX000000000002", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}},
		{ULID: "01CC022PREFIX000000000003", CommitSeq: 3, Subject: env.ID, Transition: TAccept, Actor: Actor{System: env.To0()}},
	}
}

func mustNotPanic(t *testing.T, fn func() Result) Result {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("fold must never panic on flagged input: %v", r)
		}
	}()
	return fn()
}

func assertFlag(t *testing.T, result Result, kind FlagKind, eventULID string) {
	t.Helper()
	for _, f := range result.Flags {
		if f.Kind == kind && f.EventULID == eventULID {
			return
		}
	}
	t.Fatalf("expected flag %s for event %s, got %+v", kind, eventULID, result.Flags)
}
