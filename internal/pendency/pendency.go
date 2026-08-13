// Package pendency is the single home for "whose move is it" (spec's I7):
// one relation over (artifact, system), computed once, that every surface
// asks instead of restating its own version of the rule.
//
// The relation reads no clock and no registry. Every row resolves from
// envelope facts the caller already has (kind, state, from, normalized to,
// broadcast, ack_requested, the folded per-recipient ack set, required
// approvers, recorded approvals, the active manifest participant set, and
// the parent envelope's from for a response) — never a live clock and never
// consumes.yaml. The two rows that would have needed those (contract
// deprecated's migration deadline and its retire-readiness gate) defer to
// POL-006 (internal/validate/policy_retire.go) instead of restating it
// here: this package answers "who owes the next move", not "has the
// deadline passed".
package pendency

import (
	"errors"
	"fmt"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// Input carries only facts a caller already has — no clock read, no
// registry read, no import of internal/cache.
type Input struct {
	Kind  fold.Kind
	State fold.State

	From string   // envelope `from` (owner / requester / author / producing system)
	To   []string // normalized `to`

	Broadcast    bool // true when `to` addressed "all"
	AckRequested bool // announcement's ack_requested flag

	Acks      map[string]bool // folded per-recipient ack set (D-025), keyed by acking system
	Approvals map[string]bool // recorded decision approvals, keyed by approving system

	RequiredApprovers  []string // decision's required approver set
	ActiveParticipants []string // ACTIVE manifest participant systems (broadcast expansion)

	// ExtraAddressees are systems the CALLER has established are addressed
	// by this artifact through a fact this package cannot see. It exists
	// for exactly one shipped case: P4 Edge 3's late adopter — a consumer
	// that adopted a contract AFTER a deprecation announcement's `to:` was
	// frozen is owed the same acknowledge as everyone in `to:`, but the
	// only evidence is that system's own `consumes.yaml`.
	//
	// The split is the same one internal/validate/policy_retire.go draws
	// and for the same reason: the RULE ("an addressed system that has not
	// acknowledged owes an acknowledge") lives here, once; the FACT ("who
	// is addressed") is resolved by the caller that can do I/O. Letting the
	// fact in through the front door is what keeps the rule from being
	// re-implemented at the call site — which is the defect P11 W1 exists
	// to remove, and which this field replaced when it was found there.
	//
	// Known incompleteness, and it is in the fact rather than the rule:
	// internal/cache resolves this from ONE system's registry
	// (myDependencyContracts reads the local system's consumes.yaml), so a
	// verdict asked about somebody ELSE's late adoption is silently narrow.
	// A caller that can read every participant's registry may hand in the
	// full set without any change here.
	ExtraAddressees []string

	// HasFulfillingResponse says a response artifact naming this artifact as
	// its parent has been submitted. It is set only for a requirement, and it
	// is a caller-resolved FACT for the same reason ExtraAddressees is: the
	// evidence lives on a DIFFERENT artifact, and this package answers about
	// one at a time.
	//
	// The requirement's acknowledged state is the only row that needs it, and
	// it needs it because the domain splits there (03-domain.md §3.4.2, actor
	// column: "target publishes, requester verifies — satisfy event is
	// requester's"). Before a response exists the target owes the work; after
	// it exists the requester owes `satisfy`. Collapsing the two is how this
	// table shipped a row telling the target to emit an event fold refuses
	// from it.
	HasFulfillingResponse bool

	// LeftParticipants are systems whose manifest membership status is
	// `left` — a caller-resolved FACT for the same reason ExtraAddressees
	// is (this package reads no manifest), and the input CC-062 needs.
	//
	// A `left` system cannot write to the space, so a debt named on it can
	// never be cleared by any legal act; the surfaces would show a frozen
	// row with an owner who has no move. internal/validate already draws
	// exactly this exclusion for exactly this reason — policy_retire.go's
	// RegisteredConsumer.Left, "excluded from the ack set entirely, they
	// never block retire and are never counted as un-acked" (§5.4 bullet
	// (a)). Before this field, pendency and that policy disagreed about the
	// same consumer.
	//
	// EMPTY MEANS "no one has left, OR the caller cannot see membership" —
	// the two are not distinguished, deliberately. A caller with no manifest
	// gets exactly today's behaviour rather than having every owner
	// silently dropped; internal/cache, the caller that has the manifest,
	// populates it. Named here rather than hidden because it is the one
	// fail-open in this package.
	LeftParticipants []string

	// ParentFrom is the PARENT envelope's `from`, populated only when
	// Kind is fold.KindResponse — verify/dispute resolve against the
	// parent's envelope, never the response's own (domain 3.4.6).
	ParentFrom string

	// ParentDisputeReopenFailed says the dispute event that put THIS
	// response into `disputed` failed to reopen its parent — fold.go's own
	// D-024 comment: "if the parent is not currently responded (e.g.
	// already closed, or reopened by an earlier dispute), this
	// parent-level effect is itself illegal and is flagged separately
	// (FlagIllegalTransition) — the response-level disputed marking above
	// still stands regardless." A caller-resolved FACT for the same reason
	// ParentFrom is (this package reads no fold.Result.Flags): internal/
	// cache's mirror already computes Flags per artifact and can see
	// whether THIS dispute event's own reopen attempt raised one.
	//
	// It exists so (response, disputed)'s nobodyRow can say the honest
	// thing (spec 06's 2026-08-09 amendment): ordinarily "the debt is on
	// the parent that the dispute reopened" is true and the Why says so.
	// When the reopen itself failed, that sentence would name a discharge
	// that never happened — nobody owes on this response AND nobody
	// inherited the debt on a parent that was never actually reopened.
	//
	// EMPTY (false) MEANS "the reopen succeeded, OR the caller cannot see
	// the flag" — the same fail-open discipline LeftParticipants and
	// BlockedByOwner already document: a caller with no flag visibility
	// gets today's ordinary behaviour, never a synthesized refusal.
	ParentDisputeReopenFailed bool

	// DeliveryUnresolvable says a response fulfilling THIS question or
	// work_request claimed `result: delivered` while the bytes it names
	// are absent from the space's own mirror — a caller-resolved FACT for
	// the same reason ParentFrom and BlockedByOwner are (this package
	// reads no registry, no filesystem, and no fold.Result.Flags; it
	// starts nothing on its own): internal/cache resolves it by
	// correlating the response back to the handoff that names the
	// package it claims (a response carries no package reference of its
	// own — spec 04's 2026-08-09 restatement), then asking the SAME
	// presence check `a2a data fetch` already runs
	// (internal/cache/packageresolver.go's mirrorPackageResolver).
	//
	// AC9 (spec 06 §8, from the live incident fb-20260808-d5740f): a
	// delivery reaches a space as two independent pull requests — the
	// payload and the response that announces it — and nothing couples
	// their outcomes. The response can merge while the payload stays open
	// and red, and the computed next move must not then tell the sender
	// it owes verification/close over an obligation nobody actually
	// discharged. The (question|work_request, responded) row is the one
	// that splits on it: ordinarily the SENDER owes close; when this is
	// true, the RESPONDER owes a fresh respond instead — fold.CheckLegality
	// admits `respond` from `responded` (table.go's own StateResponded row,
	// RoleTarget), so unlike blockedRow/the orphaned-counterparty branch
	// this is a real, legal transition of THIS artifact, not the "owners,
	// no Expected" third shape.
	//
	// EMPTY (false) MEANS "the delivery resolved, OR the caller cannot see
	// the fact, OR this response never claimed delivered at all" — the
	// same fail-open discipline every other caller-resolved fact in this
	// struct documents: a caller with no visibility gets today's ordinary
	// behaviour (the sender owes close), never a synthesized refusal.
	DeliveryUnresolvable bool

	// OperationalDebtOwed is P5 AC1's derivation (specs/05-declared-nature.md,
	// "The P-1 problem, stated honestly"): true when the CALLER has already
	// established that a published contract has a registered consumer AND
	// the space's own floor clears the derivation — a caller-resolved FACT
	// for the same reason ExtraAddressees/LeftParticipants are (this
	// package reads no registry and no floor config; §8 AC1's own words:
	// "the derivation is GATED ON THE SPACE'S OWN FLOOR").
	//
	// It is set from registration and publication ALONE, never from
	// `x_operational` — reading that field would reproduce exactly the
	// opt-in-field failure P-1 forbids ("The obligation must derive from
	// facts the system ALREADY holds, and the declaration must change what
	// those facts MEAN, not whether they exist... The implementor must not
	// weaken this into an opt-in field"). So this fires identically whether
	// the descriptor names an item `state: absent` or carries no
	// `x_operational` field at all — §6's testing row requires the
	// derivation to "pass with every new field absent", and this field is
	// how that requirement reaches the row: it is never conditioned on the
	// schema field internal/cache resolves it from `x_operational` for.
	//
	// EMPTY (false) MEANS "no registered consumer was found for this
	// contract's published major, OR the space is below floor, OR the
	// caller cannot resolve either fact" — the same fail-open discipline
	// every other caller-resolved fact in this struct documents: a caller
	// with no visibility gets today's ordinary behaviour (nobody owes
	// anything on a published contract), never a synthesized debt.
	OperationalDebtOwed bool

	// BlockedByOwner is the system this artifact's own facts say is
	// actually being waited on while it sits at `blocked` — a
	// caller-resolved FACT for the same reason ExtraAddressees and
	// LeftParticipants are (this package reads no registry and cannot
	// itself discover which party a `block` event's target meant):
	// internal/cache resolves it from a fulfilling response's own
	// `blocked_by.owner` (envelope/v2/response.schema.json) and hands it
	// in, never re-derived here.
	//
	// P1's US-3, transferred to this field: "as a blocked receiver, I do
	// not want to be told I owe `unblock` when the requester is what
	// blocks me". Today's row names the TARGET unconditionally, which is
	// an ATTRIBUTION failure, not an authorization one — the target IS
	// authorized to unblock, it just is not always who the artifact's own
	// facts say the wait is on.
	//
	// P-2 (never name a party with no legal move) is the caller's job,
	// not this package's: the caller has ALREADY checked the named system
	// against fold.CheckLegality before handing it in, against `note`
	// (RoleEitherParty) as the floor "has any legal move on this
	// artifact" bar — `unblock` itself is unblock's dynamic row's own
	// Role: RoleTarget always, so a named owner other than the target can
	// never legally issue it, and asking CheckLegality about `unblock`
	// specifically would refuse the exact case this field exists for. A
	// legality check against manifest membership needs the manifest,
	// which this package does not read; the caller does, the same way it
	// already resolves LeftParticipants from one.
	//
	// EMPTY MEANS "nothing named a blocker, OR the caller's legality
	// check refused the named party" — the two are not distinguished,
	// deliberately, the same fail-open LeftParticipants documents: either
	// way the artifact answers exactly as it does today, target owing
	// `unblock`, rather than silently naming nobody.
	BlockedByOwner string
}

// Verdict is one (artifact, system-set) answer: who the next move is
// owed by, what transition they owe, and why. An empty Owners set means
// nobody owes anything — Why is still populated, because "settled" is a
// claim that must be justified, never a fall-through.
//
// Owners MAY be non-empty while Expected is "": somebody owes something the
// §3.4 tables cannot name as a transition of THIS artifact. The requirement's
// acknowledged state is the shipped case — the target owes a published
// contract version and a response, and neither is a requirement transition
// (from `acknowledged` the table offers the target only `decline`). Naming
// nobody there would hide a real debt; naming a transition would name a move
// the tool refuses. Saying "you, but the act is not a move on this artifact"
// is the only honest third answer.
type Verdict struct {
	Owners       []string     // systems the next move is owed by; nil/empty means nobody
	Expected     string       // the transition owed; "" when Owners is empty
	Why          string       // the rule this verdict came from, always populated
	RuleIdentity RuleIdentity // stable identity of the (kind, state) row that answered

	// HumanGate names the §3.7 gate the owed transition sits behind ("G3"),
	// or "" when the owed move is one an agent makes on its own. Read from
	// fold.HumanGate — never a second list here.
	//
	// It is a FIELD rather than prose in Why because this product is
	// AI-first: an agent reading a verdict must be able to branch on "I
	// cannot self-serve this" without parsing a sentence. Its absence was a
	// real defect (spec 11 §18e/J3): decision/proposed told the approver's
	// system it owed `approve` while CC-021 says an agent doing exactly that
	// is ignored and flagged by the fold — the surface naming a move the
	// tool refuses, which is the one thing threads.md promises cannot
	// happen. §18d's own P-i row carried "(G3, human)" and it was dropped in
	// re-derivation; this is that qualifier restored.
	//
	// Note the owner is unchanged and deliberately so: the required
	// approver's SYSTEM is still who the move is owed by. The gate says how
	// the act must be produced (a PR under a human's own account), not that
	// nobody owes it — dropping the owner would hide a real, blocking debt.
	HumanGate string
}

// RuleIdentity names the exact pendency table row that produced a verdict.
// Why is diagnostic prose and may become more precise as scars are found; this
// value is the stable, typed lookup key a composer or catalogue can branch on.
type RuleIdentity string

// Known reports whether identity names a row in the current pendency table.
// The table remains the only registry: composers can reject an unknown typed
// identity without maintaining a second kind/state list.
func (identity RuleIdentity) Known() bool {
	for _, r := range table {
		if identity == r.identity {
			return true
		}
	}
	return false
}

// RuleIdentities returns the complete current row-identity set, sorted and
// freshly allocated. The table remains the source: adding a row cannot require
// a second hand-maintained identity list.
func RuleIdentities() []RuleIdentity {
	identities := make([]RuleIdentity, 0, len(table))
	for _, r := range table {
		identities = append(identities, r.identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	return identities
}

// ErrNoRow is returned by Resolve when no row exists for the given
// (Kind, State) pair — a lookup miss is an explicit, named refusal, never
// a silent zero-value Verdict.
var ErrNoRow = errors.New("pendency: no row for (kind, state)")

// Resolve answers "whose move is it" for one artifact, from the facts in
// in. It reads no clock and no registry.
func Resolve(in Input) (Verdict, error) {
	r, ok := table[key{Kind: in.Kind, State: in.State}]
	if !ok {
		return Verdict{}, fmt.Errorf("%w: kind=%q state=%q", ErrNoRow, in.Kind, in.State)
	}

	owners := dedupSorted(r.who(in))

	// CC-062, applied to every row rather than to one kind: a debt named on
	// a system that has LEFT the space can be cleared by no legal act,
	// because that system can no longer write. The row itself is right about
	// who WOULD owe the move; membership is what makes the answer useless.
	// So the orphans are dropped from Owners, and when dropping them empties
	// the set the pendency TRANSFERS TO THE SENDER as a cancel/re-route
	// decision — spec 11 §18d's own qualifier, and AC-102.2's
	// `orphaned-counterparty`.
	//
	// The transfer uses the table's documented third verdict shape (owners,
	// no Expected): "cancel or re-route" is a judgement about the exchange,
	// and which transition carries it differs by kind — naming one here
	// would be this table inventing a move again, which is exactly the §15
	// defect. Saying "you, and the act is not a move on this artifact" is
	// the honest answer the shape exists for.
	if orphans := intersect(owners, in.LeftParticipants); len(orphans) > 0 {
		owners = without(owners, orphans)
		if len(owners) == 0 {
			return Verdict{
				Owners:       owner(in),
				Expected:     "",
				RuleIdentity: r.identity,
				Why: "orphaned counterparty (CC-062): " + joinSystems(orphans) +
					" left the space and can no longer write, so the " + r.expected +
					" this row names can never land; the sender owes a cancel or re-route decision instead",
			}, nil
		}
	}

	if len(owners) == 0 {
		why := r.why
		if r.onEmpty != nil {
			why = r.onEmpty(in)
		}
		return Verdict{Owners: nil, Expected: "", Why: why, RuleIdentity: r.identity}, nil
	}
	expected, why := r.expected, r.why
	if r.expectedFor != nil {
		expected = r.expectedFor(in)
	}
	if r.whyFor != nil {
		why = r.whyFor(in)
	}
	return Verdict{
		Owners:       owners,
		Expected:     expected,
		Why:          why,
		RuleIdentity: r.identity,
		// Asked of fold, never listed here — the whole reason
		// fold.HumanGate exists is that this fact already had two
		// hand-maintained homes.
		HumanGate: fold.HumanGate(expected),
	}, nil
}

// key is the table's lookup key: one (kind, fromState) subject, exactly
// the pair internal/fold.SubjectStates enumerates.
type key struct {
	Kind  fold.Kind
	State fold.State
}

// row is one table entry: the resolver that names who owes the move, the
// transition owed when someone does, the rule's rationale, and (for
// resolvers that can legitimately answer "nobody" even though the row is
// not a settled/terminal state — a missing envelope fact, or an
// already-satisfied condition) the degraded rationale to use instead.
type row struct {
	who      resolver
	expected string
	why      string
	identity RuleIdentity
	onEmpty  func(Input) string // nil for rows that are unconditionally "nobody"

	// expectedFor/whyFor override expected/why for a row whose answer
	// genuinely depends on a fact rather than only on (kind, state). Only
	// the requirement's acknowledged row needs them today; keeping them nil
	// everywhere else means the table still reads as a table.
	expectedFor func(Input) string
	whyFor      func(Input) string
}

// resolver is one of the seven named resolvers this package uses — no
// others: nobody, owner, target, parentOwner, unackedTargets,
// pendingApprovers, contractActivationOwner. (blockedRow and respondedRow
// additionally define their own inline, single-use resolvers — see each
// row constructor's own doc comment.)
type resolver func(Input) []string

func nobody(Input) []string { return nil }

// owner resolves to the envelope's own `from` — the same resolver used
// for the table's "sender" rows (question/work_request `responded`,
// handoff `rejected`'s producer), which are the owner's own `from` too.
func owner(in Input) []string {
	if in.From == "" {
		return nil
	}
	return []string{in.From}
}

// target resolves to the envelope's normalized `to`.
func target(in Input) []string {
	if len(in.To) == 0 {
		return nil
	}
	return append([]string(nil), in.To...)
}

// parentOwner resolves to the PARENT envelope's `from` (response's
// verify/dispute, domain 3.4.6).
func parentOwner(in Input) []string {
	if in.ParentFrom == "" {
		return nil
	}
	return []string{in.ParentFrom}
}

// unackedTargets resolves to the addressed set minus whoever has already
// acknowledged (D-025's per-recipient ack set); a broadcast expands to
// the active manifest participants except the author.
//
// AckRequested gates the `to:`-matched half ONLY. This is two of the
// architect's rows sharing one resolver, and they carry different
// qualifiers (spec 11 §18d, §18e/J1):
//
//   - P-m — "XA published, ack_requested, recipient without a folded ack".
//     Gated on the flag: domain 3.4.7 says delivery completes on publish and
//     a plain announcement requires no acknowledgement.
//   - P-n — "XA deprecation: any currently-member REGISTERED consumer
//     without an ack, REGISTRY-matched rather than `to:`-matched". No flag
//     qualifier anywhere in its statement, and none in what reads the same
//     fact: 03-domain.md:115 conditions retire on "all registered consumers
//     acked (`left` systems excluded)", and internal/validate's
//     CheckRetirePrecondition implements exactly that with no ack_requested
//     gate at all.
//
// Collapsing them behind one early return is how this shipped: a deprecation
// announcement without the flag resolved to "nobody owes anything" while
// POL-006 was refusing its retire on the very consumer being told it owed
// nothing — two surfaces of one binary answering one question differently.
// The same early return also swallowed ExtraAddressees, so P4 Edge 3's
// late-adopter recovery never ran on a flagless announcement either.
//
// So the flag zeroes `targets` and nothing else; the caller-resolved
// registry half is unioned after it and survives.
func unackedTargets(in Input) []string {
	targets := in.To
	if in.Broadcast {
		targets = in.ActiveParticipants
	}
	if !in.AckRequested {
		targets = nil
	}
	// A union, never a replacement: a system that WAS in `to:` and has
	// since dropped the dependency must not watch the announcement stop
	// owing it — internal/cache/inbox.go's addressedToMe draws the same
	// union for the same reason ("disappearing messages are not an
	// acceptable price for fixing a race in an append-only system").
	targets = append(append([]string(nil), targets...), in.ExtraAddressees...)
	var out []string
	for _, t := range targets {
		// "all" is the broadcast sentinel itself (internal/cache's
		// normalizeTo passes it through verbatim, decode.go:123-134) —
		// skipped here as a defensive no-op in case a caller's `To`
		// still carries it despite setting Broadcast, since Broadcast
		// is what actually selects ActiveParticipants above.
		if t == "" || t == "all" || t == in.From {
			continue
		}
		if in.Acks[t] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// contractActivate names the transition a published contract's operational
// debt is owed as (26A's `a2a contract activate`, spec 05's 2026-08-10
// amendment: "activation {version, status: live, satisfies[], note?} +
// transition activate | event (event/v2, conditional block like
// publication)"). It is a plain string, not a fold.T* constant, because
// internal/fold's transition table (off-limits to this wave) carries no
// `activate` row at all: activation is deliberately NOT a contract
// lifecycle state transition (draft/published/deprecated/retired) — it is
// a side fact about a published version's operational readiness, the same
// reasoning `contract adopt`'s own consumes.yaml write already applies
// (also un-evaluated by fold). The literal below matches the one
// cmd_contract.go's runActivate writes onto the committed event
// (Transition: "activate") and event/v2's own transition enum member.
const contractActivate = "activate"

// contractActivationOwner resolves to the contract's own `from` (the
// producer — the only party who can clear this debt, spec 05 §11's
// "producer departure" concern) when the CALLER has established
// OperationalDebtOwed; nil otherwise, which lets Resolve's ordinary
// "owners empty" path answer with today's unconditional "nobody" text via
// contractPublishedRow's onEmpty.
func contractActivationOwner(in Input) []string {
	if !in.OperationalDebtOwed {
		return nil
	}
	return owner(in)
}

// pendingApprovers resolves to the decision's required approvers who
// have not yet recorded an approval (domain 3.4.4's quorum gate).
func pendingApprovers(in Input) []string {
	var out []string
	for _, a := range in.RequiredApprovers {
		if a == "" {
			continue
		}
		if in.Approvals[a] {
			continue
		}
		out = append(out, a)
	}
	return out
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// intersect returns the members of owners that also appear in other,
// sorted and deduplicated. Used for CC-062's orphan detection only.
func intersect(owners, other []string) []string {
	if len(owners) == 0 || len(other) == 0 {
		return nil
	}
	in := make(map[string]bool, len(other))
	for _, s := range other {
		if s != "" {
			in[s] = true
		}
	}
	var out []string
	for _, o := range owners {
		if in[o] {
			out = append(out, o)
		}
	}
	return dedupSorted(out)
}

// without returns owners minus drop, preserving owners' order (already
// sorted by dedupSorted at the call site).
func without(owners, drop []string) []string {
	if len(drop) == 0 {
		return owners
	}
	gone := make(map[string]bool, len(drop))
	for _, s := range drop {
		gone[s] = true
	}
	var out []string
	for _, o := range owners {
		if !gone[o] {
			out = append(out, o)
		}
	}
	return out
}

// joinSystems renders a system list for a Why string — deterministic,
// because a verdict's rationale is compared in tests and read by agents.
func joinSystems(systems []string) string {
	switch len(systems) {
	case 0:
		return ""
	case 1:
		return systems[0]
	}
	out := systems[0]
	for _, s := range systems[1:] {
		out += ", " + s
	}
	return out
}

// --- row constructors, one per named resolver ---

func nobodyRow(why string) row {
	return row{who: nobody, expected: "", why: why}
}

func ownerRow(expected, why string) row {
	return row{
		who:      owner,
		expected: expected,
		why:      why,
		onEmpty: func(Input) string {
			return "no owner (`from`) was recorded on this envelope, so the " + expected + " it would owe cannot be attributed"
		},
	}
}

func targetRow(expected, why string) row {
	return row{
		who:      target,
		expected: expected,
		why:      why,
		onEmpty: func(Input) string {
			return "no target (`to`) was recorded on this envelope, so the " + expected + " it would owe cannot be attributed"
		},
	}
}

// blockedRow is the question/work_request `blocked` row (P1's US-3,
// transferred). Absent a caller-resolved BlockedByOwner it is byte-identical
// to the old unconditional targetRow(fold.TUnblock, ...): the target owns
// the move, because unblock's dynamic row (table.go) is Role: RoleTarget
// always and no other system can legally issue it.
//
// When BlockedByOwner IS present, Owners names it instead — but Expected is
// deliberately "", the same third verdict shape the orphaned-counterparty
// branch and the requirement/acknowledged row already use (Verdict's own
// doc): the named owner cannot legally call `unblock` either (that
// transition stays the target's alone), so naming it as Expected would be
// exactly the "surface names a move the tool refuses" failure the authority
// gate exists to catch. Naming nobody would hide a real debt instead — the
// artifact's own facts say the wait is on this system, not the target.
func blockedRow() row {
	return row{
		who: func(in Input) []string {
			if in.BlockedByOwner != "" {
				return []string{in.BlockedByOwner}
			}
			return target(in)
		},
		expectedFor: func(in Input) string {
			if in.BlockedByOwner != "" {
				return ""
			}
			return fold.TUnblock
		},
		whyFor: func(in Input) string {
			if in.BlockedByOwner != "" {
				return "US-3: this artifact's own facts (a fulfilling response's blocked_by.owner) name " +
					in.BlockedByOwner + " as who the wait is actually on, legality-checked by the caller " +
					"before it reached here — but unblock stays the TARGET's own event (domain 3.4.3), so no " +
					"transition of THIS artifact is owed by " + in.BlockedByOwner + " either"
			}
			return "domain 3.4.3 makes unblock the target's own event; the referenced blocker is a separate artifact carrying its own pendency"
		},
		onEmpty: func(Input) string {
			return "no target (`to`) was recorded on this envelope, so the " + fold.TUnblock + " it would owe cannot be attributed"
		},
	}
}

// respondedRow is the question/work_request `responded` row (AC9, spec 06
// §8's 2026-08-09 amendment). Absent a caller-resolved DeliveryUnresolvable
// it is byte-identical to what the table used to say unconditionally —
// ownerRow(fold.TClose, ...), then spelled `senderRow`, a one-line alias
// this row's arrival left with no other caller and which is now gone: the
// sender (envelope `from`) owes verification then close.
//
// When DeliveryUnresolvable IS true, Owners names the TARGET instead and
// Expected is fold.TRespond, not "" — unlike blockedRow's own conditional
// branch, this is a REAL, legal transition of THIS artifact for the named
// owner (table.go's StateResponded row admits TRespond, Role: RoleTarget),
// so naming it does not repeat the "surface names a move the tool refuses"
// defect the third verdict shape exists to avoid. Telling the sender it
// owes close over a delivery nobody can retrieve would be the lie AC9
// exists to remove; naming nobody would hide that the responder still owes
// bytes it never actually produced.
func respondedRow() row {
	return row{
		who: func(in Input) []string {
			if in.DeliveryUnresolvable {
				return target(in)
			}
			return owner(in)
		},
		expectedFor: func(in Input) string {
			if in.DeliveryUnresolvable {
				return fold.TRespond
			}
			return fold.TClose
		},
		whyFor: func(in Input) string {
			if in.DeliveryUnresolvable {
				return "AC9 (spec 06 §8): the fulfilling response claims `delivered`, but the bytes it names " +
					"are absent from this space's mirror (fb-20260808-d5740f) — the RESPONDER owes a fresh " +
					"respond, not the sender a close over a delivery nobody can retrieve"
			}
			return "the answer landed; the sender owes verification then close, or a dispute"
		},
		onEmpty: func(in Input) string {
			if in.DeliveryUnresolvable {
				return "no target (`to`) was recorded on this envelope, so the " + fold.TRespond + " it would owe cannot be attributed"
			}
			return "no owner (`from`) was recorded on this envelope, so the " + fold.TClose + " it would owe cannot be attributed"
		},
	}
}

// contractPublishedRow is P5 AC1's own row (spec 05, "The P-1 problem,
// stated honestly"): a published contract owed nobody unconditionally
// until this wave. When the CALLER establishes OperationalDebtOwed (a
// registered consumer against this contract's published major, above the
// space's own floor — cache.FindRegisteredConsumersForMajor,
// caller-resolved for the same reason every other registry-derived fact
// in Input is), the producer owes `activate`. Absent that fact, this is
// byte-identical to the row's old unconditional nobodyRow: "alive and
// settled: the owner MAY publish a successor or deprecate, but neither is
// a move anyone waits for" — §8 AC1's own words, "below it [the floor],
// no derived row is emitted and the thread reads exactly as it does
// today."
func contractPublishedRow() row {
	return row{
		who:      contractActivationOwner,
		expected: contractActivate,
		why: "P5 AC1 (specs/05-declared-nature.md): a registered consumer is " +
			"building against this contract's published major, so the operational " +
			"half is owed from registration and publication alone — the debt is " +
			"the same whether x_operational names the gap `state: absent` or says " +
			"nothing at all (P-1: undeclared is itself the debt, never an opt-in " +
			"field); the producer owes activate",
		onEmpty: func(in Input) string {
			if in.OperationalDebtOwed {
				// contractActivationOwner only returns nil here because
				// owner(in) itself found in.From empty — the debt IS owed,
				// the producer just could not be attributed. Reusing the
				// unconditional "settled" text would misreport a real,
				// unattributed debt as nothing owed at all.
				return "no owner (`from`) was recorded on this contract's envelope, so the " + contractActivate + " it would owe cannot be attributed"
			}
			return "alive and settled: the owner MAY publish a successor or deprecate, but neither is a move anyone waits for"
		},
	}
}

func parentOwnerRow(expected, why string) row {
	return row{
		who:      parentOwner,
		expected: expected,
		why:      why,
		onEmpty: func(Input) string {
			return "the parent envelope's `from` could not be resolved, so the " + expected + " it would owe cannot be attributed"
		},
	}
}

func unackedTargetsRow(expected, why string) row {
	return row{
		who:      unackedTargets,
		expected: expected,
		why:      why,
		onEmpty: func(in Input) string {
			if !in.AckRequested {
				// Only honest once the registry half has also come back
				// empty — with no ack_requested AND no registry-matched
				// consumer there is genuinely nothing owed. A flagless
				// deprecation whose consumer IS registry-matched never
				// reaches here any more (§18e/J1).
				return "domain 3.4.7 — delivery completes on publish, no acknowledgement is required, and no registry-matched consumer is owed one either"
			}
			return "every addressed target has already acknowledged"
		},
	}
}

func pendingApproversRow(expected, why string) row {
	return row{
		who:      pendingApprovers,
		expected: expected,
		why:      why,
		onEmpty: func(in Input) string {
			if len(in.RequiredApprovers) == 0 {
				return "no required approvers were recorded on this decision, so no approval can be pending"
			}
			return "every required approver has already recorded an approval"
		},
	}
}

// table is the pendency relation, keyed by (kind, fromState). It is
// lead-authored from docs/the-plan/plan/03-domain.md 3.4.1-3.4.7 and is
// implemented literally, not re-derived or re-ordered.
var table = buildTable()

func buildTable() map[key]row {
	m := make(map[key]row, 40)

	// 3.4.1 contract
	m[key{fold.KindContract, fold.StateDraft}] = ownerRow(fold.TPublish,
		"an unpublished draft is work its author still owes")
	m[key{fold.KindContract, fold.StatePublished}] = contractPublishedRow()
	m[key{fold.KindContract, fold.StateDeprecated}] = nobodyRow(
		"migration is owed by consumers on the deprecation announcement's own ack set; retire-readiness (acks AND a passed sunset) is POL-006's gate, never a second copy here")

	// 3.4.2 requirement
	m[key{fold.KindRequirement, fold.StateDraft}] = ownerRow(fold.TPublish,
		"unpublished demand")
	m[key{fold.KindRequirement, fold.StatePublished}] = targetRow(fold.TAcknowledge,
		`domain 3.4.2: the target owes "seen"; decline is the same owed turn, refused`)
	m[key{fold.KindRequirement, fold.StateAcknowledged}] = row{
		who: func(in Input) []string {
			if in.HasFulfillingResponse {
				return owner(in)
			}
			return target(in)
		},
		expectedFor: func(in Input) string {
			if in.HasFulfillingResponse {
				return fold.TSatisfy
			}
			// Deliberately none: publishing a contract version and
			// submitting a response are acts on OTHER artifacts. See
			// Verdict's own doc comment.
			return ""
		},
		whyFor: func(in Input) string {
			if in.HasFulfillingResponse {
				return "a fulfilling response has landed, and `satisfy` is the REQUESTER's own event " +
					"(03-domain.md §3.4.2: \"target publishes, requester verifies — satisfy event is requester's\")"
			}
			return "the target owes a published contract version and a response; neither is a transition " +
				"of this requirement, so no move on THIS artifact is named — declining is the only " +
				"requirement row the target has from here, and it is a refusal, not the owed act"
		},
		onEmpty: func(Input) string {
			return "neither party could be attributed from this envelope"
		},
	}
	m[key{fold.KindRequirement, fold.StateSatisfied}] = nobodyRow(
		"settled; supersede is the owner's escape hatch")
	m[key{fold.KindRequirement, fold.StateDeclined}] = nobodyRow(
		"settled; the remedy is a new requirement")
	m[key{fold.KindRequirement, fold.StateWithdrawn}] = nobodyRow(
		"settled by the requester's own act")

	// 3.4.3 question / work_request — identical lifecycle.
	for _, k := range []fold.Kind{fold.KindQuestion, fold.KindWorkRequest} {
		m[key{k, fold.StateDraft}] = ownerRow(fold.TSubmit, "unsent")
		m[key{k, fold.StateSubmitted}] = targetRow(fold.TAcknowledge,
			`domain 3.4.3: the target owes "seen"`)
		m[key{k, fold.StateAcknowledged}] = targetRow(fold.TRespond,
			"the answer is what is owed; accept/start are optional granularity and the respond row admits acknowledged directly")
		m[key{k, fold.StateAccepted}] = targetRow(fold.TRespond,
			"committed to; the answer is outstanding")
		m[key{k, fold.StateInProgress}] = targetRow(fold.TRespond,
			"same, in flight")
		m[key{k, fold.StateBlocked}] = blockedRow()
		m[key{k, fold.StateResponded}] = respondedRow()
	}

	// 3.4.4 decision
	m[key{fold.KindDecision, fold.StateDraft}] = ownerRow(fold.TPropose,
		"not yet put to the parties")
	m[key{fold.KindDecision, fold.StateProposed}] = pendingApproversRow(fold.TApprove,
		"domain 3.4.4's quorum gate; reject is the same owed turn")
	m[key{fold.KindDecision, fold.StateApproved}] = nobodyRow(
		"settled; supersede by a new approved decision is an escape hatch")
	m[key{fold.KindDecision, fold.StateRejected}] = nobodyRow(
		"settled; the revision is a NEW XD on the thread, not a move owed on this one")

	// 3.4.5 handoff
	m[key{fold.KindHandoff, fold.StateDraft}] = ownerRow(fold.TSubmit,
		"not yet handed over")
	m[key{fold.KindHandoff, fold.StateSubmitted}] = targetRow(fold.TAcknowledge,
		`domain 3.4.5: the receiver owes "seen"`)
	m[key{fold.KindHandoff, fold.StateAcknowledged}] = targetRow(fold.TVerifyPass,
		"the receiver owes the stated verification — pass or fail, both are the same owed turn")
	m[key{fold.KindHandoff, fold.StateRejected}] = ownerRow(fold.TSupersede,
		"domain 3.4.5 says the producer resubmits as a new XH linking this one; here supersede is OWED, not an escape hatch — the one place the two coincide, and it is deliberate")

	// 3.4.6 response
	m[key{fold.KindResponse, fold.StateDraft}] = ownerRow(fold.TSubmit, "unsent")
	m[key{fold.KindResponse, fold.StateSubmitted}] = parentOwnerRow(fold.TVerify,
		"domain 3.4.6: verify/dispute resolve against the PARENT's envelope, never the response's own")
	// table.go carried a `supersede` escape hatch out of `disputed` from
	// 2026-08-08 to 2026-08-09 (matching decision `rejected` and handoff
	// `rejected`'s own exits); it was never an OWED act even while it
	// existed, because D-024's dispute ADDITIONALLY reopens the PARENT to
	// `in_progress`, and the (question|work_request, in_progress) row above
	// (targetRow(fold.TRespond, ...)) already sends the producer back
	// through a fresh `respond` — the practical remedy every
	// disputed-response scenario resolves to. 2026-08-09 deleted the row
	// (spec 06's amendment, epic-backlog B8): P8's tagged conformance
	// matrix proved no shipped verb ever reached it, so there is no longer
	// any exit on THIS artifact at all, owed or otherwise — closer now to
	// decision `rejected`'s own shape ("the revision is a NEW artifact on
	// the thread, not a move owed on this one") than to handoff
	// `rejected`'s genuinely-owed resubmission ever was.
	//
	// The Why is conditional (spec 06's 2026-08-09 amendment: "nobody owes
	// on this object — the debt is on the parent that the dispute
	// reopened. Where that parent-level reopen was itself refused..., the
	// Why says so and carries the refusal's flag"): fold.go's own D-024
	// comment records that the reopen can itself fail — "if the parent is
	// not currently responded (e.g. already closed, or reopened by an
	// earlier dispute), this parent-level effect is itself illegal and is
	// flagged separately (FlagIllegalTransition)" — and a Why that always
	// pointed to "the reopened parent" would lie in exactly that case: it
	// would name a discharge that never happened. ParentDisputeReopenFailed
	// is the caller-resolved fact (this package reads no Result.Flags)
	// that lets it say so instead.
	m[key{fold.KindResponse, fold.StateDisputed}] = row{
		who:      nobody,
		expected: "",
		why: "nobody owes on this object — the debt is on the parent that the dispute reopened: " +
			"D-024's reopen sends the producer back through a fresh respond there, not here",
		onEmpty: func(in Input) string {
			if in.ParentDisputeReopenFailed {
				return "nobody owes on this object, and there is no reopened parent to carry the debt " +
					"either — this dispute's own attempt to reopen the parent to in_progress was itself " +
					"refused (the parent was not `responded` when the dispute landed: already closed, or " +
					"already reopened by an earlier dispute), flagged FlagIllegalTransition on the " +
					"parent's own envelope (fold.go's D-024 comment)"
			}
			return "nobody owes on this object — the debt is on the parent that the dispute reopened: " +
				"D-024's reopen sends the producer back through a fresh respond there, not here"
		},
	}

	// 3.4.7 announcement
	m[key{fold.KindAnnouncement, fold.StateDraft}] = ownerRow(fold.TPublish, "unsent")
	m[key{fold.KindAnnouncement, fold.StatePublished}] = unackedTargetsRow(fold.TAcknowledge,
		"D-025's per-recipient ack set; a broadcast expands to active manifest participants except the author")

	// Resting-only states (AC10, spec 06 §8): every pair below is a member
	// of fold.RestingStates() — a subject of this kind CAN be found sitting
	// here — but no row in fold's table ever departs FROM it, so
	// fold.SubjectStates() never surfaces it and TestI8Totality never asks
	// about it. Each is a genuine terminal point of its kind's lifecycle:
	// the answer is "nobody", and it is an ANSWER, not a gap left for the
	// generic ErrNoRow fallback to narrate at runtime (the failure
	// internal/cache/inbox.go's resolveVerdict doc comment records: a
	// terminal artifact's lookup miss used to reach `a2a inbox --json` as
	// "this should be unreachable").
	m[key{fold.KindAnnouncement, fold.StateSuperseded}] = nobodyRow(
		"settled; the replacement is a new announcement, not a move owed on this one")
	m[key{fold.KindContract, fold.StateRetired}] = nobodyRow(
		"settled; POL-006's sunset already ran and drove this to retire — nothing further is owed")
	m[key{fold.KindDecision, fold.StateSuperseded}] = nobodyRow(
		"settled; replaced by a new approved decision — the successor carries its own pendency")
	m[key{fold.KindDecision, fold.StateWithdrawn}] = nobodyRow(
		"settled by the proposer's own act; the remedy is a new decision, not a move owed on this one")
	m[key{fold.KindHandoff, fold.StateAccepted}] = nobodyRow(
		"settled; verification passed and the handoff is accepted — nothing further is owed")
	m[key{fold.KindHandoff, fold.StateSuperseded}] = nobodyRow(
		"settled; replaced by a new handoff — the successor carries its own pendency")
	for _, k := range []fold.Kind{fold.KindQuestion, fold.KindWorkRequest} {
		m[key{k, fold.StateCancelled}] = nobodyRow(
			"settled by the sender's own act; nothing further is owed")
		m[key{k, fold.StateClosed}] = nobodyRow(
			"settled; the sender closed it after the answer landed")
		m[key{k, fold.StateDeclined}] = nobodyRow(
			"settled; the remedy is a new " + string(k) + ", not a move owed on this one")
		m[key{k, fold.StateSuperseded}] = nobodyRow(
			"settled; replaced by a new " + string(k) + " — the successor carries its own pendency")
	}
	m[key{fold.KindRequirement, fold.StateSuperseded}] = nobodyRow(
		"settled; replaced by a new requirement — the successor carries its own pendency")
	// response/superseded is NOT a row here: fold.table.go's
	// {response, disputed, supersede} row was its sole producer, and that
	// row was deleted 2026-08-09 (spec 06's amendment, epic-backlog B8) —
	// no shipped verb ever reached it. Deleting the row also removed this
	// pair from fold.RestingStates() entirely, so TestEveryRestingStateHas-
	// APendencyRow no longer asks about it; a row here now would be a claim
	// about a state no response can rest in.
	m[key{fold.KindResponse, fold.StateVerified}] = nobodyRow(
		"settled; the requester accepted the response as delivered — nothing further is owed")

	for k, r := range m {
		r.identity = RuleIdentity(string(k.Kind) + "/" + string(k.State))
		m[k] = r
	}

	return m
}
