package fold

import "sort"

// NewResult returns the starting carrier for kind BEFORE any event is
// applied: `draft`, for every kind. A real committed event stream begins
// WITH the submit/publish/propose entry event (03-domain.md §3.4: "the
// submit/publish event travels in the same PR as the artifact file") —
// so the pre-event state the entry event's fromState must match is
// `draft`, not the post-submission state that transition produces.
//
// Fold's zero-events case is a separate, explicitly documented edge case
// (see postSubmissionState below), not this function's job — collapsing
// the two would make the entry event look like an illegal transition on
// every real fold (fromState would already be past `draft`).
func NewResult(kind Kind) Result {
	return Result{Kind: kind, State: StateDraft}
}

// postSubmissionState is 03-domain.md §3.4's explicit zero-events
// fallback ("An artifact present in the space with zero events folds to
// its class's post-submission state") — used ONLY by Fold when the event
// slice is empty; NOT the normal per-event starting state (see
// NewResult). Decision has no submit/publish entry transition (its
// first committed event is `propose`); this phase's reading is that
// decision's zero-events case stays at `draft` rather than a
// post-submission state — recorded as a deviation, since the domain doc
// does not spell out decision's zero-events case explicitly.
func postSubmissionState(kind Kind) State {
	switch kind {
	case KindContract, KindRequirement, KindAnnouncement:
		return StatePublished
	case KindQuestion, KindWorkRequest, KindHandoff, KindResponse:
		return StateSubmitted
	case KindDecision:
		return StateDraft
	default:
		return StateNone
	}
}

// Fold is the full-fold entry point (§T1 "Full fold" row): a pure
// function of kind, envelope facts, the full event set (any order — Fold
// sorts a copy by the caller-supplied ordering key) and a membership view.
// For a non-empty event set it is defined purely in terms of repeated
// Apply calls in canonical order from NewResult(kind) — which is what
// makes T2 agreement with the incremental path structural rather than
// coincidental. For a truly empty event set it returns the documented
// zero-events fallback (postSubmissionState) directly.
func Fold(kind Kind, env Envelope, events []Event, membership MembershipView) Result {
	if len(events) == 0 {
		return Result{Kind: kind, State: postSubmissionState(kind)}
	}
	sorted := make([]Event, len(events))
	copy(sorted, events)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CommitSeq != sorted[j].CommitSeq {
			return sorted[i].CommitSeq < sorted[j].CommitSeq
		}
		return sorted[i].ULID < sorted[j].ULID
	})
	result := NewResult(kind)
	for _, e := range sorted {
		result = Apply(kind, env, result, e, membership)
	}
	return result
}

// Apply is the incremental-fold entry point (§T1 "Incremental fold" row):
// prior folded state + exactly one next event IN CANONICAL ORDER ->
// updated state. It never errors or panics on an illegal or unauthorized
// event (3.5 rules 2-3) — it flags and otherwise no-ops. Duplicate
// replay of an already-applied event ULID is a no-op (T2 AC5).
func Apply(kind Kind, env Envelope, prior Result, event Event, membership MembershipView) Result {
	result := prior.clone()
	if result.Kind == "" {
		result.Kind = kind
	}
	if event.ULID != "" {
		if result.Applied == nil {
			result.Applied = map[string]bool{}
		}
		if result.Applied[event.ULID] {
			return result
		}
		result.Applied[event.ULID] = true
	}

	switch {
	case event.Transition == TNote:
		applyNote(env, &result, event, membership)
	case event.Transition == TAcknowledge && kind == KindAnnouncement:
		applyBroadcastAck(&result, event, membership)
	case event.Transition == TVerify || event.Transition == TDispute:
		applyResponseScoped(env, &result, event, membership)
	default:
		applyPrimaryScoped(kind, env, &result, event, membership)
	}
	return result
}

// applyNote handles D-025's transition-free `note` kind: exempt from the
// transition table entirely (never illegal-transition, never a state
// change), for either party (sender or target) of the current artifact.
func applyNote(env Envelope, result *Result, event Event, membership MembershipView) {
	if !authorized(RoleEitherParty, env, event.Actor.System, membership) {
		result.Flags = append(result.Flags, Flag{Kind: FlagUnauthorizedActor, EventULID: event.ULID, Subject: event.Subject})
	}
}

// applyBroadcastAck handles D-025's transition-free broadcast-acknowledge:
// exempt from the transition table entirely; accumulates into the
// per-recipient ack set (deduplicated by acting system — a duplicate ack
// from the same recipient is a no-op on the set), never changes State.
func applyBroadcastAck(result *Result, event Event, membership MembershipView) {
	if membership != nil && membership(event.Actor.System) != MembershipMember {
		result.Flags = append(result.Flags, Flag{Kind: FlagUnauthorizedActor, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	if result.Acks == nil {
		result.Acks = map[string]bool{}
	}
	result.Acks[event.Actor.System] = true
}

// applyPrimaryScoped handles every transition except note/broadcast-ack
// (transition-free) and verify/dispute (response-scoped, D-024).
func applyPrimaryScoped(kind Kind, env Envelope, result *Result, event Event, membership MembershipView) {
	if event.Transition == TUnblock {
		applyUnblock(kind, env, result, event, membership)
		return
	}
	if kind == KindDecision && event.Transition == TApprove {
		applyApprove(env, result, event, membership)
		return
	}

	key := tableKey{Kind: kind, From: result.State, Transition: event.Transition}
	entry, ok := transitionTable[key]
	if !ok {
		result.Flags = append(result.Flags, Flag{Kind: FlagIllegalTransition, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	if !authorized(entry.Role, env, event.Actor.System, membership) {
		result.Flags = append(result.Flags, Flag{Kind: FlagUnauthorizedActor, EventULID: event.ULID, Subject: event.Subject})
		return
	}

	if event.Transition == TBlock {
		result.PreBlockState = result.State // recomputed from the event sequence, carried in the pure result
	}
	result.State = entry.To
	if event.Transition == TRespond && event.ResponseID != "" {
		if result.Responses == nil {
			result.Responses = map[string]State{}
		}
		result.Responses[event.ResponseID] = StateSubmitted
	}
	checkClaim(result, event, entry.To)
}

// applyUnblock resolves `unblock`'s dynamic target: the state that held
// immediately before the `block` event, recomputed from the carried
// PreBlockState (itself recomputed from the event sequence by
// applyPrimaryScoped's `block` handling — never externally stored).
func applyUnblock(_ Kind, env Envelope, result *Result, event Event, membership MembershipView) {
	if result.State != StateBlocked {
		result.Flags = append(result.Flags, Flag{Kind: FlagIllegalTransition, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	if !authorized(RoleTarget, env, event.Actor.System, membership) {
		result.Flags = append(result.Flags, Flag{Kind: FlagUnauthorizedActor, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	result.State = result.PreBlockState
	checkClaim(result, event, result.State)
}

// applyApprove resolves decision approve's dynamic target: proposed
// (n/m recorded) unless this is the last required approval, in which
// case fold detects quorum = all required and moves to approved.
func applyApprove(env Envelope, result *Result, event Event, membership MembershipView) {
	if result.State != StateProposed {
		result.Flags = append(result.Flags, Flag{Kind: FlagIllegalTransition, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	if !authorized(RoleApprover, env, event.Actor.System, membership) {
		result.Flags = append(result.Flags, Flag{Kind: FlagUnauthorizedActor, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	if result.Approvals == nil {
		result.Approvals = map[string]bool{}
	}
	result.Approvals[event.Actor.System] = true
	if quorumReached(env, result.Approvals) {
		result.State = StateApproved
	}
	checkClaim(result, event, result.State)
}

func quorumReached(env Envelope, approvals map[string]bool) bool {
	if len(env.RequiredApprovers) == 0 {
		return false
	}
	for _, a := range env.RequiredApprovers {
		if !approvals[a] {
			return false
		}
	}
	return true
}

// applyResponseScoped handles verify/dispute (D-024 closure model): the
// one place fold's subject resolution branches on transition name, not
// current state. Subject is the response (XS) id, not the primary
// artifact's own id; authorization is checked against the PRIMARY
// artifact's owner (the original requester), never a response's own
// facts (responses carry no separate envelope in this package's model —
// they exist only as sub-state on their parent's Result).
func applyResponseScoped(env Envelope, result *Result, event Event, membership MembershipView) {
	if !authorized(RoleOwner, env, event.Actor.System, membership) {
		result.Flags = append(result.Flags, Flag{Kind: FlagUnauthorizedActor, EventULID: event.ULID, Subject: event.Subject})
		return
	}

	current := StateNone
	if result.Responses != nil {
		if s, ok := result.Responses[event.Subject]; ok {
			current = s
		}
	}
	key := tableKey{Kind: KindResponse, From: current, Transition: event.Transition}
	entry, ok := transitionTable[key]
	if !ok {
		result.Flags = append(result.Flags, Flag{Kind: FlagIllegalTransition, EventULID: event.ULID, Subject: event.Subject})
		return
	}
	if result.Responses == nil {
		result.Responses = map[string]State{}
	}
	result.Responses[event.Subject] = entry.To
	checkClaim(result, event, entry.To)

	if event.Transition == TDispute {
		// D-024: dispute ADDITIONALLY reopens the parent responded->
		// in_progress. If the parent is not currently `responded` (e.g.
		// already closed, or reopened by an earlier dispute), this
		// parent-level effect is itself illegal and is flagged
		// separately — the response-level disputed marking above still
		// stands regardless.
		if result.State == StateResponded {
			result.State = StateInProgress
		} else {
			result.Flags = append(result.Flags, Flag{Kind: FlagIllegalTransition, EventULID: event.ULID, Subject: env.ID})
		}
	}
}

// checkClaim implements 3.5 rule 5: the event's resulting-state claim is
// informational; the fold's computed state wins, a mismatch is flagged
// (never honored, never fatal). Transition-free events carry no
// meaningful claim and never reach this function.
func checkClaim(result *Result, event Event, actual State) {
	if event.ClaimedState == StateNone {
		return
	}
	if event.ClaimedState != actual {
		result.Flags = append(result.Flags, Flag{Kind: FlagStateClaimMismatch, EventULID: event.ULID, Subject: event.Subject})
	}
}

// authorized checks BOTH the role-derived system match AND manifest
// membership validity (D-017: authorization is evaluated against the
// manifest as of the event's commit). A nil membership view or a
// non-member (left/unknown) actor fails closed.
func authorized(role Role, env Envelope, actorSystem string, membership MembershipView) bool {
	if membership == nil {
		return false
	}
	return legalRole(role, env, actorSystem, membership(actorSystem))
}

func legalRole(role Role, env Envelope, actorSystem string, status MembershipStatus) bool {
	if status != MembershipMember {
		return false
	}
	return roleAuthorizes(role, env, actorSystem)
}

func roleAuthorizes(role Role, env Envelope, actorSystem string) bool {
	switch role {
	case RoleOwner:
		return actorSystem == env.From
	case RoleTarget:
		return env.To0() != "" && actorSystem == env.To0()
	case RoleApprover:
		for _, a := range env.RequiredApprovers {
			if a == actorSystem {
				return true
			}
		}
		return false
	case RoleEitherParty:
		return actorSystem == env.From || (env.To0() != "" && actorSystem == env.To0())
	case RoleAny:
		return true
	default:
		return false
	}
}
