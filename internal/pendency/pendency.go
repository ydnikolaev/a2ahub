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

	// ParentFrom is the PARENT envelope's `from`, populated only when
	// Kind is fold.KindResponse — verify/dispute resolve against the
	// parent's envelope, never the response's own (domain 3.4.6).
	ParentFrom string
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
	return Verdict{Owners: owners, Expected: expected, Why: why}, nil
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
// the active manifest participants except the author. When
// AckRequested is false it yields nobody unconditionally — delivery
// completes on publish, no acknowledgement is required (domain 3.4.7).
func unackedTargets(in Input) []string {
	if !in.AckRequested {
		return nil
	}
	targets := in.To
	if in.Broadcast {
		targets = in.ActiveParticipants
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
				return "domain 3.4.7 — delivery completes on publish, no acknowledgement is required"
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
		m[key{k, fold.StateBlocked}] = targetRow(fold.TUnblock,
			"domain 3.4.3 makes unblock the target's own event; the referenced blocker is a separate artifact carrying its own pendency")
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

	// 3.4.7 announcement
	m[key{fold.KindAnnouncement, fold.StateDraft}] = ownerRow(fold.TPublish, "unsent")
	m[key{fold.KindAnnouncement, fold.StatePublished}] = unackedTargetsRow(fold.TAcknowledge,
		"D-025's per-recipient ack set; a broadcast expands to active manifest participants except the author")

	return m
}
