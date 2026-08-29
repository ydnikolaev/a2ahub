package livee2e

import (
	"sort"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// restingcoverage_test.go is P8's resting-state analogue of
// pathcoverage_test.go's own transition gate (plan
// docs/features/archive/agent-exchange-2026-08/plans/08-paths.plan.md, D1/D4):
// every fold.RestingStates() pair must be ENTERED by some declared Path's own
// Step — reached, not merely defined by the enumerator — or listed in
// restingStateExemptions() with a reason that names WHY no path can ever
// reach it, mirroring uncoveredTransitions() exactly: a per-pair reason,
// never a bare name list, and the gate asserts every exemption still
// carries one.
//
// UNTAGGED — runs under the plain `go test ./internal/livee2e/...`, same
// precedent pathcoverage_test.go's own doc comment sets ("a gate that only
// runs during a 40-minute live run is a gate nobody runs").

// restingStateExemption pairs one fold.KindRestingState this gate cannot
// credit with the reason no declared path can ever ENTER it.
type restingStateExemption struct {
	fold.KindRestingState
	reason string
}

func krs(kind fold.Kind, state fold.State) fold.KindRestingState {
	return fold.KindRestingState{Kind: kind, State: state}
}

// restingStateExemptions is the honest gap list (D4): resting-state members
// fold.RestingStates() enumerates that no declared path can ever ENTER,
// each with a written reason — never a bare name. Unlike
// uncoveredTransitions() (a class-oriented helper for six families), this
// list is deliberately expected to stay SHORT: D4 names exactly one member
// today, and the plan is explicit that P1's plan forbids an allowlist in
// ITS totality gate for a different reason (every member there is
// answerable) that does not transfer here.
func restingStateExemptions() []restingStateExemption {
	return []restingStateExemption{
		{
			KindRestingState: krs(fold.KindDecision, fold.StateDraft),
			reason: "D4 (plan 08-paths.plan.md): a decision's entry transition is " +
				"`propose` (fold/table.go: {KindDecision, StateDraft, TPropose, " +
				"StateProposed}) — decisionRows() carries no row landing ON draft " +
				"either, so the ONLY way this grammar ever resolves a decision to " +
				"StateDraft is a `create` step's own resolveCreate " +
				"(fold.NewResult), which describes a STAGED, uncommitted artifact " +
				"('a2a new' writes to local staging only, 'a2a submit' commits the " +
				"artifact already proposed) — never something a shipped surface " +
				"could read back at rest. This is the identical reasoning " +
				"assertedTriple's own doc comment (reason 1, pathcoverage_test.go) " +
				"already gives for excluding a decision's create step from " +
				"transition-coverage credit, applied here to resting-state entry: " +
				"no CLI verb commits a decision with zero events, so no path can " +
				"ever ENTER {decision draft} even though fold.RestingStates() " +
				"(correctly) enumerates it as a pair Fold can compute " +
				"(postSubmissionState(KindDecision) == StateDraft).",
		},
	}
}

// enteredRestingStates walks every declared path's OWN Steps (mirroring
// TestPathCatalogueCoversEveryTransition's own `covered` map construction)
// and unions the (Kind, To) of every landed step into the set of resting
// states some path actually ENTERS — recording which path id first entered
// it, for the "both entered and exempt" contradiction check below.
//
// A step's resolved To is skipped when !assertedTriple(to) — today that
// means exactly `to == fold.StateDraft` — for the same reason
// assertedTriple's own doc comment (reason 1) already gives: a `create`
// step's resolved state describes a STAGED, uncommitted artifact
// (resolveCreate/fold.NewResult), never a state some shipped `--json`
// surface could read back as a subject genuinely AT REST. Crediting it here
// would credit exactly the predicate checkFoldedState (pathdriver_live.go)
// unconditionally skips — reusing assertedTriple rather than re-deriving
// the same judgment independently.
func enteredRestingStates(t *testing.T, paths []Path, byID map[string]Path) map[fold.KindRestingState]string {
	t.Helper()
	entered := map[fold.KindRestingState]string{}
	for _, p := range paths {
		outcomes, err := pathTransitionOutcomes(byID, p.ID)
		if err != nil {
			t.Fatalf("pathTransitionOutcomes(%s): %v", p.ID, err)
		}
		for _, o := range outcomes {
			if !assertedTriple(o.To) {
				continue
			}
			pair := fold.KindRestingState{Kind: o.Kind, State: o.To}
			if _, ok := entered[pair]; !ok {
				entered[pair] = p.ID
			}
		}
	}
	return entered
}

// TestRestingStatesContainsDecisionApproved is D1's own load-bearing first
// assertion, restated at THIS package's boundary: "its first assertion is
// that (Decision, approved) is in the universe at all. A gate that is green
// because its universe is SHORT is the defect this epic exists to remove."
// internal/fold's own restingstates_test.go already pins this from inside
// the package that computes it; this copy pins it from the coverage gate's
// own vantage point, so a regression to the enumerator is caught here too,
// not only by a sibling package's test the coverage gate does not depend
// on.
func TestRestingStatesContainsDecisionApproved(t *testing.T) {
	want := fold.KindRestingState{Kind: fold.KindDecision, State: fold.StateApproved}
	for _, pair := range fold.RestingStates() {
		if pair == want {
			return
		}
	}
	t.Fatalf("fold.RestingStates() does not contain %+v — D1's gate is structurally short without P0's Outcomes correction on the dynamic approve rows", want)
}

// TestPathCatalogueCoversEveryRestingState is the coverage gate itself
// (D1/D4): every fold.RestingStates() pair must be entered by some declared
// Path's own Step, OR named in restingStateExemptions() with a reason. An
// unexplained gap fails, naming the exact pairs — mirroring
// TestPathCatalogueCoversEveryTransition exactly, one universe swapped for
// the other.
//
// HISTORY, not current status. This gate WAS red on its first run, on two
// pairs neither in restingStateExemptions() nor entered by any declared path:
// {decision withdrawn} and {response superseded}. Both were closed in the same
// wave. It is GREEN at HEAD, and the narrative below is kept because the
// reasoning it records is what a future reader needs when the next pair
// appears — not because the gate is failing.
//
// The readiness audit of 2026-08-09 found this comment still written in the
// present tense and mistook it for a live failure until it ran the test. A
// narrated history in the present tense is indistinguishable from a status
// report, which is the same defect class this epic exists to remove.
//
// {response superseded}'s cause is also gone: the row that entered it —
// {response, disputed, supersede} — is DELETED, per spec 06's amendment of
// 2026-08-09 (epic-backlog B8). The pair is no longer in the universe at all.
//
// The original finding, for the record: Both are reachable, not exempted, per the plan's own D4
// instruction ("where a pair IS reachable and simply has no path, that is
// the finding: report it. Do NOT exempt a reachable pair to go green"):
//
//   - {decision withdrawn} is entered ONLY by (KindDecision, StateProposed,
//     TWithdraw) — one of uncoveredTransitions()'s own "P1 added these four
//     owner-side exits" class (pathcoverage_test.go), reasoned there as
//     genuinely blocked on a membership axis ("the catalogue has no way to
//     make a participant LEAVE mid-scenario") the catalogue does not have
//     today, NOT as impossible in principle the way {decision draft} is.
//   - {response superseded} is entered ONLY by (KindResponse, StateDisputed,
//     TSupersede) — uncoveredTransitions()'s own P6 class, whose reason
//     ends "authoring that is P8's own catalogue call, not this phase's to
//     add."
//
// No declared path drives either entering transition yet. Closing both gaps
// means authoring a new Path in pathcatalogue_paths.go — decision:
// create -> propose -> withdraw for the first, and a second branch off the
// existing disputed-response precondition ending in supersede for the
// second — a pathcatalogue_paths.go change outside this wave's own scope
// (this wave's allowlist is the two coverage test files only), and outside
// this test file's own allowlist too. Whoever adds those paths must also
// delete the matching entries from uncoveredTransitions() in the same
// change, or that gate's own "BOTH covered and explained" branch will red.
// Left legitimately red rather than silently exempting a reachable pair to
// force green — exactly the one failure D4 says this gate cannot survive.
func TestPathCatalogueCoversEveryRestingState(t *testing.T) {
	paths := ConformancePaths()
	byID, err := pathsByID(paths)
	if err != nil {
		t.Fatalf("pathsByID: %v", err)
	}
	entered := enteredRestingStates(t, paths, byID)

	exemptByPair := map[fold.KindRestingState]string{}
	for _, ex := range restingStateExemptions() {
		exemptByPair[ex.KindRestingState] = ex.reason
	}

	var unreached []string
	for _, pair := range fold.RestingStates() {
		_, isEntered := entered[pair]
		_, isExempt := exemptByPair[pair]
		switch {
		case isEntered && isExempt:
			t.Errorf("pair %s/%s is BOTH entered (by path %q) and listed as exempt — remove it from restingStateExemptions()",
				pair.Kind, pair.State, entered[pair])
		case !isEntered && !isExempt:
			unreached = append(unreached, string(pair.Kind)+"/"+string(pair.State))
		}
	}
	if len(unreached) > 0 {
		sort.Strings(unreached)
		t.Fatalf("%d resting state(s) are neither entered by a declared path nor listed in restingStateExemptions() with a reason:\n  %s",
			len(unreached), joinLines(unreached))
	}
}

// TestRestingStateExemptionsNamesOnlyRealPairs is the reverse half
// (TestUncoveredTransitionsNamesOnlyRealTriples's own precedent): an
// exemption naming a pair fold.RestingStates() does not actually have would
// exempt nothing — a typo or a stale entry left behind after the
// enumerator changed. Reported so the failing test names both, and every
// exemption's reason must be non-empty — an exemption whose reason does not
// survive reading is worse than a red gate.
func TestRestingStateExemptionsNamesOnlyRealPairs(t *testing.T) {
	real := map[fold.KindRestingState]bool{}
	for _, pair := range fold.RestingStates() {
		real[pair] = true
	}

	var stale []string
	for _, ex := range restingStateExemptions() {
		if !real[ex.KindRestingState] {
			stale = append(stale, string(ex.Kind)+"/"+string(ex.State))
		}
		if ex.reason == "" {
			t.Errorf("restingStateExemptions() entry %s/%s carries an empty reason", ex.Kind, ex.State)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("restingStateExemptions() names %d pair(s) fold.RestingStates() does not have — stale entries exempt nothing:\n  %s",
			len(stale), joinLines(stale))
	}
}

// TestRestingStateExemptionsHasNoDuplicate guards restingStateExemptions()
// itself: the same pair listed twice would silently double-count in the
// gate above without either check catching it.
func TestRestingStateExemptionsHasNoDuplicate(t *testing.T) {
	seen := map[fold.KindRestingState]bool{}
	for _, ex := range restingStateExemptions() {
		if seen[ex.KindRestingState] {
			t.Fatalf("restingStateExemptions() lists %s/%s twice", ex.Kind, ex.State)
		}
		seen[ex.KindRestingState] = true
	}
}
