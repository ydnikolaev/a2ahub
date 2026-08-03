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
		tc := tc
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
		target := target
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
		contender := contender
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

func newTestWorkLeaseStore(t *testing.T) (*WorkLeaseStore, string) {
	t.Helper()
	cacheRoot := t.TempDir()
	store, err := NewWorkLeaseStore(cacheRoot)
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
