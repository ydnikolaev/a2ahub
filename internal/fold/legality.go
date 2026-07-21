package fold

// CheckLegality is the pre-write legality primitive (§T1 "Legality check"
// row): given the current folded state, a candidate (not-yet-committed)
// transition, the artifact's envelope facts, the candidate actor and its
// resolved manifest-membership status as of the target commit, it
// returns a verdict in {legal, illegal-transition, unauthorized-actor}.
//
// It is the ONLY surface this package exports that rejects anything —
// and only pre-write. It shares the exact same transition-table data as
// Fold/Apply (never a second, divergent copy of the rules); its verdict
// set is a strict subset of the fold's flag set: state-claim-mismatch has
// no meaning here, since a not-yet-committed candidate event carries no
// committed claim to compare against.
//
// membership is the actor's OWN resolved status (the caller — P3's V2
// path — already has the manifest and resolves exactly one system's
// status; unlike Fold/Apply, which must resolve many different systems'
// statuses across a whole history and so takes a MembershipView).
func CheckLegality(kind Kind, currentState State, transition string, env Envelope, actor Actor, membership MembershipStatus) Verdict {
	if transition == TUnblock {
		if currentState != StateBlocked {
			return VerdictIllegalTransition
		}
		if !legalRole(RoleTarget, env, actor.System, membership) {
			return VerdictUnauthorizedActor
		}
		return VerdictLegal
	}
	if kind == KindDecision && transition == TApprove {
		if currentState != StateProposed {
			return VerdictIllegalTransition
		}
		if !legalRole(RoleApprover, env, actor.System, membership) {
			return VerdictUnauthorizedActor
		}
		return VerdictLegal
	}

	key := tableKey{Kind: kind, From: currentState, Transition: transition}
	entry, ok := transitionTable[key]
	if !ok {
		return VerdictIllegalTransition
	}
	if !legalRole(entry.Role, env, actor.System, membership) {
		return VerdictUnauthorizedActor
	}
	return VerdictLegal
}
