package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// seedUpdateCache writes a T3 update-check.json at dir/update-check.json
// via release.WriteCheck, and returns its path.
func seedUpdateCache(t *testing.T, dir string, latest string, checkedAt time.Time) string {
	t.Helper()
	path := filepath.Join(dir, "update-check.json")
	if err := release.WriteCheck(path, release.CheckState{CheckedAt: checkedAt, Latest: latest, Source: "github"}); err != nil {
		t.Fatalf("seedUpdateCache: WriteCheck: %v", err)
	}
	return path
}

// TestUpdateNotice_NotEnabled asserts the zero-value contract: a Store
// that never called EnableUpdateNotice always renders GradeNone, whatever
// the cache on disk holds.
func TestUpdateNotice_NotEnabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seedUpdateCache(t, dir, "9.9.9", time.Now())

	store := NewStore("axon", t.TempDir(), nil, time.Now, 0)
	n := store.UpdateNotice()
	if n.Grade != release.GradeNone {
		t.Fatalf("Grade = %v, want GradeNone (not enabled)", n.Grade)
	}
	if n.UpdateAvailable || n.Required {
		t.Fatalf("want no update available/required, got %+v", n)
	}
}

// TestUpdateNotice_GradeAvailable is spec 19 T4: latest > current and no
// floor violation renders GradeAvailable with the short segment form.
func TestUpdateNotice_GradeAvailable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, dir, "0.3.0", now)

	store := NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)

	n := store.UpdateNotice()
	if n.Grade != release.GradeAvailable {
		t.Fatalf("Grade = %v, want GradeAvailable", n.Grade)
	}
	if !n.UpdateAvailable || n.Required {
		t.Fatalf("want UpdateAvailable=true Required=false, got %+v", n)
	}
	if n.Current != "0.1.2" || n.Latest != "0.3.0" {
		t.Fatalf("Current/Latest = %q/%q, want 0.1.2/0.3.0", n.Current, n.Latest)
	}
	if n.Segment == "" {
		t.Fatal("want a non-empty segment")
	}
	if n.CheckedAt != now || n.Source != "github" || !n.Fresh ||
		n.ReleaseURL != "https://github.com/ydnikolaev/a2ahub/releases/tag/v0.3.0" {
		t.Fatalf("update provenance = %+v", n)
	}
}

// TestUpdateNotice_GradeRequired is spec 19 T4: a connected space's
// min_binary_version pin above the running binary renders GradeRequired
// (the CC-085 write-refusal remedy hint), naming the pinning space.
func TestUpdateNotice_GradeRequired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, dir, "0.3.0", now)

	spaces := []SpaceMirror{
		{SpaceID: "sp-low", Manifest: space.Manifest{MinBinaryVersion: "0.1.0"}},
		{SpaceID: "sp-high", Manifest: space.Manifest{MinBinaryVersion: "0.4.0"}},
	}

	store := NewStore("axon", t.TempDir(), spaces, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)

	n := store.UpdateNotice()
	if n.Grade != release.GradeRequired {
		t.Fatalf("Grade = %v, want GradeRequired", n.Grade)
	}
	if !n.Required {
		t.Fatal("want Required=true")
	}
	if n.Floor != "0.4.0" || n.FloorSpace != "sp-high" {
		t.Fatalf("Floor/FloorSpace = %q/%q, want 0.4.0/sp-high (max over spaces)", n.Floor, n.FloorSpace)
	}
}

// TestUpdateNotice_GradeNone_UpToDate is spec 19 T4: latest <= current and
// no floor violation renders GradeNone.
func TestUpdateNotice_GradeNone_UpToDate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, dir, "0.1.2", now)

	store := NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)

	n := store.UpdateNotice()
	if n.Grade != release.GradeNone {
		t.Fatalf("Grade = %v, want GradeNone (up to date)", n.Grade)
	}
	if n.UpdateAvailable || n.Required {
		t.Fatalf("want no update available/required, got %+v", n)
	}
}

// TestUpdateNotice_EmptyCache_GradeNone covers the "no known latest at all"
// case (a corrupt/absent T3 cache): release.ReadLatest reports "" and the
// notice degrades to GradeNone, never an error.
func TestUpdateNotice_EmptyCache_GradeNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := filepath.Join(dir, "does-not-exist.json")

	store := NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)

	n := store.UpdateNotice()
	if n.Grade != release.GradeNone {
		t.Fatalf("Grade = %v, want GradeNone (absent cache)", n.Grade)
	}
}

func TestStatuslineRefreshNeeded_UpdateStale(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	staleAt := now.Add(-2 * time.Hour)
	cachePath := seedUpdateCache(t, dir, "0.1.0", staleAt)

	store := NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.0", cachePath, time.Hour, func(context.Context) {})
	if !store.StatuslineRefreshNeeded() {
		t.Fatal("stale update cache did not request refresh")
	}
}

func TestStatuslineRefreshNeeded_UpdateFresh(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	freshAt := now.Add(-1 * time.Minute)
	cachePath := seedUpdateCache(t, dir, "0.1.0", freshAt)

	store := NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.0", cachePath, time.Hour, func(context.Context) {})
	if store.StatuslineRefreshNeeded() {
		t.Fatal("fresh update cache requested refresh")
	}
}

func TestStatuslineRefreshNeeded_UpdateNotEnabled(t *testing.T) {
	t.Parallel()
	store := NewStore("axon", t.TempDir(), nil, time.Now, 0)
	if store.StatuslineRefreshNeeded() {
		t.Fatal("disabled update notice requested refresh")
	}
}
