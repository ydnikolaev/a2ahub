package fold

// Transition names (§5.2.2 `transition` enum; union of every §3.4
// transition name + `note`). Kept as plain strings, not a closed Go enum,
// because the same name means different things across kinds (e.g.
// "supersede") and the table below is the single source of truth for
// which (kind, fromState, transition) combinations are legal — exactly
// the anti-duplication rule spec §5 calls for.
const (
	// TCreate names the schema's own create transition (schemas/event/v1's
	// enum still carries it — §5.2.2 defines the enum as the union of
	// every §3.4 transition name, and §3.4 lists `create` alongside
	// committed events because the domain tables describe LOCAL authoring
	// too). It carries NO row in this package's table (spec §18a,
	// 2026-08-06 operator decision): Fold always starts a kind at
	// NewResult(kind) = StateDraft, so a committed create event's own
	// fromState (StateNone) is a state the fold never occupies — a
	// committed create event is therefore ALREADY flagged
	// illegal-transition by the generic "no row for this triple" path,
	// with or without a dedicated row, and removing the row changes
	// nothing observable. Kept as a constant only because the schema
	// enum's own value must still be nameable by anything that reads a
	// (schema-legal, fold-inert) create event off the wire.
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
	// TActivate is the producer's attestation that a published contract
	// version's OPERATIONAL half now exists (P5 AC1). Transition-free, and
	// never in this table, for the reason the review corpus gives: readiness
	// changes AFTER publication while a descriptor is immutable, so it has
	// to be an event — but it moves no artifact state, because a contract
	// that becomes reachable is still `published`. Modelling it as a real
	// transition would need a state the domain does not have and would make
	// every reader below the floor fold an unknown move.
	TActivate = "activate"
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

	// Outcomes are the concrete states this row can actually produce when
	// To is the StateDynamic sentinel. Non-empty EXACTLY when To is
	// StateDynamic (asserted, both directions, by table_test.go) — so the
	// dynamic set is a property of the rows rather than a list of verb
	// names three consumers each had to know.
	//
	// Before this field, buildTable and applyPrimaryScoped each excluded
	// the dynamic rows by hardcoded transition name, mirrored, and
	// RestingStates skipped the sentinel outright. That cost two real
	// defects: a THIRD dynamic transition added by anyone panicked the
	// package at init on a duplicate table key, and (decision, approved)
	// was absent from RestingStates entirely even though a quorum-reached
	// approve demonstrably lands there.
	Outcomes []State
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
	// per possible pre-block state, for test-scenario documentation. Each
	// row DECLARES that pre-block state as its outcome, so RestingStates
	// and buildTable read the property rather than the verb name.
	for _, pre := range []State{StateAcknowledged, StateAccepted, StateInProgress} {
		out = append(out, Row{Kind: kind, From: StateBlocked, Transition: TUnblock, To: StateDynamic, Role: RoleTarget, Scenario: "pre-block=" + string(pre), Outcomes: []State{pre}})
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
	// draft...in_progress AND blocked | cancel | cancelled | sender
	//
	// `blocked` is in the list, and its absence was a real gap: a requester
	// whose target is blocked had to either wait or reach for `supersede`,
	// which says "replaced by" rather than "no longer needed". Making a
	// sender misstate one as the other is the substitution this epic exists
	// to end, and the blocking party is by definition not the one who can
	// resolve it.
	//
	// One source line, two triples — exchangeRows runs for KindQuestion and
	// KindWorkRequest both.
	for _, from := range []State{StateDraft, StateSubmitted, StateAcknowledged, StateAccepted, StateInProgress, StateBlocked} {
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
		{Kind: KindDecision, From: StateDraft, Transition: TPropose, To: StateProposed, Role: RoleOwner},
		// The two approve rows share (Kind, From, Transition) and differ
		// only by quorum arithmetic a static lookup cannot perform, so
		// they stay dynamic — but each now declares the concrete state it
		// produces. {decision approved} reaches RestingStates through the
		// second row and through nothing else: no decision row carries
		// StateApproved as a literal To.
		{Kind: KindDecision, From: StateProposed, Transition: TApprove, To: StateDynamic, Role: RoleApprover, Scenario: "quorum-not-reached", Outcomes: []State{StateProposed}},
		{Kind: KindDecision, From: StateProposed, Transition: TApprove, To: StateDynamic, Role: RoleApprover, Scenario: "quorum-reached", Outcomes: []State{StateApproved}},
		{Kind: KindDecision, From: StateProposed, Transition: TReject, To: StateRejected, Role: RoleApprover},
		// The proposer's own exits. Without them a decision whose required
		// approvers have all left the space can never leave `proposed`:
		// approve and reject belong to the approvers, and pendency correctly
		// transfers the obligation to the sender in that case
		// (pendency.go:177, "the sender owes a cancel or re-route decision
		// instead") — a verdict the table could not honour, because the
		// sender had no legal move at all.
		//
		// Withdraw and supersede say different things and both are needed:
		// "no longer needed" and "replaced by this other decision". Making a
		// proposer misstate one as the other is the substitution this epic
		// exists to end.
		{Kind: KindDecision, From: StateProposed, Transition: TWithdraw, To: StateWithdrawn, Role: RoleOwner},
		{Kind: KindDecision, From: StateProposed, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner},
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
		{Kind: KindHandoff, From: StateDraft, Transition: TSubmit, To: StateSubmitted, Role: RoleOwner},
		{Kind: KindHandoff, From: StateSubmitted, Transition: TAcknowledge, To: StateAcknowledged, Role: RoleTarget},
		{Kind: KindHandoff, From: StateAcknowledged, Transition: TVerifyPass, To: StateAccepted, Role: RoleTarget},
		{Kind: KindHandoff, From: StateAcknowledged, Transition: TVerifyFail, To: StateRejected, Role: RoleTarget},
		{Kind: KindHandoff, From: StateRejected, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner},
		// The producer's exits before verification. A handoff carries
		// committed payload bytes, and until now the producer could not
		// withdraw or replace one once submitted: every exit from
		// `submitted` and `acknowledged` belonged to the receiver. When the
		// receiver leaves, pendency names the producer and the table had
		// nothing for them.
		{Kind: KindHandoff, From: StateSubmitted, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner},
		{Kind: KindHandoff, From: StateAcknowledged, Transition: TSupersede, To: StateSuperseded, Role: RoleOwner},
	}
}

// 3.4.6 response (attached exchange) — its own minimal submit lifecycle
// (no create row; see TCreate's own doc comment), plus the closure-model
// verify/dispute rows (D-024). Role on
// verify/dispute is documented as RoleOwner but is resolved specially
// (against the PARENT's From, not a response's own From) by
// applyResponseScoped — the one place fold's subject resolution branches
// on transition name (spec's own callout), never modeled as a distinct
// Role value.
//
// 2026-08-09 amendment ("the disputed exit is DELETED, not routed to",
// spec 06): a {StateDisputed, TSupersede -> StateSuperseded, RoleOwner} row
// shipped here on 2026-08-08 to give a disputed producer an exit. P8's
// tagged conformance matrix then found no shipped verb ever reaches it — a
// response carries no separate envelope in fold's model, so its closure
// state is sub-state on the PARENT's Result.Responses, and the only reader
// of that sub-state (applyResponseScoped) is scoped by its own doc comment
// to verify/dispute alone. `a2a supersede <XS-id>` was refused with LFC-001
// regardless of the row (epic-backlog B8). The row is DELETED rather than
// routed to: widening applyResponseScoped's branch would make a response's
// own state independently addressable, the model change P0 and P8 both
// rest on not happening. The exit already exists one level up — the row
// directly above (StateSubmitted, TDispute -> StateDisputed) is what CREATES
// the disputed state, and it stays: dispute reopens the PARENT to
// in_progress, where the responder is RoleTarget and respond is legal.
func responseRows() []Row {
	return []Row{
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

// dynamicKey names a (Kind, Transition) pair whose target state the table
// cannot carry, because resolving it needs facts only the fold holds —
// unblock's pre-block recovery, decision approve's quorum arithmetic.
type dynamicKey struct {
	Kind       Kind
	Transition string
}

// dynamicResolver applies one dynamic transition, replacing the generic
// table lookup for it. Every resolver takes the same arguments as the
// generic path so the dispatch in applyPrimaryScoped stays uniform.
type dynamicResolver func(kind Kind, env Envelope, result *Result, event Event, membership MembershipView)

// dynamicResolvers is the dispatch half of the Outcomes declaration above:
// which function resolves each dynamic row. Its key set MUST equal the set
// of (Kind, Transition) pairs carried by rows with a non-empty Outcomes —
// asserted by table_test.go, in BOTH directions, so a dynamic row without
// a resolver and a resolver without a row are each a red test rather than
// a silent misfold.
//
// Declared here, beside the rows, because it and Outcomes are two halves
// of one fact. Until this existed, applyPrimaryScoped carried the same two
// transition names buildTable did, mirrored, and neither knew why.
var dynamicResolvers = map[dynamicKey]dynamicResolver{
	{Kind: KindQuestion, Transition: TUnblock}:    applyUnblock,
	{Kind: KindWorkRequest, Transition: TUnblock}: applyUnblock,
	{Kind: KindDecision, Transition: TApprove}:    applyApprove,
}

// transitionTable is the generic (kind, fromState, transition) -> (toState,
// role) lookup used by every row EXCEPT the dynamic ones (unblock;
// decision approve), which dedicated logic in fold.go resolves — those
// are deliberately excluded here so the generic path never sees a
// StateDynamic sentinel.
var transitionTable = buildTable()

// roleTable answers the one question the PRE-write gate actually asks —
// "who may attempt this transition from this state" — for EVERY row,
// dynamic ones included. It exists because CheckCandidate needs no target
// state at all: a verdict is (is there a row) plus (may this actor act),
// and the dynamic rows carry both facts in From and Role already.
//
// Before this, CheckCandidate carried its own copy of the same two verb
// names buildTable and applyPrimaryScoped did. That is the mirror that
// matters most: with the property-keyed dispatch on the post-write side
// only, a third dynamic transition would be APPLIED by Apply and REFUSED
// by CheckLegality. This file's own comment at legality.go:88-98 records
// that exact divergence happening for broadcast-ack — every `a2a ack` on
// an announcement refused LFC-001 while the fold stood ready to apply the
// event — because the rule was shipped twice and the stricter copy won
// silently.
var roleTable = buildRoleTable()

func buildRoleTable() map[tableKey]Role {
	m := make(map[tableKey]Role, len(rows))
	for _, r := range rows {
		key := tableKey{Kind: r.Kind, From: r.From, Transition: r.Transition}
		if existing, ok := m[key]; ok {
			// Two rows sharing a key is legitimate here and NOT in
			// transitionTable: the two decision `approve` rows differ only
			// by the quorum arithmetic that picks between their outcomes,
			// and both name the same authorized role. Two rows disagreeing
			// about WHO may act is a real contradiction in the table —
			// programmer error at package init, same class as the
			// duplicate-key panic above.
			if existing != r.Role {
				panic("fold: rows disagree about the authorized role for " + string(r.Kind) + "/" + string(r.From) + "/" + r.Transition + ": " + string(existing) + " vs " + string(r.Role))
			}
			continue
		}
		m[key] = r.Role
	}
	return m
}

func buildTable() map[tableKey]tableEntry { return buildTableFrom(rows) }

// buildTableFrom takes the row slice as an argument so a test can prove
// the exclusion is property-keyed by handing it a row set the package does
// not actually carry — a third dynamic transition, which used to panic
// here at init on a duplicate key. Testing that against the package-level
// `rows` would require adding the row for real.
func buildTableFrom(rs []Row) map[tableKey]tableEntry {
	m := make(map[tableKey]tableEntry, len(rs))
	for _, r := range rs {
		// The exclusion is keyed on the PROPERTY that makes a row
		// unlookupable, not on the two verb names that happen to have it
		// today. A third dynamic transition used to panic here on a
		// duplicate key the moment it was added; now it is excluded for
		// the reason it should always have been.
		if len(r.Outcomes) > 0 {
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
