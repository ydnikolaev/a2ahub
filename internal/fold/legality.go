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
	// D-025's transition-free broadcast-acknowledge, checked pre-write by
	// the SAME predicate applyBroadcastAck applies post-write
	// (broadcastAckPermitted, fold.go) — never a second reading of the rule.
	//
	// Without this branch the generic table lookup below decided it, and
	// announcementRows() carries no acknowledge row, so EVERY `a2a ack` on
	// an announcement was refused LFC-001 while the fold stood ready to
	// apply the very event no verb could author. D-025 states the rule
	// plainly ("per-recipient broadcast-ack sets are first-class, exempt
	// from illegal-transition folding") and the fold implemented it; this
	// side did not, so the stricter of the two guards silently won.
	//
	// The cost was not cosmetic: `ack_requested: true` was unanswerable, and
	// `contract retire`'s precondition — every registered consumer has acked
	// the deprecation — could never be satisfied except by --override. Live
	// run 5 (2026-07-25) is what surfaced it, at the first row that ever had
	// a registered consumer with something to ack.
	//
	// Membership, not role, is deliberately the whole test: a broadcast ack
	// is answerable by any current member, INCLUDING one that never appears
	// in the announcement's `to:`. That is not a loosening — it is what
	// AC-971.1 requires, since a consumer registered via `contract adopt`
	// receives the deprecation precisely because it is registered, not
	// because it was addressed.
	if kind == KindAnnouncement && transition == TAcknowledge {
		if !broadcastAckPermitted(membership) {
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
