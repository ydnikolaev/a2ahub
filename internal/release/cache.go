package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CheckState is the T3 update-check cache's on-disk shape: one machine-
// level fact, "latest known release", written by the few call sites that
// legitimately touch the network (NewChecker, `a2a sync`, `a2a update
// [--check]`) and read by every advisory surface.
type CheckState struct {
	// CheckedAt is when this fact was fetched.
	CheckedAt time.Time `json:"checked_at"`
	// Latest is the latest known release's bare version.
	Latest string `json:"latest"`
	// Source identifies which Source implementation produced this fact
	// (Source.Name(), e.g. "github").
	Source string `json:"source"`
}

// CachePath returns the T3 cache file location:
// os.UserCacheDir()/a2a/update-check.json — MACHINE-level, deliberately
// NOT inside any project's .a2a/cache/ (the binary is per-machine; one
// check serves every project).
func CachePath() (string, error) {
	const op = "CachePath"
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", &Error{Op: op, Err: fmt.Errorf("%w: %v", ErrCacheUnavailable, err)}
	}
	return filepath.Join(dir, "a2a", "update-check.json"), nil
}

// ReadCheck reads and decodes the cache at path. A missing file, an
// unreadable file, or malformed JSON all report (CheckState{}, false) —
// NEVER an error: a corrupt/absent update-check cache means "no notice",
// nothing more (T3).
func ReadCheck(path string) (CheckState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CheckState{}, false
	}
	var cs CheckState
	if err := json.Unmarshal(data, &cs); err != nil {
		return CheckState{}, false
	}
	return cs, true
}

// WriteCheck writes cs to path as JSON, creating path's directory if
// needed. This is the one write path in the cache's lifecycle that CAN
// fail loudly (unlike ReadCheck) — a write failure means the notice will
// not refresh, which the few network-touching callers (NewChecker, sync,
// update) should surface.
func WriteCheck(path string, cs CheckState) error {
	const op = "WriteCheck"
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &Error{Op: op, Input: dir, Err: fmt.Errorf("%w: %v", ErrCacheUnavailable, err)}
	}
	data, err := json.Marshal(cs)
	if err != nil {
		return &Error{Op: op, Err: fmt.Errorf("%w: %v", ErrCacheUnavailable, err)}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return &Error{Op: op, Input: path, Err: fmt.Errorf("%w: %v", ErrCacheUnavailable, err)}
	}
	return nil
}

// ReadLatest reads the cache at path and reports its Latest value plus
// whether it is fresh (now - CheckedAt <= ttl). A corrupt/absent cache
// reports ("", false) — the caller (statusline, inbox, doctor) renders no
// notice, never an error. A stale-but-present cache still reports its
// Latest (advisory surfaces MAY still show it while a background refresh
// is in flight) with fresh=false, so a caller wanting strict silence on
// staleness checks fresh itself.
func ReadLatest(path string, now time.Time, ttl time.Duration) (latest string, fresh bool) {
	cs, ok := ReadCheck(path)
	if !ok {
		return "", false
	}
	age := now.Sub(cs.CheckedAt)
	return cs.Latest, age >= 0 && age <= ttl
}
