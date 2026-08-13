package cache

import (
	"sort"
	"time"
)

// OrderAttention is the single attention-list order ADR-007 requires the
// domain read model, rather than HTML, to own. It returns a fresh slice ordered
// through six exclusive highest-match tiers: (1) YourMove, (2) Blocking,
// (3) any remaining HumanGate, (4) needed_by past under overdueAt's end-of-day
// rule, (5) SyncStale, then (6) LatestEventAt descending. Tiers 1–5 tie by id
// then space. Tier 6 puts zero activity times last, then ties by id and space.
// Priority is not an ordering tier. The function consumes only facts already
// carried on Item and never resolves pendency or persists a rank.
func OrderAttention(items []Item, now time.Time) []Item {
	ordered := make([]Item, len(items))
	copy(ordered, items)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		leftTier, rightTier := attentionTier(left, now), attentionTier(right, now)
		if leftTier != rightTier {
			return leftTier < rightTier
		}
		if leftTier == 6 && !left.LatestEventAt.Equal(right.LatestEventAt) {
			switch {
			case left.LatestEventAt.IsZero():
				return false
			case right.LatestEventAt.IsZero():
				return true
			default:
				return left.LatestEventAt.After(right.LatestEventAt)
			}
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Space < right.Space
	})
	return ordered
}

func attentionTier(item Item, now time.Time) int {
	switch {
	case item.YourMove:
		return 1
	case item.Blocking:
		return 2
	case item.HumanGate != "":
		return 3
	case overdueAt(item.NeededBy, now):
		return 4
	case item.SyncStale:
		return 5
	default:
		return 6
	}
}
