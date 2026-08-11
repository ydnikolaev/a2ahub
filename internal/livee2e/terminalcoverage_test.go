package livee2e

import (
	"fmt"
	"sort"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// terminalcoverage_test.go is AC6's own coverage gate (plan
// docs/features/active/agent-exchange-2026-08/plans/11-verification.plan.md,
// "Wave D"): every fold.Kind must have at least one declared Path whose own
// (per-Kind) FINAL landed step both (a) resolves to a state fold.Terminal
// declares terminal for that Kind, AND (b) carries the Terminal(...)
// predicate (pathgrammar.go) on that SAME step — not merely a FoldedState
// assertion of a state that happens to be terminal. FoldedState alone would
// only prove the path reaches the state it names; Terminal alone would pass
// on ANY terminal state, including a WRONG one (Terminal's own doc comment,
// pathgrammar.go) — this gate demands both, on the same step.
//
// Kinds are enumerated from fold.RestingStates() itself (the Kind half of
// every pair it returns, deduplicated) rather than a literal list of eight:
// RestingStates() walks fold's own internal `kinds` var
// (restingstates.go), so a ninth Kind fold ever adds shows up here
// automatically and is judged by this gate the same day — the same
// discipline restingcoverage_test.go already applies to (Kind, State)
// pairs, one level up to Kind alone.
//
// UNTAGGED — runs under the plain `go test ./internal/livee2e/...`, same
// precedent restingcoverage_test.go/pathcoverage_test.go already set:
// pathcatalogue_paths.go and pathgrammar.go carry no build tag, so this
// gate needs no `-tags=livee2e` run to catch a regression.
//
// No exemption list, unlike restingcoverage_test.go's own gate: the plan's
// own "Wave D" section re-derived the census by hand and found 8 of 8
// kinds already reach a terminal state through a real, named (possibly
// chained) path once Precondition and Step.Refused are both honoured
// correctly — recorded there because the FIRST hand-derivation, which
// walked each Path's own Steps without following Precondition, wrongly
// found contract had ZERO. An unreached Kind here is therefore a genuine
// regression to report, not a defensible gap needing a written reason.

// domainKinds returns every fold.Kind fold.RestingStates() names, deduped
// and sorted — this gate's own Kind universe, derived from the domain
// rather than hand-listed.
func domainKinds(t *testing.T) []fold.Kind {
	t.Helper()
	seen := map[fold.Kind]bool{}
	var out []fold.Kind
	for _, pair := range fold.RestingStates() {
		if seen[pair.Kind] {
			continue
		}
		seen[pair.Kind] = true
		out = append(out, pair.Kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// pathHasTerminalPredicate reports whether step carries a Terminal(...)
// predicate (pathgrammar.go's own PredicateTerminal) whose OWN artifact
// name matches a FoldedState predicate ALSO declared on step — never a
// bare "some Terminal predicate exists somewhere on this step" check,
// which would credit a step for a Terminal(...) call that names a
// DIFFERENT symbolic artifact than the one this step's own transition
// actually advances (e.g. a copy-paste leaving Terminal("response") on a
// step whose Kind is Question — Terminal's own doc comment requires it
// sit BESIDE the specific FoldedState it strengthens, and this check is
// what makes that requirement structural rather than a comment nobody
// enforces).
func pathHasTerminalPredicate(step Step) bool {
	foldedArtifacts := map[string]bool{}
	for _, p := range step.Predicates {
		if p.kind == PredicateFoldedState {
			foldedArtifacts[p.artifact] = true
		}
	}
	for _, p := range step.Predicates {
		if p.kind == PredicateTerminal && foldedArtifacts[p.artifact] {
			return true
		}
	}
	return false
}

// pathOwnLandedSteps resolves path id's OWN Steps (never its precondition
// chain's — the same split PathTransitions/pathTransitionOutcomes already
// make, pathgrammar.go) to two PARALLEL, index-aligned slices: the landed
// (non-refused) Step values themselves (needed here to inspect their own
// declared Predicates, which pathTransitionOutcomes' transitionOutcome
// does not carry) and their resolved transitionOutcome (Kind/From/
// Transition/To). walkSteps' own iteration order guarantees the two line
// up: both this function and walkSteps skip a refused step identically
// (walkSteps' own `continue` before any append to its result, mirrored
// here by the same Step.Refused check walking the SAME path.Steps slice in
// the SAME order) — so landed[i] and outcomes[i] always describe the same
// step.
func pathOwnLandedSteps(byID map[string]Path, id string) ([]Step, []transitionOutcome, error) {
	path, ok := byID[id]
	if !ok {
		return nil, nil, fmt.Errorf("livee2e: unknown path id %q", id)
	}
	states := map[fold.Kind]fold.State{}
	if path.Precondition != "" {
		inherited, err := chainEndStates(byID, path.Precondition, map[string]bool{})
		if err != nil {
			return nil, nil, fmt.Errorf("livee2e: path %q precondition: %w", id, err)
		}
		states = inherited
	}
	outcomes, err := walkSteps(path.Steps, path.RequiredApprovers, states)
	if err != nil {
		return nil, nil, fmt.Errorf("livee2e: path %q: %w", id, err)
	}
	landed := make([]Step, 0, len(outcomes))
	for _, s := range path.Steps {
		if s.Refused != nil {
			continue
		}
		landed = append(landed, s)
	}
	return landed, outcomes, nil
}

// kindsWithTerminalCoverage walks every declared path's OWN landed steps
// and, per Kind, finds the LAST landed step touching that Kind within THAT
// path — the state fold's own per-Kind state machine actually rests in
// once this path (continuing from its own precondition, if any) is done
// driving that Kind, mirroring how walkSteps/chainEndStates already thread
// states[kind] as "the current value for this Kind". A Kind is credited
// only when that step's own resolved To satisfies fold.Terminal AND the
// SAME step declares the Terminal(...) predicate — both conditions,
// exactly like Terminal's own doc comment (pathgrammar.go) says a
// FoldedState-only step must not be credited.
func kindsWithTerminalCoverage(t *testing.T, paths []Path, byID map[string]Path) map[fold.Kind]bool {
	t.Helper()
	covered := map[fold.Kind]bool{}
	for _, p := range paths {
		landed, outcomes, err := pathOwnLandedSteps(byID, p.ID)
		if err != nil {
			t.Fatalf("pathOwnLandedSteps(%s): %v", p.ID, err)
		}
		lastIdx := map[fold.Kind]int{}
		for i, o := range outcomes {
			lastIdx[o.Kind] = i
		}
		for kind, i := range lastIdx {
			if !fold.Terminal(kind, outcomes[i].To) {
				continue
			}
			if pathHasTerminalPredicate(landed[i]) {
				covered[kind] = true
			}
		}
	}
	return covered
}

// TestPathCatalogueCoversEveryKindTerminal is AC6's own gate: every Kind
// domainKinds() names must have its own terminal state reached AND
// asserted (Terminal(...), not merely FoldedState) by some declared path's
// own per-Kind final landed step. An unexplained gap fails, naming the
// exact kinds — mirroring TestPathCatalogueCoversEveryRestingState's own
// shape, one universe swapped for Kind alone (and, unlike that gate, no
// exemption list — see this file's own package doc for why).
func TestPathCatalogueCoversEveryKindTerminal(t *testing.T) {
	paths := ConformancePaths()
	byID, err := pathsByID(paths)
	if err != nil {
		t.Fatalf("pathsByID: %v", err)
	}
	covered := kindsWithTerminalCoverage(t, paths, byID)

	var missing []string
	for _, kind := range domainKinds(t) {
		if !covered[kind] {
			missing = append(missing, string(kind))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d kind(s) have no declared path whose own final step both reaches a fold.Terminal state AND carries the Terminal(...) predicate:\n  %s",
			len(missing), joinLines(missing))
	}
}
