package notification

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFileLockReleaseDoesNotReapATakenOverLock is computed-not-listed-2026-08
// P6 AC-4 (US-3): a holder slower than staleLockAge must not have its lock
// reaped by a contender and then, on its own late release, delete the
// contender's now-live lock out from under it.
//
// Before this phase, withFileLock's fifth advisory lock removed
// unconditionally (`defer func() { _ = os.Remove(lock) }()`), unlike the
// other four locks in this repo (internal/space/mirrorlock.go's compare-
// before-delete Release, internal/space/worklease_store.go's four-way
// os.SameFile check). This test drives exactly the race
// internal/space/mirrorlock.go's own doc comment names: a slow holder A
// stays past staleLockAge, a contender B takes the lock over as stale, and
// then A's own release finally runs. B's lock must survive.
func TestFileLockReleaseDoesNotReapATakenOverLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, "state.json.lock")

	// Holder A acquires the lock.
	tokenA, err := createFileLock(lockPath)
	if err != nil {
		t.Fatalf("createFileLock (A): %v", err)
	}

	// A is slow: back-date the lock file's mtime past staleLockAge, exactly
	// what withFileLock's own staleness check reads (os.Stat + ModTime).
	stale := time.Now().Add(-(staleLockAge + time.Second))
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatalf("os.Chtimes: %v", err)
	}

	// Contender B observes the lock is stale and takes it over — the exact
	// steps withFileLock's own loop performs (remove, then re-race via
	// O_EXCL).
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove stale lock: %v", err)
	}
	tokenB, err := createFileLock(lockPath)
	if err != nil {
		t.Fatalf("createFileLock (B): %v", err)
	}
	if tokenA == tokenB {
		t.Fatalf("A and B minted the same token: %q", tokenA)
	}

	// A finally releases, late. It must NOT delete B's live lock.
	if err := releaseFileLock(lockPath, tokenA); err != nil {
		t.Fatalf("releaseFileLock (A, late): %v", err)
	}
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("B's lock did not survive A's late release: %v", err)
	}
	if string(raw) != tokenB {
		t.Fatalf("lock content = %q, want B's token %q — A's release touched a lock it no longer owned", raw, tokenB)
	}

	// B releases normally; now the lock is actually gone.
	if err := releaseFileLock(lockPath, tokenB); err != nil {
		t.Fatalf("releaseFileLock (B): %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the lock removed after B's own release, stat err = %v", err)
	}
}

// TestFileLockReleaseIsIdempotent double-releasing the SAME token must not
// error — withFileLock's deferred release runs even when fn() itself already
// released nothing, and the shape must tolerate an already-gone lock.
func TestFileLockReleaseIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, "state.json.lock")

	token, err := createFileLock(lockPath)
	if err != nil {
		t.Fatalf("createFileLock: %v", err)
	}
	if err := releaseFileLock(lockPath, token); err != nil {
		t.Fatalf("releaseFileLock (first): %v", err)
	}
	if err := releaseFileLock(lockPath, token); err != nil {
		t.Fatalf("releaseFileLock (second, already gone): %v", err)
	}
}

// TestWithFileLockSurvivesATakeoverAcrossAHold is the end-to-end shape of
// the same fix, driven through withFileLock itself rather than the two
// unexported helpers directly: a slow holder's deferred release must not
// destroy a contender's lock that took over while it was still running fn().
func TestWithFileLockSurvivesATakeoverAcrossAHold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	lockPath := statePath + ".lock"

	slowHolderDone := make(chan struct{})
	slowErr := make(chan error, 1)
	go func() {
		slowErr <- withFileLock(t.Context(), statePath, func() error {
			// Simulate a hold that outlives staleLockAge: back-date the
			// lock's own mtime so a contender observes it as stale while
			// this holder is still "working".
			stale := time.Now().Add(-(staleLockAge + time.Second))
			if err := os.Chtimes(lockPath, stale, stale); err != nil {
				return err
			}
			<-slowHolderDone
			return nil
		})
	}()

	// Give the slow holder a moment to acquire and back-date the lock.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(lockPath); err == nil {
			if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the slow holder to back-date its lock")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The contender takes the lock over and finishes quickly, BEFORE the
	// slow holder's own deferred release runs.
	contenderDone := make(chan error, 1)
	go func() {
		contenderDone <- withFileLock(t.Context(), statePath, func() error { return nil })
	}()
	if err := <-contenderDone; err != nil {
		t.Fatalf("contender withFileLock: %v", err)
	}

	// Only now let the slow holder finish and release.
	close(slowHolderDone)
	if err := <-slowErr; err != nil {
		t.Fatalf("slow holder withFileLock: %v", err)
	}

	// The lock must be gone (the contender released it), never left behind
	// by the slow holder deleting something it no longer owned and the
	// contender's own release then finding nothing.
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no lock file left behind, stat err = %v", err)
	}
}
