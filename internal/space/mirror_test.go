package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func TestCloneOrFetchFreshClone(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("CloneOrFetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "space.yaml")); err != nil {
		t.Fatalf("expected cloned space.yaml: %v", err)
	}
}

func TestCloneOrFetchRerunIsFetchNotReClone(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("first CloneOrFetch: %v", err)
	}
	// Mark the working tree with a sentinel file a re-clone would wipe;
	// a fetch must leave it alone.
	sentinel := filepath.Join(dest, "untracked-local-sentinel")
	if err := os.WriteFile(sentinel, []byte("still here"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("second CloneOrFetch: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected sentinel to survive a fetch-not-reclone rerun: %v", err)
	}
}

func TestCloneOrFetchNonGitNonEmptyTargetRejected(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "unrelated.txt"), []byte("pre-existing"), 0o644); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}

	err := CloneOrFetch(context.Background(), dest, fx.RemoteURL())
	if !errors.Is(err, ErrNonGitTarget) {
		t.Fatalf("CloneOrFetch error = %v, want ErrNonGitTarget", err)
	}
}

// TestCloneOrFetchConcurrentWritersNoLostWrite is the regression for the
// live-e2e matrix's `concurrent-writes-no-lost-write` row: two a2a
// processes writing the SAME mirror must not destroy each other's write.
// Before the index.lock retry, one of these concurrent CloneOrFetch calls
// reliably lost the race on git's own index.lock and returned exit status
// 128 instead of waiting; every one of the n goroutines here must succeed.
func TestCloneOrFetchConcurrentWritersNoLostWrite(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("seed clone: %v", err)
	}

	const n = 12
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = CloneOrFetch(context.Background(), dest, fx.RemoteURL())
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent CloneOrFetch[%d]: %v", i, err)
		}
	}
}

// TestCheckoutRemoteHeadLockNeverReleasedReturnsBoundedTypedError plants
// an index.lock that is never removed (simulating a crashed/hung holder)
// and asserts checkoutRemoteHead does not wait forever: it gives up once
// indexLockWaitBudget elapses and returns a typed error naming the
// contention, rather than hanging or returning git's own raw exit code.
func TestCheckoutRemoteHeadLockNeverReleasedReturnsBoundedTypedError(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("seed clone: %v", err)
	}

	gitDir, err := resolveGitDir(dest)
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	lockPath := filepath.Join(gitDir, "index.lock")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatalf("plant index.lock: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(lockPath) })

	start := time.Now()
	err = checkoutRemoteHead(context.Background(), dest)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrMirrorLocked) {
		t.Fatalf("checkoutRemoteHead error = %v, want ErrMirrorLocked", err)
	}
	if elapsed < indexLockWaitBudget {
		t.Fatalf("returned before the wait budget elapsed: elapsed=%s want >= %s", elapsed, indexLockWaitBudget)
	}
	// Bounded: well under 2x the budget, never "hangs".
	if elapsed > 2*indexLockWaitBudget {
		t.Fatalf("did not bound the wait: elapsed=%s want <= %s", elapsed, 2*indexLockWaitBudget)
	}
}

// TestCheckoutRemoteHeadCtxCancelWhileWaitingReturnsPromptly asserts that
// cancelling ctx while checkoutRemoteHead is waiting out lock contention
// returns promptly (well under indexLockWaitBudget) and surfaces the
// cancellation itself, rather than swallowing it and either hanging or
// returning ErrMirrorLocked instead.
func TestCheckoutRemoteHeadCtxCancelWhileWaitingReturnsPromptly(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("seed clone: %v", err)
	}

	gitDir, err := resolveGitDir(dest)
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	lockPath := filepath.Join(gitDir, "index.lock")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatalf("plant index.lock: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(lockPath) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = checkoutRemoteHead(ctx, dest)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("checkoutRemoteHead error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= indexLockWaitBudget {
		t.Fatalf("did not honour ctx cancellation: elapsed=%s want < %s", elapsed, indexLockWaitBudget)
	}
}
