package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maxStateBytes = 2 << 20
	staleLockAge  = 3 * time.Second
)

func readJSON(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxStateBytes {
		return fmt.Errorf("notification state %s exceeds %d bytes", path, maxStateBytes)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("notification state %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func withFileLock(ctx context.Context, path string, fn func() error) error {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(750 * time.Millisecond)
	defer timeout.Stop()
	for {
		token, err := createFileLock(lock)
		if err == nil {
			defer func() { _ = releaseFileLock(lock, token) }()
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		// O_EXCL lock files survive an ungraceful process exit. Reap only a
		// lock old enough that no bounded local JSON mutation should still own
		// it; a concurrent reaper losing the remove race simply retries.
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			if removeErr := os.Remove(lock); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return removeErr
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return fmt.Errorf("notification state busy: %s", path)
		case <-ticker.C:
		}
	}
}

// createFileLock atomically creates the advisory lock file at path (O_EXCL,
// same "only one creator wins" primitive as before) and stamps it with a
// token unique to THIS acquisition, so releaseFileLock can compare before
// deleting — internal/space/mirrorlock.go's own idiom (its Release doc:
// "the correctness-critical part — a compare-and-delete"), copied rather
// than a sixth locking idiom invented here.
//
// Fixes computed-not-listed-2026-08 P6 US-3: the FIFTH advisory lock (this
// one) used to remove unconditionally
// (`defer func() { _ = os.Remove(lock) }()`), so a holder slower than
// staleLockAge — after a contender legitimately took the lock over as
// stale — deleted the NEW legitimate holder's lock out from under it on its
// own, now-late release. The token is process+time, not pid alone: two
// sequential acquisitions from the SAME process must still be told apart.
func createFileLock(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	token := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	if _, err := f.WriteString(token); err != nil {
		return "", err
	}
	return token, nil
}

// releaseFileLock removes the lock at path only if it still holds exactly
// the token this call's own acquisition wrote — never an unconditional
// delete. A holder that outlives staleLockAge and loses the lock to a
// takeover therefore cannot reap the new legitimate holder's lock when its
// own (now stale) release finally runs.
func releaseFileLock(path, token string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Already gone (taken over as stale, or double-released) —
			// nothing to do.
			return nil
		}
		return err
	}
	if string(raw) != token {
		// Not ours anymore (superseded by a stale takeover) — leave it for
		// its real owner.
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
