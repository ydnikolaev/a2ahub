package release

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fakeSource is a minimal Source stub for NewChecker tests — it has no
// Download/Verifier/Swap reachable from it, matching the structural
// D-021 guarantee NewChecker's closure relies on.
type fakeSource struct {
	rel  Release
	err  error
	name string
}

func (f fakeSource) Latest(context.Context) (Release, error) { return f.rel, f.err }
func (f fakeSource) Name() string                            { return f.name }

func TestNewChecker_WritesCache(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "update-check.json")
	fixedNow := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)

	src := fakeSource{rel: Release{Version: "0.5.0"}, name: "github"}
	checker := NewChecker(src, path, func() time.Time { return fixedNow })
	checker(context.Background())

	got, ok := ReadCheck(path)
	if !ok {
		t.Fatal("ReadCheck: ok = false after checker ran, want true")
	}
	if got.Latest != "0.5.0" || got.Source != "github" || !got.CheckedAt.Equal(fixedNow) {
		t.Fatalf("ReadCheck() = %+v, want Latest=0.5.0 Source=github CheckedAt=%v", got, fixedNow)
	}
}

func TestNewChecker_SourceErrorWritesNoCache(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "update-check.json")

	src := fakeSource{err: errors.New("network down")}
	checker := NewChecker(src, path, time.Now)
	checker(context.Background()) // must not panic, must not write

	if _, ok := ReadCheck(path); ok {
		t.Fatal("ReadCheck: ok = true after a failed source, want false (nothing written)")
	}
}
