package space

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func TestWorkLeaseStoreCreateLoadCASDelete(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	ctx := context.Background()
	lease := testWorkLease("create-load")

	revision, err := store.CompareAndSwap(ctx, lease.Identity.LeaseKey, "", &lease)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if revision == "" {
		t.Fatal("create returned empty revision")
	}
	loaded, loadedRevision, err := store.Load(ctx, lease.Identity.LeaseKey)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loadedRevision != revision || loaded.Summary != lease.Summary {
		t.Fatalf("loaded lease/revision = (%q, %q), want (%q, %q)", loaded.Summary, loadedRevision, lease.Summary, revision)
	}
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	raw, err := os.ReadFile(filepath.Join(cacheRoot, workLeaseDirectory, name))
	if err != nil {
		t.Fatalf("read stored lease: %v", err)
	}
	if revision != revisionOf(raw) {
		t.Fatalf("revision = %q, want digest of exact stored bytes %q", revision, revisionOf(raw))
	}

	updated := loaded.Clone()
	updated.Summary = "updated semantic checkpoint"
	nextRevision, err := store.CompareAndSwap(ctx, lease.Identity.LeaseKey, revision, &updated)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if nextRevision == revision {
		t.Fatal("changed exact bytes retained the old revision")
	}
	actual, err := store.CompareAndSwap(ctx, lease.Identity.LeaseKey, revision, &updated)
	if !errors.Is(err, workreport.ErrSequenceConflict) || actual != nextRevision {
		t.Fatalf("stale CAS = (%q, %v), want (%q, ErrSequenceConflict)", actual, err, nextRevision)
	}

	deletedRevision, err := store.CompareAndSwap(ctx, lease.Identity.LeaseKey, nextRevision, nil)
	if err != nil || deletedRevision != "" {
		t.Fatalf("delete = (%q, %v), want empty revision", deletedRevision, err)
	}
	if _, _, err := store.Load(ctx, lease.Identity.LeaseKey); !errors.Is(err, workreport.ErrLeaseNotFound) {
		t.Fatalf("load after delete = %v, want ErrLeaseNotFound", err)
	}
}

func TestWorkLeaseStorePermissionsAndFixedNames(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	lease := testWorkLease("permissions")
	if _, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease); err != nil {
		t.Fatalf("create: %v", err)
	}
	dirInfo, err := os.Stat(filepath.Join(cacheRoot, workLeaseDirectory))
	if err != nil {
		t.Fatalf("stat work-leases: %v", err)
	}
	if !validWorkLeaseDirectory(dirInfo) {
		t.Fatalf("work-leases failed platform directory checks: mode=%v", dirInfo.Mode())
	}
	name, err := workLeaseFilename(lease.Identity.LeaseKey)
	if err != nil {
		t.Fatalf("filename: %v", err)
	}
	if strings.ContainsAny(name, "/\\:") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("unsafe lease filename %q", name)
	}
	fileInfo, err := os.Stat(filepath.Join(cacheRoot, workLeaseDirectory, name))
	if err != nil {
		t.Fatalf("stat lease: %v", err)
	}
	if !validWorkLeaseRegularFile(fileInfo) {
		t.Fatalf("lease failed platform regular-file checks: mode=%v", fileInfo.Mode())
	}
	for _, invalid := range []string{"", "sha256:nope", "sha256:" + strings.Repeat("A", 64), "../outside"} {
		if _, _, err := store.Load(context.Background(), invalid); !errors.Is(err, workreport.ErrInvalidLease) {
			t.Errorf("Load(%q) = %v, want ErrInvalidLease", invalid, err)
		}
	}
}

func TestWorkLeaseStoreRejectsCorruptOversizeAndUnknownVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  func(t *testing.T, lease workreport.Lease) []byte
		want error
	}{
		{name: "corrupt", raw: func(*testing.T, workreport.Lease) []byte { return []byte("{not-json") }, want: workreport.ErrInvalidLease},
		{name: "oversize", raw: func(*testing.T, workreport.Lease) []byte {
			return bytes.Repeat([]byte("x"), workreport.MaximumEncodedLease+1)
		}, want: workreport.ErrLeaseTooLarge},
		{name: "unknown-version", raw: func(t *testing.T, lease workreport.Lease) []byte {
			raw, err := workreport.MarshalLease(lease)
			if err != nil {
				t.Fatalf("marshal lease: %v", err)
			}
			return bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
		}, want: workreport.ErrInvalidLease},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, _ := newTestWorkLeaseStore(t)
			lease := testWorkLease(tc.name)
			name, _ := workLeaseFilename(lease.Identity.LeaseKey)
			raw := tc.raw(t, lease)
			if err := store.root.WriteFile(name, raw, 0o600); err != nil {
				t.Fatalf("seed invalid lease: %v", err)
			}
			if _, _, err := store.Load(context.Background(), lease.Identity.LeaseKey); !errors.Is(err, tc.want) {
				t.Fatalf("Load = %v, want %v", err, tc.want)
			}
			if _, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, revisionOf(raw), &lease); !errors.Is(err, tc.want) {
				t.Fatalf("CompareAndSwap over invalid stored bytes = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWorkLeaseStoreConcurrentCreateHasOneWinner(t *testing.T) {
	t.Parallel()
	store, _ := newTestWorkLeaseStore(t)
	lease := testWorkLease("concurrent")
	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, workreport.ErrSequenceConflict):
			conflicts++
		default:
			t.Fatalf("concurrent CAS returned %v", err)
		}
	}
	if winners != 1 || conflicts != contenders-1 {
		t.Fatalf("winners/conflicts = %d/%d, want 1/%d", winners, conflicts, contenders-1)
	}
}

func TestWorkLeaseStoreRejectsSymlinkRootDirectoryLeafTempAndLock(t *testing.T) {
	t.Parallel()
	t.Run("root", func(t *testing.T) {
		t.Parallel()
		parent := t.TempDir()
		outside := t.TempDir()
		root := filepath.Join(parent, "cache")
		if err := os.Symlink(outside, root); err != nil {
			t.Fatalf("symlink root: %v", err)
		}
		if _, err := NewWorkLeaseStore(root); !errors.Is(err, workreport.ErrInvalidLease) {
			t.Fatalf("NewWorkLeaseStore(symlink root) = %v, want ErrInvalidLease", err)
		}
	})
	t.Run("work-leases directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, workLeaseDirectory)); err != nil {
			t.Fatalf("symlink directory: %v", err)
		}
		if _, err := NewWorkLeaseStore(root); !errors.Is(err, workreport.ErrInvalidLease) {
			t.Fatalf("NewWorkLeaseStore(symlink dir) = %v, want ErrInvalidLease", err)
		}
	})
	for _, target := range []string{"leaf", "temp", "lock"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			store, cacheRoot := newTestWorkLeaseStore(t)
			lease := testWorkLease("symlink-" + target)
			name, _ := workLeaseFilename(lease.Identity.LeaseKey)
			outside := filepath.Join(t.TempDir(), "outside")
			const sentinel = "must-not-change"
			if err := os.WriteFile(outside, []byte(sentinel), 0o600); err != nil {
				t.Fatalf("seed outside: %v", err)
			}
			linkName := name
			switch target {
			case "temp":
				store.randomToken = func() (string, error) { return "fixed", nil }
				linkName = name + ".tmp-fixed"
			case "lock":
				linkName = name + ".lock"
			}
			if err := os.Symlink(outside, filepath.Join(cacheRoot, workLeaseDirectory, linkName)); err != nil {
				t.Fatalf("plant %s symlink: %v", target, err)
			}
			if _, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease); err == nil {
				t.Fatalf("CAS accepted %s symlink", target)
			}
			raw, err := os.ReadFile(outside)
			if err != nil || string(raw) != sentinel {
				t.Fatalf("outside target = %q, %v; symlink was followed", raw, err)
			}
		})
	}
}

func TestWorkLeaseStoreCapabilitySurvivesConcurrentDirectorySwap(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	lease := testWorkLease("directory-swap")
	original := filepath.Join(cacheRoot, workLeaseDirectory)
	held := filepath.Join(cacheRoot, "work-leases-held")
	outside := t.TempDir()

	start := make(chan struct{})
	swapDone := make(chan error, 1)
	casDone := make(chan error, 1)
	go func() {
		<-start
		if err := os.Rename(original, held); err != nil {
			swapDone <- err
			return
		}
		swapDone <- os.Symlink(outside, original)
	}()
	go func() {
		<-start
		_, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease)
		casDone <- err
	}()
	close(start)
	if err := <-swapDone; err != nil {
		t.Fatalf("swap work-leases path: %v", err)
	}
	if err := <-casDone; err != nil {
		t.Fatalf("CAS through retained capability: %v", err)
	}
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	if _, err := os.Stat(filepath.Join(held, name)); err != nil {
		t.Fatalf("retained directory did not receive lease: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside directory received lease, stat = %v", err)
	}
}

func TestWorkLeaseStoreReusesUnlockedCrashRemnant(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	lease := testWorkLease("crash-remnant")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	lock := filepath.Join(cacheRoot, workLeaseDirectory, name+".lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("seed unlocked crash remnant: %v", err)
	}
	if _, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease); err != nil {
		t.Fatalf("CAS after unlocked crash remnant: %v", err)
	}
}

func TestWorkLeaseStoreSlowHolderCannotBeEvictedByAge(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	other, err := NewWorkLeaseStore(cacheRoot)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	t.Cleanup(func() { _ = other.Close() }) // reason: test assertions report the primary failure
	lease := testWorkLease("slow-holder")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	lockName := name + ".lock"

	held, err := store.acquire(context.Background(), lockName)
	if err != nil {
		t.Fatalf("acquire slow holder: %v", err)
	}
	lockPath := filepath.Join(cacheRoot, workLeaseDirectory, lockName)
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
		t.Fatalf("age live lock file: %v", err)
	}
	probe, err := other.openLockFile(lockName)
	if err != nil {
		t.Fatalf("open aged live lock for contention probe: %v", err)
	}
	probeErr := tryLockWorkLeaseFile(probe)
	if probeErr == nil {
		_ = unlockWorkLeaseFile(probe) // reason: the contention assertion below is the primary failure
		_ = probe.Close()              // reason: the contention assertion below is the primary failure
		t.Fatal("aged live holder lost its kernel lock")
	}
	if !isWorkLeaseLockContended(probeErr) {
		_ = probe.Close() // reason: preserve the unexpected platform lock error
		t.Fatalf("probe aged live holder: %v", probeErr)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close contention probe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := other.acquire(ctx, lockName); !errors.Is(err, context.Canceled) {
		t.Fatalf("aged live holder was evicted, acquire = %v", err)
	}
	if err := store.release(held); err != nil {
		t.Fatalf("release slow holder: %v", err)
	}
	reclaimed, err := other.acquire(context.Background(), lockName)
	if err != nil {
		t.Fatalf("reclaim after holder release: %v", err)
	}
	if err := other.release(reclaimed); err != nil {
		t.Fatalf("release reclaimed lock: %v", err)
	}
}

func TestWorkLeaseStoreLockFileIsRegularAndBounded(t *testing.T) {
	t.Parallel()
	store, _ := newTestWorkLeaseStore(t)
	lease := testWorkLease("bounded-lock")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	lockName := name + ".lock"
	held, err := store.acquire(context.Background(), lockName)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := store.readLock(lockName); err != nil {
		t.Fatalf("read rooted lock file: %v", err)
	}
	if err := store.release(held); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := store.root.WriteFile(lockName, bytes.Repeat([]byte("x"), 1025), 0o600); err != nil {
		t.Fatalf("seed oversized lock: %v", err)
	}
	if _, err := store.readLock(lockName); !errors.Is(err, workreport.ErrInvalidLease) {
		t.Fatalf("read oversized lock = %v, want ErrInvalidLease", err)
	}
}

func TestWorkLeaseStoreContendedAcquireCancelsWithoutPollingDelay(t *testing.T) {
	t.Parallel()
	store, _ := newTestWorkLeaseStore(t)
	lease := testWorkLease("cancel-contended")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	lockName := name + ".lock"
	held, err := store.acquire(context.Background(), lockName)
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.acquire(ctx, lockName); !errors.Is(err, context.Canceled) {
		t.Fatalf("contended cancelled acquire = %v, want context.Canceled", err)
	}
	if err := store.release(held); err != nil {
		t.Fatalf("release holder: %v", err)
	}
}

func TestWorkLeaseStoreReadLockRefusesConcurrentSymlinkSwap(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	lease := testWorkLease("read-lock-swap")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	lockPath := filepath.Join(cacheRoot, workLeaseDirectory, name+".lock")
	outside := filepath.Join(t.TempDir(), "outside")
	const outsideData = "outside-must-never-be-read"
	if err := os.WriteFile(outside, []byte(outsideData), 0o600); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("inside"), 0o600); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(lockPath)                             // reason: each race iteration tolerates either competing state
			_ = os.Symlink(outside, lockPath)                   // reason: each race iteration tolerates either competing state
			_ = os.Remove(lockPath)                             // reason: each race iteration tolerates either competing state
			_ = os.WriteFile(lockPath, []byte("inside"), 0o600) // reason: each race iteration tolerates either competing state
		}
	}()
	for range 500 {
		raw, err := store.readLock(name + ".lock")
		if err == nil && string(raw) == outsideData {
			close(stop)
			<-done
			t.Fatal("readLock followed a concurrently swapped external symlink")
		}
	}
	close(stop)
	<-done
	raw, err := os.ReadFile(outside)
	if err != nil || string(raw) != outsideData {
		t.Fatalf("outside target = %q, %v; swap escaped root", raw, err)
	}
}

func TestWorkLeaseStorePreservesFilesystemErrorIdentity(t *testing.T) {
	t.Parallel()
	cacheRoot := t.TempDir()
	store, err := NewWorkLeaseStore(cacheRoot)
	if err != nil {
		t.Fatalf("NewWorkLeaseStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lease := testWorkLease("closed-root-error")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	_, err = store.readLock(name + ".lock")
	var pathErr *os.PathError
	if err == nil || !errors.As(err, &pathErr) {
		t.Fatalf("readLock on closed root = %v, want wrapped *os.PathError", err)
	}
}

func TestWorkLeaseStoreReleasePreservesUnlockError(t *testing.T) {
	t.Parallel()
	store, _ := newTestWorkLeaseStore(t)
	lease := testWorkLease("release-error")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	held, err := store.acquire(context.Background(), name+".lock")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := held.file.Close(); err != nil {
		t.Fatalf("pre-close held fd: %v", err)
	}
	if err := store.release(held); err == nil {
		t.Fatal("release closed fd = nil, want preserved platform unlock error")
	}
}

func TestWorkLeaseStoreCrashCloseAllowsConcurrentCASReclaim(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	lease := testWorkLease("crash-reclaim")
	name, _ := workLeaseFilename(lease.Identity.LeaseKey)
	held, err := store.acquire(context.Background(), name+".lock")
	if err != nil {
		t.Fatalf("acquire simulated crashed holder: %v", err)
	}
	if err := held.file.Close(); err != nil { // closing without LOCK_UN simulates process exit
		t.Fatalf("simulate holder crash: %v", err)
	}

	const contenders = 6
	stores := make([]*WorkLeaseStore, contenders)
	for index := range stores {
		stores[index], err = NewWorkLeaseStore(cacheRoot)
		if err != nil {
			t.Fatalf("open contender %d: %v", index, err)
		}
		t.Cleanup(func() { _ = stores[index].Close() }) // reason: contender assertions report the primary failure
	}
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for _, contender := range stores {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := contender.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, workreport.ErrSequenceConflict) {
			t.Fatalf("reclaim contender = %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("crash reclaim winners = %d, want exactly one", winners)
	}
}

func TestWorkLeaseStoreHonorsCancelledContext(t *testing.T) {
	t.Parallel()
	store, _ := newTestWorkLeaseStore(t)
	lease := testWorkLease("cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.CompareAndSwap(ctx, lease.Identity.LeaseKey, "", &lease); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompareAndSwap cancelled = %v, want context.Canceled", err)
	}
	if _, _, err := store.Load(ctx, lease.Identity.LeaseKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load cancelled = %v, want context.Canceled", err)
	}
}

func TestWorkLeaseStoreListFiltersExpiryScopeAndSortsDetachedCopies(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	store, _ := newTestWorkLeaseStoreWithOptions(t, WithWorkLeaseClock(fixedWorkLeaseClock{now: now}))
	project := testWorkLeaseDigest("project:list")
	leases := []workreport.Lease{
		workLeaseForSelection("z-current", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY2", "session:z", now.Add(time.Minute), false),
		workLeaseForSelection("a-current", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY1", "session:a", now, false),
		workLeaseForSelection("equal-current", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY3", "session:e", now, false),
		workLeaseForSelection("expired", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY4", "session:x", now.Add(-time.Nanosecond), false),
		workLeaseForSelection("other-space", project, "beta", "work:01K20ABCDEFHJKMNPQRSTVWXY5", "session:b", now, false),
		workLeaseForSelection("other-project", testWorkLeaseDigest("project:other"), "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY6", "session:p", now, false),
	}
	for index := range leases {
		seedWorkLease(t, store, leases[index])
	}

	got, err := store.ListWork(context.Background(), project, "alpha", "", false)
	if err != nil {
		t.Fatalf("ListWork: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("current list length = %d, want 3", len(got))
	}
	wantOrder := []string{
		"work:01K20ABCDEFHJKMNPQRSTVWXY1",
		"work:01K20ABCDEFHJKMNPQRSTVWXY2",
		"work:01K20ABCDEFHJKMNPQRSTVWXY3",
	}
	for index, want := range wantOrder {
		if got[index].Identity.WorkID != want {
			t.Fatalf("list[%d].work_id = %q, want %q", index, got[index].Identity.WorkID, want)
		}
	}
	got[0].WaitingOn = append(got[0].WaitingOn, workreport.WaitingOn{Kind: workreport.WaitHuman, ID: "mutated"})
	again, err := store.ListWork(context.Background(), project, "alpha", wantOrder[0], false)
	if err != nil || len(again) != 1 {
		t.Fatalf("filtered ListWork = %d, %v, want one", len(again), err)
	}
	if len(again[0].WaitingOn) != 0 {
		t.Fatal("caller mutation reached the persisted/listed lease")
	}

	all, err := store.ListWork(context.Background(), project, "alpha", "", true)
	if err != nil || len(all) != 4 {
		t.Fatalf("include-expired ListWork = %d, %v, want four", len(all), err)
	}
}

func TestWorkLeaseStoreExpiredClosingPendingIsNeverCurrent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	store, _ := newTestWorkLeaseStoreWithOptions(t, WithWorkLeaseClock(fixedWorkLeaseClock{now: now}))
	project := testWorkLeaseDigest("project:closing")
	lease := workLeaseForSelection("closing", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY7", "session:closing", now.Add(-time.Nanosecond), false)
	lease = closingPendingWorkLease(t, lease)
	seedWorkLease(t, store, lease)

	current, err := store.ListWork(context.Background(), project, "alpha", "", false)
	if err != nil || len(current) != 0 {
		t.Fatalf("current closing expired list = %d, %v, want empty", len(current), err)
	}
	if _, err := store.ResolveWork(context.Background(), project, "alpha", "", ""); !errors.Is(err, workreport.ErrLeaseNotFound) {
		t.Fatalf("implicit ResolveWork expired closing = %v, want ErrLeaseNotFound", err)
	}
	resolved, err := store.ResolveWork(context.Background(), project, "alpha", lease.Identity.WorkID, lease.Identity.Actor.Session)
	if err != nil || resolved.WorkID != lease.Identity.WorkID {
		t.Fatalf("explicit ResolveWork expired closing = %+v, %v", resolved, err)
	}
}

func TestWorkLeaseStoreResolveWorkExactAbsentAndAmbiguous(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	store, _ := newTestWorkLeaseStoreWithOptions(t, WithWorkLeaseClock(fixedWorkLeaseClock{now: now}))
	project := testWorkLeaseDigest("project:resolve")
	first := workLeaseForSelection("resolve-a", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY8", "session:a", now, false)
	second := workLeaseForSelection("resolve-b", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXY9", "session:b", now, false)
	seedWorkLease(t, store, first)
	seedWorkLease(t, store, second)

	if _, err := store.ResolveWork(context.Background(), project, "alpha", "", ""); !errors.Is(err, ErrWorkSelectionAmbiguous) {
		t.Fatalf("implicit ResolveWork = %v, want ErrWorkSelectionAmbiguous", err)
	}
	resolved, err := store.ResolveWork(context.Background(), project, "alpha", second.Identity.WorkID, second.Identity.Actor.Session)
	if err != nil {
		t.Fatalf("ResolveWork exact: %v", err)
	}
	if resolved.ProjectID != project || resolved.Space != "alpha" || resolved.Thread != second.Identity.Thread ||
		resolved.WorkID != second.Identity.WorkID || resolved.Actor != second.Identity.Actor {
		t.Fatalf("resolved identity = %+v, want exact owner %+v", resolved, second.Identity)
	}
	if _, err := store.ResolveWork(context.Background(), project, "alpha", second.Identity.WorkID, ""); !errors.Is(err, workreport.ErrLeaseNotOwned) {
		t.Fatalf("work-only ResolveWork = %v, want ErrLeaseNotOwned", err)
	}
	if _, err := store.ResolveWork(context.Background(), project, "alpha", "", second.Identity.Actor.Session); !errors.Is(err, workreport.ErrLeaseNotOwned) {
		t.Fatalf("session-only ResolveWork = %v, want ErrLeaseNotOwned", err)
	}
	if _, err := store.ResolveWork(context.Background(), project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXYZ", "session:missing"); !errors.Is(err, workreport.ErrLeaseNotFound) {
		t.Fatalf("missing ResolveWork = %v, want ErrLeaseNotFound", err)
	}
	if _, err := store.ResolveWork(context.Background(), project, "beta", second.Identity.WorkID, second.Identity.Actor.Session); !errors.Is(err, workreport.ErrLeaseNotFound) {
		t.Fatalf("cross-space ResolveWork = %v, want ErrLeaseNotFound", err)
	}
	if _, err := store.ResolveWork(context.Background(), testWorkLeaseDigest("project:other"), "alpha", second.Identity.WorkID, second.Identity.Actor.Session); !errors.Is(err, workreport.ErrLeaseNotFound) {
		t.Fatalf("cross-project ResolveWork = %v, want ErrLeaseNotFound", err)
	}
}

func TestWorkLeaseStoreListingFailsClosedOnUnsafeCorruptAndOversize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		seed func(t *testing.T, store *WorkLeaseStore, cacheRoot string)
		want error
	}{
		{name: "irregular-name", seed: func(t *testing.T, _ *WorkLeaseStore, root string) {
			writeWorkLeaseTestFile(t, root, "notes.txt", []byte("data"))
		}, want: workreport.ErrInvalidLease},
		{name: "symlink", seed: func(t *testing.T, _ *WorkLeaseStore, root string) {
			lease := testWorkLease("list-symlink")
			name, _ := workLeaseFilename(lease.Identity.LeaseKey)
			if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(root, workLeaseDirectory, name)); err != nil {
				t.Fatalf("seed symlink: %v", err)
			}
		}, want: workreport.ErrInvalidLease},
		{name: "corrupt", seed: func(t *testing.T, _ *WorkLeaseStore, root string) {
			lease := testWorkLease("list-corrupt")
			name, _ := workLeaseFilename(lease.Identity.LeaseKey)
			writeWorkLeaseTestFile(t, root, name, []byte("{bad"))
		}, want: workreport.ErrInvalidLease},
		{name: "oversize", seed: func(t *testing.T, _ *WorkLeaseStore, root string) {
			lease := testWorkLease("list-oversize")
			name, _ := workLeaseFilename(lease.Identity.LeaseKey)
			writeWorkLeaseTestFile(t, root, name, bytes.Repeat([]byte("x"), workreport.MaximumEncodedLease+1))
		}, want: workreport.ErrLeaseTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, root := newTestWorkLeaseStore(t)
			tc.seed(t, store, root)
			if _, err := store.ListWork(context.Background(), "project", "alpha", "", true); !errors.Is(err, tc.want) {
				t.Fatalf("ListWork = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWorkLeaseStoreListingBoundedByEntryCount(t *testing.T) {
	t.Parallel()
	store, root := newTestWorkLeaseStore(t)
	directory := filepath.Join(root, workLeaseDirectory)
	for index := 0; index <= maximumWorkLeaseEntries; index++ {
		name := fmt.Sprintf("%064x.json.lock", index)
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatalf("seed bounded entry %d: %v", index, err)
		}
	}
	if _, err := store.ListWork(context.Background(), "project", "alpha", "", true); !errors.Is(err, ErrWorkLeaseEnumerationLimit) {
		t.Fatalf("ListWork count bound = %v, want ErrWorkLeaseEnumerationLimit", err)
	}
}

func TestWorkLeaseStoreListingBoundedByNameBytes(t *testing.T) {
	t.Parallel()
	store, root := newTestWorkLeaseStore(t)
	directory := filepath.Join(root, workLeaseDirectory)
	sample := fmt.Sprintf("%064x.json.lock", 0)
	entries := maximumWorkLeaseListingBytes/len(sample) + 1
	if entries >= maximumWorkLeaseEntries {
		t.Fatalf("test precondition: name-byte bound needs %d entries, count bound is %d", entries, maximumWorkLeaseEntries)
	}
	for index := 0; index < entries; index++ {
		name := fmt.Sprintf("%064x.json.lock", index)
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatalf("seed name-byte entry %d: %v", index, err)
		}
	}
	if _, err := store.ListWork(context.Background(), "project", "alpha", "", true); !errors.Is(err, ErrWorkLeaseEnumerationLimit) {
		t.Fatalf("ListWork name-byte bound = %v, want ErrWorkLeaseEnumerationLimit", err)
	}
}

func TestWorkLeaseStoreConcurrentCASAndList(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	store, _ := newTestWorkLeaseStoreWithOptions(t, WithWorkLeaseClock(fixedWorkLeaseClock{now: now}))
	project := testWorkLeaseDigest("project:race-list")
	lease := workLeaseForSelection("race-list", project, "alpha", "work:01K20ABCDEFHJKMNPQRSTVWXYA", "session:race", now.Add(time.Hour), false)
	seedWorkLease(t, store, lease)
	ctx := context.Background()

	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		for range 50 {
			loaded, revision, err := store.Load(ctx, lease.Identity.LeaseKey)
			if err != nil {
				errorsSeen <- err
				return
			}
			loaded.HeartbeatSequence++
			if _, err := store.CompareAndSwap(ctx, lease.Identity.LeaseKey, revision, &loaded); err != nil {
				errorsSeen <- err
				return
			}
		}
	}()
	go func() {
		defer wait.Done()
		for range 100 {
			listed, err := store.ListWork(ctx, project, "alpha", "", false)
			if err != nil {
				errorsSeen <- err
				return
			}
			if len(listed) != 1 {
				errorsSeen <- fmt.Errorf("listed %d leases, want one", len(listed))
				return
			}
		}
	}()
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatalf("concurrent CAS/list: %v", err)
	}
}

type fixedWorkLeaseClock struct{ now time.Time }

func (c fixedWorkLeaseClock) Now() time.Time { return c.now }

func newTestWorkLeaseStoreWithOptions(t *testing.T, options ...WorkLeaseStoreOption) (*WorkLeaseStore, string) {
	t.Helper()
	cacheRoot := t.TempDir()
	store, err := NewWorkLeaseStore(cacheRoot, options...)
	if err != nil {
		t.Fatalf("NewWorkLeaseStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store, cacheRoot
}

func workLeaseForSelection(seed, project, spaceID, workID, session string, expiresAt time.Time, waiting bool) workreport.Lease {
	lease := testWorkLease(seed)
	lease.Identity.ProjectID = project
	lease.Identity.Space = spaceID
	lease.Identity.WorkID = workID
	lease.Identity.Actor.Session = session
	lease.StartedAt = expiresAt.Add(-workreport.DefaultTTL)
	lease.RenewedAt = lease.StartedAt
	lease.ExpiresAt = expiresAt
	if waiting {
		lease.Mode = workreport.ModeWaiting
		lease.WaitingOn = []workreport.WaitingOn{{Kind: workreport.WaitSystem, ID: "dependency"}}
	}
	return lease
}

func closingPendingWorkLease(t *testing.T, lease workreport.Lease) workreport.Lease {
	t.Helper()
	prepared, err := workreport.NewPreparedJournal([]byte(`{"prepared":"closing"}`))
	if err != nil {
		t.Fatalf("NewPreparedJournal: %v", err)
	}
	lease.Mode = workreport.ModePaused
	lease.Closing = true
	lease.SemanticSequence = 2
	key, err := operation.Work(lease.Identity.WorkID, 2, string(workreport.ActionStop))
	if err != nil {
		t.Fatalf("operation.Work: %v", err)
	}
	lease.Pending = &workreport.PendingOperation{
		OperationKey: key, Action: workreport.ActionStop, ArtifactID: "XA-test-20260803-a2b3", EventID: "01K20ABCDEFHJKMNPQRSTVWXYZ",
		SemanticSequence: 2, Prepared: prepared, LocalTarget: workreport.TargetClosing,
	}
	return lease
}

func seedWorkLease(t *testing.T, store *WorkLeaseStore, lease workreport.Lease) {
	t.Helper()
	if _, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease); err != nil {
		t.Fatalf("seed lease %s: %v", lease.Identity.WorkID, err)
	}
}

func writeWorkLeaseTestFile(t *testing.T, cacheRoot, name string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cacheRoot, workLeaseDirectory, name), raw, 0o600); err != nil {
		t.Fatalf("seed work lease file: %v", err)
	}
}

func newTestWorkLeaseStore(t *testing.T) (*WorkLeaseStore, string) {
	return newTestWorkLeaseStoreWithOptions(t)
}

func testWorkLease(seed string) workreport.Lease {
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	return workreport.Lease{
		SchemaVersion: workreport.SchemaVersion,
		Identity: workreport.Identity{
			LeaseKey:  testWorkLeaseDigest("lease:" + seed),
			ProjectID: testWorkLeaseDigest("project:" + seed),
			Space:     "checkout-core",
			Thread:    "thread:axon-20260803-a2b3",
			WorkID:    "work:01K20ABCDEFHJKMNPQRSTVWXYZ",
			Actor: workreport.Actor{
				Kind: "agent", Name: "codex", System: "axon", Model: "gpt-5", Session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ",
			},
		},
		SubjectRef:        "XW-axon-20260728-c3d4",
		Mode:              workreport.ModeImplementing,
		Summary:           "Implementing ingest against the agreed contract",
		StartedAt:         now,
		RenewedAt:         now,
		ExpiresAt:         now.Add(workreport.DefaultTTL),
		HeartbeatSequence: 1,
		SemanticSequence:  1,
	}
}

func testWorkLeaseDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest)
}
