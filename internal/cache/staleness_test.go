package cache

import (
	"os"
	"path/filepath"
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
