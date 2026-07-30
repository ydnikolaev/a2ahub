package cache

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaimStatuslineRefreshLease_ConcurrentSingleWinner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := NewStore("axon", dir, []SpaceMirror{{SpaceID: "sp1", Dir: t.TempDir()}}, func() time.Time { return now }, time.Hour)

	const contenders = 16
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(contenders)
	for range contenders {
		go func() {
			defer wg.Done()
			if store.ClaimStatuslineRefreshLease() {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("lease winners = %d, want exactly 1", got)
	}
}

func TestClaimStatuslineRefreshLease_FreshAndUpdateCases(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	t.Run("fresh mirror does not claim", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		head := filepath.Join(dir, ".git", "HEAD")
		if err := os.MkdirAll(filepath.Dir(head), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(head, now, now); err != nil {
			t.Fatal(err)
		}
		store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: dir}}, func() time.Time { return now }, time.Hour)
		if store.ClaimStatuslineRefreshLease() {
			t.Fatal("fresh mirror claimed a refresh lease")
		}
	})

	t.Run("stale update checker claims without spaces", func(t *testing.T) {
		t.Parallel()
		cachePath := seedUpdateCache(t, t.TempDir(), "0.1.0", now.Add(-2*time.Hour))
		store := NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, time.Hour)
		store.EnableUpdateNotice("0.1.0", cachePath, time.Hour, func(_ context.Context) {})
		if !store.ClaimStatuslineRefreshLease() {
			t.Fatal("stale enabled update cache did not claim a refresh lease")
		}
	})
}

func TestClaimStatuslineRefreshLease_ExpiredAndRelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := NewStore("axon", dir, []SpaceMirror{{SpaceID: "sp1", Dir: t.TempDir()}}, func() time.Time { return now }, time.Hour)

	if !store.ClaimStatuslineRefreshLease() {
		t.Fatal("first claim failed")
	}
	if store.ClaimStatuslineRefreshLease() {
		t.Fatal("live lease allowed a second claim")
	}

	ReleaseStatuslineRefreshLease(dir)
	if !store.ClaimStatuslineRefreshLease() {
		t.Fatal("explicit release did not allow a new claim")
	}

	later := now.Add(statuslineRefreshLeaseTTL + time.Second)
	expired := NewStore("axon", dir, []SpaceMirror{{SpaceID: "sp1", Dir: t.TempDir()}}, func() time.Time { return later }, time.Hour)
	if !expired.ClaimStatuslineRefreshLease() {
		t.Fatal("expired lease did not allow recovery")
	}
}

func TestClaimStatuslineRefreshLease_ConcurrentExpiredSingleWinner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claimedAt := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	staleStore := NewStore("axon", dir, []SpaceMirror{{SpaceID: "sp1", Dir: t.TempDir()}}, func() time.Time { return claimedAt }, time.Hour)
	if !staleStore.ClaimStatuslineRefreshLease() {
		t.Fatal("seed stale claim failed")
	}

	now := claimedAt.Add(statuslineRefreshLeaseTTL + time.Second)
	store := NewStore("axon", dir, []SpaceMirror{{SpaceID: "sp1", Dir: t.TempDir()}}, func() time.Time { return now }, time.Hour)
	const contenders = 16
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(contenders)
	for range contenders {
		go func() {
			defer wg.Done()
			if store.ClaimStatuslineRefreshLease() {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("expired lease winners = %d, want exactly 1", got)
	}
}
