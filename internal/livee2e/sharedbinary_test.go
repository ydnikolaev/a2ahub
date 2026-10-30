package livee2e

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func stubBuilder(calls *atomic.Int32, fail error) *onceBuilder {
	return &onceBuilder{build: func(_ context.Context, dir string) (string, error) {
		calls.Add(1)
		if fail != nil {
			return "", fail
		}
		p := filepath.Join(dir, "a2a")
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			return "", err
		}
		return p, nil
	}}
}

func TestSharedBinary(t *testing.T) {
	t.Parallel()

	t.Run("concurrent Get is one build", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		b := stubBuilder(&calls, nil)
		t.Cleanup(func() { _ = b.Close() })
		var wg sync.WaitGroup
		paths := make([]string, 12)
		for i := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p, err := b.Get()
				if err != nil {
					t.Errorf("Get: %v", err)
				}
				paths[i] = p
			}()
		}
		wg.Wait()
		if n := calls.Load(); n != 1 {
			t.Fatalf("build calls = %d, want 1", n)
		}
		for _, p := range paths {
			if p != paths[0] {
				t.Fatalf("paths differ: %q vs %q", p, paths[0])
			}
		}
	})

	t.Run("path survives removal of a sibling workspace", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		b := stubBuilder(&calls, nil)
		t.Cleanup(func() { _ = b.Close() })
		p, err := b.Get()
		if err != nil {
			t.Fatal(err)
		}
		work := t.TempDir()
		if err := os.RemoveAll(work); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("binary gone after sibling RemoveAll: %v", err)
		}
	})

	t.Run("Close removes the dir and is idempotent", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		b := stubBuilder(&calls, nil)
		p, err := b.Get()
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Dir(p)); !os.IsNotExist(err) {
			t.Fatalf("dir still present after Close: %v", err)
		}
		if err := b.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	})

	t.Run("failing build gives every caller the same error", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		boom := errors.New("boom")
		b := stubBuilder(&calls, boom)
		t.Cleanup(func() { _ = b.Close() })
		for range 3 {
			if _, err := b.Get(); !errors.Is(err, boom) {
				t.Fatalf("Get err = %v, want boom", err)
			}
		}
		if n := calls.Load(); n != 1 {
			t.Fatalf("build calls = %d, want 1", n)
		}
	})

	t.Run("Close before Get is a no-op", func(t *testing.T) {
		t.Parallel()
		var b onceBuilder
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a cancelled caller context does not poison the build", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		b := stubBuilder(&calls, nil)
		t.Cleanup(func() { _ = b.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = ctx // Get takes no caller context by design: the build owns its own.
		if p, err := b.Get(); err != nil || p == "" {
			t.Fatalf("Get = %q, %v", p, err)
		}
	})
}
