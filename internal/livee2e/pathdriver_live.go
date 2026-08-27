//go:build livee2e

package livee2e

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// pathdriver_live.go is plan W3's path DRIVER (brief "DELIVERABLE 1"): it
// makes pathgrammar.go/pathcatalogue_paths.go's declared Path/Step
// sequences execute against the REAL `a2a` binary, over the SAME
// newLogicHarness rig logic_harness_live_test.go/logic_runner_live_test.go
// already stand up — no second harness, no fifth mechanism (plan D1).
//
// One t.Run subtest per drivenPathIDs() entry (pathdrivability.go), named
// by the path's own ID, but nested one level BELOW a "group-N" subtest
// (runConformancePaths splits the ids across harness groups), so:
//
//	go test ./internal/livee2e/... -tags=livee2e -race -count=1 \
//	  -run '^TestLogicMatrix$/^group-[0-9]+$/^contract-baseline-published-settled$'
//
// runs exactly that one path (plus TestLogicMatrix's own family matrix —
// see runConformancePaths' doc comment for why that residual exists and is
// reported, not hidden).
//
// THE MIDDLE SEGMENT IS NOT OPTIONAL, and leaving it out is a green run that
// drove nothing. This comment carried the two-segment form until P11 wave D
// ran it: `-run '^TestLogicMatrix$/contract-baseline-published-settled'`
// matches no subtest at level 1, so Go prunes the whole subtree, prints
// `--- PASS: TestLogicMatrix` and exits 0. The only signal is a `[no tests
// to run]` suffix on the `ok` line, which is easy to skim past — measured,
// not reasoned: 10.6s green with zero path subtests entered.
//
// D4 (plan): a path whose Precondition names an earlier path REPLAYS that
// path's own Steps first, by calling that earlier path's own driver
// function directly (never t.Run-nested — see runConformancePaths) — so
// every path is runnable alone by name without snapshot/restore machinery.
// Each replay mints its OWN, uniquely-slugged artifacts (runTag, derived
// from the OUTERMOST subtest's own path id) rather than reusing a sibling
// subtest's — two subtests sharing one harness must never collide on a
// standing slug.
//
// Every step's predicates are checked by READING pathgrammar.go's own
// declared Predicate values (its doc comment: "the next stage's driver
// lives in this same package ... reads these fields directly") — this file
// never re-derives what to assert, only how to reach the state the
// declaration already names.

// pathIDs maps a path-local symbolic name (a Predicate's own `artifact`/
// `thread` field, e.g. "contract", "question", "delivery-1") to the REAL
// committed artifact id a driven CLI call minted for it. Built up as a
// path's (and its replayed precondition chain's) steps execute.
type pathIDs map[string]string

// mustPath resolves id from ConformancePaths() or fails the (sub)test
// loudly — every driver function below calls this first, so a stale id (a
// typo, or drivenPathIDs() naming something pathcatalogue_paths.go no
// longer declares) is reported at the point of use, not as a nil-pointer
// panic three calls deep.
func mustPath(t *testing.T, id string) Path {
	t.Helper()
	byID, err := pathsByID(ConformancePaths())
	if err != nil {
		t.Fatalf("pathdriver: pathsByID: %v", err)
	}
	p, ok := byID[id]
	if !ok {
		t.Fatalf("pathdriver: %q is not a declared ConformancePaths() id", id)
	}
	return p
}

// checkoutForActor resolves a livee2e.SystemA/SystemB row-label (the SAME
// space Step.Actor and Predicate's own `system` field live in — catalogue.go's
// "A"/"B" convention) to the harness checkout that plays it. Never compare
// a row-label against checkout.System directly: that field is the harness's
// OWN local system id ("alpha"/"bravo", harness_live.go), a different
// namespace entirely.
func checkoutForActor(h *harness, actor string) *checkout {
	if actor == SystemA {
		return h.A
	}
	return h.B
}

// --- predicate checking ----------------------------------------------

// checkStepPredicates re-reads the shipped --json surface each of step's
// own Predicates names (pathgrammar.go's own PredicateKind doc comments)
// through actor's checkout, and fails the CURRENT (sub)test naming path,
// step index, actor, transition, expected vs actual — the brief's own
// requirement for a predicate failure's message.
func checkStepPredicates(ctx context.Context, t *testing.T, h *harness, actor *checkout, pathID string, stepIndex int, step Step, ids pathIDs) {
	t.Helper()
	for _, p := range step.Predicates {
		checkPredicate(ctx, t, h, actor, pathID, stepIndex, step, p, ids)
	}
}

func predicateLabel(pathID string, stepIndex int, step Step) string {
	return fmt.Sprintf("path %s step %d (actor=%s kind=%s transition=%s)", pathID, stepIndex, step.Actor, step.Kind, step.Transition)
}

func checkPredicate(ctx context.Context, t *testing.T, h *harness, actor *checkout, pathID string, stepIndex int, step Step, p Predicate, ids pathIDs) {
	t.Helper()
	label := predicateLabel(pathID, stepIndex, step)

	switch p.kind {
	case PredicateFoldedState:
		checkFoldedState(ctx, t, actor, label, p, ids)
	case PredicatePendingOn:
		checkPendingOn(ctx, t, h, actor, label, p, ids)
	case PredicateExpectedTransition:
		checkExpectedTransition(ctx, t, actor, label, p, ids)
	case PredicateAbsentFromOpenItems:
		checkAbsentFromOpenItems(ctx, t, actor, label, p, ids)
	case PredicateThreadSettled:
		checkThreadSettled(ctx, t, actor, label, p, ids)
	case PredicateActionable:
		checkActionable(ctx, t, h, actor, label, p, ids)
	case PredicateTerminal:
		checkTerminal(ctx, t, actor, label, p, ids)
	default:
		t.Fatalf("%s: predicate kind %q has no checker in pathdriver_live.go", label, p.kind)
	}
}

// checkFoldedState asserts artifact's folded state via `a2a show --json`.
//
// A `folded_state == draft` predicate is a KNOWN I9 gap, not silently
// worked around: `a2a show` reads cache.Store, which is built only from
// the committed mirror (internal/cli/cmd_new.go's own doc comment,
// "drafts never enter the [store]") — a freshly drafted, not-yet-submitted
// artifact has no shipped --json surface that can answer this. Confirmed
// empirically two ways, both treated the same (skip, log, never assert):
//   - the common case: `a2a show` REFUSES outright (ref not found) for an
//     artifact whose create+first-transition are two separate driver calls
//     (draft, then a LATER `a2a submit`) — the draft step's own read lands
//     strictly before the submit.
//   - the deprecate-bundle case: `a2a contract deprecate` authors the
//     linked announcement's create+publish IN THE SAME atomic write (§5.4;
//     driveDeprecateBundle's own doc comment) — the announcement's TCreate
//     step is only ever checked AFTER that one call lands, by which point
//     `a2a show` succeeds but reports the FINAL state (published), because
//     the announcement was never independently observable in `draft` at
//     all. Both are the identical I9 fact (draft is not a state any
//     shipped --json surface can observe), reached by a different code
//     path — an error in one case, a stale-relative-to-the-predicate
//     success in the other.
func checkFoldedState(ctx context.Context, t *testing.T, actor *checkout, label string, p Predicate, ids pathIDs) {
	t.Helper()
	id, ok := ids[p.artifact]
	if !ok {
		t.Fatalf("%s: predicate folded_state(%s): no known committed id for symbolic name %q", label, p.artifact, p.artifact)
	}
	stdout, stderr, err := actor.Run(ctx, "show", id, "--json")
	if err != nil {
		if p.state == fold.StateDraft {
			t.Logf("%s: SKIPPED folded_state(%s)==draft — a2a show %s (%s) refused (no shipped surface reads an uncommitted draft, I9 gap): %v: %s", label, p.artifact, id, actor.System, err, strings.TrimSpace(stderr))
			return
		}
		t.Fatalf("%s: predicate folded_state(%s): a2a show %s (%s): %v: %s", label, p.artifact, id, actor.System, err, strings.TrimSpace(stderr))
	}
	var doc cache.ShowResult
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		t.Fatalf("%s: predicate folded_state(%s): decode a2a show %s --json: %v", label, p.artifact, id, jerr)
	}
	if p.state == fold.StateDraft && doc.State != string(fold.StateDraft) {
		t.Logf("%s: SKIPPED folded_state(%s)==draft — a2a show %s (%s) succeeded but already reports %q: this artifact's create was bundled atomically with a later transition (I9 gap, same fact as the refusal case)", label, p.artifact, id, actor.System, doc.State)
		return
	}
	if doc.State != string(p.state) {
		t.Errorf("%s: predicate folded_state(%s): got state %q, want %q (id=%s)", label, p.artifact, doc.State, p.state, id)
	}
}

// terminalStatesFor returns the FULL terminal-state set fold.Terminal
// declares for kind, sourced from fold.RestingStates() (the same
// enumerator restingcoverage_test.go already trusts as the domain's own
// state universe, never an independently maintained list here) filtered
// down to the pairs fold.Terminal itself says have no legal transition
// out — used only to make a terminal-predicate FAILURE message name what
// terminal WOULD have looked like, not to decide pass/fail (fold.Terminal
// alone decides that, in checkTerminal below).
func terminalStatesFor(kind fold.Kind) []string {
	var out []string
	for _, pair := range fold.RestingStates() {
		if pair.Kind != kind {
			continue
		}
		if fold.Terminal(pair.Kind, pair.State) {
			out = append(out, string(pair.State))
		}
	}
	sort.Strings(out)
	return out
}

// isKnownRestingPair reports whether (kind, state) is a pair
// fold.RestingStates() actually enumerates — the domain's own universe of
// facts a subject can genuinely be found at rest in. checkTerminal's own
// guard (below) exists because fold.Terminal itself is written
// fail-CLOSED-on-a-real-row-but-open-on-an-UNKNOWN-one: it walks fold's
// transition rows for (kind, state) and returns true (terminal) the moment
// none matches — the SAME shape that is correct for a genuinely resting,
// no-legal-move-left pair and WRONG for a garbled or empty `.type`/`.state`
// this predicate never anticipated (an unrecognized kind matches zero rows
// for ANY state, so fold.Terminal would report "terminal" vacuously).
// driveRefusedContractRetire (this file) already refuses an analogous
// silent-pass shape for an empty Refused.Code ("would make the check
// vacuously true for ANY non-zero exit") — this is the same discipline
// applied to fold.Terminal's own open side.
func isKnownRestingPair(kind fold.Kind, state fold.State) bool {
	for _, pair := range fold.RestingStates() {
		if pair.Kind == kind && pair.State == state {
			return true
		}
	}
	return false
}

// checkTerminal asserts artifact's folded (kind, state) satisfies
// fold.Terminal (AC6) — reading BOTH facts off the SAME `a2a show --json`
// call checkFoldedState already uses (doc.Type resolves the kind exactly
// the way cache.buildShowResult populates it, from the committed
// envelope's own `type:` field — the same string fold.Kind's own consts
// are spelled in; doc.State resolves the state), rather than threading a
// second Kind parameter through the grammar (Terminal's own doc comment,
// pathgrammar.go). isKnownRestingPair is checked FIRST and Fatalf's by
// name if it fails, before fold.Terminal is ever asked — an unrecognized
// (kind, state) pair is a driver/decode bug, never a "pass" (fold.Terminal
// itself would otherwise report one vacuously; see isKnownRestingPair's
// own doc comment). Failure names the artifact, the observed (kind,
// state), and the FULL terminal-state set fold declares for that kind
// (terminalStatesFor), so a reader sees not just "not terminal" but what
// terminal would have looked like.
func checkTerminal(ctx context.Context, t *testing.T, actor *checkout, label string, p Predicate, ids pathIDs) {
	t.Helper()
	id, ok := ids[p.artifact]
	if !ok {
		t.Fatalf("%s: predicate terminal(%s): no known committed id for symbolic name %q", label, p.artifact, p.artifact)
	}
	stdout, stderr, err := actor.Run(ctx, "show", id, "--json")
	if err != nil {
		t.Fatalf("%s: predicate terminal(%s): a2a show %s (%s): %v: %s", label, p.artifact, id, actor.System, err, strings.TrimSpace(stderr))
	}
	var doc cache.ShowResult
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		t.Fatalf("%s: predicate terminal(%s): decode a2a show %s --json: %v", label, p.artifact, id, jerr)
	}
	kind := fold.Kind(doc.Type)
	state := fold.State(doc.State)
	if !isKnownRestingPair(kind, state) {
		t.Fatalf("%s: predicate terminal(%s): (kind=%s, state=%q) is not a pair fold.RestingStates() recognizes at all — refusing to ask fold.Terminal, which would otherwise report an unrecognized pair as vacuously terminal (id=%s)",
			label, p.artifact, kind, doc.State, id)
	}
	if !fold.Terminal(kind, state) {
		t.Errorf("%s: predicate terminal(%s): kind=%s is resting in %q, which fold.Terminal does NOT consider terminal (its terminal set for %s is %v) (id=%s)",
			label, p.artifact, kind, doc.State, kind, terminalStatesFor(kind), id)
	}
}

func readThread(ctx context.Context, actor *checkout, id string) (cache.ThreadResult, error) {
	stdout, stderr, err := actor.Run(ctx, "thread", id, "--json")
	if err != nil {
		return cache.ThreadResult{}, fmt.Errorf("a2a thread %s (%s): %w: %s", id, actor.System, err, strings.TrimSpace(stderr))
	}
	var result cache.ThreadResult
	if jerr := json.Unmarshal([]byte(stdout), &result); jerr != nil {
		return cache.ThreadResult{}, fmt.Errorf("decode a2a thread %s --json: %w", id, jerr)
	}
	return result, nil
}

func findOpenItem(result cache.ThreadResult, id string) (cache.OpenItem, bool) {
	for _, item := range result.OpenItems {
		if item.ID == id {
			return item, true
		}
	}
	return cache.OpenItem{}, false
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// checkPendingOn asserts artifact is present in open_items pending on
// EXACTLY p.systems (set equality — the empty set is "pending on nobody",
// the O3/O4 regression's own assertion).
// Surface: `a2a thread <artifact> --json` -> open_items[].waiting_on
func checkPendingOn(ctx context.Context, t *testing.T, h *harness, actor *checkout, label string, p Predicate, ids pathIDs) {
	t.Helper()
	id, ok := ids[p.artifact]
	if !ok {
		t.Fatalf("%s: predicate pending_on(%s): no known committed id for symbolic name %q", label, p.artifact, p.artifact)
	}
	result, err := readThread(ctx, actor, id)
	if err != nil {
		t.Fatalf("%s: predicate pending_on(%s): %v", label, p.artifact, err)
	}
	item, present := findOpenItem(result, id)
	if !present {
		t.Fatalf("%s: predicate pending_on(%s): %s is not present in open_items at all", label, p.artifact, id)
	}
	want := translateRowLabels(h, p.systems)
	if !sameStringSet(item.WaitingOn, want) {
		t.Errorf("%s: predicate pending_on(%s): got waiting_on=%v, want %v (declared as %v) (id=%s)", label, p.artifact, item.WaitingOn, want, p.systems, id)
	}
}

// translateRowLabels maps a slice of livee2e.SystemA/SystemB row-labels
// (the space a Predicate's own `systems`/`system` fields live in) to the
// REAL local system ids the harness's checkouts registered as
// (systemAlpha/systemBravo — harness_live.go) and the shipped --json
// surfaces actually report. Never compare a row-label directly against a
// surface's own system field — that is the SAME translation checkActionable
// applies to `p.system`, generalized to a slice for pending_on's own
// `systems` set.
func translateRowLabels(h *harness, labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		out = append(out, checkoutForActor(h, l).System)
	}
	return out
}

// checkExpectedTransition asserts artifact's open-item ExpectedTransition.
// Surface: `a2a thread <artifact> --json` -> open_items[].expected_transition
func checkExpectedTransition(ctx context.Context, t *testing.T, actor *checkout, label string, p Predicate, ids pathIDs) {
	t.Helper()
	id, ok := ids[p.artifact]
	if !ok {
		t.Fatalf("%s: predicate expected_transition(%s): no known committed id for symbolic name %q", label, p.artifact, p.artifact)
	}
	result, err := readThread(ctx, actor, id)
	if err != nil {
		t.Fatalf("%s: predicate expected_transition(%s): %v", label, p.artifact, err)
	}
	item, present := findOpenItem(result, id)
	if !present {
		t.Fatalf("%s: predicate expected_transition(%s): %s is not present in open_items at all", label, p.artifact, id)
	}
	if item.ExpectedTransition != p.transition {
		t.Errorf("%s: predicate expected_transition(%s): got %q, want %q (id=%s)", label, p.artifact, item.ExpectedTransition, p.transition, id)
	}
}

// checkAbsentFromOpenItems asserts artifact does NOT appear in open_items.
// Surface: `a2a thread <artifact> --json` -> open_items (absence)
func checkAbsentFromOpenItems(ctx context.Context, t *testing.T, actor *checkout, label string, p Predicate, ids pathIDs) {
	t.Helper()
	id, ok := ids[p.artifact]
	if !ok {
		t.Fatalf("%s: predicate absent_from_open_items(%s): no known committed id for symbolic name %q", label, p.artifact, p.artifact)
	}
	result, err := readThread(ctx, actor, id)
	if err != nil {
		t.Fatalf("%s: predicate absent_from_open_items(%s): %v", label, p.artifact, err)
	}
	if item, present := findOpenItem(result, id); present {
		t.Errorf("%s: predicate absent_from_open_items(%s): still present in open_items (waiting_on=%v) (id=%s)", label, p.artifact, item.WaitingOn, id)
	}
}

// checkThreadSettled asserts thread's open_items array is empty.
// Surface: `a2a thread <thread> --json` -> open_items (empty)
func checkThreadSettled(ctx context.Context, t *testing.T, actor *checkout, label string, p Predicate, ids pathIDs) {
	t.Helper()
	id, ok := ids[p.thread]
	if !ok {
		t.Fatalf("%s: predicate thread_settled(%s): no known committed id for symbolic name %q", label, p.thread, p.thread)
	}
	result, err := readThread(ctx, actor, id)
	if err != nil {
		t.Fatalf("%s: predicate thread_settled(%s): %v", label, p.thread, err)
	}
	if len(result.OpenItems) != 0 {
		t.Errorf("%s: predicate thread_settled(%s): open_items is non-empty: %+v", label, p.thread, result.OpenItems)
	}
}

// checkActionable asserts artifact's presence (p.want==true) or absence
// (p.want==false) in actor's own `--actionable` set.
// Surface: `a2a inbox --actionable --json`
func checkActionable(ctx context.Context, t *testing.T, h *harness, actor *checkout, label string, p Predicate, ids pathIDs) {
	t.Helper()
	id, ok := ids[p.artifact]
	if !ok {
		t.Fatalf("%s: predicate actionable(%s): no known committed id for symbolic name %q", label, p.artifact, p.artifact)
	}
	// Read from the checkout p.system NAMES (livee2e.SystemA/SystemB,
	// catalogue.go's own row-label convention), never actor's own — every
	// declared Actionable/NotActionable happens to name the step's own
	// actor today, but that is a fact about the catalogue, not a rule this
	// checker may assume: a future path predicate naming the OTHER system
	// must read that system's own inbox, not silently pass by reading the
	// wrong one.
	if p.system != SystemA && p.system != SystemB {
		t.Fatalf("%s: predicate actionable(%s): p.system %q is neither SystemA nor SystemB", label, p.artifact, p.system)
	}
	reader := checkoutForActor(h, p.system)
	stdout, stderr, err := reader.Run(ctx, "inbox", "--actionable", "--json")
	if err != nil {
		t.Fatalf("%s: predicate actionable(%s): a2a inbox --actionable (%s): %v: %s", label, p.artifact, reader.System, err, strings.TrimSpace(stderr))
	}
	var items []cache.Item
	if jerr := json.Unmarshal([]byte(stdout), &items); jerr != nil {
		t.Fatalf("%s: predicate actionable(%s): decode a2a inbox --actionable --json: %v", label, p.artifact, jerr)
	}
	found := false
	for _, it := range items {
		if it.ID == id {
			found = true
			break
		}
	}
	if found != p.want {
		t.Errorf("%s: predicate actionable(%s): got present=%v, want present=%v (id=%s, system=%s)", label, p.artifact, found, p.want, id, reader.System)
	}
}

// --- CLI-driving building blocks --------------------------------------

// simpleVerbForTransition maps a fold transition constant to the OP-211 CLI
// verb name driving it, for the single-artifact, single-PR batch verbs
// (cmd_lifecycle.go's lifecycleVerbTable) — irregular ONLY for acknowledge
// ("ack"); every other entry here is transition==verb.
var simpleVerbForTransition = map[string]string{
	fold.TAcknowledge: "ack",
	fold.TAccept:      "accept",
	fold.TStart:       "start",
	fold.TClose:       "close",
	fold.TVerifyPass:  "verify-pass",
	fold.TVerifyFail:  "verify-fail",
	fold.TSupersede:   "supersede",
	fold.TWithdraw:    "withdraw",
	fold.TCancel:      "cancel",
	fold.TDecline:     "decline",
	fold.TBlock:       "block",
	fold.TUnblock:     "unblock",
	fold.TSatisfy:     "satisfy",
	fold.TApprove:     "approve",
	fold.TReject:      "reject",
}

// driveCreateAndFirstTransition realizes a path's own (Kind,TCreate) step
// followed immediately by (Kind,TPublish|TSubmit) via `a2a new <kind>` then
// `a2a submit <id>` — submitFirstTransition (cmd_submit.go) picks the right
// transition name from the artifact's own kind, so this ONE pair of CLI
// calls is what realizes EITHER shape depending on kind (h.DraftAndSubmit's
// own doc comment). Checks both steps' own predicates in between/after.
func driveCreateAndFirstTransition(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, createIdx, firstIdx int, kind, localName string, ids pathIDs, draftExtra ...string) submitted {
	t.Helper()
	id, _, err := actor.Draft(ctx, kind, draftExtra...)
	if err != nil {
		t.Fatalf("path %s step %d: a2a new %s (%s): %v", path.ID, createIdx, kind, actor.System, err)
	}
	ids[localName] = id
	checkStepPredicates(ctx, t, h, actor, path.ID, createIdx, path.Steps[createIdx], ids)

	sub, err := h.submitDrafted(ctx, actor, id)
	if err != nil {
		t.Fatalf("path %s step %d: a2a submit %s (%s): %v", path.ID, firstIdx, id, actor.System, err)
	}
	if err := happyLandAndSync(ctx, h, actor, sub.PRNumber); err != nil {
		t.Fatalf("path %s step %d: land+sync submit PR #%d: %v", path.ID, firstIdx, sub.PRNumber, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, firstIdx, path.Steps[firstIdx], ids)
	return sub
}

// driveRefusedCreateAndSubmit realizes a path's own (Kind,TCreate) step via
// `a2a new <kind>` — which LANDS, exactly like driveCreateAndFirstTransition's
// own create leg — followed by an `a2a submit <id>` the real binary MUST
// REFUSE (submitIdx's own Step.Refused, checked the same way
// driveRefusedSimpleVerb/driveRefusedContractRetire check theirs: a non-zero
// exit whose combined output names step.Refused.Code is the ONLY pass). No
// PR is ever pulled — a V2 schema-class refusal fires before the write
// funnel is reached, same pre-write ordering every other refused-step driver
// in this file already relies on.
//
// no-silent-yes-2026-08/P3 stage 1's own use (pathcatalogue_format.go,
// "work-request-bad-needed-by-format-refused"): draftExtra carries the
// malformed field (`--field needed_by=next tuesday`), so `a2a new` succeeds
// (it authors the file, it does not validate it — cmd_new.go calls no
// validator) and the refusal is `a2a submit`'s own V2 ValidateForSubmit
// call, exactly like every other refused step in this file being a REAL
// act through the REAL binary, never a seeded raw prior.
func driveRefusedCreateAndSubmit(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, createIdx, submitIdx int, kind, localName string, ids pathIDs, draftExtra ...string) {
	t.Helper()
	step := path.Steps[submitIdx]
	if step.Refused == nil {
		t.Fatalf("path %s step %d: driveRefusedCreateAndSubmit called on a step with no Refused expectation", path.ID, submitIdx)
	}
	if step.Refused.Code == "" {
		t.Fatalf("path %s step %d: declared Refused.Code is empty — a refusal must name the exact expected code", path.ID, submitIdx)
	}

	id, _, err := actor.Draft(ctx, kind, draftExtra...)
	if err != nil {
		t.Fatalf("path %s step %d: a2a new %s (%s): %v", path.ID, createIdx, kind, actor.System, err)
	}
	ids[localName] = id
	checkStepPredicates(ctx, t, h, actor, path.ID, createIdx, path.Steps[createIdx], ids)

	if _, err := h.submitDrafted(ctx, actor, id); err == nil {
		t.Fatalf("path %s step %d: a2a submit %s (%s): expected a REFUSAL naming %s, got success", path.ID, submitIdx, id, actor.System, step.Refused.Code)
	} else if !strings.Contains(err.Error(), step.Refused.Code) {
		t.Fatalf("path %s step %d: a2a submit %s (%s): refused as expected but combined output does not name %s: %v", path.ID, submitIdx, id, actor.System, step.Refused.Code, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, submitIdx, step, ids)
}

// driveSimpleVerb drives one OP-211 batch verb against targetID, lands +
// syncs its own PR (resolved from the deterministic BranchName(system,
// verb, id) — every simple verb this file drives resolves that way, per
// the existing scenario families' own precedent), then checks stepIdx's
// own predicates.
func driveSimpleVerb(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, transition, targetID string, ids pathIDs, extraArgs ...string) {
	t.Helper()
	verb, ok := simpleVerbForTransition[transition]
	if !ok {
		t.Fatalf("path %s step %d: no simple-verb mapping for transition %q", path.ID, stepIdx, transition)
	}
	args := append([]string{verb}, extraArgs...)
	args = append(args, targetID)
	if _, stderr, err := actor.Run(ctx, args...); err != nil {
		t.Fatalf("path %s step %d: a2a %s %s (%s): %v: %s", path.ID, stepIdx, verb, targetID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, space.BranchName(actor.System, verb, targetID))
	if err != nil {
		t.Fatalf("path %s step %d: resolve PR for %s %s: %v", path.ID, stepIdx, verb, targetID, err)
	}
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync %s PR #%d: %v", path.ID, stepIdx, verb, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, path.Steps[stepIdx], ids)
}

// driveRespondBundle drives ONE `a2a respond --result <result> <parentID>`
// call, which realizes THREE declared steps in a single PR (D-026's
// draft+submit collapse, plus the parent's own TRespond event landing in
// the same write — pathcatalogue_paths.go's own comment on this exact
// bundle): the response's (Kind=Response,TCreate), (Kind=Response,TSubmit)
// and the parent's own (Kind=Question|WorkRequest,TRespond). idxCreate/
// idxSubmit/idxRespond are path.Steps' own indices for those three, in that
// order; responseLocalName is the symbolic name later steps use to refer to
// the minted response (e.g. "response").
func driveRespondBundle(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, idxCreate, idxSubmit, idxRespond int, parentID, result, responseLocalName string, ids pathIDs) string {
	t.Helper()
	respondKey, respondBranch := respondOperation(actor.System, parentID, result)
	if _, stderr, err := actor.Run(ctx, respondCommandArgs(parentID, result)...); err != nil {
		t.Fatalf("path %s: a2a respond --result %s %s (%s): %v: %s", path.ID, result, parentID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, respondBranch)
	if err != nil {
		t.Fatalf("path %s: resolve respond PR for %s: %v", path.ID, parentID, err)
	}
	responseID, err := operationArtifactID(pr.Body, respondKey, parentID, "XS-")
	if err != nil {
		t.Fatalf("path %s: resolve response id from respond PR #%d: %v", path.ID, pr.Number, err)
	}
	ids[responseLocalName] = responseID
	if err := subfamAwaitGreenAndLand(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s: land+sync respond PR #%d: %v", path.ID, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, idxCreate, path.Steps[idxCreate], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxSubmit, path.Steps[idxSubmit], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxRespond, path.Steps[idxRespond], ids)
	return responseID
}

// driveSecondRespondBundle drives a SECOND `a2a respond --result <result>
// --field title=<distinguishingTitle> <parentID>` call on a parent that
// already has one landed response (3.4.6's multi-response allowance,
// Family 14's own multi-response reconciliation paths,
// pathcatalogue_paths.go). The plain `--result`-only form driveRespondBundle
// uses would mint the IDENTICAL content-derived responseID as the FIRST
// call (RespondCommand.Run's own HIGH-1 fix-wave doc comment: "a retry with
// IDENTICAL inputs reproduces the IDENTICAL responseID"), landing on the
// funnel's SAME dedup branch instead of authoring a genuinely second
// response — confirmed by cmd_lifecycle_test.go's own
// TestVerifyMultiResponseDoesNotAutoClose precedent ("Second response MUST
// carry different content"). respondOperationWithFields (operationpull.go)
// is the harness-side key derivation generalized to thread the SAME fields
// map the real CLI call carries, so the resolved branch matches the
// product's own operationKey exactly.
func driveSecondRespondBundle(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, idxCreate, idxSubmit, idxRespond int, parentID, result, distinguishingTitle, responseLocalName string, ids pathIDs) string {
	t.Helper()
	fields := map[string]string{"title": distinguishingTitle}
	respondKey, respondBranch := respondOperationWithFields(actor.System, parentID, result, fields, nil)
	args := append([]string{}, respondCommandArgs(parentID, result)...)
	// respondCommandArgs' own last element is parentID; insert --field
	// before it so parentID stays the trailing positional argument.
	args = append(args[:len(args)-1], "--field", "title="+distinguishingTitle, parentID)
	if _, stderr, err := actor.Run(ctx, args...); err != nil {
		t.Fatalf("path %s: a2a respond --result %s --field title=%q %s (%s): %v: %s", path.ID, result, distinguishingTitle, parentID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, respondBranch)
	if err != nil {
		t.Fatalf("path %s: resolve second respond PR for %s: %v", path.ID, parentID, err)
	}
	responseID, err := operationArtifactID(pr.Body, respondKey, parentID, "XS-")
	if err != nil {
		t.Fatalf("path %s: resolve second response id from respond PR #%d: %v", path.ID, pr.Number, err)
	}
	ids[responseLocalName] = responseID
	if err := subfamAwaitGreenAndLand(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s: land+sync second respond PR #%d: %v", path.ID, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, idxCreate, path.Steps[idxCreate], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxSubmit, path.Steps[idxSubmit], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxRespond, path.Steps[idxRespond], ids)
	return responseID
}

// driveVerifyThenAutoClose drives `a2a verify <responseID>` — which, for a
// single-response exchange, ALSO closes the parent in the SAME PR (D-024's
// convenience, cmd_lifecycle.go VerifyCommand.Run: "len(result.Responses)
// == 1" closes the parent too). Every declared path pairing
// (Kind=Response,TVerify) with a following (Kind=Question|WorkRequest,
// TClose) step relies on this ONE call for both — an explicit second
// `a2a close` would be refused (already closed). idxVerify/idxClose are
// path.Steps' own indices for the two declared steps this one call
// realizes.
func driveVerifyThenAutoClose(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, idxVerify, idxClose int, responseID string, ids pathIDs) {
	t.Helper()
	// rules-that-reach-2026-08 P1: `a2a verify` now refuses at SUBMIT when
	// verdicts[] does not name every acceptance criterion the parent
	// declares (REF-023), where it used to succeed and be caught at merge.
	// Every draft this driver mints carries exactly one criterion
	// (draftfields.go: `acceptance_criteria=["the artifact validates"]`), and
	// the bare-string form means the ordinal token is the referent. Naming it
	// keeps these paths exercising the lifecycle route they exist for rather
	// than the refusal — the refusal has its own coverage in internal/e2e.
	stdout, stderr, err := actor.Run(ctx, "verify", responseID,
		"--verdict", "0:met:"+actor.System)
	if err != nil {
		t.Fatalf("path %s step %d: a2a verify %s (%s): %v: %s", path.ID, idxVerify, responseID, actor.System, err, strings.TrimSpace(stderr))
	}
	// The PR number comes from the verb's OWN output, not from guessing a
	// branch name, and that is load-bearing rather than tidier. Supplying
	// --verdict switches the funnel's dedup key from the batch's artifact id
	// to operation.Verify's content-derived key (cmd_lifecycle.go's own
	// comment above operationKey says so: two invocations naming the same
	// targets with DIFFERENT judgements must not collide onto one
	// content-independent branch). So the branch stops carrying the response
	// id, and matchCompositeBranch — which looks a "+"-separated artifact id
	// up under "a2a/<system>/verify/" — can no longer find it. Reading the
	// number the command itself printed is independent of the grammar.
	prNumber, perr := prNumberFromVerbOutput(stdout)
	if perr != nil {
		t.Fatalf("path %s step %d: resolve verify PR for %s: %v (stdout=%q)", path.ID, idxVerify, responseID, perr, strings.TrimSpace(stdout))
	}
	pr := branchPull{Number: prNumber}
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync verify PR #%d: %v", path.ID, idxVerify, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, idxVerify, path.Steps[idxVerify], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxClose, path.Steps[idxClose], ids)
}

// driveDisputeBundle drives ONE `a2a dispute --reason <text> <responseID>`
// call, which realizes both the response's own (Kind=Response,TDispute)
// AND the parent's own (Kind=Question|WorkRequest,TDispute) row — D-024's
// reopening side effect is fold's OWN cross-artifact interpretation of the
// SAME authored event (cmd_lifecycle.go DisputeCommand's own doc comment:
// "never a second authored event"), not a second CLI call.
func driveDisputeBundle(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, idxResponseDispute, idxParentDispute int, responseID string, ids pathIDs) {
	t.Helper()
	if _, stderr, err := actor.Run(ctx, "dispute", "--reason", "path driver: verification found a discrepancy", responseID); err != nil {
		t.Fatalf("path %s step %d: a2a dispute %s (%s): %v: %s", path.ID, idxResponseDispute, responseID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, space.BranchName(actor.System, "dispute", responseID))
	if err != nil {
		t.Fatalf("path %s step %d: resolve dispute PR for %s: %v", path.ID, idxResponseDispute, responseID, err)
	}
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync dispute PR #%d: %v", path.ID, idxResponseDispute, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, idxResponseDispute, path.Steps[idxResponseDispute], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxParentDispute, path.Steps[idxParentDispute], ids)
}

// driveContractRepublish drives `a2a contract publish <id> --bump <bump>`
// on an ALREADY-landed contract (no --staging: contractPublishVersionArgs'
// own doc comment, "an empty staging path publishes the already-landed
// contract") — the shape a SECOND publish on a contract first landed via
// the generic `a2a submit` path (submitFirstTransition) needs, since that
// first submit consumed the draft and left no local staging tree to point
// `--staging` at. Returns the real version string the product minted
// (published.Plan.TargetVersion), so a caller that needs to name this
// version later (deprecate/retire) never has to guess it.
func driveContractRepublish(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, contractID, bump string, ids pathIDs) string {
	t.Helper()
	published, pr, err := contractPublishPull(ctx, h, actor, []string{"contract", "publish", contractID, "--bump", bump})
	if err != nil {
		t.Fatalf("path %s step %d: a2a contract publish %s --bump %s (%s): %v", path.ID, stepIdx, contractID, bump, actor.System, err)
	}
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync contract publish PR #%d: %v", path.ID, stepIdx, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, path.Steps[stepIdx], ids)
	return published.Plan.TargetVersion
}

// driveDeprecateBundle drives ONE `a2a contract deprecate` call, which
// authors the contract's own TDeprecate event AND a linked deprecation
// announcement IN THE SAME PR (§5.4; happyContractLifecycle's own doc
// comment on this file's sibling scenario family) — realizing THREE
// declared steps: (Kind=Contract,TDeprecate), (Kind=Announcement,TCreate),
// (Kind=Announcement,TPublish).
func driveDeprecateBundle(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, idxDeprecate, idxAnnCreate, idxAnnPublish int, contractID, version, successor, sunset, announcementLocalName string, ids pathIDs) string {
	t.Helper()
	key, branch := contractDeprecateOperation(actor.System, contractID, version, successor, sunset)
	if _, stderr, err := actor.Run(ctx, "contract", "deprecate", contractID,
		"--version", version, "--successor", successor, "--sunset", sunset); err != nil {
		t.Fatalf("path %s step %d: a2a contract deprecate %s (%s): %v: %s", path.ID, idxDeprecate, contractID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, branch)
	if err != nil {
		t.Fatalf("path %s step %d: resolve deprecate PR for %s: %v", path.ID, idxDeprecate, contractID, err)
	}
	announcementID, err := operationArtifactID(pr.Body, key, contractID, "XA-")
	if err != nil {
		t.Fatalf("path %s step %d: resolve announcement id from deprecate PR #%d: %v", path.ID, idxDeprecate, pr.Number, err)
	}
	ids[announcementLocalName] = announcementID
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync deprecate PR #%d: %v", path.ID, idxDeprecate, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, idxDeprecate, path.Steps[idxDeprecate], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxAnnCreate, path.Steps[idxAnnCreate], ids)
	checkStepPredicates(ctx, t, h, actor, path.ID, idxAnnPublish, path.Steps[idxAnnPublish], ids)
	return announcementID
}

// driveContractRetire drives `a2a contract retire <id> --version <v>` —
// same single-artifact/single-PR shape as driveSimpleVerb, but under the
// `contract` sub-command rather than a top-level OP-211 verb.
func driveContractRetire(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, contractID, version string, ids pathIDs) {
	t.Helper()
	if _, stderr, err := actor.Run(ctx, "contract", "retire", contractID, "--version", version); err != nil {
		t.Fatalf("path %s step %d: a2a contract retire %s (%s): %v: %s", path.ID, stepIdx, contractID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, space.BranchName(actor.System, "contract-retire", contractID))
	if err != nil {
		t.Fatalf("path %s step %d: resolve contract-retire PR for %s: %v", path.ID, stepIdx, contractID, err)
	}
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync contract-retire PR #%d: %v", path.ID, stepIdx, pr.Number, err)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, path.Steps[stepIdx], ids)
}

// driveRefusedContractRetire attempts `a2a contract retire <id> --version <v>`
// and asserts the REAL binary REFUSES it — the paired negative control for
// driveContractRetire: same CLI shape, opposite outcome (Step.Refused's own
// doc comment, pathgrammar.go). stepIdx's own Step MUST carry a non-nil
// Refused; t.Fatalf's immediately if it does not, so a mis-wired call is
// caught at the point of use rather than silently checking the wrong thing.
//
// Consequence 1 (Refused's own doc comment): a non-zero exit whose combined
// stdout+stderr names step.Refused.Code. A wrong-code refusal is a FAILURE,
// not a pass — checked with the same plain substring idiom internal/e2e's
// own POL-006 assertions use (contract_cov_test.go:50), never a weaker "any
// non-zero exit" check.
//
// Unlike driveContractRetire, this never calls h.pullForBranch or
// happyLandAndSync — a refused act opens no PR to pull (the funnel/host is
// never reached, same fact
// TestContractRetireBlockedUnackedConsumerDirectConstruction proves
// at the internal/e2e layer via fakeHost.Opens). stepIdx's own Predicates are
// checked immediately after the refusal is confirmed, against whatever state
// the refusal LEFT UNCHANGED (pathgrammar.go's own walkSteps non-advance).
func driveRefusedContractRetire(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, contractID, version string, ids pathIDs) {
	t.Helper()
	step := path.Steps[stepIdx]
	if step.Refused == nil {
		t.Fatalf("path %s step %d: driveRefusedContractRetire called on a step with no Refused expectation", path.ID, stepIdx)
	}
	if step.Refused.Code == "" {
		// An empty Code would make the check below (strings.Contains(combined,
		// "")) vacuously true for ANY non-zero exit — exactly the failure
		// mode MUTATION PROOF (a) demonstrated is dangerous, reachable here
		// without touching this function at all. Refused loudly rather than
		// silently accepting whatever the binary said.
		t.Fatalf("path %s step %d: declared Refused.Code is empty — a refusal must name the exact expected code", path.ID, stepIdx)
	}
	stdout, stderr, err := actor.Run(ctx, "contract", "retire", contractID, "--version", version)
	if err == nil {
		t.Fatalf("path %s step %d: a2a contract retire %s (%s): expected a REFUSAL naming %s, got success: stdout=%s", path.ID, stepIdx, contractID, actor.System, step.Refused.Code, stdout)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, step.Refused.Code) {
		t.Fatalf("path %s step %d: a2a contract retire %s (%s): refused as expected but combined output does not name %s: %s", path.ID, stepIdx, contractID, actor.System, step.Refused.Code, combined)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, step, ids)
}

// driveRefusedSimpleVerb attempts one of driveSimpleVerb's own OP-211 batch
// verbs and asserts the REAL binary REFUSES it — the paired negative control
// for driveSimpleVerb, same CLI shape (simpleVerbForTransition's own mapping,
// `<verb> [extraArgs] <targetID>`), opposite outcome (Step.Refused's own doc
// comment, pathgrammar.go). Same shape/consequences as
// driveRefusedContractRetire (this file), generalized off the contract-only
// `contract retire` CLI form to any simple verb this file already knows how
// to drive: stepIdx's own Step MUST carry a non-nil Refused (t.Fatalf's
// immediately if not); a non-zero exit whose combined stdout+stderr names
// step.Refused.Code is the ONLY pass (a wrong-code refusal is a FAILURE, not
// a pass — internal/e2e's own POL-006/LFC- substring idiom); no PR is ever
// pulled (a refused act never reaches the write funnel — same fact
// driveRefusedContractRetire's own doc comment cites for retire, true here
// for the exact same pre-write legality-check ordering every lifecycle verb
// command shares, cmd_lifecycle.go).
//
// Deliberately NOT the stronger form scenarios_illegal_live.go's own
// illegalfamRefusalStep uses (before/after h.countPRsForBranch, asserting
// the refused branch's own PR count never moved): same scope choice
// driveRefusedContractRetire already made — asserted here is the code plus
// the post-refusal state/pendency the brief asks for, not the no-PR-opened
// property, which is a real, narrower guarantee than illegalfamRefusalStep's
// own two-part check.
func driveRefusedSimpleVerb(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, transition, targetID string, ids pathIDs, extraArgs ...string) {
	t.Helper()
	step := path.Steps[stepIdx]
	if step.Refused == nil {
		t.Fatalf("path %s step %d: driveRefusedSimpleVerb called on a step with no Refused expectation", path.ID, stepIdx)
	}
	if step.Refused.Code == "" {
		t.Fatalf("path %s step %d: declared Refused.Code is empty — a refusal must name the exact expected code", path.ID, stepIdx)
	}
	verb, ok := simpleVerbForTransition[transition]
	if !ok {
		t.Fatalf("path %s step %d: no simple-verb mapping for transition %q", path.ID, stepIdx, transition)
	}
	args := append([]string{verb}, extraArgs...)
	args = append(args, targetID)
	stdout, stderr, err := actor.Run(ctx, args...)
	if err == nil {
		t.Fatalf("path %s step %d: a2a %s %s (%s): expected a REFUSAL naming %s, got success: stdout=%s", path.ID, stepIdx, verb, targetID, actor.System, step.Refused.Code, stdout)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, step.Refused.Code) {
		t.Fatalf("path %s step %d: a2a %s %s (%s): refused as expected but combined output does not name %s: %s", path.ID, stepIdx, verb, targetID, actor.System, step.Refused.Code, combined)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, step, ids)
}

// driveRefusedRespond attempts `a2a respond --result <result> <parentID>`
// and asserts the REAL binary REFUSES it — the paired negative control for
// driveRespondBundle, same CLI shape (respondCommandArgs, operationpull.go),
// opposite outcome. Same consequences as driveRefusedSimpleVerb/
// driveRefusedContractRetire: stepIdx's own Step must carry a non-nil
// Refused with a non-empty Code, a non-zero exit whose combined stdout+
// stderr names it is the ONLY pass, and no PR is ever pulled — RespondCommand.Run
// (cmd_lifecycle.go) evaluates fold's own legality verdict strictly before
// minting the response id's draft write or calling the funnel, same
// pre-write ordering every other lifecycle verb shares. Same scope choice
// as driveRefusedSimpleVerb's own doc comment: no no-PR-opened assertion
// (illegalfamRefusalStep's own stronger form) — code plus post-refusal
// state/pendency only.
func driveRefusedRespond(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, parentID, result string, ids pathIDs, extraArgs ...string) {
	t.Helper()
	step := path.Steps[stepIdx]
	if step.Refused == nil {
		t.Fatalf("path %s step %d: driveRefusedRespond called on a step with no Refused expectation", path.ID, stepIdx)
	}
	if step.Refused.Code == "" {
		t.Fatalf("path %s step %d: declared Refused.Code is empty — a refusal must name the exact expected code", path.ID, stepIdx)
	}
	stdout, stderr, err := actor.Run(ctx, respondCommandArgs(parentID, result, extraArgs...)...)
	if err == nil {
		t.Fatalf("path %s step %d: a2a respond --result %s %s (%s): expected a REFUSAL naming %s, got success: stdout=%s", path.ID, stepIdx, result, parentID, actor.System, step.Refused.Code, stdout)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, step.Refused.Code) {
		t.Fatalf("path %s step %d: a2a respond --result %s %s (%s): refused as expected but combined output does not name %s: %s", path.ID, stepIdx, result, parentID, actor.System, step.Refused.Code, combined)
	}
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, step, ids)
}

// syncBoth syncs both checkouts — used between an actor switch so the NEXT
// actor's own read/write sees what the PREVIOUS actor just landed.
func syncBoth(ctx context.Context, t *testing.T, h *harness) {
	t.Helper()
	if _, stderr, err := h.A.Run(ctx, "sync"); err != nil {
		t.Fatalf("a2a sync (A): %v: %s", err, strings.TrimSpace(stderr))
	}
	if _, stderr, err := h.B.Run(ctx, "sync"); err != nil {
		t.Fatalf("a2a sync (B): %v: %s", err, strings.TrimSpace(stderr))
	}
}

// --- per-path drivers ---------------------------------------------------
//
// Each function drives exactly one declared Path's OWN Steps (never its
// precondition's — D4: a chained path replays the precondition by calling
// ITS driver function directly, first). runTag scopes every standing slug
// this call mints (contract/requirement drafts) to the CALLING (sub)test,
// so two subtests sharing one harness — an outer path and its own
// precondition replay — never collide on one.

func runPathContractBaseline(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "contract-baseline-published-settled")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (%s) before draft: %v: %s", path.ID, a.System, err, strings.TrimSpace(stderr))
	}

	slug := liveRunSlug(runTag+"-baseline", h.PRFloor)
	driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "contract", "contract", ids, "--slug", slug)
	return ids
}

func runPathContractSuccessor(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "contract-successor-compatible-publish")
	ids := runPathContractBaseline(ctx, t, h, runTag)
	a := h.A

	ids["contract-version"] = driveContractRepublish(ctx, t, h, a, path, 0, ids["contract"], "minor", ids)
	return ids
}

// runPathQuestionAcknowledged drives the shared create->submit->acknowledge
// prefix (pathcatalogue_paths.go's own "question-lifecycle-acknowledged")
// the two refusal controls below continue from — the SAME three real acts
// question-lifecycle-to-responded's own prefix performs, kept as their own
// standalone driver so a chained refusal path never has to slice into
// runPathQuestionToResponded's own steps (D4: a chained path replays its
// precondition by calling THAT path's own driver function directly).
func runPathQuestionAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-lifecycle-acknowledged")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "question", "question", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TAcknowledge, sub.ID, ids)
	return ids
}

// runPathQuestionCloseBeforeRespondedRefused drives the paired NEGATIVE
// control for `close`'s own table row: A (the asker) attempts `close` while
// the question sits at `acknowledged`, never responded to — refused
// illegal-transition (LFC-001), reached by COMPOSITION (real create/submit/
// acknowledge acts through the real binary), not by seeding a raw prior.
func runPathQuestionCloseBeforeRespondedRefused(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-close-before-responded-refused")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveRefusedSimpleVerb(ctx, t, h, a, path, 0, fold.TClose, ids["question"], ids)
	return ids
}

// runPathQuestionRespondByAskerRefused drives the paired NEGATIVE control
// for `respond`'s own Role: A (the asker, RoleOwner) attempts `respond` on
// its OWN question while it sits at `acknowledged` (a state respond's table
// row DOES admit) — refused unauthorized-actor (LFC-002), because Role
// resolution is about the ACTOR here, not the state.
func runPathQuestionRespondByAskerRefused(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-respond-by-the-asker-refused")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveRefusedRespond(ctx, t, h, a, path, 0, ids["question"], "answered", ids)
	return ids
}

func runPathQuestionToResponded(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-lifecycle-to-responded")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "question", "question", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TAcknowledge, sub.ID, ids)

	driveRespondBundle(ctx, t, h, b, path, 3, 4, 5, sub.ID, "answered", "response", ids)
	return ids
}

func runPathQuestionVerifiedClosed(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-lifecycle-verified-closed")
	ids := runPathQuestionToResponded(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveVerifyThenAutoClose(ctx, t, h, a, path, 0, 1, ids["response"], ids)
	return ids
}

func runPathQuestionDisputed(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-lifecycle-disputed-responder-owes")
	ids := runPathQuestionToResponded(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveDisputeBundle(ctx, t, h, a, path, 0, 1, ids["response"], ids)
	return ids
}

func runPathWorkRequestLifecycle(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-lifecycle-accept-start-respond-verify-close")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "work_request", "work-request", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TAcknowledge, sub.ID, ids)
	driveSimpleVerb(ctx, t, h, b, path, 3, fold.TAccept, sub.ID, ids)
	driveSimpleVerb(ctx, t, h, b, path, 4, fold.TStart, sub.ID, ids)

	driveRespondBundle(ctx, t, h, b, path, 5, 6, 7, sub.ID, "delivered", "response", ids)

	syncBoth(ctx, t, h)
	driveVerifyThenAutoClose(ctx, t, h, a, path, 8, 9, ids["response"], ids)
	return ids
}

func runPathDataLoopSetup(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "data-loop-contract-and-request")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	slug := liveRunSlug(runTag+"-dloop-contract", h.PRFloor)
	driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "contract", "contract", ids, "--slug", slug)

	wr := driveCreateAndFirstTransition(ctx, t, h, a, path, 2, 3, "work_request", "work-request", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 4, fold.TAcknowledge, wr.ID, ids)
	return ids
}

func runPathDataLoopAttemptOneFails(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "data-loop-attempt-one-fails")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	delivery1 := driveCreateAndFirstTransition(ctx, t, h, b, path, 0, 1, "handoff", "delivery-1", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, a, path, 2, fold.TAcknowledge, delivery1.ID, ids)
	driveSimpleVerb(ctx, t, h, a, path, 3, fold.TVerifyFail, delivery1.ID, ids, "--findings", "record_count disagreement (path driver)")

	// The successor ref supersede names must exist as a well-formed id, but
	// fold/the CLI validate no cross-artifact existence for `--refs`
	// (cmd_lifecycle.go's lifecycleRefsFromFlag takes any non-empty
	// string) — so a throwaway LOCAL draft (never submitted, so it never
	// enters the committed mirror or collides with anything; handoff is
	// not a standing type, so it needs no --slug) is enough to mint a
	// well-formed successor id. attempt-two-passes' OWN Steps mint the
	// REAL "delivery-2" separately, when this path is chained under it.
	placeholderID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 4: mint a placeholder successor handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 4, fold.TSupersede, delivery1.ID, ids, "--refs", placeholderID)
	return ids
}

func runPathDataLoopAttemptTwoPasses(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "data-loop-attempt-two-passes")
	ids := runPathDataLoopAttemptOneFails(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	delivery2 := driveCreateAndFirstTransition(ctx, t, h, b, path, 0, 1, "handoff", "delivery-2", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, a, path, 2, fold.TAcknowledge, delivery2.ID, ids)
	driveSimpleVerb(ctx, t, h, a, path, 3, fold.TVerifyPass, delivery2.ID, ids)
	return ids
}

func runPathDataLoopRequestClosed(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "data-loop-request-answered-closed")
	ids := runPathDataLoopAttemptTwoPasses(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveRespondBundle(ctx, t, h, b, path, 0, 1, 2, ids["work-request"], "delivered", "response-to-request", ids)

	syncBoth(ctx, t, h)
	driveVerifyThenAutoClose(ctx, t, h, a, path, 3, 4, ids["response-to-request"], ids)
	return ids
}

// runPathResponseDeliversUnlandedRefused drives judge-the-thing-2026-08 P1's
// own declared path: B answers the acknowledged work_request naming a data
// package the space cannot resolve, and the REAL binary must refuse it,
// naming REF-024.
//
// The package id is well-formed and deliberately absent — a DP id for B's
// own system that no delivery ever created, which is the SAME fact at the
// refusal's seat as the incident's "the payload PR has not merged yet"
// (this tier's host stand-in auto-merges every write, so an open PR is not
// something a path can hold; see the path's own Intent).
func runPathResponseDeliversUnlandedRefused(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "response-delivers-unlanded-package-refused")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveRefusedRespond(ctx, t, h, b, path, 0, ids["work-request"], "delivered", ids,
		"--delivers", unlandedDataPackageID(b.System))
	return ids
}

// unlandedDataPackageID mints a well-formed DP id (spec 05a §T2.1:
// DP-<system>-<YYYYMMDD>-<rand4>, Crockford-base32 suffix) for a package
// that has never been delivered into this space, so ResolveDataPackage
// answers ErrDataPackageNotFound against origin/main. A FIXED literal
// rather than a random one: the refusal must be reproducible, and this id
// must never accidentally collide with a package a sibling path delivers.
func unlandedDataPackageID(system string) string {
	return fmt.Sprintf("DP-%s-20260101-nvr1", system)
}

func runPathContractDeprecateRetire(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "contract-deprecate-retire-after-sunset")
	ids := runPathContractSuccessor(ctx, t, h, runTag)
	a, b := h.A, h.B
	contractID := ids["contract"]

	// B becomes a REGISTERED consumer BEFORE deprecate, so the retire
	// gate's own consumer set (cache.FindRegisteredConsumersForMajor) is
	// non-empty and the "acks AND a passed sunset" gate (W0's C1 decision)
	// is actually exercised rather than vacuously satisfied by zero
	// consumers — `contract adopt` carries no fold Step (03-domain.md
	// §10.5: the transition enum has no `adopt`), same as the
	// announcement's own ack below, so neither is asserted against here.
	syncBoth(ctx, t, h)
	if _, stderr, err := b.Run(ctx, "contract", "adopt", contractID); err != nil {
		t.Fatalf("path %s: a2a contract adopt %s (%s): %v: %s", path.ID, contractID, b.System, err, strings.TrimSpace(stderr))
	}
	adoptPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "contract-adopt", contractID))
	if err != nil {
		t.Fatalf("path %s: resolve contract-adopt PR for %s: %v", path.ID, contractID, err)
	}
	if err := happyLandAndSync(ctx, h, b, adoptPR.Number); err != nil {
		t.Fatalf("path %s: land+sync contract-adopt PR #%d: %v", path.ID, adoptPR.Number, err)
	}

	syncBoth(ctx, t, h)
	successor := contractID + "-successor@2.0.0"
	// A past sunset means retire's own precondition (acks AND sunset
	// passed) needs no injected clock: wall-clock "now" already exceeds
	// it, matching happyContractLifecycle's own `--sunset 2020-01-01`
	// precedent (scenarios_happy_live.go).
	const sunset = "2020-01-01"
	announcementID := driveDeprecateBundle(ctx, t, h, a, path, 0, 1, 2, contractID, ids["contract-version"], successor, sunset, "deprecation-notice", ids)

	// B acknowledges the deprecation announcement — transition-free (D-025),
	// never a Step (pathcatalogue_paths.go's own package doc), so no
	// predicate check follows this call; it exists only to satisfy the
	// retire gate's consumer-ack requirement for real.
	syncBoth(ctx, t, h)
	if _, stderr, err := b.Run(ctx, "ack", announcementID); err != nil {
		t.Fatalf("path %s: a2a ack %s (%s): %v: %s", path.ID, announcementID, b.System, err, strings.TrimSpace(stderr))
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", announcementID))
	if err != nil {
		t.Fatalf("path %s: resolve ack PR for %s: %v", path.ID, announcementID, err)
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		t.Fatalf("path %s: land+sync ack PR #%d: %v", path.ID, ackPR.Number, err)
	}

	syncBoth(ctx, t, h)
	driveContractRetire(ctx, t, h, a, path, 3, contractID, ids["contract-version"], ids)
	return ids
}

// runPathContractRetireRefusedWithoutAck is the paired negative control for
// runPathContractDeprecateRetire (W0's C1 decision: acks AND a passed
// sunset are BOTH required) — same shape EXCEPT B never acknowledges the
// deprecation announcement, so A's retire attempt must be REFUSED naming
// POL-006 and the contract must still be `deprecated` afterwards.
func runPathContractRetireRefusedWithoutAck(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "contract-retire-refused-without-ack")
	ids := runPathContractSuccessor(ctx, t, h, runTag)
	a, b := h.A, h.B
	contractID := ids["contract"]

	// B becomes a REGISTERED consumer BEFORE deprecate, same reasoning as
	// runPathContractDeprecateRetire's own comment: a registered consumer
	// who never acks is what makes the refusal POL-006 specifically —
	// zero registered consumers would let retire proceed regardless of
	// this control's own missing ack, proving nothing about the gate.
	syncBoth(ctx, t, h)
	if _, stderr, err := b.Run(ctx, "contract", "adopt", contractID); err != nil {
		t.Fatalf("path %s: a2a contract adopt %s (%s): %v: %s", path.ID, contractID, b.System, err, strings.TrimSpace(stderr))
	}
	adoptPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "contract-adopt", contractID))
	if err != nil {
		t.Fatalf("path %s: resolve contract-adopt PR for %s: %v", path.ID, contractID, err)
	}
	if err := happyLandAndSync(ctx, h, b, adoptPR.Number); err != nil {
		t.Fatalf("path %s: land+sync contract-adopt PR #%d: %v", path.ID, adoptPR.Number, err)
	}

	syncBoth(ctx, t, h)
	successor := contractID + "-successor@2.0.0"
	// A past sunset, same as runPathContractDeprecateRetire — this control
	// isolates the MISSING ACK as the refusal's cause, not an unpassed
	// sunset.
	const sunset = "2020-01-01"
	driveDeprecateBundle(ctx, t, h, a, path, 0, 1, 2, contractID, ids["contract-version"], successor, sunset, "deprecation-notice", ids)

	// B deliberately never acks the deprecation announcement — the ONE
	// difference from runPathContractDeprecateRetire's own sequence, and
	// the whole point of this control.
	syncBoth(ctx, t, h)
	driveRefusedContractRetire(ctx, t, h, a, path, 3, contractID, ids["contract-version"], ids)
	return ids
}

// --- Family 7 — the non-cooperative counterparty (P11 W3c) --------------

// driveDeclineFullySync drives `a2a decline --reason <text> <targetID>`
// and checks stepIdx's own Predicates ONLY after a full syncBoth — never
// via driveSimpleVerb's own single-actor happyLandAndSync, which syncs
// the ACTING checkout alone. This matters here specifically: decline's
// own Predicates assert NotActionable for BOTH parties (the brief's own
// "leaves both parties' actionable lists" requirement), and every
// question/work_request TEMPLATE defaults to `blocking: true`
// (schemas/templates/v1/question.md, work_request.md) — actionableReasons'
// own condition 4 (p1-or-blocking-open, internal/cache/inbox.go) fires
// for EITHER party while the artifact is still OPEN, regardless of who
// owes the next transition. Checking the NON-acting party's own
// NotActionable predicate before ITS checkout has synced past this
// decline reads the PRE-decline (still-open, still-blocking) truth and
// wrongly reports present=true — confirmed empirically: an earlier
// version of this driver did exactly that (checked the non-acting
// party's predicates via a checkout driveSimpleVerb's own single sync
// never reached), and it failed with "predicate actionable(question):
// got present=true, want present=false" — a driver staleness artifact,
// not a product defect, and it disappeared once both checkouts were
// synced BEFORE the one and only predicate check.
func driveDeclineFullySync(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, targetID, reason string, ids pathIDs) {
	t.Helper()
	if _, stderr, err := actor.Run(ctx, "decline", "--reason", reason, targetID); err != nil {
		t.Fatalf("path %s step %d: a2a decline %s (%s): %v: %s", path.ID, stepIdx, targetID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, space.BranchName(actor.System, "decline", targetID))
	if err != nil {
		t.Fatalf("path %s step %d: resolve decline PR for %s: %v", path.ID, stepIdx, targetID, err)
	}
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync decline PR #%d: %v", path.ID, stepIdx, pr.Number, err)
	}
	syncBoth(ctx, t, h)
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, path.Steps[stepIdx], ids)
}

// runPathQuestionDeclinedAfterAcknowledge drives DELIVERABLE 1a: the
// target declines outright after acknowledging.
func runPathQuestionDeclinedAfterAcknowledge(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-declined-after-acknowledge")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 0, ids["question"],
		"path driver: counterparty declines outright (W3c)", ids)
	return ids
}

// runPathWorkRequestDeclinedFromSubmitted drives DELIVERABLE 1b: the
// target declines straight from `submitted`, without ever acknowledging.
func runPathWorkRequestDeclinedFromSubmitted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-declined-from-submitted")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "work_request", "work-request", ids)

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 2, sub.ID,
		"path driver: counterparty declines without acknowledging (W3c)", ids)
	return ids
}

// runPathQuestionBlockThenUnblock drives DELIVERABLE 2: the target blocks
// an in-flight question from `accepted`, then unblocks it — the load-
// bearing assertion (path's own Intent) is that unblock restores
// `accepted` specifically, not `acknowledged` or `in_progress`. The
// blocker is a throwaway LOCAL draft handoff (never submitted), the same
// `--refs` pattern runPathDataLoopAttemptOneFails already established
// (fold/the CLI validate no cross-artifact existence for `--refs`).
func runPathQuestionBlockThenUnblock(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-block-then-unblock-restores-accepted")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["question"], ids)

	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 1: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TBlock, ids["question"], ids, "--refs", placeholderBlockerID)

	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TUnblock, ids["question"], ids)
	return ids
}

// --- Family 8 — requirement (P11 W3c DELIVERABLE 1) ---------------------

// runPathRequirementPublishedAcknowledged drives the shared prefix every
// other requirement path below continues from — the control for spec §15's
// pendency fix (requirementPaths' own doc comment, pathcatalogue_paths.go).
func runPathRequirementPublishedAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-lifecycle-published-acknowledged")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	slug := liveRunSlug(runTag+"-requirement", h.PRFloor)
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "requirement", "requirement", ids, "--slug", slug)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TAcknowledge, sub.ID, ids)
	return ids
}

// runPathRequirementDeclinedFromPublished drives the TARGET declining a
// requirement straight from `published`, without ever acknowledging — a
// fresh standalone instance (a `create` step always starts a genuinely new
// artifact).
func runPathRequirementDeclinedFromPublished(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-declined-from-published")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	slug := liveRunSlug(runTag+"-requirement", h.PRFloor)
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "requirement", "requirement", ids, "--slug", slug)

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 2, sub.ID,
		"path driver: target declines a requirement outright (W3c)", ids)
	return ids
}

// runPathRequirementDeclinedFromAcknowledged drives the TARGET declining
// after already acknowledging, continuing from the family's own shared
// prefix.
func runPathRequirementDeclinedFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-declined-from-acknowledged")
	ids := runPathRequirementPublishedAcknowledged(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 0, ids["requirement"],
		"path driver: target declines a requirement after acknowledging (W3c)", ids)
	return ids
}

// runPathRequirementWithdrawnFromPublished drives the REQUESTER withdrawing
// its own published requirement before the target acts — Role Owner
// (requirementRows()), the requester's own uncooperative branch (contrast
// decline, the target's own). A fresh standalone instance, same reasoning
// as runPathRequirementDeclinedFromPublished.
func runPathRequirementWithdrawnFromPublished(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-withdrawn-from-published")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	slug := liveRunSlug(runTag+"-requirement", h.PRFloor)
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "requirement", "requirement", ids, "--slug", slug)

	driveSimpleVerb(ctx, t, h, a, path, 2, fold.TWithdraw, sub.ID, ids)
	return ids
}

// runPathRequirementWithdrawnFromAcknowledged drives the REQUESTER
// withdrawing after the target has already acknowledged, continuing from
// the family's own shared prefix.
func runPathRequirementWithdrawnFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-withdrawn-from-acknowledged")
	ids := runPathRequirementPublishedAcknowledged(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, a, path, 0, fold.TWithdraw, ids["requirement"], ids)
	return ids
}

// --- Family 9 — decision (P11 W3c DELIVERABLE 2) -------------------------

// runPathDecisionPartialQuorumThenApproved drives DELIVERABLE 2's own
// load-bearing assertion: B approves first (quorum 1 of 2 — the decision
// must still be `proposed`, A still owing `approve`), then A approves
// second (quorum 2 of 2 — `approved`, terminal).
func runPathDecisionPartialQuorumThenApproved(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "decision-lifecycle-partial-quorum-then-approved")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "decision", "decision", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TApprove, sub.ID, ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, a, path, 3, fold.TApprove, sub.ID, ids)
	return ids
}

// driveLandedSuccessorDraft mints a throwaway `decision` draft in actor's
// OWN checkout, submits it (a decision's own first committed event is
// `propose`, §3.4.4), lands + syncs its PR, and returns the minted id —
// the fixture both decision-supersede successor drivers below need,
// factored out here rather than duplicated a second time.
//
// This is deliberately NOT driveCreateAndFirstTransition (this file's own
// predecessor-decision helper): that helper calls checkStepPredicates
// against path.Steps[createIdx]/[firstIdx], which requires the PATH's own
// declared journey to carry steps for the successor's create+propose too.
// The successor here is a supporting fixture, not a step of the path
// under test — no predicate is ever asserted about it, matching how a
// bare placeholder was minted before this wave (this wave's report,
// D-1/D-2/D-3: the placeholder itself was never the problem; where it was
// minted was).
//
// Landing (not merely drafting) is load-bearing, discovered this wave:
// `a2a new` writes into `.a2a/staging/`, a directory
// MirrorResolver.ensureIndex (internal/cli/adapters.go) never walks — only
// `.a2a/cache/mirrors/<space>/` is indexed. A bare, unsubmitted draft is
// therefore UNRESOLVABLE from ANY checkout, including its own author's,
// regardless of this wave's own resolver fix — the brief's own literal
// "draft the successor in the acting checkout" wording undersold what
// resolvability actually requires; this driver mints the fact the
// wording was reaching for.
func driveLandedSuccessorDraft(ctx context.Context, t *testing.T, h *harness, actor *checkout, pathID string, stepIdx int) string {
	t.Helper()
	id, _, err := actor.Draft(ctx, "decision")
	if err != nil {
		t.Fatalf("path %s step %d: mint a successor decision draft for --refs (%s): %v", pathID, stepIdx, actor.System, err)
	}
	sub, err := h.submitDrafted(ctx, actor, id)
	if err != nil {
		t.Fatalf("path %s step %d: a2a submit %s (successor decision, %s): %v", pathID, stepIdx, id, actor.System, err)
	}
	if err := happyLandAndSync(ctx, h, actor, sub.PRNumber); err != nil {
		t.Fatalf("path %s step %d: land+sync successor decision submit PR #%d: %v", pathID, stepIdx, sub.PRNumber, err)
	}
	return id
}

// runPathDecisionApprovedSuperseded drives decisionRows()'s own escape
// hatch from `approved` (Role Any — table.go's own documented deviation:
// this row's OWN envelope, the PREDECESSOR's, cannot itself carry the
// successor's state). The successor is landed in the ACTING checkout
// (SystemA) via driveLandedSuccessorDraft — RESOLVABLE from A's own
// mirror, reaching `proposed` (a decision's own first committed event,
// §3.4.4) — but never APPROVED: the PreconditionSuccessorApproved row's
// own precondition is genuinely UNSATISFIED (resolved-but-failing, spec
// 06 AC 1/AC 9(b) — never the UNRESOLVED case, which pairs an LFC-006
// advisory alongside LFC-005).
func runPathDecisionApprovedSuperseded(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "decision-approved-superseded")
	ids := runPathDecisionPartialQuorumThenApproved(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	successorID := driveLandedSuccessorDraft(ctx, t, h, a, path.ID, 0)
	driveRefusedSimpleVerb(ctx, t, h, a, path, 0, fold.TSupersede, ids["decision"], ids, "--refs", successorID)
	return ids
}

// runPathDecisionRejected drives reject — Role Approver with NO quorum
// gate (decisionRows(): a single approver's reject moves `proposed`
// straight to `rejected`, unlike approve's own dynamic quorum row) — a
// fresh standalone instance (propose/reject and propose/approve are
// mutually exclusive branches from the SAME `proposed` state).
func runPathDecisionRejected(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "decision-lifecycle-rejected")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "decision", "decision", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TReject, sub.ID, ids,
		"--reason", "path driver: this decision no longer applies (W3c)")
	return ids
}

// runPathDecisionRejectedSuperseded drives decisionRows()'s own escape
// hatch from `rejected` (Role Any, same documented deviation as the
// approved branch) — internal/pendency's own row: "settled; the revision
// is a NEW XD on the thread, not a move owed on this one". A POSITIVE
// control (this wave's report): the successor is landed in the ACTING
// checkout (SystemA) via driveLandedSuccessorDraft, mirroring
// internal/cli/cmd_lifecycle_test.go's own green
// TestSupersedeDecisionRegressionFix/rejected_by_successor_author_succeeds
// — its author (SystemA) equals the acting system driving `supersede`
// below, so PreconditionSuccessorAuthor is satisfied and the act SUCCEEDS.
func runPathDecisionRejectedSuperseded(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "decision-rejected-superseded")
	ids := runPathDecisionRejected(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	successorID := driveLandedSuccessorDraft(ctx, t, h, a, path.ID, 0)
	driveSimpleVerb(ctx, t, h, a, path, 0, fold.TSupersede, ids["decision"], ids, "--refs", successorID)
	return ids
}

// runPathDecisionProposedWithdrawn drives the third branch off `proposed`,
// beside approve and reject: the author withdrawing their own proposal.
//
// P1 added the row for the departed-approver case, and P8's resting-state
// gate then found nothing entered {decision, withdrawn}. The transition is
// `RoleOwner` UNCONDITIONALLY, so this driver needs no departure — which is
// the distinction P1's own exemption blurred and P8's gate made visible: the
// SCENARIO wants approvers gone, the TRANSITION never did.
func runPathDecisionProposedWithdrawn(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "decision-proposed-withdrawn")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "decision", "decision", ids)

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, a, path, 2, fold.TWithdraw, sub.ID, ids)
	return ids
}

// --- Family 15 — the departed counterparty (P8 wave 31, correcting wave
// 30B's genesis-timed attempt) ---------------------------------------------
//
// Every driver below runs against a dedicated harness (runDepartedCounter-
// partyPaths' own caller, logic_runner_live_test.go) — never one of the
// ordinary round-robin path-space harnesses — but, unlike wave 30B, that
// harness starts with the ORDINARY two-active-participant scaffold: `h.B`
// DOES issue CLI-observable state (it is addressed by `required_approvers`/
// `to` while still active) before ensureFamily15CounterpartyActive/
// departFamily15Counterparty below flip it mid-path. See
// pathcatalogue_paths.go's own Family 15 doc comment for the product-level
// reasoning (D-017, REF-006, CC-062) these three drivers exercise, and for
// why genesis departure could never reach past the first submission.

// ensureFamily15CounterpartyActive restores h.B to "active" before a Family
// 15 driver's own first step, unconditionally. The three drivers below
// share ONE dedicated harness (runDepartedCounterpartyPaths runs them
// sequentially against the SAME h, never t.Parallel()'d against each
// other), and departFamily15Counterparty's own mid-path mutation is exactly
// as irreversible as wave 30B's genesis one was — no `a2a` CLI verb ever
// rejoins a departed participant either. Without this call, the SECOND and
// THIRD drivers to run would try to address an already-`left` h.B at their
// own create+first-transition step and hit the identical REF-006 refusal
// this family exists to route around. SetParticipantStatusMidPath's own
// alreadyAtStatus contract makes this a true no-op (no clone, no commit, no
// push) on a harness where h.B is already active — the harness's own
// starting state, and every path's own state once this call has run — so
// calling it unconditionally, rather than only from the second/third
// driver, is what keeps all three independent of run order.
func ensureFamily15CounterpartyActive(ctx context.Context, t *testing.T, h *harness) {
	t.Helper()
	if err := SetParticipantStatusMidPath(ctx, h.Seam.CloneURL(), t.TempDir(), h.B.System, "active"); err != nil {
		t.Fatalf("Family 15: ensure %s active before path: %v", h.B.System, err)
	}
}

// departFamily15Counterparty is the MID-PATH manifest mutation itself: it
// flips h.B (the sole required approver / handoff receiver a Family 15
// driver's own create+first-transition step has JUST addressed, while
// active) to "left" on the harness's own origin
// (SetParticipantStatusMidPath, provision_live.go), syncs A's own local
// mirror so it can see the change (`a2a thread` reads the mirror, not the
// remote directly), then asserts CC-062's own orphaned-counterparty
// transfer — PendingOn(A) and ExpectedTransition("") on artifactLocalName —
// through the SAME checker functions every declared Predicate uses
// (checkPendingOn/checkExpectedTransition), so this assertion can never
// silently drift from what a real Step's own Predicate would check. It is
// deliberately NOT a declared Step's own Predicates: the manifest edit
// itself has no fold transition (pathgrammar.go's own Step doc comment —
// an act with no fold transition is never expressible as one), so there is
// no Step for pathgrammar to attach it to; the driver performs and asserts
// the real act around the declared Steps instead, exactly as that doc
// comment says a transition-free domain act always must.
func departFamily15Counterparty(ctx context.Context, t *testing.T, h *harness, pathID, artifactLocalName, id string) {
	t.Helper()
	if err := SetParticipantStatusMidPath(ctx, h.Seam.CloneURL(), t.TempDir(), h.B.System, "left"); err != nil {
		t.Fatalf("path %s: depart %s mid-path: %v", pathID, h.B.System, err)
	}
	if _, stderr, err := h.A.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) after departing %s: %v: %s", pathID, h.B.System, err, strings.TrimSpace(stderr))
	}

	ids := pathIDs{artifactLocalName: id}
	label := fmt.Sprintf("path %s mid-path departure (system=%s)", pathID, h.B.System)
	checkPendingOn(ctx, t, h, h.A, label, PendingOn(artifactLocalName, SystemA), ids)
	checkExpectedTransition(ctx, t, h.A, label, ExpectedTransition(artifactLocalName, ""), ids)
}

// runPathDecisionProposedWithdrawnDeparted drives
// decision-proposed-withdrawn-by-author-after-approvers-left: A proposes a
// decision whose SOLE required approver is B (fully active, and addressed
// by `required_approvers`), B then leaves (departFamily15Counterparty), and
// A withdraws — Role Owner, unconditional. The `--field
// required_approvers=[<bravo>]` override is applied HERE, not in
// pathcatalogue_paths.go (untagged, and it may not reference a real system
// id — catalogue.go's own SystemA/SystemB row-labels are the only system
// vocabulary that file may use): draftFieldArgs' own decision default
// names BOTH systems as required approvers, which would leave A (still
// active) owing `approve` too, and CC-062's owners-empties-to-orphan
// transfer never fires.
func runPathDecisionProposedWithdrawnDeparted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "decision-proposed-withdrawn-by-author-after-approvers-left")
	ids := pathIDs{}
	a := h.A

	ensureFamily15CounterpartyActive(ctx, t, h)

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "decision", "decision", ids,
		"--field", "required_approvers=["+h.B.System+"]")

	departFamily15Counterparty(ctx, t, h, path.ID, "decision", sub.ID)

	driveSimpleVerb(ctx, t, h, a, path, 2, fold.TWithdraw, sub.ID, ids)
	return ids
}

// runPathDecisionProposedSupersededDeparted drives
// decision-proposed-superseded-by-author-after-approvers-left: the same
// active-then-departed-sole-approver setup as the withdraw driver above,
// ending in decisionRows()' own direct `proposed -> superseded` escape
// hatch instead (Role Owner) — a fresh decision instance, same reasoning
// runPathDecisionApprovedSuperseded's own doc comment gives for `--refs`.
func runPathDecisionProposedSupersededDeparted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "decision-proposed-superseded-by-author-after-approvers-left")
	ids := pathIDs{}
	a := h.A

	ensureFamily15CounterpartyActive(ctx, t, h)

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "decision", "decision", ids,
		"--field", "required_approvers=["+h.B.System+"]")

	departFamily15Counterparty(ctx, t, h, path.ID, "decision", sub.ID)

	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "decision", sub.ID, ids)
	return ids
}

// runPathHandoffSubmittedSupersededDeparted drives
// handoff-submitted-superseded-by-producer-after-receiver-left: A (the
// producer) submits a handoff whose sole `to:` target is B (fully active,
// and addressed by the default `to=[peer]`), B then leaves
// (departFamily15Counterparty), A never sees an acknowledge land, and
// supersedes straight from `submitted`. No draft-field override is
// needed: draftFieldArgs' own handoff default already names `to=[peer]`
// (the single target), and `peer` here is B.
func runPathHandoffSubmittedSupersededDeparted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "handoff-submitted-superseded-by-producer-after-receiver-left")
	ids := pathIDs{}
	a := h.A

	ensureFamily15CounterpartyActive(ctx, t, h)

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "handoff", "handoff", ids)

	departFamily15Counterparty(ctx, t, h, path.ID, "handoff", sub.ID)

	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "handoff", sub.ID, ids)
	return ids
}

// runDepartedCounterpartyPaths drives pathdrivability.go's own
// departedCounterpartyPathIDs, one t.Run subtest per id, against h — a
// harness whose OWN space (never one of the ordinary round-robin
// path-space harnesses runConformancePaths splits drivenPathIDs() across)
// seeds systemBravo `left` at genesis (newLogicHarness(ctx, t, systemBravo),
// logic_runner_live_test.go). Deliberately its own small dispatch loop, not
// folded into runPathGroup: these three ids are EXCLUDED from
// runConformancePaths' own split (pathdrivability.go's own doc comment
// explains why sharing a space would be wrong), so the two loops must never
// silently reconverge into driving the same id twice or, worse, driving
// these three against the wrong harness.
func runDepartedCounterpartyPaths(ctx context.Context, t *testing.T, h *harness) {
	t.Helper()
	for _, id := range departedCounterpartyPathIDs() {
		id := id
		t.Run(id, func(t *testing.T) {
			defer func() {
				_, _, _ = h.A.Run(ctx, "sync")
			}()
			driver, ok := driverForPath[id]
			if !ok {
				t.Fatalf("pathdriver: %q is in departedCounterpartyPathIDs() but has no entry in driverForPath", id)
			}
			driver(ctx, t, h, id)
		})
	}
}

// runClassificationBilateralPaths drives pathdrivability.go's own
// classificationBilateralDedicatedSpacePathIDs, one t.Run subtest per id,
// against h — a harness whose OWN space (never one of the ordinary
// round-robin path-space harnesses runConformancePaths splits
// drivenPathIDs() across) carries a THIRD, always-active, never-addressed
// participant added at genesis (provision_live.go's
// AddInertParticipantGenesis, logic_runner_live_test.go's own
// classificationHarness). Deliberately its own small dispatch loop, the
// same shape runDepartedCounterpartyPaths above uses and for the identical
// reason: this one id is EXCLUDED from runConformancePaths' own split
// (classificationBilateralDedicatedSpacePathIDs()'s own doc comment
// explains why sharing a space would be wrong), so the two loops must never
// silently reconverge into driving the same id twice or, worse, driving it
// against the wrong harness.
func runClassificationBilateralPaths(ctx context.Context, t *testing.T, h *harness) {
	t.Helper()
	for _, id := range classificationBilateralDedicatedSpacePathIDs() {
		id := id
		t.Run(id, func(t *testing.T) {
			defer func() {
				_, _, _ = h.A.Run(ctx, "sync")
			}()
			driver, ok := driverForPath[id]
			if !ok {
				t.Fatalf("pathdriver: %q is in classificationBilateralDedicatedSpacePathIDs() but has no entry in driverForPath", id)
			}
			driver(ctx, t, h, id)
		})
	}
}

// --- Family 10 — supersede (P11 W3e Deliverable 1) -----------------------

// driveSupersedeWithPlaceholderRef drives `a2a supersede --refs <placeholder> <targetID>`
// — supersede is REQUIRE-REFS (cmd_lifecycle.go's lifecycleVerbTable), and
// fold/the CLI validate no cross-artifact existence for `--refs`
// (runPathDataLoopAttemptOneFails' own comment, first established this
// pattern), so a throwaway, never-submitted successor draft of the SAME
// kind is enough to mint a well-formed ref — the same pattern
// runPathDecisionApprovedSuperseded/runPathDecisionRejectedSuperseded (one
// family up) already established inline, generalized here because Family
// 10 has enough callers to earn it (rule of three, several times over).
// slugArgs threads a `--slug <value>` for a standing successor draft
// (requirement) — checkout.Draft's own standingDraftTypes refusal demands
// one; omit slugArgs entirely for every non-standing kind this family
// drives (question, work_request, announcement).
func driveSupersedeWithPlaceholderRef(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, kind, targetID string, ids pathIDs, slugArgs ...string) {
	t.Helper()
	placeholderID, _, err := actor.Draft(ctx, kind, slugArgs...)
	if err != nil {
		t.Fatalf("path %s step %d: mint a placeholder successor %s draft for --refs (%s): %v", path.ID, stepIdx, kind, actor.System, err)
	}
	driveSimpleVerb(ctx, t, h, actor, path, stepIdx, fold.TSupersede, targetID, ids, "--refs", placeholderID)
}

func runPathQuestionSupersedeFromSubmitted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-supersede-from-submitted")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "question", "question", ids)

	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "question", sub.ID, ids)
	return ids
}

func runPathQuestionSupersedeFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-supersede-from-acknowledged")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "question", ids["question"], ids)
	return ids
}

func runPathQuestionSupersedeFromAccepted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-supersede-from-accepted")
	ids := runPathQuestionBlockThenUnblock(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "question", ids["question"], ids)
	return ids
}

func runPathQuestionSupersedeFromInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-supersede-from-in-progress")
	ids := runPathQuestionDisputed(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "question", ids["question"], ids)
	return ids
}

func runPathQuestionSupersedeFromBlocked(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-supersede-from-blocked")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["question"], ids)

	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 1: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TBlock, ids["question"], ids, "--refs", placeholderBlockerID)

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "question", ids["question"], ids)
	return ids
}

func runPathQuestionSupersedeFromResponded(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-supersede-from-responded")
	ids := runPathQuestionToResponded(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "question", ids["question"], ids)
	return ids
}

func runPathWorkRequestSupersedeFromSubmitted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-supersede-from-submitted")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "work_request", "work-request", ids)

	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "work_request", sub.ID, ids)
	return ids
}

func runPathWorkRequestSupersedeFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-supersede-from-acknowledged")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "work_request", ids["work-request"], ids)
	return ids
}

func runPathWorkRequestSupersedeFromAccepted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-supersede-from-accepted")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 1, "work_request", ids["work-request"], ids)
	return ids
}

func runPathWorkRequestSupersedeFromInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-supersede-from-in-progress")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TStart, ids["work-request"], ids)

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "work_request", ids["work-request"], ids)
	return ids
}

func runPathWorkRequestSupersedeFromBlocked(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-supersede-from-blocked")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 0: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TBlock, ids["work-request"], ids, "--refs", placeholderBlockerID)

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 1, "work_request", ids["work-request"], ids)
	return ids
}

func runPathWorkRequestSupersedeFromResponded(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-supersede-from-responded")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveRespondBundle(ctx, t, h, b, path, 0, 1, 2, ids["work-request"], "delivered", "response", ids)

	syncBoth(ctx, t, h)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 3, "work_request", ids["work-request"], ids)
	return ids
}

func runPathAnnouncementSupersedeFromPublished(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "announcement-supersede-from-published")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "announcement", "announcement", ids)

	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "announcement", sub.ID, ids)
	return ids
}

func runPathRequirementSupersedeFromPublished(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-supersede-from-published")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	slug := liveRunSlug(runTag+"-requirement", h.PRFloor)
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "requirement", "requirement", ids, "--slug", slug)

	successorSlug := liveRunSlug(runTag+"-requirement-successor", h.PRFloor)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 2, "requirement", sub.ID, ids, "--slug", successorSlug)
	return ids
}

func runPathRequirementSupersedeFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-supersede-from-acknowledged")
	ids := runPathRequirementPublishedAcknowledged(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	successorSlug := liveRunSlug(runTag+"-requirement-successor", h.PRFloor)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "requirement", ids["requirement"], ids, "--slug", successorSlug)
	return ids
}

func runPathRequirementSupersedeFromDeclined(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-supersede-from-declined")
	ids := runPathRequirementDeclinedFromPublished(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	successorSlug := liveRunSlug(runTag+"-requirement-successor", h.PRFloor)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "requirement", ids["requirement"], ids, "--slug", successorSlug)
	return ids
}

func runPathRequirementSupersedeFromWithdrawn(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "requirement-supersede-from-withdrawn")
	ids := runPathRequirementWithdrawnFromPublished(ctx, t, h, runTag)
	a := h.A

	successorSlug := liveRunSlug(runTag+"-requirement-successor", h.PRFloor)
	driveSupersedeWithPlaceholderRef(ctx, t, h, a, path, 0, "requirement", ids["requirement"], ids, "--slug", successorSlug)
	return ids
}

// --- Family 11 — cancel (P11 W3e) ----------------------------------------

// driveCancelFullySync drives `a2a cancel <targetID>` and checks stepIdx's
// own Predicates ONLY after a full syncBoth — same reasoning as
// driveDeclineFullySync's own doc comment (Family 7, above): cancel's own
// Predicates ALSO assert NotActionable for BOTH parties (the brief's own
// "assert the debt is gone from BOTH parties afterwards" requirement), and
// question's own template defaults `blocking: true` — a stale, pre-sync
// read of the non-acting party's own checkout would still show the
// true-before-cancel positive (p1-or-blocking-open) and wrongly look like
// a passing negative. `cancel` requires no `--reason`
// (cmd_lifecycle.go's own lifecycleVerbTable) — the one difference from
// driveDeclineFullySync's own call signature.
func driveCancelFullySync(ctx context.Context, t *testing.T, h *harness, actor *checkout, path Path, stepIdx int, targetID string, ids pathIDs) {
	t.Helper()
	if _, stderr, err := actor.Run(ctx, "cancel", targetID); err != nil {
		t.Fatalf("path %s step %d: a2a cancel %s (%s): %v: %s", path.ID, stepIdx, targetID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranch(ctx, space.BranchName(actor.System, "cancel", targetID))
	if err != nil {
		t.Fatalf("path %s step %d: resolve cancel PR for %s: %v", path.ID, stepIdx, targetID, err)
	}
	if err := happyLandAndSync(ctx, h, actor, pr.Number); err != nil {
		t.Fatalf("path %s step %d: land+sync cancel PR #%d: %v", path.ID, stepIdx, pr.Number, err)
	}
	syncBoth(ctx, t, h)
	checkStepPredicates(ctx, t, h, actor, path.ID, stepIdx, path.Steps[stepIdx], ids)
}

func runPathQuestionCancelFromSubmitted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-cancel-from-submitted")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "question", "question", ids)

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 2, sub.ID, ids)
	return ids
}

func runPathQuestionCancelFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-cancel-from-acknowledged")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 0, ids["question"], ids)
	return ids
}

func runPathQuestionCancelFromAccepted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-cancel-from-accepted")
	ids := runPathQuestionBlockThenUnblock(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 0, ids["question"], ids)
	return ids
}

func runPathQuestionCancelFromInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-cancel-from-in-progress")
	ids := runPathQuestionDisputed(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 0, ids["question"], ids)
	return ids
}

func runPathWorkRequestCancelFromSubmitted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-cancel-from-submitted")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "work_request", "work-request", ids)

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 2, sub.ID, ids)
	return ids
}

func runPathWorkRequestCancelFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-cancel-from-acknowledged")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a := h.A

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 0, ids["work-request"], ids)
	return ids
}

func runPathWorkRequestCancelFromAccepted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-cancel-from-accepted")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 1, ids["work-request"], ids)
	return ids
}

func runPathWorkRequestCancelFromInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-cancel-from-in-progress")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TStart, ids["work-request"], ids)

	syncBoth(ctx, t, h)
	driveCancelFullySync(ctx, t, h, a, path, 2, ids["work-request"], ids)
	return ids
}

// --- Family 12 — decline, the remaining from-states (P11 W3e) -----------

func runPathQuestionDeclinedFromSubmitted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-declined-from-submitted")
	ids := pathIDs{}
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (A) before draft: %v: %s", path.ID, err, strings.TrimSpace(stderr))
	}
	sub := driveCreateAndFirstTransition(ctx, t, h, a, path, 0, 1, "question", "question", ids)

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 2, sub.ID,
		"path driver: counterparty declines without acknowledging (W3e)", ids)
	return ids
}

func runPathQuestionDeclinedFromAccepted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-declined-from-accepted")
	ids := runPathQuestionBlockThenUnblock(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 0, ids["question"],
		"path driver: counterparty declines after re-accepting (W3e)", ids)
	return ids
}

func runPathQuestionDeclinedFromInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-declined-from-in-progress")
	ids := runPathQuestionDisputed(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 0, ids["question"],
		"path driver: counterparty declines after a dispute reopened the question (W3e)", ids)
	return ids
}

func runPathWorkRequestDeclinedFromAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-declined-from-acknowledged")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 0, ids["work-request"],
		"path driver: counterparty declines after acknowledging (W3e)", ids)
	return ids
}

func runPathWorkRequestDeclinedFromAccepted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-declined-from-accepted")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 1, ids["work-request"],
		"path driver: counterparty declines after accepting (W3e)", ids)
	return ids
}

func runPathWorkRequestDeclinedFromInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-declined-from-in-progress")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TStart, ids["work-request"], ids)

	syncBoth(ctx, t, h)
	driveDeclineFullySync(ctx, t, h, b, path, 2, ids["work-request"],
		"path driver: counterparty declines after starting (W3e)", ids)
	return ids
}

// --- Family 13 — block/unblock, the remaining from-states (P11 W3d-W3f) ---

func runPathQuestionBlockThenUnblockRestoresAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-block-then-unblock-restores-acknowledged")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 0: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TBlock, ids["question"], ids, "--refs", placeholderBlockerID)

	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TUnblock, ids["question"], ids)
	return ids
}

func runPathQuestionBlockThenUnblockRestoresInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-block-then-unblock-restores-in-progress")
	ids := runPathQuestionDisputed(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 0: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TBlock, ids["question"], ids, "--refs", placeholderBlockerID)

	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TUnblock, ids["question"], ids)
	return ids
}

func runPathWorkRequestBlockThenUnblockRestoresAcknowledged(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-block-then-unblock-restores-acknowledged")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 0: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TBlock, ids["work-request"], ids, "--refs", placeholderBlockerID)

	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TUnblock, ids["work-request"], ids)
	return ids
}

func runPathWorkRequestBlockThenUnblockRestoresAccepted(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-block-then-unblock-restores-accepted")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)

	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 1: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TBlock, ids["work-request"], ids, "--refs", placeholderBlockerID)

	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TUnblock, ids["work-request"], ids)
	return ids
}

func runPathWorkRequestBlockThenUnblockRestoresInProgress(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-block-then-unblock-restores-in-progress")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TStart, ids["work-request"], ids)

	placeholderBlockerID, _, err := b.Draft(ctx, "handoff")
	if err != nil {
		t.Fatalf("path %s step 2: mint a placeholder blocker handoff draft (%s): %v", path.ID, b.System, err)
	}
	driveSimpleVerb(ctx, t, h, b, path, 2, fold.TBlock, ids["work-request"], ids, "--refs", placeholderBlockerID)

	driveSimpleVerb(ctx, t, h, b, path, 3, fold.TUnblock, ids["work-request"], ids)
	return ids
}

// --- Family 14 — granularity variants (P11 W3d-W3f) ----------------------

func runPathQuestionAcceptStartRespond(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-lifecycle-accept-start-respond")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["question"], ids)
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TStart, ids["question"], ids)

	driveRespondBundle(ctx, t, h, b, path, 2, 3, 4, ids["question"], "answered", "response", ids)
	return ids
}

func runPathQuestionAcceptedRespondDirect(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-lifecycle-accepted-respond-direct")
	ids := runPathQuestionAcknowledged(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["question"], ids)

	driveRespondBundle(ctx, t, h, b, path, 1, 2, 3, ids["question"], "answered", "response", ids)
	return ids
}

func runPathWorkRequestAcceptedRespondDirect(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-accepted-respond-direct")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)

	driveRespondBundle(ctx, t, h, b, path, 1, 2, 3, ids["work-request"], "delivered", "response", ids)
	return ids
}

func runPathWorkRequestDisputedSenderOwes(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-lifecycle-disputed-sender-owes")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	a, b := h.A, h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TStart, ids["work-request"], ids)

	responseID := driveRespondBundle(ctx, t, h, b, path, 2, 3, 4, ids["work-request"], "delivered", "response", ids)

	syncBoth(ctx, t, h)
	driveDisputeBundle(ctx, t, h, a, path, 5, 6, responseID, ids)
	return ids
}

func runPathQuestionMultiResponseReconciliation(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "question-multi-response-reconciliation")
	ids := runPathQuestionToResponded(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSecondRespondBundle(ctx, t, h, b, path, 0, 1, 2, ids["question"], "answered",
		"second response (path driver, W3d-W3f multi-response reconciliation)", "response-2", ids)
	return ids
}

func runPathWorkRequestMultiResponseReconciliation(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-multi-response-reconciliation")
	ids := runPathDataLoopSetup(ctx, t, h, runTag)
	b := h.B

	syncBoth(ctx, t, h)
	driveSimpleVerb(ctx, t, h, b, path, 0, fold.TAccept, ids["work-request"], ids)
	driveSimpleVerb(ctx, t, h, b, path, 1, fold.TStart, ids["work-request"], ids)

	driveRespondBundle(ctx, t, h, b, path, 2, 3, 4, ids["work-request"], "delivered", "response", ids)

	syncBoth(ctx, t, h)
	driveSecondRespondBundle(ctx, t, h, b, path, 5, 6, 7, ids["work-request"], "delivered",
		"second response (path driver, W3d-W3f multi-response reconciliation)", "response-2", ids)
	return ids
}

// runPathWorkRequestBadNeededByFormatRefused drives no-silent-yes-2026-08/P3
// stage 1's own declared path (pathcatalogue_format.go): a work_request
// drafted with a malformed `needed_by` is refused at `a2a submit`, naming
// SCH-012 — see that path's own Intent for the honest limitation (this
// reds until internal/validate/schema_class.go's format-keyword mapping
// lands, which is outside this stage's own allowlist).
func runPathWorkRequestBadNeededByFormatRefused(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "work-request-bad-needed-by-format-refused")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (%s) before draft: %v: %s", path.ID, a.System, err, strings.TrimSpace(stderr))
	}

	driveRefusedCreateAndSubmit(ctx, t, h, a, path, 0, 1, "work_request", "work-request", ids,
		"--field", "needed_by=next tuesday")
	return ids
}

// runPathRestrictedClassificationExceedsBilateralRefused drives
// no-silent-yes-2026-08/P3 stage 2's own declared path
// (pathcatalogue_classification.go): a question drafted with
// classification=restricted is refused at `a2a submit`, naming POL-024 —
// genuinely driven against h, a dedicated space carrying a THIRD,
// always-active, never-addressed participant added at genesis
// (provision_live.go's AddInertParticipantGenesis,
// logic_runner_live_test.go's own classificationHarness), so the space's
// own active membership {alpha, bravo, charlie} genuinely exceeds a
// from=alpha/to=[bravo] submission's own {from} ∪ to. See that path's own
// Intent for the full finding.
func runPathRestrictedClassificationExceedsBilateralRefused(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs {
	t.Helper()
	path := mustPath(t, "restricted-classification-exceeds-bilateral-refused")
	ids := pathIDs{}
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		t.Fatalf("path %s: a2a sync (%s) before draft: %v: %s", path.ID, a.System, err, strings.TrimSpace(stderr))
	}

	driveRefusedCreateAndSubmit(ctx, t, h, a, path, 0, 1, "question", "question", ids,
		"--field", "classification=restricted")
	return ids
}

// driverForPath maps a drivenPathIDs() entry to the function that drives it
// STANDALONE (its own runTag == its own path id). A path missing here would
// panic runConformancePaths rather than silently not run — deliberate: this
// map and drivenPathIDs() must stay in lockstep, and TestPathDrivabilityCoversEveryPath
// (untagged) only proves the ID LIST is complete, not that this map is;
// the panic is this file's own half of that guarantee.
// driverForPath deliberately keeps an entry for every path this file
// implements a driver for, whether or not pathdrivability.go currently
// drives it. undrivablePaths() carried five parked paths once (the
// declaration defect where every non-question (Kind,TSubmit) step declared
// the transition owed AFTER acknowledge instead of acknowledge itself,
// contradicting both the pendency table and the real `a2a thread --json`)
// and was empty from that fix until P11 W3c, which parked one path there
// again for a real reason (requirement-satisfied — see undrivablePaths()'
// own entry). Keeping the map total means parking a path is a one-line
// move of its id, never a driver rewrite.
var driverForPath = map[string]func(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs{
	"contract-baseline-published-settled":                      runPathContractBaseline,
	"contract-successor-compatible-publish":                    runPathContractSuccessor,
	"question-lifecycle-acknowledged":                          runPathQuestionAcknowledged,
	"question-close-before-responded-refused":                  runPathQuestionCloseBeforeRespondedRefused,
	"question-respond-by-the-asker-refused":                    runPathQuestionRespondByAskerRefused,
	"question-lifecycle-to-responded":                          runPathQuestionToResponded,
	"question-lifecycle-verified-closed":                       runPathQuestionVerifiedClosed,
	"question-lifecycle-disputed-responder-owes":               runPathQuestionDisputed,
	"work-request-lifecycle-accept-start-respond-verify-close": runPathWorkRequestLifecycle,
	"data-loop-contract-and-request":                           runPathDataLoopSetup,
	"data-loop-attempt-one-fails":                              runPathDataLoopAttemptOneFails,
	"data-loop-attempt-two-passes":                             runPathDataLoopAttemptTwoPasses,
	"data-loop-request-answered-closed":                        runPathDataLoopRequestClosed,
	"response-delivers-unlanded-package-refused":               runPathResponseDeliversUnlandedRefused,
	"contract-deprecate-retire-after-sunset":                   runPathContractDeprecateRetire,
	"contract-retire-refused-without-ack":                      runPathContractRetireRefusedWithoutAck,
	"question-declined-after-acknowledge":                      runPathQuestionDeclinedAfterAcknowledge,
	"work-request-declined-from-submitted":                     runPathWorkRequestDeclinedFromSubmitted,
	"question-block-then-unblock-restores-accepted":            runPathQuestionBlockThenUnblock,
	"requirement-lifecycle-published-acknowledged":             runPathRequirementPublishedAcknowledged,
	"requirement-declined-from-published":                      runPathRequirementDeclinedFromPublished,
	"requirement-declined-from-acknowledged":                   runPathRequirementDeclinedFromAcknowledged,
	"requirement-withdrawn-from-published":                     runPathRequirementWithdrawnFromPublished,
	"requirement-withdrawn-from-acknowledged":                  runPathRequirementWithdrawnFromAcknowledged,
	"decision-lifecycle-partial-quorum-then-approved":          runPathDecisionPartialQuorumThenApproved,
	"decision-approved-superseded":                             runPathDecisionApprovedSuperseded,
	"decision-proposed-withdrawn":                              runPathDecisionProposedWithdrawn,
	"decision-lifecycle-rejected":                              runPathDecisionRejected,
	"decision-rejected-superseded":                             runPathDecisionRejectedSuperseded,
	"question-supersede-from-submitted":                        runPathQuestionSupersedeFromSubmitted,
	"question-supersede-from-acknowledged":                     runPathQuestionSupersedeFromAcknowledged,
	"question-supersede-from-accepted":                         runPathQuestionSupersedeFromAccepted,
	"question-supersede-from-in-progress":                      runPathQuestionSupersedeFromInProgress,
	"question-supersede-from-blocked":                          runPathQuestionSupersedeFromBlocked,
	"question-supersede-from-responded":                        runPathQuestionSupersedeFromResponded,
	"work-request-supersede-from-submitted":                    runPathWorkRequestSupersedeFromSubmitted,
	"work-request-supersede-from-acknowledged":                 runPathWorkRequestSupersedeFromAcknowledged,
	"work-request-supersede-from-accepted":                     runPathWorkRequestSupersedeFromAccepted,
	"work-request-supersede-from-in-progress":                  runPathWorkRequestSupersedeFromInProgress,
	"work-request-supersede-from-blocked":                      runPathWorkRequestSupersedeFromBlocked,
	"work-request-supersede-from-responded":                    runPathWorkRequestSupersedeFromResponded,
	"announcement-supersede-from-published":                    runPathAnnouncementSupersedeFromPublished,
	"requirement-supersede-from-published":                     runPathRequirementSupersedeFromPublished,
	"requirement-supersede-from-acknowledged":                  runPathRequirementSupersedeFromAcknowledged,
	"requirement-supersede-from-declined":                      runPathRequirementSupersedeFromDeclined,
	"requirement-supersede-from-withdrawn":                     runPathRequirementSupersedeFromWithdrawn,
	"question-cancel-from-submitted":                           runPathQuestionCancelFromSubmitted,
	"question-cancel-from-acknowledged":                        runPathQuestionCancelFromAcknowledged,
	"question-cancel-from-accepted":                            runPathQuestionCancelFromAccepted,
	"question-cancel-from-in-progress":                         runPathQuestionCancelFromInProgress,
	"work-request-cancel-from-submitted":                       runPathWorkRequestCancelFromSubmitted,
	"work-request-cancel-from-acknowledged":                    runPathWorkRequestCancelFromAcknowledged,
	"work-request-cancel-from-accepted":                        runPathWorkRequestCancelFromAccepted,
	"work-request-cancel-from-in-progress":                     runPathWorkRequestCancelFromInProgress,
	"question-declined-from-submitted":                         runPathQuestionDeclinedFromSubmitted,
	"question-declined-from-accepted":                          runPathQuestionDeclinedFromAccepted,
	"question-declined-from-in-progress":                       runPathQuestionDeclinedFromInProgress,
	"work-request-declined-from-acknowledged":                  runPathWorkRequestDeclinedFromAcknowledged,
	"work-request-declined-from-accepted":                      runPathWorkRequestDeclinedFromAccepted,
	"work-request-declined-from-in-progress":                   runPathWorkRequestDeclinedFromInProgress,
	"question-block-then-unblock-restores-acknowledged":        runPathQuestionBlockThenUnblockRestoresAcknowledged,
	"question-block-then-unblock-restores-in-progress":         runPathQuestionBlockThenUnblockRestoresInProgress,
	"work-request-block-then-unblock-restores-acknowledged":    runPathWorkRequestBlockThenUnblockRestoresAcknowledged,
	"work-request-block-then-unblock-restores-accepted":        runPathWorkRequestBlockThenUnblockRestoresAccepted,
	"work-request-block-then-unblock-restores-in-progress":     runPathWorkRequestBlockThenUnblockRestoresInProgress,
	"question-lifecycle-accept-start-respond":                  runPathQuestionAcceptStartRespond,
	"question-lifecycle-accepted-respond-direct":               runPathQuestionAcceptedRespondDirect,
	"work-request-accepted-respond-direct":                     runPathWorkRequestAcceptedRespondDirect,
	"work-request-lifecycle-disputed-sender-owes":              runPathWorkRequestDisputedSenderOwes,
	"question-multi-response-reconciliation":                   runPathQuestionMultiResponseReconciliation,
	"work-request-multi-response-reconciliation":               runPathWorkRequestMultiResponseReconciliation,
	"work-request-bad-needed-by-format-refused":                runPathWorkRequestBadNeededByFormatRefused,
	"restricted-classification-exceeds-bilateral-refused":      runPathRestrictedClassificationExceedsBilateralRefused,

	// Family 15 — the departed counterparty (P8 wave 30B). Present in this
	// map for the SAME reason every other entry is (mustPath/runPathGroup's
	// own fatal-on-miss guard), but never reached through runPathGroup: the
	// three ids are excluded from runConformancePaths' own split
	// (drivenPathIDs() minus departedCounterpartyPathIDs()) and driven only
	// by runDepartedCounterpartyPaths, against its own dedicated harness.
	"decision-proposed-withdrawn-by-author-after-approvers-left":   runPathDecisionProposedWithdrawnDeparted,
	"decision-proposed-superseded-by-author-after-approvers-left":  runPathDecisionProposedSupersededDeparted,
	"handoff-submitted-superseded-by-producer-after-receiver-left": runPathHandoffSubmittedSupersededDeparted,
}

// runConformancePaths drives every drivenPathIDs() entry as its own t.Run
// subtest, named by the path's id — DELIVERABLE 1's own requirement:
//
//	go test ./internal/livee2e/... -tags=livee2e -race -count=1 \
//	  -run '^TestLogicMatrix$/^group-[0-9]+$/^<path-id>$'
//
// isolates exactly that one path's OWN assertions. The `group-[0-9]+`
// segment is required — the ids are nested under this function's own group
// subtests, and a two-segment pattern silently matches nothing and passes
// (P11 wave D; see this file's header for the measurement). The residual this
// command does NOT eliminate: `-run` prunes t.Run subtests, never
// TestLogicMatrix's own body — driveFamilies' 30-row family matrix (called
// immediately before this function, in TestLogicMatrix's own body) still
// runs regardless of the filter. Wrapping driveFamilies itself in its own
// t.Run was considered and rejected: TestLogicTierWritesNothingOutsideItsOwnTempDirs
// and the D-7 marker (logicTierMarkerLine) both read state driveFamilies
// populates directly off `run`/`report` in TestLogicMatrix's own body, and
// restructuring that call site is a decision this brief's scope does not
// grant — reported as a deviation, not hidden.
//
// h is the SAME harness TestLogicMatrix already built via newLogicHarness —
// no second rig (D1).
func runConformancePaths(ctx context.Context, t *testing.T, harnesses []*harness) {
	t.Helper()
	if len(harnesses) == 0 {
		t.Fatal("runConformancePaths: no harness supplied — nothing could be driven, and a green result would mean nothing")
	}

	// The paths are split across the harnesses supplied, and each GROUP runs
	// as one parallel subtest while the paths inside a group stay strictly
	// sequential against their own space.
	//
	// Spec 10 §5 is the constraint this shape exists to satisfy:
	// "Concurrency is a knob, and 1 must reproduce today exactly." With one
	// harness there is one group, containing every path in drivenPathIDs()
	// order, run by a single t.Parallel subtest — same order, same space,
	// same verdicts as the serial version. That is what makes a regression
	// reversible by one constant instead of a revert, and bisecting a flake
	// possible at all.
	//
	// Groups, not per-path harnesses: setup is ~1s of provisioning plus a
	// binary build, so 71 harnesses would spend more on setup than the
	// serialisation costs. Contiguous blocks rather than round-robin, so a
	// group's contents stay predictable from the declaration order when
	// reading a failure.
	//
	// departedCounterpartyPathIDs() and classificationBilateralDedicated-
	// SpacePathIDs() (pathdrivability.go) are SUBTRACTED before splitting —
	// those ids are driven separately, against their own dedicated
	// harnesses (runDepartedCounterpartyPaths / runClassificationBilateralPaths),
	// never one of THESE ordinary ones (each function's own doc comment
	// says why sharing a space would be wrong — a silently departed
	// counterparty, or a silently exceeded bilateral audience, every OTHER
	// path here still assumes is not the case). They stay IN drivenPathIDs()
	// itself (the union gate, pathdrivability_test.go, is honest that they
	// ARE driven) — only this split excludes them.
	excluded := map[string]bool{}
	for _, id := range departedCounterpartyPathIDs() {
		excluded[id] = true
	}
	for _, id := range classificationBilateralDedicatedSpacePathIDs() {
		excluded[id] = true
	}
	var regular []string
	for _, id := range drivenPathIDs() {
		if !excluded[id] {
			regular = append(regular, id)
		}
	}
	groups := splitPathIDs(regular, len(harnesses))
	for i, ids := range groups {
		if len(ids) == 0 {
			continue
		}
		h := harnesses[i]
		ids := ids
		t.Run(fmt.Sprintf("group-%d", i+1), func(t *testing.T) {
			t.Parallel()
			runPathGroup(ctx, t, h, ids)
		})
	}
}

// splitPathIDs cuts ids into at most n contiguous groups, as evenly as the
// count allows. n <= 1 returns exactly one group holding everything, which is
// the identity case spec 10 §5 requires.
func splitPathIDs(ids []string, n int) [][]string {
	if n <= 1 || len(ids) == 0 {
		return [][]string{ids}
	}
	if n > len(ids) {
		n = len(ids)
	}
	out := make([][]string, 0, n)
	base, extra := len(ids)/n, len(ids)%n
	start := 0
	for i := 0; i < n; i++ {
		size := base
		if i < extra {
			size++
		}
		out = append(out, ids[start:start+size])
		start += size
	}
	return out
}

// runPathGroup drives one group's paths in order against ONE harness. The
// paths inside a group are never concurrent with each other: they share a
// space and each resets both checkouts when it returns, which is exactly the
// coupling P10 W1 removed BETWEEN suites and must not reintroduce WITHIN one.
func runPathGroup(ctx context.Context, t *testing.T, h *harness, ids []string) {
	t.Helper()
	for _, id := range ids {
		id := id
		t.Run(id, func(t *testing.T) {
			// Park both checkouts on a clean main when this subtest returns,
			// pass OR fail (runContractIntegrityScenarios' own precedent,
			// scenarios_contract_integrity_live.go: "leave both checkouts on
			// a clean main so whichever family runs next does not inherit a
			// dirty working tree that looks like its own bug"). Every
			// existing scenario family already does this; this subtest is
			// the first CALLER of driveFamilies' own harness that runs
			// BEFORE it, so it is this file's job to keep that same
			// property, not driveFamilies'. Best-effort, errors ignored by
			// design — same as the existing precedent.
			defer func() {
				_, _, _ = h.A.Run(ctx, "sync")
				_, _, _ = h.B.Run(ctx, "sync")
			}()
			driver, ok := driverForPath[id]
			if !ok {
				t.Fatalf("pathdriver: %q is in drivenPathIDs() but has no entry in driverForPath", id)
			}
			driver(ctx, t, h, id)
		})
	}
}
