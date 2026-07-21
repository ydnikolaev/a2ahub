package fold

// Transition names (§5.2.2 `transition` enum; union of every §3.4
// transition name + `note`). Kept as plain strings, not a closed Go enum,
// because the same name means different things across kinds (e.g.
// "supersede") and the table below is the single source of truth for
// which (kind, fromState, transition) combinations are legal — exactly
// the anti-duplication rule spec §5 calls for.
const (
	TCreate      = "create"
	TPublish     = "publish"
	TDeprecate   = "deprecate"
	TRetire      = "retire"
	TAcknowledge = "acknowledge"
	TSatisfy     = "satisfy"
	TDecline     = "decline"
	TWithdraw    = "withdraw"
	TSupersede   = "supersede"
	TSubmit      = "submit"
	TAccept      = "accept"
	TStart       = "start"
	TBlock       = "block"
	TUnblock     = "unblock"
	TRespond     = "respond"
	TClose       = "close"
	TDispute     = "dispute"
	TCancel      = "cancel"
	TPropose     = "propose"
	TApprove     = "approve"
	TReject      = "reject"
	TVerifyPass  = "verify-pass"
	TVerifyFail  = "verify-fail"
	TVerify      = "verify"
	TNote        = "note" // transition-free (D-025); never in this table
)

// Row is one exploded (kind, fromState, transition) -> (toState, role)
// table entry — the single source for both the fold's lookup and the T2
// fixture list (spec §5, §T1.1: "do not hand-duplicate rows into test
// code"). Rows whose To is StateDynamic are NOT looked up generically;
// dedicated logic resolves them (unblock's pre-block recovery, decision
// approve's quorum arithmetic) — Scenario then labels which of the
// dynamic row's concrete outcomes a given table entry documents/exercises,
// purely for test naming; it carries no runtime meaning.
type Row struct {
	Kind       Kind
	From       State
	Transition string
	To         State
	Role       Role
	Scenario   string
}

// rows is the exploded §3.4.1-§3.4.7 transition table. Every range in the
// plan's prose tables ("submitted...in_progress", "any open", etc.) is
// expanded here into its constituent (kind, fromState, transition) rows —
// this expansion, not the prose table, is what AC3's meta-test counts
// against the exercised subtests.
var rows = buildRows()

func buildRows() []Row {
	var out []Row
	out = append(out, contractRows()...)
	out = append(out, requirementRows()...)
	out = append(out, exchangeRows(KindQuestion)...)
	out = append(out, exchangeRows(KindWorkRequest)...)
	out = append(out, decisionRows()...)
	out = append(out, handoffRows()...)
	out = append(out, responseRows()...)
	out = append(out, announcementRows()...)
	return out
}

// 3.4.1 contract
func contractRows() []Row {
	return []Row{
		{Kind: KindContract, From: StateNone, Transition: TCreate, To: StateDraft, Role: RoleOwner},
		{Kind: KindContract, From: StateDraft, Transition: TPublish, To: StatePublished, Role: RoleOwner, Scenario: "first-publish"},
		{Kind: KindContract, From: StatePublished, Transition: TPublish, To: StatePublished, Role: RoleOwner, Scenario: "new-version-publish"},
		{Kind: KindContract, From: StatePublished, Transition: TDeprecate, To: StateDeprecated, Role: RoleOwner},
		{Kind: KindContract, From: StateDeprecated, Transition: TRetire, To: StateRetired, Role: RoleOwner},
	}
}

// 3.4.2 requirement
func requirementRows() []Row {
	var out []Row
	out = append(out,
		Row{Kind: KindRequirement, From: StateNone, Transition: TCreate, To: StateDraft, Role: RoleOwner},
		Row{Kind: KindRequirement, From: StateDraft, Transition: TPublish, To: StatePublished, Role: RoleOwner},
		Row{Kind: KindRequirement, From: StatePublished, Transition: TAcknowledge, To: StateAcknowledged, Role: RoleTarget},
		Row{Kind: KindRequirement, From: StateAcknowledged, Transition: TSatisfy, To: StateSatisfied, Role: RoleOwner},
	)
	// published/acknowledged | decline | declined | target
	for _, from := range []State{StatePublished, StateAcknowledged} {
		out = append(out, Row{Kind: KindRequirement, From: from, Transition: TDecline, To: StateDeclined, Role: RoleTarget})
	}
	// any pre-satisfied | withdraw | withdrawn | requesting system
	for _, from := range []State{StateDraft, StatePublished, StateAcknowledged} {
		out = append(out, Row{Kind: KindRequirement, From: from, Transition: TWithdraw, To: StateWithdrawn, Role: RoleOwner})
	}
	// any | supersede | superseded | requesting system
	for _, from := range []State{StateDraft, StatePublished, StateAcknowledged, StateSatisfied, StateDeclined, StateWithdrawn} {
		out = append(out, Row{Kind: KindRequirement, From: from, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner})
	}
	return out
}

// 3.4.3 question / work_request — identical lifecycle, generated for
// both kinds from one shared definition so each is independently
// exercised (one subtest per row per kind).
func exchangeRows(kind Kind) []Row {
	var out []Row
	out = append(out,
		Row{Kind: kind, From: StateNone, Transition: TCreate, To: StateDraft, Role: RoleOwner},
		Row{Kind: kind, From: StateDraft, Transition: TSubmit, To: StateSubmitted, Role: RoleOwner},
		Row{Kind: kind, From: StateSubmitted, Transition: TAcknowledge, To: StateAcknowledged, Role: RoleTarget},
		Row{Kind: kind, From: StateAcknowledged, Transition: TAccept, To: StateAccepted, Role: RoleTarget},
		Row{Kind: kind, From: StateAccepted, Transition: TStart, To: StateInProgress, Role: RoleTarget},
	)
	// submitted...in_progress | decline | declined | target
	for _, from := range []State{StateSubmitted, StateAcknowledged, StateAccepted, StateInProgress} {
		out = append(out, Row{Kind: kind, From: from, Transition: TDecline, To: StateDeclined, Role: RoleTarget})
	}
	// acknowledged...in_progress | block | blocked | target — the target
	// state itself is fixed (blocked); applyPrimaryScoped separately
	// records PreBlockState = fromState as a side effect of this row, so
	// `unblock` can recover it.
	for _, from := range []State{StateAcknowledged, StateAccepted, StateInProgress} {
		out = append(out, Row{Kind: kind, From: from, Transition: TBlock, To: StateBlocked, Role: RoleTarget})
	}
	// blocked | unblock | *pre-block state* | target — one dynamic row
	// per possible pre-block state, for test-scenario documentation.
	for _, pre := range []State{StateAcknowledged, StateAccepted, StateInProgress} {
		out = append(out, Row{Kind: kind, From: StateBlocked, Transition: TUnblock, To: StateDynamic, Role: RoleTarget, Scenario: "pre-block=" + string(pre)})
	}
	// accepted/in_progress/acknowledged | respond | responded | target —
	// PLUS `responded` itself (multi-response support, 3.4.6: "one parent
	// MAY receive multiple responses"; the domain table's literal
	// fromState list predates that multi-response allowance — this
	// fourth fromState is this phase's explicit reconciliation, recorded
	// in the report's Deviations).
	for _, from := range []State{StateAccepted, StateInProgress, StateAcknowledged, StateResponded} {
		out = append(out, Row{Kind: kind, From: from, Transition: TRespond, To: StateResponded, Role: RoleTarget})
	}
	out = append(out,
		Row{Kind: kind, From: StateResponded, Transition: TClose, To: StateClosed, Role: RoleOwner},
		Row{Kind: kind, From: StateResponded, Transition: TDispute, To: StateInProgress, Role: RoleOwner},
	)
	// draft...in_progress | cancel | cancelled | sender
	for _, from := range []State{StateDraft, StateSubmitted, StateAcknowledged, StateAccepted, StateInProgress} {
		out = append(out, Row{Kind: kind, From: from, Transition: TCancel, To: StateCancelled, Role: RoleOwner})
	}
	// any open | supersede | superseded | sender
	for _, from := range []State{StateDraft, StateSubmitted, StateAcknowledged, StateAccepted, StateInProgress, StateBlocked, StateResponded} {
		out = append(out, Row{Kind: kind, From: from, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner})
	}
	return out
}

// 3.4.4 decision
func decisionRows() []Row {
	return []Row{
		{Kind: KindDecision, From: StateNone, Transition: TCreate, To: StateDraft, Role: RoleAny},
		{Kind: KindDecision, From: StateDraft, Transition: TPropose, To: StateProposed, Role: RoleOwner},
		{Kind: KindDecision, From: StateProposed, Transition: TApprove, To: StateDynamic, Role: RoleApprover, Scenario: "quorum-not-reached"},
		{Kind: KindDecision, From: StateProposed, Transition: TApprove, To: StateDynamic, Role: RoleApprover, Scenario: "quorum-reached"},
		{Kind: KindDecision, From: StateProposed, Transition: TReject, To: StateRejected, Role: RoleApprover},
		// Fold cannot verify "author of the successor decision" or "new
		// approved decision only" from the PREDECESSOR's own envelope
		// facts (that authorship lives on a different, not-yet-existing
		// artifact) — encoded as membership-only (RoleAny). Deviation,
		// documented in the phase report.
		{Kind: KindDecision, From: StateRejected, Transition: TSupersede, To: StateSuperseded, Role: RoleAny, Scenario: "successor-authorship-unverifiable"},
		{Kind: KindDecision, From: StateApproved, Transition: TSupersede, To: StateSuperseded, Role: RoleAny, Scenario: "new-approved-decision-only-unverifiable"},
	}
}

// 3.4.5 handoff
func handoffRows() []Row {
	return []Row{
		{Kind: KindHandoff, From: StateNone, Transition: TCreate, To: StateDraft, Role: RoleOwner},
		{Kind: KindHandoff, From: StateDraft, Transition: TSubmit, To: StateSubmitted, Role: RoleOwner},
		{Kind: KindHandoff, From: StateSubmitted, Transition: TAcknowledge, To: StateAcknowledged, Role: RoleTarget},
		{Kind: KindHandoff, From: StateAcknowledged, Transition: TVerifyPass, To: StateAccepted, Role: RoleTarget},
		{Kind: KindHandoff, From: StateAcknowledged, Transition: TVerifyFail, To: StateRejected, Role: RoleTarget},
		{Kind: KindHandoff, From: StateRejected, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner},
	}
}

// 3.4.6 response (attached exchange) — its own minimal create/submit
// lifecycle, plus the closure-model verify/dispute rows (D-024). Role on
// verify/dispute is documented as RoleOwner but is resolved specially
// (against the PARENT's From, not a response's own From) by
// applyResponseScoped — the one place fold's subject resolution branches
// on transition name (spec's own callout), never modeled as a distinct
// Role value.
func responseRows() []Row {
	return []Row{
		{Kind: KindResponse, From: StateNone, Transition: TCreate, To: StateDraft, Role: RoleAny},
		{Kind: KindResponse, From: StateDraft, Transition: TSubmit, To: StateSubmitted, Role: RoleAny},
		{Kind: KindResponse, From: StateSubmitted, Transition: TVerify, To: StateVerified, Role: RoleOwner},
		{Kind: KindResponse, From: StateSubmitted, Transition: TDispute, To: StateDisputed, Role: RoleOwner},
	}
}

// 3.4.7 announcement (broadcast) — `expired` overlay and per-recipient
// ack are exempt from this table entirely (D-025); see expired.go and the
// `acknowledge`-on-announcement bypass in fold.go.
func announcementRows() []Row {
	return []Row{
		{Kind: KindAnnouncement, From: StateNone, Transition: TCreate, To: StateDraft, Role: RoleOwner},
		{Kind: KindAnnouncement, From: StateDraft, Transition: TPublish, To: StatePublished, Role: RoleOwner},
		{Kind: KindAnnouncement, From: StatePublished, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner},
	}
}

type tableKey struct {
	Kind       Kind
	From       State
	Transition string
}

type tableEntry struct {
	To   State
	Role Role
}

// transitionTable is the generic (kind, fromState, transition) -> (toState,
// role) lookup used by every row EXCEPT the dynamic ones (unblock;
// decision approve), which dedicated logic in fold.go resolves — those
// are deliberately excluded here so the generic path never sees a
// StateDynamic sentinel.
var transitionTable = buildTable()

func buildTable() map[tableKey]tableEntry {
	m := make(map[tableKey]tableEntry, len(rows))
	for _, r := range rows {
		if r.Transition == TUnblock {
			continue
		}
		if r.Kind == KindDecision && r.Transition == TApprove {
			continue
		}
		key := tableKey{Kind: r.Kind, From: r.From, Transition: r.Transition}
		if _, exists := m[key]; exists {
			// Duplicate non-dynamic key would silently shadow a row —
			// a build-time bug in the table, not runtime data; panic is
			// appropriate here (programmer error, package init time).
			panic("fold: duplicate transition table row for " + string(r.Kind) + "/" + string(r.From) + "/" + r.Transition)
		}
		m[key] = tableEntry{To: r.To, Role: r.Role}
	}
	return m
}
