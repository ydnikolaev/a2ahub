package cache

import (
	"context"
	"sort"
)

// NotificationSnapshot is the cursor-neutral canonical input for local
// native/editor presentation. Update is included as structured data so an
// adapter never grades business severity by parsing prose.
type NotificationSnapshot struct {
	Items  []Item
	Level  StatuslineResult
	Update UpdateNotice
}

// NotificationItems returns the current semantic signal set without
// advancing the OP-207 human read cursor. It is deliberately a level
// projection: per-channel transition/newness belongs to
// internal/notification's independent delivery state.
func (s *Store) NotificationItems(ctx context.Context) ([]Item, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return nil, err
	}

	qualified := make(map[string]Item)
	for spaceID, artifacts := range idx {
		stale := s.spaceSyncStale(spaceID)
		markers, _ := ReadMarkers(s.cacheDir, spaceID)
		pending := markerSet(markers)
		sla := s.slaFor(spaceID)
		for _, fa := range artifacts {
			reasons := actionableReasons(fa, s.ownSystem)
			if ownedByMe(fa, s.ownSystem) {
				// Notification delivery must not derive transition state from
				// the human inbox cursor. Keep only current, independently
				// observable attention conditions.
				for _, reason := range attentionReasons(fa, cursorSnapshot{Items: map[string]string{
					fa.Env.ID: string(fa.Result.State),
				}}, s.now(), sla) {
					if reason != "state-changed-since-cursor" {
						reasons = appendUnique(reasons, reason)
					}
				}
			}
			if len(reasons) == 0 {
				continue
			}
			item := toItem(fa, stale, pending[fa.Env.ID])
			item.Reasons = reasons
			qualified[spaceID+"\x00"+fa.Env.ID] = item
		}
	}

	out := make([]Item, 0, len(qualified))
	for _, item := range qualified {
		out = append(out, item)
	}
	sortItems(out)
	return out, nil
}

// NotificationSnapshot returns current state without waiting on mirror refresh
// and without writing the human cursor. Unlike the prompt-budgeted statusline,
// notification consumers are short-lived processes: they synchronously run one
// bounded stale update refresh so a native-only installation can discover a
// release before the process exits.
func (s *Store) NotificationSnapshot(ctx context.Context) (NotificationSnapshot, error) {
	s.refreshUpdateNoticeForNotifications(ctx)
	items, err := s.NotificationItems(ctx)
	if err != nil {
		return NotificationSnapshot{}, err
	}
	prior, err := loadCursor(s.cursorPath())
	if err != nil {
		return NotificationSnapshot{}, err
	}
	update := s.UpdateNotice()
	result := StatuslineResult{Update: update.Segment}
	for _, item := range items {
		if _, seen := prior.Items[item.ID]; !seen {
			result.New++
		}
		for _, reason := range item.Reasons {
			if reason == "stale-sla" || reason == "needed-by-passed" {
				result.Stale++
				break
			}
		}
		if urgencyLabel(item.Priority, item.Blocking, item.Reasons) != "" {
			result.Urgent++
			if result.UrgentID == "" {
				result.UrgentKind = urgencyLabel(item.Priority, item.Blocking, item.Reasons)
				result.UrgentID = item.ID
				result.UrgentTitle = item.Title
			}
		}
	}
	result.hasItems = len(items) > 0
	severity := SeverityQuiet
	if len(items) > 0 {
		severity = SeverityItemsPending
	}
	if result.Urgent > 0 {
		severity = SeverityUrgent
		if result.Urgent > 1 {
			result.UrgentKind = "urgent"
		}
	}
	result = finishStatusline(result, severity)
	return NotificationSnapshot{Items: items, Level: result, Update: update}, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// SortNotificationReasons makes transition fingerprints deterministic.
func SortNotificationReasons(items []Item) {
	for i := range items {
		sort.Strings(items[i].Reasons)
	}
}
