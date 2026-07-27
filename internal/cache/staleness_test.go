package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMirrorSyncAge_NeverSynced(t *testing.T) {
	t.Parallel()
	_, synced := mirrorSyncAge(time.Now(), t.TempDir())
	if synced {
		t.Fatal("want synced=false for a directory with no .git at all")
	}
}

func TestMirrorSyncAge_FetchHeadWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(gitDir, "HEAD"), old, old); err != nil {
		t.Fatal(err)
	}
	recent := time.Now().Add(-time.Minute)
	if err := os.WriteFile(filepath.Join(gitDir, "FETCH_HEAD"), []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(gitDir, "FETCH_HEAD"), recent, recent); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	age, synced := mirrorSyncAge(now, dir)
	if !synced {
		t.Fatal("want synced=true")
	}
	if age > 5*time.Minute {
		t.Fatalf("age = %v, want ~1 minute (FETCH_HEAD should win over the older HEAD mtime)", age)
	}
}

func TestSpaceSyncFacts_UsesCacheTTLAndMirrorOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	freshDir := t.TempDir()
	staleDir := t.TempDir()
	for _, tc := range []struct {
		dir string
		age time.Duration
	}{
		{dir: freshDir, age: 2 * time.Minute},
		{dir: staleDir, age: 12 * time.Minute},
	} {
		gitDir := filepath.Join(tc.dir, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		head := filepath.Join(gitDir, "HEAD")
		if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := now.Add(-tc.age)
		if err := os.Chtimes(head, at, at); err != nil {
			t.Fatal(err)
		}
	}

	store := NewStore("axon", t.TempDir(), []SpaceMirror{
		{SpaceID: "fresh", Dir: freshDir},
		{SpaceID: "stale", Dir: staleDir},
		{SpaceID: "never", Dir: t.TempDir()},
	}, func() time.Time { return now }, 5*time.Minute)
	got := store.SpaceSyncFacts(context.Background())

	if len(got) != 3 || got[0].Space != "fresh" || got[1].Space != "stale" || got[2].Space != "never" {
		t.Fatalf("facts must preserve mirror order: %+v", got)
	}
	if !got[0].Synced || got[0].Stale || got[0].Age != 2*time.Minute {
		t.Fatalf("fresh fact wrong: %+v", got[0])
	}
	if !got[1].Synced || !got[1].Stale || got[1].Age != 12*time.Minute {
		t.Fatalf("stale fact wrong: %+v", got[1])
	}
	if got[2].Synced || !got[2].Stale {
		t.Fatalf("never-synced fact wrong: %+v", got[2])
	}
}

func TestSpaceSyncFacts_ProjectsExactMirrorRevision(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	store := NewStore("axon", t.TempDir(),
		[]SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		time.Now, 0)

	got := store.SpaceSyncFacts(context.Background())
	if len(got) != 1 {
		t.Fatalf("facts = %+v, want one", got)
	}
	want, err := runGitOutput(context.Background(), fx.dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse fixture HEAD: %v", err)
	}
	if got[0].Revision != strings.TrimSpace(want) || len(got[0].Revision) != 40 {
		t.Fatalf("revision = %q, want exact 40-char HEAD %q", got[0].Revision, strings.TrimSpace(want))
	}
}
