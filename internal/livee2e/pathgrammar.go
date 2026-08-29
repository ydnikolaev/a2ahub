package livee2e

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// pathgrammar.go declares the conformance-path grammar (plan W3, docs/
// features/archive/agent-ops-2026-07/plans/11-authoring-path-and-seam-
// verification.plan.md, "W3 — the catalogue"). UNTAGGED on purpose,
// matching catalogue.go's own precedent (see that file's doc comment):
// declaring the intended coverage in plain code means it is visible,
// diffable and unit-testable BEFORE any driver exists, and a path
// dropped during implementation shows up as a not-run row rather than
// as silence.
//
// This file is DATA GRAMMAR ONLY — no path bodies, no driver. The next
// stage drives Path/Step sequences against the real `a2a` binary under
// the `livee2e` build tag; this package's own coverage gate
// (pathcoverage_test.go) is what proves the six declared families
// (pathcatalogue_paths.go) reach every fold.TransitionRows() triple they
// claim to, or names the honest gap.

// Path is one declared conformance path: a stable id, a one-line intent,
// an optional Precondition (another Path's id, whose end state this path
// continues from), and its ordered Steps.
//
// D4 (plan): independence is replay. A path with a Precondition does not
// skip straight to that path's end state — the runner (next stage)
// re-executes the WHOLE chain (the precondition's own precondition, and
// so on, then this path's own Steps) against a fresh space, so every
// path is runnable alone by name. This file's own resolution helpers
// (PathTransitions, chainEndStates) model that same replay purely, over
// declared data, to validate the grammar before any driver exists.
//
// Precondition inherits the FULL per-Kind ending-state map the chain
// produced, not a single named state — a superset of the plan's own
// "an optional id of the path whose end state is its precondition"
// wording, chosen because a path may legitimately continue more than one
// Kind's own state machine from its precondition (e.g. a path that picks
// up both a work_request AND a handoff a prior path created). See
// chainEndStates.
type Path struct {
	// ID is this path's stable identifier — referenced by a later path's
	// own Precondition field, and by the coverage gate's failure output.
	ID string
	// Intent is a one-line description of what this path proves and,
	// where relevant, why a step the domain narrative implies is absent
	// (an act with no fold transition — adoption, an ack on a broadcast —
	// is never a Step; see Step's own doc comment).
	Intent string
	// Precondition is another Path's ID whose end state this path
	// continues from, or "" for a path that starts fresh (its own first
	// Step per Kind must then be a fold.TCreate). A cycle (a path naming
	// itself, directly or transitively, as its own precondition) is a
	// grammar error the resolution helpers below refuse.
	Precondition string
	// Steps are this path's own ordered legs. Steps declared on an
	// EARLIER path in the Precondition chain are never repeated here —
	// the runner replays them; this path names only what IT adds.
	Steps []Step
	// RequiredApprovers is the quorum set a fold.KindDecision `approve`
	// step in THIS path's own Steps resolves against (row-labels,
	// livee2e.SystemA/SystemB — never a real system id, same namespace
	// Step.Actor lives in). Declared here, on the Path, because the
	// grammar has no other source for a decision's own required_approvers
	// set — resolveApprove (below) refuses an approve step on a path that
	// leaves this empty rather than inferring the quorum from which
	// actors happen to drive the steps present (§11.4's own "no bare
	// supersets" spirit: inferring the set from step order would silently
	// under-declare a quorum a real draft names explicitly,
	// draftfields.go's own `required_approvers=[me,peer]`). Empty for
	// every path that declares no decision-approve step — never
	// threaded through a Precondition chain (resolveApprove's own walk-
	// local approvals record, mirroring preBlock's own doc comment): no
	// declared path today needs a decision's approvals recovered across a
	// chain boundary.
	RequiredApprovers []string
}

// Step is one path leg: an actor drives one (Kind, Transition) forward,
// then the runner checks every Predicate against a shipped --json
// surface.
//
// Step deliberately does NOT declare the resulting state, and it does
// NOT declare a `From` state either — both are RESOLVED, never
// restated: `From` comes from this path's own running per-Kind state
// machine (see ResolveStep/PathTransitions), seeded from the
// Precondition chain's end states for a chained path, and StateNone for
// a fold.TCreate step regardless of what any OTHER instance of the same
// Kind reached elsewhere in the chain (a `create` step always starts a
// genuinely NEW artifact instance). The resulting state for an ordinary
// step comes from fold's own LegalNext table via ResolveStep — this is
// plan D3: a path cannot assert a transition the domain does not admit,
// and a table change surfaces as a failing path rather than as two
// documents quietly disagreeing. `create` is the one transition ResolveStep
// is never asked to resolve (see resolveCreate below and table.go's own
// TCreate doc comment, spec §18a, 2026-08-06): fold.table.go carries no
// StateNone row for ANY kind, because a committed create event's own
// fromState is a state Fold never occupies (Fold always starts a kind at
// NewResult(kind) = draft) — so a create row would document a transition
// the fold already refuses, and does not exist.
//
// `create`, `unblock` and fold.KindDecision's own `approve` are the THREE
// transitions this grammar resolves for itself rather than asking
// ResolveStep (ResolveStep's own doc comment still refuses every
// StateDynamic row it is ever handed, and now has no create row to find
// either — walkSteps intercepts all three BEFORE calling it, the same
// pattern for each). walkSteps special-cases fold.TUnblock, recovering the
// state its own EARLIER fold.TBlock step (same Kind, same walk) recorded
// as the pre-block state, mirroring fold.go's applyUnblock exactly
// (recomputed from the event sequence, never a second interpretation of
// the rule). This is deliberately narrow: a path may only `unblock` a Kind
// it `block`ed in its OWN Steps — pre-block state does NOT cross a
// Precondition boundary (chainEndStates discards it), so a path that
// blocks in its precondition and unblocks in its own Steps gets a named
// grammar error, not a silently wrong recovered state. No declared path
// needs the cross-boundary form today; widening it is a real future
// change, not an oversight.
//
// walkSteps ALSO special-cases (fold.KindDecision, fold.TApprove),
// mirroring fold.go's own applyApprove/quorumReached: quorum arithmetic
// against the path's own declared RequiredApprovers (Path's own doc
// comment), recorded per-approving-actor in a walk-local record (never
// crossing a Precondition boundary, same rule as pre-block state) — see
// resolveApprove below.
//
// A domain act that has NO fold transition at all — contract adoption
// (03-domain.md §10.5: "the closed transition enum has no `adopt`"), or
// an acknowledgement on a broadcast announcement (D-025's transition-
// free per-recipient ack, legalnext.go's own doc comment) — is never
// expressible as a Step. A path whose narrative includes one of these
// notes it in Intent instead; the driver (next stage) still performs the
// real CLI act around the declared Steps, but this grammar has nothing
// to validate it against, by design (there is no fold row to check it
// with).
type Step struct {
	// Actor is the system driving this step — livee2e.SystemA or
	// livee2e.SystemB (config.go), never a hardcoded system name.
	Actor string
	// Kind is the fold.Kind this step's transition acts on.
	Kind fold.Kind
	// Transition is the fold transition name (fold.TCreate, fold.TPublish,
	// ... — table.go's own consts) this step ATTEMPTS. For an ordinary
	// step this transition lands; for a step whose Refused is non-nil, it
	// is the transition the actor attempts and the product REFUSES (see
	// Refused's own doc comment) — never a transition that landed under a
	// different name.
	Transition string
	// Refused declares this step a REFUSAL rather than a landed
	// transition: the actor attempts (Kind, Transition) through the real
	// binary and the product MUST refuse it, naming Refused.Code. nil for
	// every ordinary step (the ONLY value before this field existed).
	//
	// A refused step is not an ordinary Step with a different ending
	// state — it performs NO transition at all, which has three
	// consequences a driver/grammar reader must hold together:
	//
	//  1. The act is attempted through the real binary and MUST fail — a
	//     non-zero exit whose combined output names Refused.Code. A
	//     refusal with the WRONG code is a failure, not a pass: "it
	//     refused for some reason" is exactly the confusion this
	//     catalogue exists to remove.
	//  2. State resolution INVERTS: because no transition landed, the
	//     state after this step is the state BEFORE it — walkSteps skips
	//     ResolveStep entirely for a refused step (there is no resulting
	//     state to resolve) and leaves the per-Kind state machine
	//     unadvanced, so every LATER step in the path still resolves
	//     against the state that actually obtains.
	//  3. The step earns NO transition-coverage credit: walkSteps does not
	//     append a refused step's (Kind, From, Transition) to the triples
	//     PathTransitions returns. Crediting it would let the coverage
	//     number grow by adding things the product refuses to do — worse
	//     than not counting them at all.
	//
	// A refused step's own Predicates are still checked normally (they
	// assert what a fresh read shows AFTER the refusal — typically that
	// nothing moved), unaffected by any of the above.
	Refused *Refusal
	// Predicates are asserted, in order, once this step's transition has
	// landed (or, for a refused step, once the refusal itself has been
	// confirmed) and the runner has re-read the affected surface(s).
	Predicates []Predicate
}

// Refusal is the payload of a refused Step (Step.Refused's own doc
// comment) — the ONE fact a refusal declares beyond "this must not land":
// the refusal code the real binary's combined output must name.
type Refusal struct {
	// Code is the expected refusal code (e.g. "POL-006", "LFC-001",
	// "LFC-002") — matched against the driver's own captured stdout+stderr
	// via a plain substring check (same idiom as internal/e2e's own
	// POL-006 assertions, e.g. contract_cov_test.go:50). A refusal that
	// fires for the WRONG code is a failure, not a pass.
	Code string
}

// PredicateKind is the CLOSED set of assertions a Step's Predicates may
// make (plan W3 D5 / spec §11.1 I9): each names exactly one shipped
// `--json` surface, never a predicate the runner would have to compute
// itself. A predicate with no shipped surface is not a weaker
// assertion — it is a wave (spec I9's own wording): build the surface,
// or drop the assertion. The consumer's resolved-pin-line assertion
// (spec §10.2, `contract-versions.md:33-37`'s pinned-line window) is the
// concrete example this wave hits immediately (D5) — no surface renders
// it yet (that is W2's job, which runs after W3 in the chosen order), so
// no PredicateKind exists for it here, and PathTwo (pathcatalogue_paths.go)
// says so explicitly rather than inventing a weaker stand-in.
type PredicateKind string

const (
	// PredicateFoldedState asserts an artifact's folded state.
	// Surface: `a2a show <id> --json` -> .state
	PredicateFoldedState PredicateKind = "folded_state"
	// PredicatePendingOn asserts an artifact is pending on EXACTLY the
	// named system set (order-independent; the empty set is a legal want
	// value — "pending on nobody", the O3/O4 regression's own assertion).
	// I9/spec §11.4 "no bare supersets": this is an equality check against
	// the surface's own array, never a subset/superset test.
	// Surface: `a2a thread <id> --json` -> open_items[].waiting_on
	PredicatePendingOn PredicateKind = "pending_on"
	// PredicateExpectedTransition asserts the owed transition.
	// Surface: `a2a thread <id> --json` -> open_items[].expected_transition
	PredicateExpectedTransition PredicateKind = "expected_transition"
	// PredicateAbsentFromOpenItems asserts an artifact does not appear in
	// open_items at all — internal/cache's own openStates allowlist
	// (types.go) filters a genuinely terminal (kind, state) out before
	// pendency is ever asked, which is a DIFFERENT surface behaviour from
	// PredicatePendingOn's "present, waiting_on: []" (a settled-but-ALIVE
	// state, e.g. a published contract, per plan W1's pendency table).
	// Surface: `a2a thread <id> --json` -> open_items (the id's absence)
	PredicateAbsentFromOpenItems PredicateKind = "absent_from_open_items"
	// PredicateThreadSettled asserts a thread carries no open items at
	// all.
	// Surface: `a2a thread <id> --json` -> open_items (empty array)
	PredicateThreadSettled PredicateKind = "thread_settled"
	// PredicateActionable asserts whether an id appears in a system's
	// actionable set (Want true) or does not (Want false) — the paired
	// positive/negative control spec §11.4 requires.
	// Surface: `a2a inbox --actionable --json`
	PredicateActionable PredicateKind = "actionable"
	// PredicateTerminal asserts an artifact's folded (kind, state) has NO
	// legal transition out of it for ANY role — internal/fold's own
	// fold.Terminal question, never a literal state name (AC6, plan
	// docs/features/archive/agent-exchange-2026-08/plans/11-verification.plan.md,
	// "Wave D": "the terminal state the DOMAIN declares is the one
	// observed"). Attached BESIDE a FoldedState predicate on a path's
	// terminal step, never instead of it — FoldedState pins the SPECIFIC
	// state the path's own narrative claims to reach; Terminal pins that
	// the state really is one fold declares terminal for that kind.
	// Terminal alone would pass on ANY terminal state, including a WRONG
	// one, so dropping FoldedState beside it would weaken the path rather
	// than strengthen it.
	// Surface: `a2a show <artifact> --json` -> .type, .state
	PredicateTerminal PredicateKind = "terminal"
)

// Predicate is one assertion a Step's runner (next stage) checks against
// exactly one shipped `--json` surface (I9). Its fields are unexported —
// Kind fixes which are meaningful, and the constructor functions below
// (FoldedState, PendingOn, ExpectedTransition, AbsentFromOpenItems,
// ThreadSettled, Actionable, NotActionable) are the only sanctioned way
// to build one, keeping the predicate set closed by construction rather
// than by convention. The next stage's driver lives in this same
// package (a `livee2e`-tagged file), so it reads these fields directly —
// no exported accessor is added for a caller that does not exist yet.
//
// Every reference field (Artifact, Thread, System) is a PATH-LOCAL
// symbolic name this path's own Steps establish (e.g. "contract",
// "delivery-1") or a system id (SystemA/SystemB) — never a real
// committed id, since nothing executes yet; the driver resolves these
// against the real ids it creates when it runs a path.
type Predicate struct {
	kind       PredicateKind
	artifact   string
	thread     string
	system     string
	state      fold.State
	transition string
	systems    []string
	want       bool
}

// FoldedState asserts artifact's folded state equals want.
// Surface: `a2a show <artifact> --json` -> .state
func FoldedState(artifact string, want fold.State) Predicate {
	return Predicate{kind: PredicateFoldedState, artifact: artifact, state: want}
}

// PendingOn asserts artifact is pending on EXACTLY systems (order-
// independent set equality) — pass no systems to assert "pending on
// nobody" (the settled-but-alive case, e.g. a published contract).
// Surface: `a2a thread <artifact> --json` -> open_items[].waiting_on
func PendingOn(artifact string, systems ...string) Predicate {
	return Predicate{kind: PredicatePendingOn, artifact: artifact, systems: append([]string(nil), systems...)}
}

// ExpectedTransition asserts artifact's owed transition equals transition.
// Surface: `a2a thread <artifact> --json` -> open_items[].expected_transition
func ExpectedTransition(artifact, transition string) Predicate {
	return Predicate{kind: PredicateExpectedTransition, artifact: artifact, transition: transition}
}

// AbsentFromOpenItems asserts artifact does not appear in open_items at
// all.
// Surface: `a2a thread <artifact> --json` -> open_items (absence)
func AbsentFromOpenItems(artifact string) Predicate {
	return Predicate{kind: PredicateAbsentFromOpenItems, artifact: artifact}
}

// ThreadSettled asserts thread's open_items array is empty.
// Surface: `a2a thread <thread> --json` -> open_items (empty)
func ThreadSettled(thread string) Predicate {
	return Predicate{kind: PredicateThreadSettled, thread: thread}
}

// Actionable asserts artifact appears in system's actionable set.
// Surface: `a2a inbox --actionable --json` (run with --system system)
func Actionable(system, artifact string) Predicate {
	return Predicate{kind: PredicateActionable, system: system, artifact: artifact, want: true}
}

// NotActionable asserts artifact does NOT appear in system's actionable
// set — the paired negative control spec §11.4 requires for every
// positive Actionable assertion in the same narrative.
// Surface: `a2a inbox --actionable --json` (run with --system system)
func NotActionable(system, artifact string) Predicate {
	return Predicate{kind: PredicateActionable, system: system, artifact: artifact, want: false}
}

// Terminal asserts artifact's folded (kind, state) satisfies fold.Terminal
// — no legal transition leaves it, for any role (AC6). The kind is
// resolved from the SAME `a2a show <artifact> --json` surface FoldedState
// already reads (.type), never threaded through the grammar as a second
// parameter: the driver (pathdriver_live.go's checkTerminal) already has
// it on hand from that identical read, and pathgrammar.go's own Predicate
// carries no Kind field for any OTHER predicate constructor either — every
// one of them resolves its own real-world fact from the shipped surface,
// never from a second, independently-declared parameter that could drift
// from what the surface actually reports.
// Surface: `a2a show <artifact> --json` -> .type, .state
func Terminal(artifact string) Predicate {
	return Predicate{kind: PredicateTerminal, artifact: artifact}
}

// ResolveStep resolves a Step's expected resulting fold.State from
// internal/fold's own LegalNext table (never a Step's own say-so — see
// Step's doc comment / plan D3). Returns an error naming the exact
// (kind, from, transition) triple when the table admits no such row, so
// an unsound declaration fails loudly instead of the runner inventing a
// state for a transition the domain does not have.
//
// A row whose table entry resolves dynamically (fold.StateDynamic —
// `unblock`'s pre-block recovery, decision `approve`'s quorum
// arithmetic) is ALSO refused, by name, rather than handed back as the
// sentinel: a path step cannot derive its own resulting state for a row
// dedicated fold.go logic resolves at apply time, and returning the
// sentinel silently would be a dropped guard, not a resolution. This
// function is never the place that resolves `unblock` or decision
// `approve` for real — see resolveUnblock/resolveApprove and walkSteps'
// own doc comments for the two narrow exceptions this grammar makes;
// every OTHER StateDynamic row (there are none today besides these two)
// still refuses here exactly as before.
func ResolveStep(kind fold.Kind, from fold.State, transition string) (fold.State, error) {
	for _, move := range fold.LegalNext(kind, from) {
		if move.Transition != transition {
			continue
		}
		if move.To == fold.StateDynamic {
			return "", fmt.Errorf("livee2e: (kind=%s, from=%q, transition=%q) resolves dynamically (fold.StateDynamic) — a path step cannot derive its own resulting state for unblock/decision-approve", kind, from, transition)
		}
		return move.To, nil
	}
	return "", fmt.Errorf("livee2e: no transition row for (kind=%s, from=%q, transition=%q)", kind, from, transition)
}

// resolveCreate resolves a `create` step's resulting state directly from
// fold.NewResult(kind) rather than a table row (table.go's own TCreate doc
// comment, spec §18a): fold.table.go carries no StateNone row for ANY kind,
// because a committed create event's own fromState is a state Fold never
// occupies — Fold always starts a kind at NewResult(kind), and every kind's
// NewResult is StateDraft today. Asking fold's own NewResult rather than
// restating StateDraft here keeps this a real check on fold's own fact,
// never an independently-maintained copy of it — the same discipline
// resolveUnblock/resolveApprove already follow for their own two dynamic
// rows, extended to the one transition that now has no row to check
// against at all.
func resolveCreate(kind fold.Kind) fold.State {
	return fold.NewResult(kind).State
}

// resolveUnblock resolves an `unblock` step's dynamic target — the state
// that held immediately before the matching `block` — from THIS walk's
// own preBlock map, never independently of fold's own table and never by
// re-deriving the rule: fold.go's applyUnblock recovers Result.PreBlockState
// (itself recomputed from the event sequence, "never externally stored");
// this is the grammar-level mirror of that exact recovery, sourced from
// the SAME walk's own earlier TBlock step (see walkSteps).
//
// Two checks, both required, deliberately not collapsed into one:
//  1. fold.LegalNext(kind, from) must still contain a TUnblock row — this
//     stays a real check on fold's own table (never invented independent
//     of it) rather than a coincidence-of-today shortcut; today the only
//     From with an unblock row is StateBlocked, but that is a fact about
//     the current table, not a grammar invariant this function may lean
//     on without checking.
//  2. preBlock must actually hold a recorded value for kind — a path may
//     only unblock a Kind it blocked in its OWN Steps (Step's own doc
//     comment); a miss here is a genuine grammar error (unblock declared
//     with no matching block earlier in the SAME walk), not a fold
//     refusal, so it is reported distinctly.
func resolveUnblock(kind fold.Kind, from fold.State, preBlock map[fold.Kind]fold.State) (fold.State, error) {
	found := false
	for _, move := range fold.LegalNext(kind, from) {
		if move.Transition == fold.TUnblock {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("livee2e: no transition row for (kind=%s, from=%q, transition=%q)", kind, from, fold.TUnblock)
	}
	pre, ok := preBlock[kind]
	if !ok {
		return "", fmt.Errorf("livee2e: (kind=%s, from=%q, transition=%q): no recorded pre-block state for this Kind earlier in the SAME walk — a path may only unblock a Kind it blocked in its OWN Steps, never across a Precondition boundary", kind, from, fold.TUnblock)
	}
	return pre, nil
}

// resolveApprove resolves a fold.KindDecision `approve` step's dynamic
// target — quorum arithmetic mirrored from fold.go's own applyApprove/
// quorumReached, never re-derived independently of it: the decision stays
// at fold.StateProposed until every entry in requiredApprovers (Path's own
// RequiredApprovers, declared data — never inferred from step order, see
// Path's own doc comment) has approved within THIS walk, at which point it
// moves to fold.StateApproved.
//
// approvals is this walk's OWN record of which row-labels (livee2e.SystemA/
// SystemB) have approved so far, for this Kind — mirrors preBlock's own
// doc comment: local to one walkSteps call, never threaded through
// chainEndStates/PathTransitions's own precondition-chain recursion (no
// declared path today needs a decision's approvals recovered across a
// chain boundary).
//
// Two checks, both required, deliberately not collapsed into one (same
// shape as resolveUnblock):
//  1. fold.LegalNext(kind, from) must still contain a TApprove row — a
//     real check on fold's own table, never a coincidence-of-today
//     shortcut.
//  2. requiredApprovers must be non-empty — an approve step on a path that
//     declares no RequiredApprovers is a genuine grammar error (the walk
//     has no quorum to arithmetic against), not a fold refusal, so it is
//     reported distinctly.
func resolveApprove(kind fold.Kind, from fold.State, requiredApprovers []string, approvals map[string]bool, actor string) (fold.State, error) {
	found := false
	for _, move := range fold.LegalNext(kind, from) {
		if move.Transition == fold.TApprove {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("livee2e: no transition row for (kind=%s, from=%q, transition=%q)", kind, from, fold.TApprove)
	}
	if len(requiredApprovers) == 0 {
		return "", fmt.Errorf("livee2e: (kind=%s, from=%q, transition=%q): no RequiredApprovers declared on this path — an approve step needs the quorum set named on Path.RequiredApprovers, never inferred from step order", kind, from, fold.TApprove)
	}
	approvals[actor] = true
	for _, required := range requiredApprovers {
		if !approvals[required] {
			return fold.StateProposed, nil
		}
	}
	return fold.StateApproved, nil
}

// transitionOutcome pairs one landed step's own (Kind, From, Transition)
// triple (walkSteps' historical return element, still what PathTransitions
// exposes) with the resulting state that step's own resolution produced.
// The coverage split (plan W3d, spec §16 D-2) needs this second half: a
// triple counts as genuinely ASSERTED, not merely driven, only when To is a
// state some shipped `--json` surface could actually read back — the
// untagged coverage gate checks To directly (pathcoverage_test.go's own
// assertedTriple), never against a runtime record the tagged driver would
// have to emit (this package's own drivability precedent: "a gate that
// only runs during a 40-minute live run is a gate nobody runs",
// pathdrivability.go). Deliberately NOT fold.RestingStates() membership —
// see assertedTriple's own doc comment for the two concrete cases
// (decision's create step, decision's quorum-reached approve) where that
// would have given the wrong answer.
type transitionOutcome struct {
	fold.TransitionKey
	To fold.State
}

// walkSteps resolves steps in order against states (a per-Kind "current
// state" map, mutated in place as each step lands), returning the
// resolved (Kind, From, Transition) triple AND resulting state for each
// LANDED step — a fold.TCreate step always resolves From as
// fold.StateNone regardless of what states currently holds for that Kind
// (a new instance; see Step's own doc comment) and its own resulting
// state via resolveCreate (fold.NewResult(kind), never a table row —
// table.go carries none for create), any other step resolves From from
// states[step.Kind] (fold.StateNone, Go's own zero value, if that Kind
// has not appeared yet in this walk — which ResolveStep then correctly
// refuses).
//
// A step whose Refused is non-nil is skipped entirely rather than
// resolved: ResolveStep is never called (there is no resulting state to
// resolve — the act never lands, Refused's own doc comment consequence
// 2), states[step.Kind] is left exactly as it was (the walk does not
// advance), and no triple is appended for it (consequence 3 — no
// transition-coverage credit for an act the product refused).
//
// preBlock is this walk's OWN per-Kind pre-block-state record (Step's
// own doc comment on the unblock exception): a landed fold.TBlock step
// records `from` under its Kind; a landed fold.TUnblock step resolves
// via resolveUnblock instead of ResolveStep, using that same record. It
// is local to one walkSteps call and is NOT threaded through
// chainEndStates/PathTransitions's own precondition-chain recursion —
// deliberately, per Step's own doc comment: no declared path needs a
// block in one path's Steps recovered by an unblock in another.
//
// requiredApprovers is the CALLING Path's own RequiredApprovers (Path's
// own doc comment) — walkSteps never reads a Path directly (it only ever
// sees a []Step), so this is threaded in as a parameter by both call
// sites (chainEndStates, PathTransitions) exactly as they already thread
// states. approvals mirrors preBlock's own per-walk, per-Kind shape (see
// resolveApprove's own doc comment).
func walkSteps(steps []Step, requiredApprovers []string, states map[fold.Kind]fold.State) ([]transitionOutcome, error) {
	out := make([]transitionOutcome, 0, len(steps))
	preBlock := map[fold.Kind]fold.State{}
	approvals := map[fold.Kind]map[string]bool{}
	for i, step := range steps {
		if step.Refused != nil {
			continue
		}
		from := fold.StateNone
		if step.Transition != fold.TCreate {
			from = states[step.Kind]
		}

		var to fold.State
		var err error
		switch {
		case step.Transition == fold.TCreate:
			to = resolveCreate(step.Kind)
		case step.Transition == fold.TUnblock:
			to, err = resolveUnblock(step.Kind, from, preBlock)
		case step.Kind == fold.KindDecision && step.Transition == fold.TApprove:
			if approvals[step.Kind] == nil {
				approvals[step.Kind] = map[string]bool{}
			}
			to, err = resolveApprove(step.Kind, from, requiredApprovers, approvals[step.Kind], step.Actor)
		default:
			to, err = ResolveStep(step.Kind, from, step.Transition)
			if err == nil && step.Transition == fold.TBlock {
				preBlock[step.Kind] = from
			}
		}
		if err != nil {
			return nil, fmt.Errorf("step %d (actor=%s kind=%s transition=%s): %w", i, step.Actor, step.Kind, step.Transition, err)
		}
		states[step.Kind] = to
		out = append(out, transitionOutcome{
			TransitionKey: fold.TransitionKey{Kind: step.Kind, From: from, Transition: step.Transition},
			To:            to,
		})
	}
	return out, nil
}

// chainEndStates resolves id's full precondition chain PLUS id's own
// Steps, in order, to the per-Kind ending state map a path chained onto
// id would continue from (Path's own Precondition doc comment). visiting
// guards against a precondition cycle, which is a grammar error, not a
// silent infinite walk.
func chainEndStates(byID map[string]Path, id string, visiting map[string]bool) (map[fold.Kind]fold.State, error) {
	path, ok := byID[id]
	if !ok {
		return nil, fmt.Errorf("livee2e: unknown path id %q", id)
	}
	if visiting[id] {
		return nil, fmt.Errorf("livee2e: path %q precondition cycle", id)
	}
	visiting[id] = true
	defer delete(visiting, id)

	states := map[fold.Kind]fold.State{}
	if path.Precondition != "" {
		inherited, err := chainEndStates(byID, path.Precondition, visiting)
		if err != nil {
			return nil, err
		}
		states = inherited
	}
	if _, err := walkSteps(path.Steps, path.RequiredApprovers, states); err != nil {
		return nil, fmt.Errorf("livee2e: path %q: %w", id, err)
	}
	return states, nil
}

// PathTransitions resolves path id's OWN Steps (never its precondition
// chain's — those are id's own separate Path entries, resolved by their
// own call to PathTransitions) to the (Kind, From, Transition) triples
// they exercise, threading a per-Kind state machine seeded from the
// precondition chain's own end states (chainEndStates). This is the
// grammar-level version of what the next stage's runner does for real;
// this package's own coverage gate (pathcoverage_test.go) calls it,
// unioned across every declared path, to prove every fold.TransitionRows()
// triple is reached or is a named, defensible gap.
//
// Triples only — the resulting-state half a caller wanting the D3 coverage
// split needs is pathTransitionOutcomes' own job (below); PathTransitions
// itself is unchanged in shape for its three existing callers.
func PathTransitions(byID map[string]Path, id string) ([]fold.TransitionKey, error) {
	outcomes, err := pathTransitionOutcomes(byID, id)
	if err != nil {
		return nil, err
	}
	triples := make([]fold.TransitionKey, len(outcomes))
	for i, o := range outcomes {
		triples[i] = o.TransitionKey
	}
	return triples, nil
}

// pathTransitionOutcomes is PathTransitions' own full-detail sibling: same
// resolution, but keeping each triple's resulting state (transitionOutcome)
// rather than discarding it. pathcoverage_test.go's coverage split
// (D-2, spec §16) is the one caller that needs the resulting state — its
// own assertedTriple judges it to tell "driven" from "asserted".
func pathTransitionOutcomes(byID map[string]Path, id string) ([]transitionOutcome, error) {
	path, ok := byID[id]
	if !ok {
		return nil, fmt.Errorf("livee2e: unknown path id %q", id)
	}

	states := map[fold.Kind]fold.State{}
	if path.Precondition != "" {
		inherited, err := chainEndStates(byID, path.Precondition, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("livee2e: path %q precondition: %w", id, err)
		}
		states = inherited
	}

	outcomes, err := walkSteps(path.Steps, path.RequiredApprovers, states)
	if err != nil {
		return nil, fmt.Errorf("livee2e: path %q: %w", id, err)
	}
	return outcomes, nil
}

// pathsByID indexes paths by their own ID — the map PathTransitions and
// chainEndStates need to resolve a Precondition reference. Duplicate IDs
// are a grammar error.
func pathsByID(paths []Path) (map[string]Path, error) {
	out := make(map[string]Path, len(paths))
	for _, p := range paths {
		if _, exists := out[p.ID]; exists {
			return nil, fmt.Errorf("livee2e: duplicate path id %q", p.ID)
		}
		out[p.ID] = p
	}
	return out, nil
}
