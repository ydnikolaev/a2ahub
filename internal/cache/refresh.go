package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// perMirrorTimeout bounds how long SyncIfStale waits on a single mirror's
// refresh (Store.cloneOrFetch, normally space.CloneOrFetch) before giving
// up on it. A read verb must never let one unreachable origin hang the
// prompt indefinitely.
const perMirrorTimeout = 5 * time.Second

// totalBudget bounds SyncIfStale's own wall-clock spend across every
// connected space's mirror combined. With one connected space this caps
// at perMirrorTimeout (5s); the separate ceiling exists so that N
// connected spaces, one of which has an unreachable origin, cannot turn a
// single read into an N*perMirrorTimeout hang — SyncIfStale stops
// attempting further mirrors once the budget is spent and reports the
// ones it never got to.
const totalBudget = 10 * time.Second

// ErrSyncBudgetExhausted is wrapped into one returned error per mirror
// SyncIfStale did not get a chance to attempt because totalBudget ran out
// first.
var ErrSyncBudgetExhausted = errors.New("cache: SyncIfStale: budget exhausted")

// SyncIfStale refreshes every connected space's mirror whose sync-age
// exceeds the Store's TTL (AC-1050, spec 45 M1): the read path's own
// fix for a contract published on the other side after this side's last
// `sync` being silently invisible to `a2a inbox`.
//
// Every clause here is load-bearing:
//
//  1. Refresh only, NEVER first-clone. A mirror whose Dir is not already a
//     git repository is skipped — not cloned. space.CloneOrFetch clones
//     into an absent/empty dir, and a clone this func's own timeout kills
//     mid-write can leave a non-empty NON-git directory, which every later
//     call then refuses permanently with space.ErrNonGitTarget
//     (internal/space/mirror.go). A read verb must never be able to
//     poison a mirror that way. A missing mirror stays doctor/sync/
//     connect's job; buildStore already copes with a zero manifest.
//  2. Staleness reuses mirrorSyncAge + s.ttl (the same decision
//     statusline's own detached refresh already makes) — never a second
//     staleness computation. Never-synced (mirrorSyncAge's synced=false)
//     counts as stale, same as spaceSyncStale's own convention.
//  3. Bounded: each attempted mirror gets its own context.WithTimeout(ctx,
//     perMirrorTimeout) derived from ctx; the remaining totalBudget is
//     checked BEFORE starting each mirror, and once it is spent,
//     SyncIfStale stops and returns one error per mirror it never
//     attempted, naming it.
//  4. Errors are returned, never logged (rails: log-or-return, never
//     both) — one error per failed mirror, each naming the space id and
//     wrapped so errors.Is still resolves the underlying cause.
//  5. Never fatal to the read: a failure here never panics and never
//     leaves the mirror less usable than it was — see this method's own
//     doc trailer on what a timed-out fetch does to the mirror on disk.
//  6. A successful fetch also refreshes this Store's own cached
//     space.Manifest for that mirror (best-effort; a fetch that succeeds
//     but leaves an unreadable/unparseable space.yaml keeps the prior
//     manifest rather than erroring the fetch over it). Without this,
//     buildIndex (mirror.go) would find a just-fetched participant's
//     freshly-published artifact just fine — walkArtifacts/walkEvents are
//     generic tree walks, never manifest-scoped — but fold.Fold would
//     resolve that participant's own entry-transition authorization
//     (internal/fold's membership check) against the manifest AS IT
//     STOOD BEFORE the fetch. A participant who joins the space and
//     publishes their first artifact in the same push would fold to an
//     unauthorized-actor flag stuck at `draft`, not the visible-but-wrong
//     "invisible artifact" this whole method exists to kill, just one
//     layer downstream of it.
//
// A timed-out git fetch (this func's own ctxDerived expiring mid-run)
// kills the git process (exec.CommandContext's own SIGKILL-on-cancel);
// space.CloneOrFetch's fetch step only advances refs after git's fetch
// completes successfully, and its checkoutRemoteHead step (the one thing
// that touches the WORKING TREE) never runs at all when fetch itself
// returns an error. So a killed fetch leaves the mirror's refs and
// working tree exactly as they were before the call — this method reads
// nothing else from Store's own state, so there is nothing else to leave
// inconsistent.
func (s *Store) SyncIfStale(ctx context.Context) []error {
	// Indices into s.spaces, not copies — a successful refresh below
	// writes the reloaded manifest straight back into s.spaces[idx]
	// (clause 6), which a copied SpaceMirror value could not do.
	var staleIdx []int
	for i, sm := range s.spaces {
		if !isRefreshableMirror(sm.Dir) {
			continue // poisoning guard: never first-clone from a read verb
		}
		age, synced := mirrorSyncAge(s.now(), sm.Dir)
		if synced && age <= s.ttl {
			continue // fresh
		}
		staleIdx = append(staleIdx, i)
	}
	if len(staleIdx) == 0 {
		return nil
	}

	start := time.Now()
	var errs []error
	for i, idx := range staleIdx {
		if time.Since(start) >= totalBudget {
			for _, restIdx := range staleIdx[i:] {
				errs = append(errs, fmt.Errorf("cache: SyncIfStale: space %s: %w", s.spaces[restIdx].SpaceID, ErrSyncBudgetExhausted))
			}
			break
		}
		sm := s.spaces[idx]
		mctx, cancel := context.WithTimeout(ctx, perMirrorTimeout)
		err := s.cloneOrFetch(mctx, sm.Dir, sm.RepoURL)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("cache: SyncIfStale: space %s: %w", sm.SpaceID, err))
			continue
		}
		s.refreshManifestAfterFetch(idx)
	}
	return errs
}

// refreshManifestAfterFetch re-reads space.yaml from s.spaces[idx]'s own
// mirror dir (now freshly fetched) and, on success, replaces that
// mirror's cached Manifest in place — clause 6 of SyncIfStale's own doc
// comment. Best-effort: an unreadable/unparseable space.yaml right after
// a successful fetch is unexpected but not this call's job to escalate;
// it leaves the prior manifest in place rather than turning a successful
// git fetch into a SyncIfStale error over it.
func (s *Store) refreshManifestAfterFetch(idx int) {
	raw, err := os.ReadFile(filepath.Join(s.spaces[idx].Dir, "space.yaml"))
	if err != nil {
		return
	}
	m, err := space.ParseManifest(raw)
	if err != nil {
		return
	}
	s.spaces[idx].Manifest = m
}

// isRefreshableMirror reports whether dir already holds a git repository
// (a real prior clone), mirroring internal/space's own private isGitRepo
// check (unexported there, so this is this package's own copy — the
// poisoning guard SyncIfStale's clause 1 depends on).
func isRefreshableMirror(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}
