//go:build windows

package space

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func TestWorkLeaseStoreWindowsRuntimeConstructorAndCRUD(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	lease := testWorkLease("windows-runtime-crud")

	revision, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, "", &lease)
	if err != nil {
		t.Fatalf("create Windows lease below %q: %v", cacheRoot, err)
	}
	loaded, loadedRevision, err := store.Load(context.Background(), lease.Identity.LeaseKey)
	if err != nil {
		t.Fatalf("load Windows lease: %v", err)
	}
	if loadedRevision != revision || loaded.Identity.LeaseKey != lease.Identity.LeaseKey {
		t.Fatalf("loaded Windows lease = (%q, %q), want (%q, %q)", loaded.Identity.LeaseKey, loadedRevision, lease.Identity.LeaseKey, revision)
	}
	if _, err := store.CompareAndSwap(context.Background(), lease.Identity.LeaseKey, revision, nil); err != nil {
		t.Fatalf("delete Windows lease: %v", err)
	}
	if _, _, err := store.Load(context.Background(), lease.Identity.LeaseKey); !errors.Is(err, workreport.ErrLeaseNotFound) {
		t.Fatalf("load deleted Windows lease = %v, want ErrLeaseNotFound", err)
	}
}

func TestWorkLeaseStoreWindowsRuntimeLockContentionAndCrashRelease(t *testing.T) {
	t.Parallel()
	first, cacheRoot := newTestWorkLeaseStore(t)
	second, err := NewWorkLeaseStore(cacheRoot)
	if err != nil {
		t.Fatalf("open second Windows store: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() }) // reason: lock assertions report the primary failure

	lease := testWorkLease("windows-runtime-lock")
	name, err := workLeaseFilename(lease.Identity.LeaseKey)
	if err != nil {
		t.Fatalf("lease filename: %v", err)
	}
	lockName := name + ".lock"
	held, err := first.acquire(context.Background(), lockName)
	if err != nil {
		t.Fatalf("acquire first Windows lock: %v", err)
	}
	probe, err := second.openLockFile(lockName)
	if err != nil {
		t.Fatalf("open second Windows lock handle: %v", err)
	}
	probeErr := tryLockWorkLeaseFile(probe)
	if probeErr == nil {
		_ = unlockWorkLeaseFile(probe) // reason: clean up an unexpectedly acquired probe before failing
		_ = probe.Close()              // reason: the contention assertion is the primary failure
		t.Fatal("second Windows handle acquired an overlapping exclusive lock")
	}
	if !isWorkLeaseLockContended(probeErr) {
		_ = probe.Close() // reason: preserve the unexpected platform lock error
		t.Fatalf("second Windows lock = %v, want contention", probeErr)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close Windows contention probe: %v", err)
	}

	if err := held.file.Close(); err != nil { // closing without unlock simulates process termination
		t.Fatalf("simulate Windows holder crash: %v", err)
	}
	reclaimed, err := second.acquire(context.Background(), lockName)
	if err != nil {
		t.Fatalf("reclaim Windows lock after holder crash: %v", err)
	}
	if err := second.release(reclaimed); err != nil {
		t.Fatalf("release reclaimed Windows lock: %v", err)
	}
}
