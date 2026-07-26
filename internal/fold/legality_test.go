package fold

import "testing"

// TestCheckLegality exercises the pre-write legality primitive (§T1
// "Legality check" row) — the only rejecting surface this package
// exports, and only pre-write. Not one of spec 04 §8's nine named ACs,
// but it shares the exact table/role machinery Fold/Apply use and is
// covered here for correctness and the coverage floor.
func TestCheckLegality(t *testing.T) {
	t.Parallel()

	t.Run("legal_transition", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		got := CheckLegality(KindQuestion, StateSubmitted, TAcknowledge, env, Actor{System: env.To0()}, MembershipMember)
		if got != VerdictLegal {
			t.Fatalf("got %q, want legal", got)
		}
	})

	t.Run("illegal_transition_unknown_fromstate_pair", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		got := CheckLegality(KindQuestion, StateClosed, TRespond, env, Actor{System: env.To0()}, MembershipMember)
		if got != VerdictIllegalTransition {
			t.Fatalf("got %q, want illegal-transition", got)
		}
	})

	t.Run("unauthorized_actor_wrong_system", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		got := CheckLegality(KindQuestion, StateSubmitted, TAcknowledge, env, Actor{System: "outsider"}, MembershipMember)
		if got != VerdictUnauthorizedActor {
			t.Fatalf("got %q, want unauthorized-actor", got)
		}
	})

	t.Run("unauthorized_actor_left_membership", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		got := CheckLegality(KindQuestion, StateSubmitted, TAcknowledge, env, Actor{System: env.To0()}, MembershipLeft)
		if got != VerdictUnauthorizedActor {
			t.Fatalf("got %q, want unauthorized-actor", got)
		}
	})

	t.Run("unblock_dynamic_row", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindQuestion)
		legal := CheckLegality(KindQuestion, StateBlocked, TUnblock, env, Actor{System: env.To0()}, MembershipMember)
		if legal != VerdictLegal {
			t.Fatalf("got %q, want legal", legal)
		}
		illegal := CheckLegality(KindQuestion, StateAccepted, TUnblock, env, Actor{System: env.To0()}, MembershipMember)
		if illegal != VerdictIllegalTransition {
			t.Fatalf("got %q, want illegal-transition (unblock only from blocked)", illegal)
		}
	})

	t.Run("decision_approve_dynamic_row", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindDecision)
		legal := CheckLegality(KindDecision, StateProposed, TApprove, env, Actor{System: env.RequiredApprovers[0]}, MembershipMember)
		if legal != VerdictLegal {
			t.Fatalf("got %q, want legal", legal)
		}
		unauthorized := CheckLegality(KindDecision, StateProposed, TApprove, env, Actor{System: "not-an-approver"}, MembershipMember)
		if unauthorized != VerdictUnauthorizedActor {
			t.Fatalf("got %q, want unauthorized-actor", unauthorized)
		}
	})

	t.Run("verify_legal_from_submitted_response_against_parent_envelope", func(t *testing.T) {
		t.Parallel()
		// env is the PARENT exchange's own envelope (rowEnv gives From=
		// "acme", the original requester/sender) — verify's RoleOwner
		// check resolves against env.From, never a response's own facts
		// (responses carry no separate envelope in this package's model).
		parentEnv := rowEnv(KindQuestion)
		got := CheckLegality(KindQuestion, StateSubmitted, TVerify, parentEnv, Actor{System: parentEnv.From}, MembershipMember)
		if got != VerdictLegal {
			t.Fatalf("got %q, want legal", got)
		}
	})

	t.Run("verify_illegal_from_wrong_response_substate", func(t *testing.T) {
		t.Parallel()
		parentEnv := rowEnv(KindQuestion)
		// StateVerified has no From row for TVerify (a response verifies
		// exactly once from `submitted`).
		got := CheckLegality(KindQuestion, StateVerified, TVerify, parentEnv, Actor{System: parentEnv.From}, MembershipMember)
		if got != VerdictIllegalTransition {
			t.Fatalf("got %q, want illegal-transition", got)
		}
	})

	t.Run("verify_unauthorized_actor_is_not_the_parents_owner", func(t *testing.T) {
		t.Parallel()
		parentEnv := rowEnv(KindQuestion)
		// The responder (parentEnv.To0()) is NOT authorized to verify
		// their own response — only the parent's own owner (the original
		// requester) may.
		got := CheckLegality(KindQuestion, StateSubmitted, TVerify, parentEnv, Actor{System: parentEnv.To0()}, MembershipMember)
		if got != VerdictUnauthorizedActor {
			t.Fatalf("got %q, want unauthorized-actor", got)
		}
	})

	t.Run("dispute_legal_from_submitted_response_against_parent_envelope", func(t *testing.T) {
		t.Parallel()
		parentEnv := rowEnv(KindQuestion)
		got := CheckLegality(KindQuestion, StateSubmitted, TDispute, parentEnv, Actor{System: parentEnv.From}, MembershipMember)
		if got != VerdictLegal {
			t.Fatalf("got %q, want legal", got)
		}
	})

	t.Run("verdict_set_is_a_strict_subset_of_flag_kinds", func(t *testing.T) {
		t.Parallel()
		// state-claim-mismatch has no verdict counterpart — documented
		// structurally: Verdict only has three values, none of them
		// spelled like FlagStateClaimMismatch.
		for _, v := range []Verdict{VerdictLegal, VerdictIllegalTransition, VerdictUnauthorizedActor} {
			if string(v) == string(FlagStateClaimMismatch) {
				t.Fatalf("verdict set leaked state-claim-mismatch: %v", v)
			}
		}
	})
}

// TestCheckLegalityBroadcastAck is live run 5's (2026-07-25) regression.
//
// D-025 makes a broadcast acknowledge transition-free and "exempt from
// illegal-transition folding". applyBroadcastAck implemented that; this
// package's OTHER enforcement point, CheckLegality, did not — it fell
// through to the generic table lookup, and announcementRows() carries no
// acknowledge row. So `a2a ack <announcement>` was refused LFC-001
// unconditionally, for every announcement, in every state, while the fold
// stood ready to apply the very event no verb could author.
//
// The blast radius was not cosmetic: `ack_requested: true` was
// unanswerable, and `contract retire`'s precondition — every registered
// consumer has acknowledged the deprecation — could be satisfied only by
// `--override`. Nothing hermetic covered an ack on an announcement, so
// the whole deprecate -> ack -> retire chain was green everywhere and
// broken in the one place it is actually walked.
//
// TEETH: delete the `kind == KindAnnouncement && transition ==
// TAcknowledge` branch from CheckLegality and the first two subtests red
// with illegal-transition.
func TestCheckLegalityBroadcastAck(t *testing.T) {
	t.Parallel()

	t.Run("addressed_member_may_ack", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindAnnouncement)
		got := CheckLegality(KindAnnouncement, StatePublished, TAcknowledge, env, Actor{System: env.To0()}, MembershipMember)
		if got != VerdictLegal {
			t.Fatalf("got %q, want legal — D-025 exempts a broadcast ack from the transition table", got)
		}
	})

	// The case AC-971.1 depends on: a consumer registered via `contract
	// adopt` is never in the announcement's own `to:`, and must still be
	// able to answer the deprecation it was sent. Membership is the whole
	// test; addressing is not.
	t.Run("unaddressed_member_may_ack", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindAnnouncement)
		got := CheckLegality(KindAnnouncement, StatePublished, TAcknowledge, env, Actor{System: "gamma"}, MembershipMember)
		if got != VerdictLegal {
			t.Fatalf("got %q, want legal — a registered consumer acks a deprecation it was never addressed by", got)
		}
	})

	// The exemption is from the TABLE, never from membership.
	t.Run("non_member_may_not_ack", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindAnnouncement)
		for _, status := range []MembershipStatus{MembershipLeft, MembershipUnknown} {
			if got := CheckLegality(KindAnnouncement, StatePublished, TAcknowledge, env, Actor{System: "gamma"}, status); got != VerdictUnauthorizedActor {
				t.Fatalf("membership %v: got %q, want unauthorized-actor", status, got)
			}
		}
	})

	// Both enforcement points must answer the same question the same way —
	// this is the divergence that shipped, so it is asserted rather than
	// assumed. A member's ack is accepted by the fold and by the pre-write
	// check; a non-member's is rejected by both.
	t.Run("pre_write_and_post_write_agree", func(t *testing.T) {
		t.Parallel()
		env := rowEnv(KindAnnouncement)
		for _, tc := range []struct {
			status MembershipStatus
			want   bool
		}{{MembershipMember, true}, {MembershipLeft, false}, {MembershipUnknown, false}} {
			view := func(string) MembershipStatus { return tc.status }
			var result Result
			applyBroadcastAck(&result, Event{ULID: "01", Subject: env.ID, Actor: Actor{System: "gamma"}}, view)
			postAccepted := len(result.Flags) == 0 && result.Acks["gamma"]

			preAccepted := CheckLegality(KindAnnouncement, StatePublished, TAcknowledge, env, Actor{System: "gamma"}, tc.status) == VerdictLegal

			if postAccepted != tc.want || preAccepted != tc.want {
				t.Fatalf("membership %v: pre-write=%v post-write=%v, want both %v", tc.status, preAccepted, postAccepted, tc.want)
			}
		}
	})
}

// TestContractCanPublishASuccessorAfterDeprecation is the regression for a
// wall that only appears once a contract has been in real use.
//
// `a2a contract deprecate --version 1.0.0` deprecates a VERSION. This fold
// records state per SUBJECT, so it moved the whole contract to `deprecated`
// — and with no (deprecated, publish) row, every later publish was refused
// LFC-001. The ordinary life of a live contract is publish 1.0, publish 2.0,
// deprecate 1.0, publish 3.0; it hit the wall on that last step, which is
// late enough that nobody meets it until two projects are genuinely
// exchanging contracts.
//
// The test walks the whole sequence rather than asserting the single new
// row, because the row is only worth having if the SEQUENCE works.
func TestContractCanPublishASuccessorAfterDeprecation(t *testing.T) {
	t.Parallel()

	env := Envelope{Kind: KindContract, ID: "XC-alpha-widget", From: "alpha"}
	actor := Actor{Kind: "agent", Name: "bot", System: "alpha"}

	steps := []struct {
		from       State
		transition string
		wantTo     State
	}{
		{StateNone, TCreate, StateDraft},
		{StateDraft, TPublish, StatePublished},
		{StatePublished, TPublish, StatePublished},    // 2.0.0
		{StatePublished, TDeprecate, StateDeprecated}, // 1.0.0 sunset
		{StateDeprecated, TPublish, StatePublished},   // 3.0.0 — the wall
		{StatePublished, TDeprecate, StateDeprecated},
		{StateDeprecated, TRetire, StateRetired},
	}

	for i, s := range steps {
		verdict := CheckLegality(KindContract, s.from, s.transition, env, actor, MembershipMember)
		if verdict != VerdictLegal {
			t.Fatalf("step %d: %s from %s was refused (%v) — a contract that cannot publish after a "+
				"deprecation is dead the moment its first version sunsets", i, s.transition, s.from, verdict)
		}
	}
}

// TestContractRetireStillRequiresDeprecation pins the boundary the row above
// must NOT have widened: retire remains reachable only from `deprecated`, so
// the live tier's contract-retire-without-deprecation-refused row keeps
// asserting something.
func TestContractRetireStillRequiresDeprecation(t *testing.T) {
	t.Parallel()

	env := Envelope{Kind: KindContract, ID: "XC-alpha-widget", From: "alpha"}
	actor := Actor{Kind: "agent", Name: "bot", System: "alpha"}

	if v := CheckLegality(KindContract, StatePublished, TRetire, env, actor, MembershipMember); v == VerdictLegal {
		t.Fatal("retire from `published` became legal — the successor-publish row widened more than it meant to")
	}
}
