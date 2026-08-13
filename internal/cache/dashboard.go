package cache

import (
	"context"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/pendency"
)

// DashboardSnapshot is the cache-owned, one-index dashboard read. Every
// artifact's pendency verdict is evaluated once and carried into both its Item
// and ThreadResult projections; presentation never asks the relation again.
type DashboardSnapshot struct {
	Inbox   []Item
	Outbox  []Item
	Archive []Item
	Threads []ThreadResult
}

// DashboardSnapshot composes all dashboard exchange/thread projections from a
// single folded index and a single pendency verdict per artifact. In particular
// response parent lookup sees the whole space once but accepts only same-thread
// membership; the carried result is reused by a degraded thread whose parent is
// missing or belongs elsewhere rather than selecting a second fallback during
// thread construction.
func (s *Store) DashboardSnapshot(ctx context.Context) (DashboardSnapshot, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	prior, err := loadCursor(s.cursorPath())
	if err != nil {
		return DashboardSnapshot{}, err
	}

	snapshot := DashboardSnapshot{
		Inbox: []Item{}, Outbox: []Item{}, Archive: []Item{}, Threads: []ThreadResult{},
	}
	spaceIDs := make([]string, 0, len(idx))
	for spaceID := range idx {
		spaceIDs = append(spaceIDs, spaceID)
	}
	sort.Strings(spaceIDs)

	for _, spaceID := range spaceIDs {
		artifacts := idx[spaceID]
		manifest := s.manifestFor(spaceID)
		stale := s.spaceSyncStale(spaceID)
		markers, _ := ReadMarkers(s.cacheDir, spaceID)
		pending := markerSet(markers)
		byID := byArtifactID(artifacts)
		verdicts := make(verdictIndex, len(artifacts))

		for _, fa := range artifacts {
			verdict := s.resolvePendency(fa, s.ownSystem, manifest, responseParentFrom(fa, byID))
			verdicts[fa.Env.ID] = verdict

			item := toItem(fa, stale, pending[fa.Env.ID])
			_, seen := prior.Items[fa.Env.ID]

			active := exchangeActiveFromVerdict(fa, s.ownSystem, verdict)
			if active && addressedToMe(fa, s.ownSystem) {
				inboxItem := item
				carryVerdict(&inboxItem, verdict)
				inboxItem.YourMove = containsString(verdict.Owners, s.ownSystem)
				inboxItem.New = !seen
				inboxItem.Reasons = actionableReasonsFromVerdict(fa, s.ownSystem, verdict)
				inboxItem.ActivationOwed = HasReason(inboxItem.Reasons, ReasonActivationOwed)
				snapshot.Inbox = append(snapshot.Inbox, inboxItem)
			}
			if active && ownedByMe(fa, s.ownSystem) {
				outboxItem := item
				carryVerdict(&outboxItem, verdict)
				outboxItem.YourMove = containsString(verdict.Owners, s.ownSystem)
				outboxItem.Reasons = attentionReasons(fa, prior, s.now(), s.slaFor(spaceID))
				outboxItem.Overdue = hasOverdueReason(outboxItem.Reasons)
				snapshot.Outbox = append(snapshot.Outbox, outboxItem)
			}
			if !active && (ownedByMe(fa, s.ownSystem) || addressedToMe(fa, s.ownSystem)) {
				archiveItem := item
				archiveItem.New = !seen
				snapshot.Archive = append(snapshot.Archive, archiveItem)
			}
		}

		threads := map[string][]foldedArtifact{}
		for _, fa := range artifacts {
			if fa.Env.Thread != "" {
				threads[fa.Env.Thread] = append(threads[fa.Env.Thread], fa)
			}
		}
		threadIDs := make([]string, 0, len(threads))
		for threadID := range threads {
			threadIDs = append(threadIDs, threadID)
		}
		sort.Strings(threadIDs)
		for _, threadID := range threadIDs {
			result, renderErr := s.renderThreadWithVerdicts(threadID, "", spaceID, threads[threadID], verdicts)
			if renderErr != nil {
				// Match the dashboard's established per-thread degradation:
				// one malformed conversation disappears without taking down all
				// otherwise-readable exchange and thread data.
				continue
			}
			snapshot.Threads = append(snapshot.Threads, result)
		}
	}

	sortItems(snapshot.Inbox)
	sortItems(snapshot.Outbox)
	sortItems(snapshot.Archive)
	return snapshot, nil
}

func carryVerdict(item *Item, verdict pendency.Verdict) {
	item.WaitingOn = verdict.Owners
	item.ExpectedTransition = verdict.Expected
	item.Why = verdict.Why
	item.HumanGate = verdict.HumanGate
	item.RuleIdentity = verdict.RuleIdentity
}

func hasOverdueReason(reasons []string) bool {
	return HasReason(reasons, ReasonOverdueOnMe) || HasReason(reasons, ReasonNeededByPassed)
}
