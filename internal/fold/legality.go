package fold

// CheckCandidate is CheckCandidateWithSuccessor with no resolved successor
// facts — the shape both of this package's OTHER existing callers need to
// keep compiling untouched: evaluate.go's EvaluateCandidate (off this
// phase's allowlist) and this package's own pre-P6 test suite both call
// CheckCandidate at this exact signature, and neither has a successor
// artifact to resolve.
//
// Passing nil is NOT "skip the precondition" — CheckCandidateWithSuccessor
// treats nil as "unresolved", and D9's own rule (types.go's
// SuccessorPrecondition doc comment) says an unresolved fact must refuse
// wherever today's behaviour would otherwise be a silent grant, which is
// exactly the two decision-supersede rows' own shape. So EvaluateCandidate
// callers — chiefly internal/cli's lifecycleEvaluateCandidate, the CLI's
// own pre-write UX gate for `a2a supersede`/`a2a approve`/etc. — see those
// two rows refuse UNCONDITIONALLY until a caller resolves the successor
// and calls CheckCandidateWithSuccessor directly, which is deliberate: one
// rule applied uniformly at every call site, never a second, more lenient
// reading for the caller this phase's allowlist could not reach (reported
// as this phase's off-limits-file finding — a one-line edit to
// evaluate.go's own CheckCandidate call would have let it opt in for
// real).
func CheckCandidate(kind Kind, prior Result, transition, version string, env Envelope, actor Actor, membership MembershipStatus) Verdict {
	return CheckCandidateWithSuccessor(kind, prior, transition, version, env, actor, membership, nil)
}

// CheckCandidateWithSuccessor is the version-aware pre-write legality
// primitive (§T1 "Legality check" row, extended for P4's per-version
// contract lifecycle, and for P6's declared successor preconditions): given
// the prior FULL Result (not just a single State — this is what lets a
// contract answer per-version rather than per-subject), a candidate
// (not-yet-committed) transition and optional version, the artifact's
// envelope facts, the candidate actor and its resolved manifest-membership
// status as of the target commit, and the caller-resolved facts about the
// SUCCESSOR artifact a Precondition-bearing row may check (nil means
// unresolved, never "resolved and blank" — see SuccessorFacts' own doc
// comment, types.go), it returns a verdict in {legal, illegal-transition,
// unauthorized-actor}.
//
// For KindContract on publish/deprecate/retire — once prior already
// carries any recorded version, or the candidate itself names one —
// legality is decided from prior.Versions alone, via
// contractVersionVerdict (contract.go): the EXACT SAME predicate
// applyContractScoped applies post-write, never a second reading of the
// rule (this package's own precedent: broadcastAckPermitted's doc
// comment on what happened when D-025's rule shipped twice and
// disagreed). A version-less candidate against a contract with no
// recorded versions yet falls through to the legacy body below unchanged
// — the same fallback applyContractScoped's own doc comment describes,
// and what makes CheckLegality's wrapper below bit-identical to its
// pre-P4 behaviour.
//
// Everything else — every non-contract kind, contract `create`, and a
// contract still on the legacy version-less path — is decided by the
// body below, which is exactly today's CheckLegality logic (reproduced
// here rather than called, to avoid CheckLegality and CheckCandidate
// calling each other in a cycle: CheckLegality is now the thin wrapper,
// calling this function, never the reverse).
//
// CheckCandidateWithSuccessor is NOT the response-scoped path's own entry
// point in any different shape than CheckLegality always had: verify/
// dispute callers keep supplying prior.State as the RESPONSE's own closure
// sub-state (Result.Responses[responseID]) via a synthetic prior, and env
// as the PARENT's envelope, exactly as CheckLegality's own doc comment
// below requires — this function is not itself the response-scoped path.
func CheckCandidateWithSuccessor(kind Kind, prior Result, transition, version string, env Envelope, actor Actor, membership MembershipStatus, successor *SuccessorFacts) Verdict {
	if kind == KindContract && isContractVersionTransition(transition) && (version != "" || len(prior.Versions) > 0) {
		verdict := contractVersionVerdict(prior.Versions, transition, version)
		if verdict != VerdictLegal {
			return verdict
		}
		if !legalRole(contractVersionRole, env, actor.System, membership) {
			return VerdictUnauthorizedActor
		}
		return VerdictLegal
	}

	currentState := prior.State

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
	// D-025's transition-free note is legal regardless of current state. It
	// still has an authorization rule: the active actor must be either party
	// named by the artifact envelope. Use the SAME predicate applyNote uses
	// post-write so the local writer, V3 required check and fold agree.
	if transition == TNote {
		if !notePermitted(env, actor.System, membership) {
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
	// The dynamic transitions used to be two more hand-written guards right
	// here — `unblock`, and decision `approve` — each re-stating the row's
	// own From and Role. They are gone, because roleTable (table.go) is
	// built from EVERY row: a wrong current state means no key, which is
	// the VerdictIllegalTransition those guards returned, and the role
	// check is the same legalRole call they made.
	//
	// This is the point of the whole exclusion rework and not a tidy-up. A
	// third dynamic transition added by anyone is now reached identically
	// by the pre-write gate and by Apply. With the property-keyed dispatch
	// on the post-write side only, it would have been applied by one and
	// refused by the other — which is precisely what happened to
	// broadcast-ack, recorded at :88-98 above.
	key := tableKey{Kind: kind, From: currentState, Transition: transition}
	role, ok := roleTable[key]
	if !ok {
		return VerdictIllegalTransition
	}
	if !legalRole(role, env, actor.System, membership) {
		return VerdictUnauthorizedActor
	}
	// preconditionTable (table.go) is table.rows' own sibling to roleTable
	// for the ONE other fact a row can declare — a checkable requirement
	// about the SUCCESSOR artifact this transition's own envelope (env,
	// the PREDECESSOR's) cannot answer. Absent from the table (the common
	// case: every row but the two decision-supersede rows) is
	// PreconditionNone, and successorPreconditionSatisfied treats that as
	// "nothing more to check" regardless of successor — never consulted,
	// never able to refuse a row that declares nothing.
	if precondition, ok := preconditionTable[key]; ok {
		if !successorPreconditionSatisfied(precondition, actor, successor) {
			return VerdictUnauthorizedActor
		}
	}
	return VerdictLegal
}

// successorPreconditionSatisfied resolves ONE row's declared
// SuccessorPrecondition against caller-supplied facts about the successor
// artifact. successor == nil (unresolved) never satisfies a real
// precondition — D9's own rule (types.go's SuccessorPrecondition doc
// comment): an optional caller-resolved fact may fail open only where
// today's behaviour is already a refusal or a neutral, and these two rows'
// today is a GRANT, so absence must refuse rather than pass.
func successorPreconditionSatisfied(precondition SuccessorPrecondition, actor Actor, successor *SuccessorFacts) bool {
	switch precondition {
	case PreconditionNone:
		return true
	case PreconditionSuccessorAuthor:
		return successor != nil && successor.Author != "" && successor.Author == actor.System
	case PreconditionSuccessorApproved:
		return successor != nil && successor.State == StateApproved
	default:
		// An unrecognised precondition constant is a programmer error (a
		// new SuccessorPrecondition value added to types.go with no case
		// here) — refuse rather than silently grant, the same "unknown
		// means unauthorized" discipline this whole mechanism exists to
		// enforce.
		return false
	}
}

// CheckLegality is the pre-write legality primitive (§T1 "Legality check"
// row): given the current folded state, a candidate (not-yet-committed)
// transition, the artifact's envelope facts, the candidate actor and its
// resolved manifest-membership status as of the target commit, it
// returns a verdict in {legal, illegal-transition, unauthorized-actor}.
//
// It KEEPS ITS CURRENT SIGNATURE (plan decision 4: "expand/contract,
// never a flag day" — every wave boundary stays a green `make check`). It
// is now a thin wrapper over CheckCandidate, called with a synthetic
// prior (Result{Kind: kind, State: currentState}) and an empty version.
// That synthetic prior always has a nil Versions map, which lands
// CheckCandidate on its own version-less fallback for every existing
// caller — so this function's behaviour is bit-identical to before this
// phase. Wave 4 migrates callers to CheckCandidate directly, with a real
// prior Result and a real version.
//
// It shares the exact same transition-table data as Fold/Apply (never a
// second, divergent copy of the rules); its verdict set is a strict
// subset of the fold's flag set: state-claim-mismatch has no meaning
// here, since a not-yet-committed candidate event carries no committed
// claim to compare against.
//
// membership is the actor's OWN resolved status (the caller — P3's V2
// path — already has the manifest and resolves exactly one system's
// status; unlike Fold/Apply, which must resolve many different systems'
// statuses across a whole history and so takes a MembershipView).
//
// CheckLegality is NOT the response-scoped path in any different shape
// than it always was: verify/dispute callers still supply currentState
// as the RESPONSE's own closure sub-state (Result.Responses[responseID],
// not Result.State) and env as the PARENT's envelope — see
// CheckCandidate's own doc comment for the same requirement, unchanged.
//
// no-silent-yes-2026-08/P6: "bit-identical" above no longer holds for
// exactly the two decision-supersede rows (table.go) that now carry a
// declared SuccessorPrecondition — CheckCandidate supplies no successor
// facts (nil), and D9's own rule refuses an unresolved fact wherever
// today's behaviour is a grant, so CheckLegality now refuses those two
// rows for every actor where it used to grant them unconditionally. That
// behaviour change is this phase's own deliverable, not a regression; every
// OTHER row's verdict is unchanged.
func CheckLegality(kind Kind, currentState State, transition string, env Envelope, actor Actor, membership MembershipStatus) Verdict {
	return CheckCandidate(kind, Result{Kind: kind, State: currentState}, transition, "", env, actor, membership)
}
