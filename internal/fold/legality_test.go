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
