package cache

import (
	"context"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// DefaultUpdateCheckTTL is the T3 update-check cache's default freshness
// window (spec 19 T3: "default 6h ... release cadence is daily-ish, not
// minutes") — distinct from DefaultStatuslineTTL, which governs mirror
// sync-age, not the machine-level release-check cache.
const DefaultUpdateCheckTTL = 6 * time.Hour

// UpdateNotice is the T4 shared advisory fact every Store-based surface
// (statusline here; inbox/outbox/mcp in wave 12c) renders from — one
// struct, one computation (Store.UpdateNotice), so every surface's wording
// and grading stay in sync. JSON tags match spec 19 T1's `--json` /
// `a2a_read` `update` object shape verbatim: {current, latest,
// update_available, floor, floor_space, required}. Grade/Segment/Sentence
// are in-process rendering helpers, not part of that wire shape.
type UpdateNotice struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Floor           string `json:"floor"`
	FloorSpace      string `json:"floor_space"`
	Required        bool   `json:"required"`

	Grade    release.Grade `json:"-"`
	Segment  string        `json:"-"`
	Sentence string        `json:"-"`
}

// UpdateNotice computes the T4 shared advisory fact from the T3 cache:
// zero value (Grade GradeNone) when EnableUpdateNotice was never called, or
// when the cache holds no known "latest" fact at all. Freshness (TTL) only
// gates the background REFRESH (triggerUpdateRefreshIfStale) — a
// stale-but-present cache value is still rendered here, per
// release.ReadLatest's own doc comment.
func (s *Store) UpdateNotice() UpdateNotice {
	if !s.updateEnabled {
		return UpdateNotice{Grade: release.GradeNone}
	}

	latest, _ := release.ReadLatest(s.updateCachePath, s.now(), s.updateTTL)
	if latest == "" {
		return UpdateNotice{Current: s.updateBinaryVersion, Grade: release.GradeNone}
	}

	floor, floorSpace := s.updateFloor()

	// Derive every field from the shared release.Info SSOT so this object
	// agrees value-for-value with `a2a update --json` (parity across surfaces
	// — update_available/required are orthogonal, not grade-folded).
	info := release.Info(s.updateBinaryVersion, latest, floor, floorSpace)
	return UpdateNotice{
		Current:         info.Current,
		Latest:          info.Latest,
		UpdateAvailable: info.UpdateAvailable,
		Floor:           info.Floor,
		FloorSpace:      info.FloorSpace,
		Required:        info.Required,
		Grade:           info.Grade,
		Segment:         info.Segment,
		Sentence:        info.Sentence,
	}
}

// updateFloor computes spec 19 T1 step 1's FLOOR: max(min_binary_version)
// over every connected space's (already-parsed) manifest, skipping empty
// pins. floorSpace names the pinning space (the T4 REQUIRED-grade message's
// remedy hint). No manifests, or none with a pin, returns ("", "").
func (s *Store) updateFloor() (floor, floorSpace string) {
	for _, sm := range s.spaces {
		pin := sm.Manifest.MinBinaryVersion
		if pin == "" {
			continue
		}
		if floor == "" {
			floor, floorSpace = pin, sm.SpaceID
			continue
		}
		if older, err := version.OlderThan(floor, pin); err == nil && older {
			floor, floorSpace = pin, sm.SpaceID
		}
	}
	return floor, floorSpace
}

// triggerUpdateRefreshIfStale spawns exactly ONE detached, recover-guarded
// goroutine that runs the injected update-check refresh (updateChecker) —
// same pattern as triggerRefreshIfStale (context.Background(), defer
// recover): this call never waits on that goroutine, so it never affects
// this package's own render budget. A no-op when EnableUpdateNotice was
// never called, when no checker was wired, or when the T3 cache is still
// fresh.
func (s *Store) triggerUpdateRefreshIfStale(_ context.Context) {
	if !s.updateEnabled || s.updateChecker == nil {
		return
	}
	if _, fresh := release.ReadLatest(s.updateCachePath, s.now(), s.updateTTL); fresh {
		return
	}
	checker := s.updateChecker
	go func() {
		defer func() { _ = recover() }() // rails: the refresh goroutine must never panic into the caller's prompt
		checker(context.Background())
	}()
}
