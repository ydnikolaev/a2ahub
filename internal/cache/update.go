package cache

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// DefaultUpdateCheckTTL is the T3 update-check cache's default freshness
// window (spec 19 T3: "default 6h ... release cadence is daily-ish, not
// minutes") — distinct from DefaultStatuslineTTL, which governs mirror
// sync-age, not the machine-level release-check cache.
const DefaultUpdateCheckTTL = 6 * time.Hour

const notificationUpdateRefreshTimeout = 2 * time.Second

// ConfigureUpdateNotice enables the proactive update notice on s from the
// machine config's free-form defaults map (spec 19 T3): update_repo defaults
// to release.DefaultUpdateRepo; update_check_ttl (a Go duration string)
// defaults to DefaultUpdateCheckTTL. It wires the background checker that
// refreshes the machine-level update-check cache (statusline fires it via
// triggerUpdateRefreshIfStale; the checker writes the cache ONLY — D-021).
// A cache-path resolution failure disables the notice quietly (advisory
// display is never fatal). The lead calls this at each Store construction
// site (CLI read store, MCP buildStore) so every Store-based surface shares
// one configured notice. Takes the raw defaults map, not space.MachineConfig,
// so internal/cache stays decoupled from internal/space's config type.
func ConfigureUpdateNotice(s *Store, binaryVersion string, defaults map[string]string) {
	cachePath, err := release.CachePath()
	if err != nil {
		return
	}
	repo := defaults["update_repo"]
	if repo == "" {
		repo = release.DefaultUpdateRepo
	}
	ttl := DefaultUpdateCheckTTL
	if raw := defaults["update_check_ttl"]; raw != "" {
		if d, derr := time.ParseDuration(raw); derr == nil && d > 0 {
			ttl = d
		}
	}
	source := release.NewGitHubSource(http.DefaultClient, "", repo)
	checker := release.NewChecker(source, cachePath, time.Now)
	if decoder, decoderErr := notes.NewDetailDecoder(); decoderErr == nil {
		checker = release.NewCohortDetailChecker(
			source, http.DefaultClient, source.Token, cachePath,
			filepath.Join(filepath.Dir(cachePath), "release-detail.json"),
			1, release.KeylessVerifier(repo), decoder, time.Now,
		)
	}
	s.EnableUpdateNotice(binaryVersion, cachePath, ttl, checker)
	s.updateRepo = repo
}

// UpdateNotice is the T4 shared advisory fact every Store-based surface
// (statusline here; inbox/outbox/mcp in wave 12c) renders from — one
// struct, one computation (Store.UpdateNotice), so every surface's wording
// and grading stay in sync. JSON tags match spec 19 T1's `--json` /
// `a2a_read` `update` object shape verbatim: {current, latest,
// update_available, floor, floor_space, required}. Grade/Segment/Sentence
// are in-process rendering helpers, not part of that wire shape.
type UpdateNotice struct {
	Current         string    `json:"current"`
	Latest          string    `json:"latest"`
	UpdateAvailable bool      `json:"update_available"`
	Floor           string    `json:"floor"`
	FloorSpace      string    `json:"floor_space"`
	Required        bool      `json:"required"`
	CheckedAt       time.Time `json:"checked_at"`
	Source          string    `json:"source"`
	Fresh           bool      `json:"fresh"`
	ReleaseURL      string    `json:"release_url"`

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

	check, ok := release.ReadCheck(s.updateCachePath)
	latest, fresh := release.ReadLatest(s.updateCachePath, s.now(), s.updateTTL)
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
		CheckedAt:       check.CheckedAt,
		Source:          check.Source,
		Fresh:           ok && fresh,
		ReleaseURL:      release.PageURL(s.updateRepo, latest),
		Grade:           info.Grade,
		Segment:         info.Segment,
		Sentence:        info.Sentence,
	}
}

// UpdateDetail returns only a previously verified, target-bound release notes
// projection. It never refreshes the network and never exposes prose from a
// malformed, unverified, tampered, or future-schema cache record.
func (s *Store) UpdateDetail() release.DetailCache {
	notice := s.UpdateNotice()
	if !s.updateEnabled || notice.Latest == "" || s.updateDetailPath == "" {
		return release.DetailCache{Status: release.DetailMissing, TargetVersion: notice.Latest}
	}
	detail, _ := release.ReadDetailCache(s.updateDetailPath, notice.Latest)
	return detail
}

// updateFloor computes spec 19 T1 step 1's FLOOR: max(min_binary_version)
// over every connected space's (already-parsed) manifest, skipping empty
// pins. floorSpace names the pinning space (the T4 REQUIRED-grade message's
// remedy hint). No manifests, or none with a pin, returns ("", "").
func (s *Store) updateFloor() (floor, floorSpace string) {
	for _, sm := range s.spaceMirrorsSnapshot() {
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

// refreshUpdateNoticeForNotifications performs the notification-specific
// refresh path. Notification status/claim processes do not remain alive long
// enough for the statusline's detached goroutine, so the canonical checker is
// invoked in-process with a firm request deadline. The checker owns errors as
// advisory cache state; a panic is isolated from delivery just like the
// statusline's detached `a2a sync` path.
func (s *Store) refreshUpdateNoticeForNotifications(ctx context.Context) {
	if !s.updateEnabled || s.updateChecker == nil {
		return
	}
	if _, fresh := release.ReadLatest(s.updateCachePath, s.now(), s.updateTTL); fresh {
		return
	}
	refreshCtx, cancel := context.WithTimeout(ctx, notificationUpdateRefreshTimeout)
	defer cancel()
	func() {
		defer func() { _ = recover() }()
		s.updateChecker(refreshCtx)
	}()
}

// SpaceVersion is one connected space's two version facts, which are
// DELIBERATELY separate axes and were conflated by every surface until
// 2026-08-22.
//
//   - Floor is the space's own `min_binary_version` — the contract it declares
//     for anyone writing to it, reviewed in space.yaml. It is the ONLY thing
//     that refuses a write (internal/space.ErrStaleBinaryVersion).
//   - TemplatePin is the a2ahub release tag the space's committed workflows
//     reference. It moves only when somebody runs `a2a space update`.
//
// The gap between them is the whole story of 2026-08-21: an agent wrote to
// `axon` from a 0.23 binary while 0.24 was published, entirely legitimately,
// because axon's floor still said 0.23.0 — the floor had not moved because the
// space had not been updated, and NOTHING said so on any surface.
type SpaceVersion struct {
	ID    string `json:"id"`
	Floor string `json:"min_binary_version"`
	// FloorMet is false only when the floor is BOTH declared and unmet. An
	// undeclared or unparseable floor is not a violation — it is an unknown,
	// and reporting an unknown as a violation is how a gate cries wolf.
	FloorMet    bool   `json:"floor_met"`
	TemplatePin string `json:"template_pin"`
	// TemplateBehind is true only when the pin is declared, parses, and is
	// older than the newest known release. With no known latest (offline, no
	// cache) it stays false: "I could not measure this" must never render as
	// "this is fine" OR as "this is wrong".
	TemplateBehind bool `json:"template_behind"`
}

// VersionReport is what `a2a version` prints and `a2a doctor` judges: the
// binary, the release axis (the existing UpdateNotice SSOT, unchanged) and one
// row per connected space. One computation, so the two surfaces cannot
// disagree — the same reason UpdateNotice itself exists.
type VersionReport struct {
	Binary string         `json:"binary"`
	Update UpdateNotice   `json:"update"`
	Spaces []SpaceVersion `json:"spaces"`
}

// VersionReport composes the report. It never reaches the network: the release
// axis reads whatever the T3 cache already holds (the same read UpdateNotice
// does) and the space axis reads the mirrors already on disk.
func (s *Store) VersionReport(binaryVersion string, workflowPin func(dir string) (string, string)) VersionReport {
	notice := s.UpdateNotice()
	if notice.Current == "" {
		notice.Current = binaryVersion
	}
	report := VersionReport{Binary: binaryVersion, Update: notice, Spaces: []SpaceVersion{}}

	for _, sm := range s.spaceMirrorsSnapshot() {
		row := SpaceVersion{ID: sm.SpaceID, Floor: sm.Manifest.MinBinaryVersion, FloorMet: true}
		if row.Floor != "" {
			if older, err := version.OlderThan(binaryVersion, row.Floor); err == nil {
				row.FloorMet = !older
			}
		}
		if workflowPin != nil {
			pin, _ := workflowPin(sm.Dir)
			row.TemplatePin = pin
			// "mixed" is a real, reportable state (a space whose jobs pin
			// different tags) and it is NOT a version — comparing it would
			// silently answer false. Left as the pin, judged by nobody.
			if pin != "" && pin != "mixed" && notice.Latest != "" {
				if older, err := version.OlderThan(pin, notice.Latest); err == nil {
					row.TemplateBehind = older
				}
			}
		}
		report.Spaces = append(report.Spaces, row)
	}
	return report
}
