package cache

import (
	"os"
	"path/filepath"
	"time"
)

// mirrorSyncAge returns how long ago dir's mirror clone was last
// fetched/cloned: the mtime of `.git/FETCH_HEAD` (updated by every `git
// fetch`, per space.CloneOrFetch), falling back to `.git/HEAD`'s mtime
// (a fresh clone that has never been re-fetched yet — HEAD is written at
// clone time). synced=false when neither file is readable (never synced
// at all — a2a connect/sync has not run against this mirror yet).
func mirrorSyncAge(now time.Time, dir string) (age time.Duration, synced bool) {
	for _, rel := range []string{filepath.Join(".git", "FETCH_HEAD"), filepath.Join(".git", "HEAD")} {
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		return now.Sub(info.ModTime()), true
	}
	return 0, false
}
