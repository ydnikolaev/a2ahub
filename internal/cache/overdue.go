// Overdue: the deadline, shown to the party that can actually meet it.
//
// `outbox --attention` has always reported `needed-by-passed` — to the
// SENDER. That is the person who asked for the work and whose only remaining
// move is to nag. The responder, the one who can close it, was told nothing:
// `--actionable` is OP-207's normative five-condition union and `needed_by`
// is not among them, and its first condition is "addressed to me with no ack
// by me" — so the moment you acknowledge a request it leaves your actionable
// list, while the work and its deadline stay entirely yours.
//
// For a permanent record spanning two companies that is the failure that
// costs the most: not a wrong answer, a silence that both sides read
// differently. The requester sees an overdue item and infers neglect; the
// responder sees an empty list and infers nothing is owed.
//
// This is a SEPARATE query rather than a sixth condition on `--actionable`,
// deliberately. That union is quoted verbatim from the plan (OP-207, spec 07
// §T1) and is what the statusline, the MCP read surface and every consumer
// script already agree on; widening it is a protocol change and belongs in
// the surfaced-diff flow, not in the commit that noticed the gap.
package cache

import (
	"context"
	"time"
)

// Overdue returns the open artifacts addressed to this system whose
// `needed_by` date has passed — the work that is late and MINE, regardless
// of whether I have acknowledged it.
//
// It deliberately does NOT advance the read cursor, unlike Inbox. The whole
// point of this query is to be safe to run on a timer, and Inbox's cursor
// advance is what turns "new" off for the next reader: a scheduled poll
// every ten minutes would quietly consume the `[new]` marks and the
// statusline's "N new" count before any human saw them. A diagnostic read
// must not spend the signal it is diagnosing.
//
// `needed_by` is an optional date-only field (`YYYY-MM-DD`). An artifact
// without one, or with one this cannot parse, is never overdue — the
// question is whether a stated deadline has passed, and a malformed date
// states nothing. It is not reported as an error here either: the schema
// owns field validity, and a read verb that refused to answer because a
// counterparty wrote a bad date would be the silence this file exists to
// remove.
func (s *Store) Overdue(ctx context.Context) ([]Item, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()

	var out []Item
	for spaceID, artifacts := range idx {
		stale := s.spaceSyncStale(spaceID)
		markers, _ := ReadMarkers(s.cacheDir, spaceID)
		pending := markerSet(markers)
		for _, fa := range artifacts {
			if !addressedToMe(fa, s.ownSystem) {
				continue
			}
			if !isOpen(fa.kind(), fa.Result.State) {
				continue
			}
			if !overdueAt(fa.Env.NeededBy, now) {
				continue
			}
			item := toItem(fa, stale, pending[fa.Env.ID])
			item.Reasons = []string{string(ReasonOverdueOnMe)}
			out = append(out, item)
		}
	}
	sortItems(out)
	return out, nil
}

// overdueAt reports whether a `needed_by` value names a date already past.
//
// Compared date-to-date rather than instant-to-instant: `needed_by` has
// no time zone, so an artifact due "2026-08-20" is not late at 00:01 UTC on
// the 20th for a reader in UTC+2. It becomes late once the day itself is
// over, which is the reading an author writing a bare date intends. The
// existing sender-side check in outbox.go compares against midnight UTC and
// therefore calls an item late up to a day early; this is the more careful
// rule, and the difference is noted rather than silently forked — see
// TestOverdueAtIsDayGranular.
func overdueAt(neededBy string, now time.Time) bool {
	if neededBy == "" {
		return false
	}
	due, err := time.Parse("2006-01-02", neededBy)
	if err != nil {
		return false
	}
	// The deadline expires at the END of the named day.
	return now.UTC().After(due.AddDate(0, 0, 1).Add(-time.Nanosecond))
}
