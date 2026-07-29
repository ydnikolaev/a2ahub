package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/release"
)

func TestNotificationItemsDoesNotAdvanceHumanCursor(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260729-notify.md",
		wr("XW-seomatrix-20260729-notify", "native notification", "seomatrix", []string{"axon"}, "p1", false), "body")
	fx.commitEvent("seomatrix", fxULID(1500), evt("XW-seomatrix-20260729-notify", "submit", "seomatrix", base))

	cacheDir := t.TempDir()
	cursorPath := filepath.Join(cacheDir, "cursor.json")
	before := []byte(`{"schema":1,"advanced_at":"2026-07-01T00:00:00Z","items":{"sentinel":"submitted"}}`)
	if err := os.WriteFile(cursorPath, before, 0o600); err != nil {
		t.Fatalf("write cursor: %v", err)
	}
	store := NewStore("axon", cacheDir, []SpaceMirror{{
		SpaceID: "delivery", Dir: fx.dir, Manifest: mustManifest(t, fx),
	}}, func() time.Time { return base.Add(time.Hour) }, time.Hour)

	items, err := store.NotificationItems(context.Background())
	if err != nil {
		t.Fatalf("NotificationItems: %v", err)
	}
	if len(items) != 1 || items[0].Space != "delivery" || items[0].ID != "XW-seomatrix-20260729-notify" {
		t.Fatalf("items = %#v, want one qualified actionable item", items)
	}
	after, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("human cursor changed:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestNotificationItemsKeepsSameIDInDifferentSpaces(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	makeSpace := func(t *testing.T) *fixtureSpace {
		t.Helper()
		fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
		fx.commitArtifact("seomatrix/exchanges/XW-shared.md",
			wr("XW-shared", "same id", "seomatrix", []string{"axon"}, "p1", false), "body")
		fx.commitEvent("seomatrix", fxULID(1600), evt("XW-shared", "submit", "seomatrix", base))
		return fx
	}
	one, two := makeSpace(t), makeSpace(t)
	store := NewStore("axon", t.TempDir(), []SpaceMirror{
		{SpaceID: "one", Dir: one.dir, Manifest: mustManifest(t, one)},
		{SpaceID: "two", Dir: two.dir, Manifest: mustManifest(t, two)},
	}, func() time.Time { return base.Add(time.Hour) }, time.Hour)

	items, err := store.NotificationItems(context.Background())
	if err != nil {
		t.Fatalf("NotificationItems: %v", err)
	}
	if len(items) != 2 || items[0].Space == items[1].Space {
		t.Fatalf("items = %#v, want two space-qualified entries", items)
	}
}

func TestInboxSnapshotDoesNotAdvanceHumanCursor(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/XW-snapshot.md",
		wr("XW-snapshot", "dashboard snapshot", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(1700), evt("XW-snapshot", "submit", "seomatrix", base))
	cacheDir := t.TempDir()
	store := NewStore("axon", cacheDir, []SpaceMirror{{
		SpaceID: "one", Dir: fx.dir, Manifest: mustManifest(t, fx),
	}}, func() time.Time { return base.Add(time.Hour) }, time.Hour)

	items, err := store.InboxSnapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("InboxSnapshot: %v", err)
	}
	if len(items) != 1 || !items[0].New {
		t.Fatalf("items = %#v, want one new item", items)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "cursor.json")); !os.IsNotExist(err) {
		t.Fatalf("snapshot created human cursor: %v", err)
	}
}

func TestNotificationSnapshotCompletesBoundedUpdateRefreshBeforeReading(t *testing.T) {
	t.Parallel()
	store := NewStore("axon", t.TempDir(), nil, time.Now, time.Hour)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	store.EnableUpdateNotice("1.0.0", cachePath, time.Hour, func(context.Context) {
		if err := release.WriteCheck(cachePath, release.CheckState{
			CheckedAt: time.Now(), Latest: "1.1.0", Source: "test",
		}); err != nil {
			t.Errorf("WriteCheck: %v", err)
		}
	})

	got, err := store.NotificationSnapshot(context.Background())
	if err != nil {
		t.Fatalf("NotificationSnapshot: %v", err)
	}
	if !got.Update.UpdateAvailable || got.Update.Latest != "1.1.0" {
		t.Fatalf("snapshot update = %+v, want synchronously refreshed 1.1.0", got.Update)
	}
}
