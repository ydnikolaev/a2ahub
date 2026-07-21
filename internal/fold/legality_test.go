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
