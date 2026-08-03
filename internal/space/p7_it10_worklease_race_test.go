package space

import (
	"os"
	"path/filepath"
	"testing"
)

// TestP7IT10WorkLeaseCASKeepsRootAcrossDeterministicDirectorySwap forces the
// configured pathname to become an outside-root symlink after CAS has acquired
// its lock and read the expected revision, but before it creates the temporary
// lease file. randomToken is the store's existing deterministic test seam at
// exactly that boundary, so this test has no scheduler race or hang path.
func TestP7IT10WorkLeaseCASKeepsRootAcrossDeterministicDirectorySwap(t *testing.T) {
	t.Parallel()
	store, cacheRoot := newTestWorkLeaseStore(t)
	lease := testWorkLease("p7-it10-deterministic-swap")
	original := filepath.Join(cacheRoot, workLeaseDirectory)
	held := filepath.Join(cacheRoot, "work-leases-held")
	outside := t.TempDir()

	swapped := false
	store.randomToken = func() (string, error) {
		if err := os.Rename(original, held); err != nil {
			return "", err
		}
		if err := os.Symlink(outside, original); err != nil {
			return "", err
		}
		swapped = true
		return "p7it10", nil
	}
	if _, err := store.CompareAndSwap(t.Context(), lease.Identity.LeaseKey, "", &lease); err != nil {
		t.Fatalf("CAS through retained root: %v", err)
	}
	if !swapped {
		t.Fatal("test hook did not force the pathname swap during CAS")
	}

	name, err := workLeaseFilename(lease.Identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(held, name)); err != nil {
		t.Fatalf("retained rooted directory did not receive lease: %v", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("deterministic symlink swap escaped root: entries=%v err=%v", entries, err)
	}
}
