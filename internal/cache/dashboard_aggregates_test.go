package cache

import (
	"reflect"
	"testing"
	"time"
)

func TestBuildDashboardAggregateSetsMembershipPartitionAndCompleteness(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	inbox := []Item{
		{ID: "blocking", Blocking: true},
		{ID: "gate", HumanGate: "G3"},
		{ID: "overdue", NeededBy: "2026-08-12"},
		{ID: "ordinary"},
		{ID: "terminal-blocking", Blocking: true, Terminal: true},
	}
	outbox := []Item{
		{ID: "waiting-other", WaitingOn: []string{"checkout"}},
		{ID: "waiting-self", WaitingOn: []string{"atlas"}},
		{ID: "waiting-nobody"},
		{ID: "terminal-waiting-other", WaitingOn: []string{"checkout"}, Terminal: true},
	}
	degradation := ContractDependencyDegradation{
		Space: "sp2", System: "broken", Path: "broken/consumes.yaml",
		Code: DependencyDegradationMalformedRegistry, Reason: "invalid yaml",
	}
	dependencies := ContractDependencyProjection{
		Edges: []ContractDependencyEdge{
			{Space: "sp1", Contract: "XC-provider-current", Drift: DependencyDriftCurrent},
			{Space: "sp1", Contract: "XC-provider-behind", Drift: DependencyDriftBehind},
			{Space: "sp1", Contract: "XC-provider-dangling", Drift: DependencyDriftDangling},
		},
		Complete: false, Degradations: []ContractDependencyDegradation{degradation},
	}
	originalInbox := append([]Item(nil), inbox...)
	originalOutbox := append([]Item(nil), outbox...)
	originalEdges := append([]ContractDependencyEdge(nil), dependencies.Edges...)

	sets := BuildDashboardAggregateSets(inbox, outbox, "atlas", now, dependencies)
	if got, want := aggregateItemIDs(sets.NeedYou), []string{"blocking", "gate", "overdue"}; !reflect.DeepEqual(got, want) {
		t.Errorf("NeedYou = %v, want %v", got, want)
	}
	if got, want := aggregateItemIDs(sets.NoHumanMove), []string{"ordinary"}; !reflect.DeepEqual(got, want) {
		t.Errorf("NoHumanMove = %v, want %v", got, want)
	}
	if got, want := aggregateItemIDs(sets.YouAwait), []string{"waiting-other"}; !reflect.DeepEqual(got, want) {
		t.Errorf("YouAwait = %v, want %v", got, want)
	}
	if got, want := aggregateContractIDs(sets.LinesOffCurrent), []string{"XC-provider-behind", "XC-provider-dangling"}; !reflect.DeepEqual(got, want) {
		t.Errorf("LinesOffCurrent = %v, want %v", got, want)
	}
	activeInbox := 0
	for _, item := range inbox {
		if !item.Terminal {
			activeInbox++
		}
	}
	if len(sets.NeedYou)+len(sets.NoHumanMove) != activeInbox {
		t.Fatalf("NeedYou(%d) + NoHumanMove(%d) != active inbox(%d)", len(sets.NeedYou), len(sets.NoHumanMove), activeInbox)
	}
	if sets.DependencyComplete || !reflect.DeepEqual(sets.DependencyDegradations, []ContractDependencyDegradation{degradation}) {
		t.Fatalf("dependency evidence = complete:%v degradation:%+v", sets.DependencyComplete, sets.DependencyDegradations)
	}
	if !reflect.DeepEqual(inbox, originalInbox) || !reflect.DeepEqual(outbox, originalOutbox) || !reflect.DeepEqual(dependencies.Edges, originalEdges) {
		t.Fatal("BuildDashboardAggregateSets mutated an input slice")
	}
	if &sets.NeedYou[0] == &inbox[0] || &sets.YouAwait[0] == &outbox[0] || &sets.LinesOffCurrent[0] == &dependencies.Edges[1] {
		t.Fatal("aggregate sets reused an input backing array")
	}
}

func TestBuildDashboardAggregateSetsEmptyUsesFreshNonNilSlices(t *testing.T) {
	t.Parallel()

	sets := BuildDashboardAggregateSets(nil, nil, "atlas", time.Time{}, ContractDependencyProjection{Complete: true})
	if sets.NeedYou == nil || sets.NoHumanMove == nil || sets.YouAwait == nil ||
		sets.LinesOffCurrent == nil || sets.DependencyDegradations == nil {
		t.Fatalf("empty aggregate contains nil slices: %+v", sets)
	}
	if !sets.DependencyComplete {
		t.Fatal("complete empty dependency projection became incomplete")
	}
}

// TestDashboardAggregateDivergenceFromOldBrowserFormula measures the exact
// formulas P7 replaces on a faithful post-P1 payload. The carried overdue fact
// makes the inbox counts agree; the old browser still counted every active
// outbox row, while the server requires a carried wait on another system.
func TestDashboardAggregateDivergenceFromOldBrowserFormula(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	inbox := []Item{
		{ID: "blocking", Blocking: true},
		{ID: "gate", HumanGate: "G3"},
		{ID: "passed-date", NeededBy: "2026-08-12", Overdue: true},
		{ID: "ordinary"},
	}
	outbox := []Item{
		{ID: "waiting-other", WaitingOn: []string{"checkout"}},
		{ID: "waiting-self", WaitingOn: []string{"atlas"}},
		{ID: "waiting-nobody"},
	}
	dependencies := ContractDependencyProjection{Complete: true, Edges: []ContractDependencyEdge{
		{Contract: "current", Drift: DependencyDriftCurrent},
		{Contract: "behind", Drift: DependencyDriftBehind},
	}}
	sets := BuildDashboardAggregateSets(inbox, outbox, "atlas", now, dependencies)

	oldNeedYou := 0
	for _, item := range inbox {
		if item.Blocking || item.HumanGate != "" || item.Overdue {
			oldNeedYou++
		}
	}
	oldNoHumanMove := len(inbox) - oldNeedYou
	oldYouAwait := len(outbox)
	oldLinesOffCurrent := 0
	for _, edge := range dependencies.Edges {
		if edge.Drift != DependencyDriftCurrent {
			oldLinesOffCurrent++
		}
	}
	if got, want := [4]int{oldNeedYou, oldNoHumanMove, oldYouAwait, oldLinesOffCurrent}, [4]int{3, 1, 3, 1}; got != want {
		t.Fatalf("old-browser fixture counts = %v, want %v", got, want)
	}
	if got, want := [4]int{len(sets.NeedYou), len(sets.NoHumanMove), len(sets.YouAwait), len(sets.LinesOffCurrent)}, [4]int{3, 1, 1, 1}; got != want {
		t.Fatalf("server aggregate counts = %v, want %v", got, want)
	}
	t.Logf("aggregate_divergence old_browser need_you=3 no_human_move=1 you_await=3 lines_off_current=1 server need_you=3 no_human_move=1 you_await=1 lines_off_current=1")
}

func aggregateItemIDs(items []Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func aggregateContractIDs(edges []ContractDependencyEdge) []string {
	ids := make([]string, 0, len(edges))
	for _, edge := range edges {
		ids = append(ids, edge.Contract)
	}
	return ids
}
