package fold

import "testing"

// TestClosureModel is spec 04 §8 AC 6 (D-024): multi-response
// independence, dispute reopens parent, close only from responded (and
// is a no-op-illegal from any other state) — the exact scenario named in
// spec §6: "2 responses, verify one, dispute the other, then close".
func TestClosureModel(t *testing.T) {
	t.Parallel()
	env := rowEnv(KindQuestion)
	requester, target := env.From, env.To0()

	respond1 := Event{ULID: "01CLOSURE000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TRespond, ResponseID: "XS-one", Actor: Actor{System: target}}
	respond2 := Event{ULID: "01CLOSURE000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TRespond, ResponseID: "XS-two", Actor: Actor{System: target}}

	prior := Result{Kind: KindQuestion, State: StateAccepted}
	got := Apply(KindQuestion, env, prior, respond1, alwaysMember)
	got = Apply(KindQuestion, env, got, respond2, alwaysMember)

	if got.State != StateResponded {
		t.Fatalf("after two responds: state = %q, want responded", got.State)
	}
	if got.Responses["XS-one"] != StateSubmitted || got.Responses["XS-two"] != StateSubmitted {
		t.Fatalf("both responses should independently open at submitted: %+v", got.Responses)
	}

	// Verify response one.
	verify := Event{ULID: "01CLOSURE000000000000003", CommitSeq: 3, Subject: "XS-one", Transition: TVerify, Actor: Actor{System: requester}}
	got = Apply(KindQuestion, env, got, verify, alwaysMember)
	if got.Responses["XS-one"] != StateVerified {
		t.Fatalf("XS-one should be verified: %q", got.Responses["XS-one"])
	}
	if got.Responses["XS-two"] != StateSubmitted {
		t.Fatalf("verifying XS-one must not affect XS-two (multi-response independence): %q", got.Responses["XS-two"])
	}
	if got.State != StateResponded {
		t.Fatalf("verify must not change the parent's own state: %q", got.State)
	}

	// Dispute response two — reopens the parent.
	dispute := Event{ULID: "01CLOSURE000000000000004", CommitSeq: 4, Subject: "XS-two", Transition: TDispute, Actor: Actor{System: requester}}
	got = Apply(KindQuestion, env, got, dispute, alwaysMember)
	if got.Responses["XS-two"] != StateDisputed {
		t.Fatalf("XS-two should be disputed: %q", got.Responses["XS-two"])
	}
	if got.Responses["XS-one"] != StateVerified {
		t.Fatalf("disputing XS-two must not affect XS-one (multi-response independence): %q", got.Responses["XS-one"])
	}
	if got.State != StateInProgress {
		t.Fatalf("dispute must reopen the parent responded->in_progress: %q", got.State)
	}
	if len(got.Flags) != 0 {
		t.Fatalf("unexpected flags before close: %+v", got.Flags)
	}

	// Close: illegal here, since the parent is in_progress, not
	// responded (the dispute reopened it) — "close is a no-op-illegal
	// from any other state".
	close1 := Event{ULID: "01CLOSURE000000000000005", CommitSeq: 5, Subject: env.ID, Transition: TClose, Actor: Actor{System: requester}}
	afterClose := Apply(KindQuestion, env, got, close1, alwaysMember)
	if afterClose.State != StateInProgress {
		t.Fatalf("close from in_progress must be a no-op: state = %q", afterClose.State)
	}
	assertFlag(t, afterClose, FlagIllegalTransition, close1.ULID)

	t.Run("close_only_from_responded", func(t *testing.T) {
		t.Parallel()
		respondedPrior := Result{Kind: KindQuestion, State: StateResponded}
		closeEvent := Event{ULID: "01CLOSURE000000000000006", CommitSeq: 1, Subject: env.ID, Transition: TClose, Actor: Actor{System: requester}}
		closed := Apply(KindQuestion, env, respondedPrior, closeEvent, alwaysMember)
		if closed.State != StateClosed {
			t.Fatalf("close from responded should succeed: %q", closed.State)
		}
		if len(closed.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", closed.Flags)
		}
	})

	t.Run("close_is_illegal_from_any_other_state", func(t *testing.T) {
		t.Parallel()
		for _, from := range []State{StateAccepted, StateInProgress, StateBlocked, StateAcknowledged, StateClosed} {
			from := from
			t.Run(string(from), func(t *testing.T) {
				t.Parallel()
				prior := Result{Kind: KindQuestion, State: from}
				closeEvent := Event{ULID: "01CLOSURE000000000000007", CommitSeq: 1, Subject: env.ID, Transition: TClose, Actor: Actor{System: requester}}
				got := Apply(KindQuestion, env, prior, closeEvent, alwaysMember)
				if got.State != from {
					t.Fatalf("close from %q must be a no-op: state = %q", from, got.State)
				}
				assertFlag(t, got, FlagIllegalTransition, closeEvent.ULID)
			})
		}
	})
}
