package cache

import "time"

// DashboardAggregateSets is the cache-owned answer for the four Overview
// object sets. NeedYou and NoHumanMove partition active inbox items; YouAwait
// contains active outbox items waiting on another system; LinesOffCurrent
// contains every readable dependency edge whose drift is not current. Counts
// are always the lengths of these slices. DependencyComplete and
// DependencyDegradations keep a partial dependency scan from posing as a
// confident total. The builder consumes carried Item facts only and never
// resolves pendency.
type DashboardAggregateSets struct {
	NeedYou                []Item
	NoHumanMove            []Item
	YouAwait               []Item
	LinesOffCurrent        []ContractDependencyEdge
	DependencyComplete     bool
	DependencyDegradations []ContractDependencyDegradation
}

// BuildDashboardAggregateSets returns fresh deterministic slices without
// changing the order or backing storage of any input.
func BuildDashboardAggregateSets(
	inbox, outbox []Item,
	self string,
	now time.Time,
	dependencies ContractDependencyProjection,
) DashboardAggregateSets {
	sets := DashboardAggregateSets{
		NeedYou:                []Item{},
		NoHumanMove:            []Item{},
		YouAwait:               []Item{},
		LinesOffCurrent:        []ContractDependencyEdge{},
		DependencyComplete:     dependencies.Complete,
		DependencyDegradations: append([]ContractDependencyDegradation{}, dependencies.Degradations...),
	}
	for _, item := range inbox {
		if item.Terminal {
			continue
		}
		if item.Blocking || item.HumanGate != "" || overdueAt(item.NeededBy, now) {
			sets.NeedYou = append(sets.NeedYou, item)
			continue
		}
		sets.NoHumanMove = append(sets.NoHumanMove, item)
	}
	for _, item := range outbox {
		if item.Terminal || !waitingOnAnotherSystem(item.WaitingOn, self) {
			continue
		}
		sets.YouAwait = append(sets.YouAwait, item)
	}
	for _, edge := range dependencies.Edges {
		if edge.Drift != DependencyDriftCurrent {
			sets.LinesOffCurrent = append(sets.LinesOffCurrent, edge)
		}
	}
	return sets
}

func waitingOnAnotherSystem(waitingOn []string, self string) bool {
	for _, system := range waitingOn {
		if system != "" && system != self {
			return true
		}
	}
	return false
}
