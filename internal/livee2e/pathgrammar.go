package livee2e

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// pathgrammar.go declares the conformance-path grammar (plan W3, docs/
// features/active/agent-ops-2026-07/plans/11-authoring-path-and-seam-
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
// genuinely NEW artifact instance — table.go's own rows encode no other
// (kind, StateNone) row). The resulting state comes from fold's own
// LegalNext table via ResolveStep. This is plan D3: a path cannot assert
// a transition the domain does not admit, and a table change surfaces as
// a failing path rather than as two documents quietly disagreeing.
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
	// ... — table.go's own consts).
	Transition string
	// Predicates are asserted, in order, once this step's transition has
	// landed and the runner has re-read the affected surface(s).
	Predicates []Predicate
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
// sentinel silently would be a dropped guard, not a resolution.
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

// walkSteps resolves steps in order against states (a per-Kind "current
// state" map, mutated in place as each step lands), returning the
// resolved (Kind, From, Transition) triple for each — a fold.TCreate
// step always resolves From as fold.StateNone regardless of what states
// currently holds for that Kind (a new instance; see Step's own doc
// comment), any other step resolves From from states[step.Kind]
// (fold.StateNone, Go's own zero value, if that Kind has not appeared
// yet in this walk — which ResolveStep then correctly refuses unless the
// step itself is a create).
func walkSteps(steps []Step, states map[fold.Kind]fold.State) ([]fold.TransitionKey, error) {
	out := make([]fold.TransitionKey, 0, len(steps))
	for i, step := range steps {
		from := fold.StateNone
		if step.Transition != fold.TCreate {
			from = states[step.Kind]
		}
		to, err := ResolveStep(step.Kind, from, step.Transition)
		if err != nil {
			return nil, fmt.Errorf("step %d (actor=%s kind=%s transition=%s): %w", i, step.Actor, step.Kind, step.Transition, err)
		}
		states[step.Kind] = to
		out = append(out, fold.TransitionKey{Kind: step.Kind, From: from, Transition: step.Transition})
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
	if _, err := walkSteps(path.Steps, states); err != nil {
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
func PathTransitions(byID map[string]Path, id string) ([]fold.TransitionKey, error) {
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

	triples, err := walkSteps(path.Steps, states)
	if err != nil {
		return nil, fmt.Errorf("livee2e: path %q: %w", id, err)
	}
	return triples, nil
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
