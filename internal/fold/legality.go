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
	// verify/dispute are response-scoped (D-024, applyResponseScoped's own
	// pre-write mirror): the subject is a RESPONSE, not the primary
	// artifact `kind` describes, so the table lookup is hardcoded to
	// KindResponse regardless of the caller's `kind` argument (which
	// names the PARENT's own kind for every other transition this
	// function checks) — a generic `key := tableKey{Kind: kind, ...}`
	// lookup would silently miss (parent kind has no verify/dispute row)
	// whenever a caller, correctly, passes the parent's own kind here.
	// The caller is responsible for supplying `currentState` as the
	// RESPONSE's own closure sub-state (Result.Responses[responseID], not
	// Result.State) and `env` as the PARENT's envelope (RoleOwner
	// resolves to the original requester, i.e. the parent's `from` —
	// never the response artifact's own `from`, which has no meaning in
	// this package's model since a response carries no separate
	// envelope of its own).
	if transition == TVerify || transition == TDispute {
		key := tableKey{Kind: KindResponse, From: currentState, Transition: transition}
		entry, ok := transitionTable[key]
		if !ok {
			return VerdictIllegalTransition
		}
		if !legalRole(entry.Role, env, actor.System, membership) {
			return VerdictUnauthorizedActor
		}
		return VerdictLegal
	}
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
