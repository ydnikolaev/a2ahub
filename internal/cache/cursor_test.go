package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCursor_LoadMissingIsEmpty(t *testing.T) {
	t.Parallel()
	c, err := loadCursor(filepath.Join(t.TempDir(), "cursor.json"))
	if err != nil {
		t.Fatalf("loadCursor: %v", err)
	}
	if len(c.Items) != 0 {
		t.Fatalf("want empty snapshot, got %+v", c)
	}
}

func TestCursor_SaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sub", "cursor.json")
	want := cursorSnapshot{AdvancedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Items: map[string]string{"XW-a": "submitted"}}
	if err := saveCursor(path, want); err != nil {
		t.Fatalf("saveCursor: %v", err)
	}
	got, err := loadCursor(path)
	if err != nil {
		t.Fatalf("loadCursor: %v", err)
	}
	if got.Items["XW-a"] != "submitted" {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCursor_CorruptFileTreatedAsEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cursor.json")
	if err := saveCursor(path, cursorSnapshot{}); err != nil {
		t.Fatalf("saveCursor: %v", err)
	}
	// Overwrite with garbage (a stale/corrupt schema version).
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	c, err := loadCursor(path)
	if err != nil {
		t.Fatalf("loadCursor on corrupt file should not error: %v", err)
	}
	if len(c.Items) != 0 {
		t.Fatalf("want empty snapshot for corrupt file, got %+v", c)
	}
}
