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
	Owners   []string // systems the next move is owed by; nil/empty means nobody
	Expected string   // the transition owed; "" when Owners is empty
	Why      string   // the rule this verdict came from, always populated

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
				Owners:   owner(in),
				Expected: "",
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
		return Verdict{Owners: nil, Expected: "", Why: why}, nil
	}
	expected, why := r.expected, r.why
	if r.expectedFor != nil {
		expected = r.expectedFor(in)
	}
	if r.whyFor != nil {
		why = r.whyFor(in)
	}
	return Verdict{
		Owners:   owners,
		Expected: expected,
		Why:      why,
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
	onEmpty  func(Input) string // nil for rows that are unconditionally "nobody"

	// expectedFor/whyFor override expected/why for a row whose answer
	// genuinely depends on a fact rather than only on (kind, state). Only
	// the requirement's acknowledged row needs them today; keeping them nil
	// everywhere else means the table still reads as a table.
	expectedFor func(Input) string
	whyFor      func(Input) string
}

// resolver is one of the six named resolvers this package uses — no
// others: nobody, owner, target, parentOwner, unackedTargets,
// pendingApprovers.
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

// senderRow is ownerRow under the table's "sender" name — the resolver
// is the same envelope `from`; only the table's own prose distinguishes
// "owner" from "sender" by which lifecycle role is talking.
func senderRow(expected, why string) row {
	return ownerRow(expected, why)
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
	m[key{fold.KindContract, fold.StatePublished}] = nobodyRow(
		"alive and settled: the owner MAY publish a successor or deprecate, but neither is a move anyone waits for")
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
		m[key{k, fold.StateResponded}] = senderRow(fold.TClose,
			"the answer landed; the sender owes verification then close, or a dispute")
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
	// table.go's 2026-08-08 amendment gives the producer a `supersede`
	// escape hatch out of `disputed` (matching decision `rejected` and
	// handoff `rejected`'s own exits), but unlike those two it is not an
	// OWED act: D-024's dispute ADDITIONALLY reopens the PARENT to
	// `in_progress`, and the (question|work_request, in_progress) row
	// above (targetRow(fold.TRespond, ...)) already sends the producer
	// back through a fresh `respond` — the practical remedy every
	// disputed-response scenario resolves to. Naming supersede as owed
	// here would tell the producer twice, on two artifacts, for one
	// situation. Closer to decision `rejected`'s own shape ("the revision
	// is a NEW artifact on the thread, not a move owed on this one") than
	// to handoff `rejected`'s genuinely-owed resubmission.
	m[key{fold.KindResponse, fold.StateDisputed}] = nobodyRow(
		"settled from this artifact's own perspective: the parent's own in_progress row already " +
			"sends the producer back through a fresh respond; supersede is an available escape hatch on " +
			"THIS response, never an owed act")

	// 3.4.7 announcement
	m[key{fold.KindAnnouncement, fold.StateDraft}] = ownerRow(fold.TPublish, "unsent")
	m[key{fold.KindAnnouncement, fold.StatePublished}] = unackedTargetsRow(fold.TAcknowledge,
		"D-025's per-recipient ack set; a broadcast expands to active manifest participants except the author")

	return m
}
