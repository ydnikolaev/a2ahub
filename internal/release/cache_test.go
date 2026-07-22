package release

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteCheck_ReadCheck_RoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "update-check.json")

	want := CheckState{CheckedAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC), Latest: "0.3.0", Source: "github"}
	if err := WriteCheck(path, want); err != nil {
		t.Fatalf("WriteCheck: %v", err)
	}

	got, ok := ReadCheck(path)
	if !ok {
		t.Fatal("ReadCheck: ok = false, want true")
	}
	if !got.CheckedAt.Equal(want.CheckedAt) || got.Latest != want.Latest || got.Source != want.Source {
		t.Fatalf("ReadCheck() = %+v, want %+v", got, want)
	}
}

func TestReadCheck_CorruptOrAbsent(t *testing.T) {
	t.Parallel()

	t.Run("absent file", func(t *testing.T) {
		t.Parallel()
		_, ok := ReadCheck(filepath.Join(t.TempDir(), "nope.json"))
		if ok {
			t.Fatal("ReadCheck: ok = true for absent file, want false")
		}
	})

	t.Run("corrupt json", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "corrupt.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
			t.Fatalf("seed corrupt file: %v", err)
		}
		_, ok := ReadCheck(path)
		if ok {
			t.Fatal("ReadCheck: ok = true for corrupt file, want false")
		}
	})
}

func TestReadLatest_Freshness(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "update-check.json")
	checkedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if err := WriteCheck(path, CheckState{CheckedAt: checkedAt, Latest: "0.3.0", Source: "github"}); err != nil {
		t.Fatalf("WriteCheck: %v", err)
	}

	t.Run("within ttl", func(t *testing.T) {
		now := checkedAt.Add(5 * time.Hour)
		latest, fresh := ReadLatest(path, now, 6*time.Hour)
		if latest != "0.3.0" || !fresh {
			t.Fatalf("ReadLatest() = (%q, %v), want (0.3.0, true)", latest, fresh)
		}
	})

	t.Run("beyond ttl", func(t *testing.T) {
		now := checkedAt.Add(7 * time.Hour)
		latest, fresh := ReadLatest(path, now, 6*time.Hour)
		if latest != "0.3.0" || fresh {
			t.Fatalf("ReadLatest() = (%q, %v), want (0.3.0, false)", latest, fresh)
		}
	})

	t.Run("corrupt cache reports zero, false", func(t *testing.T) {
		latest, fresh := ReadLatest(filepath.Join(t.TempDir(), "absent.json"), checkedAt, 6*time.Hour)
		if latest != "" || fresh {
			t.Fatalf("ReadLatest() = (%q, %v), want (\"\", false)", latest, fresh)
		}
	})
}

func TestCachePath(t *testing.T) {
	t.Parallel()
	path, err := CachePath()
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if filepath.Base(path) != "update-check.json" {
		t.Fatalf("CachePath() = %q, want basename update-check.json", path)
	}
	if filepath.Base(filepath.Dir(path)) != "a2a" {
		t.Fatalf("CachePath() = %q, want parent dir a2a", path)
	}
}
