package cache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/release"
)

const (
	statuslineRefreshLeaseName = "statusline-refresh.lease"
	statuslineRefreshClaimBase = "statusline-refresh-claim-"
	statuslineRefreshLeaseTTL  = DefaultStatuslineTTL
)

// StatuslineRefreshNeeded reports whether the next statusline render should
// request a refresh. It only reads local cache metadata; it never starts work.
func (s *Store) StatuslineRefreshNeeded() bool {
	if s.updateEnabled && s.updateChecker != nil {
		if _, fresh := release.ReadLatest(s.updateCachePath, s.now(), s.updateTTL); !fresh {
			return true
		}
	}
	for _, sm := range s.spaces {
		age, synced := mirrorSyncAge(s.now(), sm.Dir)
		if !synced || age > s.ttl {
			return true
		}
	}
	return false
}

// ClaimStatuslineRefreshLease atomically rate-limits detached refreshes across
// separate one-shot statusline processes. A successful sync removes the lease;
// the TTL recovers from a child that was interrupted before cleanup.
func (s *Store) ClaimStatuslineRefreshLease() bool {
	if !s.StatuslineRefreshNeeded() {
		return false
	}
	now := s.now()
	claimPath := statuslineRefreshClaimPath(s.cacheDir, now)
	if !createStatuslineRefreshLease(claimPath, now) {
		return false
	}
	path := statuslineRefreshLeasePath(s.cacheDir)
	if createStatuslineRefreshLease(path, now) {
		return true
	}
	if !statuslineRefreshLeaseExpired(path, now) {
		_ = os.Remove(claimPath) // reason: this contender did not acquire the live lease
		return false
	}
	// The generation claim above elects one stale-lease remover for this TTL
	// window. Without it, concurrent contenders could each observe the old
	// lease, then one could remove the other's newly-created replacement.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(claimPath) // reason: stale takeover failed before ownership
		return false
	}
	if createStatuslineRefreshLease(path, now) {
		return true
	}
	_ = os.Remove(claimPath) // reason: another claimant established the live lease
	return false
}

// ReleaseStatuslineRefreshLease releases this Store's claimed lease after a
// detached process failed to start.
func (s *Store) ReleaseStatuslineRefreshLease() {
	ReleaseStatuslineRefreshLease(s.cacheDir)
}

// ReleaseStatuslineRefreshLease lets the canonical sync command clear a lease
// regardless of whether sync was invoked manually or by statusline.
func ReleaseStatuslineRefreshLease(cacheDir string) {
	// Advisory cache state: absence and cleanup failure must never turn a
	// successful sync into a product failure. Claims are removed before the
	// live lease so a new contender cannot create a claim that this release
	// then accidentally removes.
	claims, _ := filepath.Glob(filepath.Join(cacheDir, statuslineRefreshClaimBase+"*.lease"))
	for _, claim := range claims {
		_ = os.Remove(claim) // reason: best-effort disposable generation cleanup
	}
	_ = os.Remove(statuslineRefreshLeasePath(cacheDir)) // reason: best-effort disposable lease cleanup
}

func statuslineRefreshLeasePath(cacheDir string) string {
	return filepath.Join(cacheDir, statuslineRefreshLeaseName)
}

func statuslineRefreshClaimPath(cacheDir string, now time.Time) string {
	generation := now.UnixNano() / statuslineRefreshLeaseTTL.Nanoseconds()
	return filepath.Join(cacheDir, statuslineRefreshClaimBase+strconv.FormatInt(generation, 10)+".lease")
}

func createStatuslineRefreshLease(path string, now time.Time) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	ok := true
	if _, err := fmt.Fprintln(f, now.UnixNano()); err != nil {
		ok = false
	}
	if err := f.Close(); err != nil {
		ok = false
	}
	if !ok {
		_ = os.Remove(path) // reason: remove only the incomplete lease this call just created
	}
	return ok
}

func statuslineRefreshLeaseExpired(path string, now time.Time) bool {
	raw, err := os.ReadFile(path)
	if err == nil {
		ns, parseErr := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if parseErr == nil {
			claimedAt := time.Unix(0, ns)
			return now.Sub(claimedAt) > statuslineRefreshLeaseTTL
		}
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		return false
	}
	return now.Sub(info.ModTime()) > statuslineRefreshLeaseTTL
}
