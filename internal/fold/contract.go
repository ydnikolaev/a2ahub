package fold

// isContractVersionTransition reports whether transition is one of the
// three contract lifecycle transitions applyContractScoped and
// CheckCandidate route through the per-version predicate below. `create`
// is deliberately excluded — 03-domain.md's create/entry event is
// unchanged by this phase and stays on applyPrimaryScoped's generic table
// path (spec 04 §3: "create: unchanged, goes through the existing table
// path").
func isContractVersionTransition(transition string) bool {
	return transition == TPublish || transition == TDeprecate || transition == TRetire
}

// contractVersionVerdict is THE single predicate deciding whether a
// (transition, version) pair is legal against a contract's prior
// per-version state map. It is read by BOTH applyContractScoped's
// post-write branch (fold.go's own precedent: applyBroadcastAck,
// applyApprove) and CheckCandidate's pre-write path (legality.go) — never
// a second reading of the rule. This package already paid for shipping a
// rule twice and having the two disagree once (D-025's broadcast-ack:
// see broadcastAckPermitted's own doc comment); this predicate exists so
// that mistake is not repeated for the contract version dimension.
// versions is read-only here — callers own the actual map mutation.
//
// version == "" is the WHOLE-contract form of deprecate/retire
// (03-domain.md §3.4.1: "deprecate (whole) = an event without version").
// publish has no whole form: every publish this predicate is asked about
// names a version (a version-less publish once any version is already
// recorded has no meaning in this model and is refused).
//
// publish v is illegal whenever versions[v] is ALREADY set, regardless of
// its current value (published, deprecated, or retired) — a version
// number, once minted, is a permanent identifier (spec §5.4a: `id@version`
// resolves through the publish event's own SHA; §3.8 forbids deletion),
// so "republish the same version after deprecating it" is a genuinely
// different, and genuinely illegal, operation from "publish a new
// version while an old one is deprecated" (the interim-row scenario this
// same predicate replaces — see the successor-publish tests in
// contract_test.go). This is a deliberate, intended divergence from the
// pre-P4 engine for that one exact input shape; see
// TestContractVersionRepublishAfterDeprecateIsIllegal.
func contractVersionVerdict(versions map[string]State, transition, version string) Verdict {
	switch transition {
	case TPublish:
		if version == "" {
			return VerdictIllegalTransition
		}
		if _, exists := versions[version]; exists {
			return VerdictIllegalTransition
		}
		return VerdictLegal
	case TDeprecate:
		if version != "" {
			if versions[version] != StatePublished {
				return VerdictIllegalTransition
			}
			return VerdictLegal
		}
		for _, s := range versions {
			if s == StatePublished {
				return VerdictLegal
			}
		}
		return VerdictIllegalTransition
	case TRetire:
		if version != "" {
			if versions[version] != StateDeprecated {
				return VerdictIllegalTransition
			}
			return VerdictLegal
		}
		if len(versions) == 0 {
			return VerdictIllegalTransition
		}
		for _, s := range versions {
			if s != StateDeprecated {
				return VerdictIllegalTransition
			}
		}
		return VerdictLegal
	default:
		return VerdictIllegalTransition
	}
}

// contractVersionRole is the single declaration of WHO may make a
// contract version transition. It is the role dimension's answer to the
// same question contractVersionVerdict answers for the transition
// dimension, and it exists for the same reason: three call sites read it
// — applyContractScoped post-write, CheckCandidate pre-write, and
// LegalNextFor when it reports who a legal move belongs to — and a bare
// RoleOwner literal at each of them is three declarations of one rule
// that nothing forces to agree.
//
// The early audit of this wave measured exactly that: changing one site
// to RoleTarget was caught only INCIDENTALLY, by a property test that
// happens to use the owner as its actor, because no test asked the three
// sites whether they still agreed. TestContractVersionRoleIsOneRule now
// does.
const contractVersionRole = RoleOwner

// contractVersionOutcome maps one contract version transition to the
// state it puts THAT VERSION in — the third and last reader of the
// version rules, alongside contractVersionVerdict's two. It exists so
// applyContractScoped (which needs the outcome to record and to compare
// the event's claim against) and LegalNextFor (which needs it to report
// where a legal move lands) never spell the mapping out twice.
func contractVersionOutcome(transition string) State {
	switch transition {
	case TPublish:
		return StatePublished
	case TDeprecate:
		return StateDeprecated
	case TRetire:
		return StateRetired
	default:
		return StateNone
	}
}

// projectContractState computes the subject-level projection over a
// contract's per-version state map (spec 04 §3, plan decision 5): the
// single Result.State value every existing consumer (inbox, statusline,
// thread, the retire precondition) keeps reading, now derived from
// Versions rather than carried independently. Precedence, in order: any
// version published -> published; else any version deprecated ->
// deprecated; else every recorded version retired (and at least one
// exists) -> retired; otherwise -> draft. Callers invoke this only once
// len(versions) > 0 — applyContractScoped's own fallback keeps a
// version-less contract off this path entirely, so it never needs to
// return the "before first publish" answer for a truly empty map.
func projectContractState(versions map[string]State) State {
	anyPublished := false
	anyDeprecated := false
	allRetired := len(versions) > 0
	for _, s := range versions {
		switch s {
		case StatePublished:
			anyPublished = true
		case StateDeprecated:
			anyDeprecated = true
		}
		if s != StateRetired {
			allRetired = false
		}
	}
	switch {
	case anyPublished:
		return StatePublished
	case anyDeprecated:
		return StateDeprecated
	case allRetired:
		return StateRetired
	default:
		return StateDraft
	}
}

// applyContractScoped is Apply's dedicated contract branch (precedent:
// applyBroadcastAck, applyApprove, applyResponseScoped, fold.go) for
// publish/deprecate/retire — the three transitions a contract's version
// dimension actually touches. `create` is not routed here; it stays on
// applyPrimaryScoped's generic table path, unchanged.
//
// THE CRITICAL FALLBACK: an event carrying no version, folded against a
// prior with no recorded versions yet, defers to today's table path
// entirely (applyPrimaryScoped) — this is what makes a version-less
// history fold byte-identically to before this phase (spec §5.1's
// migration guarantee; see the property test in property_test.go). Once
// EITHER this event carries a version OR the prior already has any
// recorded (prior.Versions non-empty), every publish/deprecate/retire on
// this contract is decided from Versions alone via
// contractVersionVerdict, which never also consults the subject-state
// table (plan decision 7) — the interim `(deprecated, publish)` row in
// table.go stays in place this wave (plan decision 5) but is never read
// once a contract is on this path, which is exactly what makes deleting
// that row in a later wave safe.
func applyContractScoped(env Envelope, result *Result, event Event, membership MembershipView) {
	if event.Version == "" && len(result.Versions) == 0 {
		applyPrimaryScoped(KindContract, env, result, event, membership)
		return
	}

	verdict := contractVersionVerdict(result.Versions, event.Transition, event.Version)
	if verdict != VerdictLegal {
		result.Flags = append(result.Flags, Flag{Kind: FlagIllegalTransition, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	if !authorized(contractVersionRole, env, event.Actor.System, membership) {
		result.Flags = append(result.Flags, Flag{Kind: FlagUnauthorizedActor, EventULID: event.ULID, Subject: event.Subject})
		return
	}

	if result.Versions == nil {
		result.Versions = map[string]State{}
	}

	// outcome is the VERSION's own resulting state — decision 6: checkClaim
	// below compares the event's claim against this, never against
	// result.State (the projected SUBJECT-level state), which can
	// correctly disagree with a single version's own transition (e.g.
	// "deprecate 1.0" while 2.0 stays published projects `published`, but
	// 1.0's own outcome is `deprecated` — the claim is about 1.0).
	outcome := contractVersionOutcome(event.Transition)
	switch event.Transition {
	case TPublish:
		result.Versions[event.Version] = outcome
	case TDeprecate:
		if event.Version != "" {
			result.Versions[event.Version] = outcome
		} else {
			for v, s := range result.Versions {
				if s == StatePublished {
					result.Versions[v] = outcome
				}
			}
		}
	case TRetire:
		if event.Version != "" {
			result.Versions[event.Version] = outcome
		} else {
			for v := range result.Versions {
				result.Versions[v] = outcome
			}
		}
	}

	result.State = projectContractState(result.Versions)
	checkClaim(result, event, outcome)
}
