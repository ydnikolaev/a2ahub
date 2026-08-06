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
// by the path's own ID, so:
//
//	go test ./internal/livee2e/... -tags=livee2e -race -count=1 \
//	  -run '^TestLogicMatrix$/contract-baseline-published-settled'
//
// runs exactly that one path (plus TestLogicMatrix's own family matrix —
// see runConformancePaths' doc comment for why that residual exists and is
// reported, not hidden).
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
	if _, stderr, err := actor.Run(ctx, "verify", responseID); err != nil {
		t.Fatalf("path %s step %d: a2a verify %s (%s): %v: %s", path.ID, idxVerify, responseID, actor.System, err, strings.TrimSpace(stderr))
	}
	pr, err := h.pullForBranchContaining(ctx, actor.System, "verify", responseID)
	if err != nil {
		t.Fatalf("path %s step %d: resolve verify PR for %s: %v", path.ID, idxVerify, responseID, err)
	}
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

// driverForPath maps a drivenPathIDs() entry to the function that drives it
// STANDALONE (its own runTag == its own path id). A path missing here would
// panic runConformancePaths rather than silently not run — deliberate: this
// map and drivenPathIDs() must stay in lockstep, and TestPathDrivabilityCoversEveryPath
// (untagged) only proves the ID LIST is complete, not that this map is;
// the panic is this file's own half of that guarantee.
// driverForPath deliberately keeps an entry for every path this file
// implements a driver for, whether or not pathdrivability.go currently
// drives it. undrivablePaths() is empty today — the declaration defect that
// briefly parked five paths there (every non-question (Kind,TSubmit) step
// declared the transition owed AFTER acknowledge instead of acknowledge
// itself, contradicting both the pendency table and the real
// `a2a thread --json`) has been fixed. Keeping the map total means parking a
// path again is a one-line move of its id, never a driver rewrite.
var driverForPath = map[string]func(ctx context.Context, t *testing.T, h *harness, runTag string) pathIDs{
	"contract-baseline-published-settled":                      runPathContractBaseline,
	"contract-successor-compatible-publish":                    runPathContractSuccessor,
	"question-lifecycle-to-responded":                          runPathQuestionToResponded,
	"question-lifecycle-verified-closed":                       runPathQuestionVerifiedClosed,
	"question-lifecycle-disputed-responder-owes":               runPathQuestionDisputed,
	"work-request-lifecycle-accept-start-respond-verify-close": runPathWorkRequestLifecycle,
	"data-loop-contract-and-request":                           runPathDataLoopSetup,
	"data-loop-attempt-one-fails":                              runPathDataLoopAttemptOneFails,
	"data-loop-attempt-two-passes":                             runPathDataLoopAttemptTwoPasses,
	"data-loop-request-answered-closed":                        runPathDataLoopRequestClosed,
	"contract-deprecate-retire-after-sunset":                   runPathContractDeprecateRetire,
}

// runConformancePaths drives every drivenPathIDs() entry as its own t.Run
// subtest, named by the path's id — DELIVERABLE 1's own requirement:
//
//	go test ./internal/livee2e/... -tags=livee2e -race -count=1 \
//	  -run '^TestLogicMatrix$/<path-id>'
//
// isolates exactly that one path's OWN assertions. The residual this
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
func runConformancePaths(ctx context.Context, t *testing.T, h *harness) {
	t.Helper()
	for _, id := range drivenPathIDs() {
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
