package space

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// seedMirrorForLockTest clones a throwaway fixture space and returns its
// working-tree root — the same dir shape CloneOrFetch/AcquireMirrorLock
// both take, and (via resolveGitDir) the git directory the lock file
// itself lives under.
func seedMirrorForLockTest(t *testing.T) (dir, gitDir string) {
	t.Helper()
	fx := spacefixture.New(t, "axon")
	dir = filepath.Join(t.TempDir(), "mirror")
	if err := CloneOrFetch(context.Background(), dir, fx.RemoteURL()); err != nil {
		t.Fatalf("seed clone: %v", err)
	}
	gd, err := resolveGitDir(dir)
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	return dir, gd
}

// writeMirrorLockFile plants a lock file at gitDir/mirrorLockFileName with
// the given payload, failing the test loudly on error — the white-box
// equivalent of "another process already acquired this lock" for tests
// that need to control its recorded acquisition time precisely.
func writeMirrorLockFile(t *testing.T, gitDir string, payload mirrorLockPayload) string {
	t.Helper()
	path := filepath.Join(gitDir, mirrorLockFileName)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal lock payload: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	return path
}

// TestAcquireMirrorLockStaleTakeover is the mandatory stale-recovery case:
// a lock whose recorded acquisition is older than mirrorLockStaleAfter is
// taken over immediately rather than waited on for the full
// mirrorLockWaitBudget (let alone forever) — the crashed-holder case the
// brief requires be tested with a concrete number.
func TestAcquireMirrorLockStaleTakeover(t *testing.T) {
	t.Parallel()

	dir, gitDir := seedMirrorForLockTest(t)
	writeMirrorLockFile(t, gitDir, mirrorLockPayload{
		PID:        999999,
		AcquiredAt: time.Now().Add(-2 * mirrorLockStaleAfter), // well past the staleness rule
	})

	start := time.Now()
	lock, err := AcquireMirrorLock(context.Background(), dir)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("AcquireMirrorLock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	// A takeover re-races via O_EXCL rather than waiting out any poll
	// interval, let alone the wait budget — bound it well under the wait
	// budget to prove this is a takeover, not a lucky win at the deadline.
	if elapsed >= mirrorLockWaitBudget {
		t.Fatalf("stale takeover took %s, want well under the wait budget %s (i.e. immediate, not a wait)",
			elapsed, mirrorLockWaitBudget)
	}
}

// TestAcquireMirrorLockLiveHolderWaitBudgetExpires: a LIVE (not stale)
// holder that never releases makes a contender wait, then fail closed with
// the typed ErrMirrorLocked once mirrorLockWaitBudget elapses — bounded,
// not a hang.
func TestAcquireMirrorLockLiveHolderWaitBudgetExpires(t *testing.T) {
	t.Parallel()

	dir, gitDir := seedMirrorForLockTest(t)
	path := writeMirrorLockFile(t, gitDir, mirrorLockPayload{
		PID:        os.Getpid(),
		AcquiredAt: time.Now(), // live: well inside mirrorLockStaleAfter
	})
	t.Cleanup(func() { _ = os.Remove(path) })

	start := time.Now()
	_, err := AcquireMirrorLock(context.Background(), dir)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrMirrorLocked) {
		t.Fatalf("AcquireMirrorLock error = %v, want ErrMirrorLocked", err)
	}
	if elapsed < mirrorLockWaitBudget {
		t.Fatalf("returned before the wait budget elapsed: elapsed=%s want >= %s", elapsed, mirrorLockWaitBudget)
	}
	// Bounded: well under 2x the budget, never "hangs".
	if elapsed > 2*mirrorLockWaitBudget {
		t.Fatalf("did not bound the wait: elapsed=%s want <= %s", elapsed, 2*mirrorLockWaitBudget)
	}
}

// TestAcquireMirrorLockCtxCancelWhileWaitingReturnsPromptly asserts that
// cancelling ctx while AcquireMirrorLock is waiting out a live holder
// returns promptly (well under mirrorLockWaitBudget) and surfaces the
// cancellation itself — not swallowed, not reported as ErrMirrorLocked.
func TestAcquireMirrorLockCtxCancelWhileWaitingReturnsPromptly(t *testing.T) {
	t.Parallel()

	dir, gitDir := seedMirrorForLockTest(t)
	path := writeMirrorLockFile(t, gitDir, mirrorLockPayload{
		PID:        os.Getpid(),
		AcquiredAt: time.Now(),
	})
	t.Cleanup(func() { _ = os.Remove(path) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := AcquireMirrorLock(ctx, dir)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireMirrorLock error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= mirrorLockWaitBudget {
		t.Fatalf("did not honour ctx cancellation: elapsed=%s want < %s", elapsed, mirrorLockWaitBudget)
	}
}

// TestMirrorLockReleaseIsIdempotent: Release may be called more than once
// (the write funnel does — an explicit release on the success path, plus a
// deferred safety net) without erroring the second time.
func TestMirrorLockReleaseIsIdempotent(t *testing.T) {
	t.Parallel()

	dir, gitDir := seedMirrorForLockTest(t)
	lock, err := AcquireMirrorLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireMirrorLock: %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}

	path := filepath.Join(gitDir, mirrorLockFileName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock file stat = %v, want IsNotExist after Release", err)
	}
}

// TestMirrorLockReleaseDoesNotStealASupersedingHolder is the
// compare-and-delete correctness net for staleness: if THIS lock's own
// hold outlives mirrorLockStaleAfter and a different contender takes it
// over (writes ITS OWN payload in its place), this lock's eventual
// Release must recognise the file is no longer its own and leave the new
// holder's lock alone — an unconditional os.Remove would delete the new
// legitimate holder's lock instead.
func TestMirrorLockReleaseDoesNotStealASupersedingHolder(t *testing.T) {
	t.Parallel()

	dir, gitDir := seedMirrorForLockTest(t)
	lock, err := AcquireMirrorLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireMirrorLock: %v", err)
	}

	// Simulate: real time passed, this holder's own lock went stale, and a
	// second contender's AcquireMirrorLock took it over and wrote its own
	// payload in its place — exactly what takeOverIfStale + a fresh
	// tryCreateMirrorLock do, reproduced here directly so this test does
	// not depend on actually waiting out mirrorLockStaleAfter.
	supersede := mirrorLockPayload{PID: os.Getpid() + 1, AcquiredAt: time.Now()}
	path := writeMirrorLockFile(t, gitDir, supersede)

	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the superseding holder's lock file to survive Release: %v", err)
	}
	var got mirrorLockPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal surviving lock payload: %v", err)
	}
	// time.Time equality via Equal, not ==: JSON round-tripping (both here
	// and inside MirrorLock.Release itself) strips the monotonic reading a
	// freshly-constructed time.Time carries, so a struct == comparison
	// against the pre-round-trip supersede value spuriously differs even
	// when both name the identical wall-clock instant.
	if got.PID != supersede.PID || !got.AcquiredAt.Equal(supersede.AcquiredAt) {
		t.Fatalf("surviving lock payload = %+v, want the superseding holder's own %+v", got, supersede)
	}
}
