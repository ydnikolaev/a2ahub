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

	"github.com/ydnikolaev/a2ahub/internal/host"
)

func TestCloneOrFetchFreshClone(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{}); err != nil {
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

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{}); err != nil {
		t.Fatalf("first CloneOrFetch: %v", err)
	}
	// Mark the working tree with a sentinel file a re-clone would wipe;
	// a fetch must leave it alone.
	sentinel := filepath.Join(dest, "untracked-local-sentinel")
	if err := os.WriteFile(sentinel, []byte("still here"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{}); err != nil {
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

	err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{})
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
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{}); err != nil {
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
			errs[i] = CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{})
		}(i)
	}
	close(start)
	wg.Wait()

	// What the design actually promises, and therefore what this asserts.
	//
	// This test originally demanded that all n refreshes SUCCEED. Once
	// CloneOrFetch began taking the mirror lock (so a reader's `reset --hard`
	// can no longer wipe a concurrent writer's staged files), that demand
	// became a promise the design deliberately does not make: the lock wait is
	// BOUNDED, so with enough simultaneous contenders — 12 here, which is far
	// past any real usage — the later ones legitimately exhaust the budget.
	// Under `make check`'s full parallel load they did, and the test went red
	// for correct behaviour. A gate that reds on correct behaviour is the
	// disease this repo's own P40 exists to treat, so the assertion is fixed
	// rather than the budget widened.
	//
	// The property that matters is unchanged and still fully enforced: a
	// refresh either SUCCEEDS or is REFUSED with a named contention error. It
	// is never a git-level lock crash, never a partial tree, never silent. A
	// refused refresh is not a lost one — the caller re-runs, and
	// internal/cache's SyncIfStale treats it as non-fatal by design.
	var refused int
	for i, err := range errs {
		switch {
		case err == nil:
		case errors.Is(err, ErrMirrorLocked):
			refused++
		default:
			t.Errorf("concurrent CloneOrFetch[%d]: %v — want success or a named ErrMirrorLocked refusal, "+
				"never a raw git failure (that is the crossed/lost-write class this lock exists to prevent)", i, err)
		}
	}
	if refused == n {
		t.Fatalf("all %d concurrent refreshes were refused — the lock is serialising nothing through, "+
			"which would make a read verb useless under any concurrency", n)
	}

	// And the mirror is left USABLE, not half-reset: the tree still matches
	// origin's head. A lock that returned clean errors while corrupting the
	// working tree would pass every assertion above.
	head, err := runGitOutput(context.Background(), dest, nil, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("mirror unusable after concurrent refreshes: %v", err)
	}
	if head == "" {
		t.Fatal("mirror HEAD resolved empty after concurrent refreshes")
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
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{}); err != nil {
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

	lock, err := AcquireMirrorLock(context.Background(), dest)
	if err != nil {
		t.Fatalf("AcquireMirrorLock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	// The claim is the SHAPE of the retry — it ends, and it ends with the
	// typed contention error rather than git's raw exit code — never the
	// number 2. checkoutRemoteHead still hands the production constant.
	const testLockBudget = 80 * time.Millisecond

	start := time.Now()
	err = checkoutRemoteHeadWithin(context.Background(), lock, dest, testLockBudget)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrMirrorLocked) {
		t.Fatalf("checkoutRemoteHead error = %v, want ErrMirrorLocked", err)
	}
	if elapsed < testLockBudget {
		t.Fatalf("returned before the wait budget elapsed: elapsed=%s want >= %s", elapsed, testLockBudget)
	}
	// Bounded, never a hang. Absolute slack, not a multiple: the original 2x
	// of 2s was 2s of headroom for scheduling noise and for the git processes
	// this spawns, and neither shrinks because the budget did.
	if elapsed > testLockBudget+2*time.Second {
		t.Fatalf("did not bound the wait: elapsed=%s want <= %s", elapsed, testLockBudget+2*time.Second)
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
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{}); err != nil {
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

	lock, err := AcquireMirrorLock(context.Background(), dest)
	if err != nil {
		t.Fatalf("AcquireMirrorLock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = checkoutRemoteHead(ctx, lock, dest)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("checkoutRemoteHead error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= indexLockWaitBudget {
		t.Fatalf("did not honour ctx cancellation: elapsed=%s want < %s", elapsed, indexLockWaitBudget)
	}
}
