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

// totalBudget bounds how much wall-clock SyncIfStale will spend STARTING new
// mirror refreshes. With one connected space the cap is perMirrorTimeout (5s);
// the separate ceiling exists so that N connected spaces, one of which has an
// unreachable origin, cannot turn a single read into an N*perMirrorTimeout hang.
//
// It is an ADMISSION budget, not a hard deadline, and the difference is worth
// stating because `a2a inbox` is the verb the protocol's floor tells every agent
// to run at session open: the remaining budget is checked BEFORE each mirror, so
// a mirror admitted at 9.9s can still run its own 5s timeout to completion. The
// real worst case is therefore totalBudget + perMirrorTimeout (~15s), and it is
// independent of how many mirrors there are. Interrupting a fetch already in
// flight to honour a hard 10s would buy 5s at the cost of throwing away a
// refresh that was about to succeed — the wrong trade for a cache.
const totalBudget = 10 * time.Second

// ErrSyncBudgetExhausted is wrapped into one returned error per mirror
// SyncIfStale did not get a chance to attempt because totalBudget ran out
// first.
var ErrSyncBudgetExhausted = errors.New("cache: SyncIfStale: budget exhausted")

// SpaceSyncResult preserves the owning space for an opt-in refresh outcome.
// Read verbs retain the historical []error surface through SyncIfStale;
// operational consumers use this typed form and never parse error strings.
type SpaceSyncResult struct {
	Space string
	Err   error
}

// readRefreshTTL is how recently a mirror must have been fetched for a READ
// VERB to accept it without fetching again.
//
// Deliberately far shorter than DefaultStatuslineTTL, because the two answer
// different questions. The statusline renders on every shell prompt, so it
// needs a window wide enough that prompts are free. `a2a inbox`, `show`,
// `thread` and `search` are asked on purpose, by a human or an agent that
// wants the current answer, and the cost of being right is one bounded
// `git fetch`.
//
// 30 seconds, not zero: an agent turn typically runs several read verbs
// seconds apart (inbox, then show, then thread), and paying a fetch for each
// buys nothing — nothing can have changed in between that the first fetch
// missed. It IS long enough to dedupe that burst and short enough that a
// counterparty's merge shows up on the next question rather than after the
// conversation has moved on.
const readRefreshTTL = 30 * time.Second

// ReadRefreshTTLForTest exposes readRefreshTTL to guards in OTHER packages —
// specifically cmd/a2a's wire tier, which drives the real `a2a inbox` closure
// and must assert that its fixture's mirror age actually straddles this window.
// Following the package's existing SetCloneOrFetchForTest convention.
//
// A cross-package test that hard-coded "30s" would keep passing after this
// constant moved, while silently no longer exercising the window it was
// written for — which is the precise vacuity that let RN-0910-1 ship past a
// green matrix.
const ReadRefreshTTLForTest = readRefreshTTL

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
//
//  2. Staleness reuses mirrorSyncAge, but against readRefreshTTL — NOT the
//     Store's statusline TTL. Never-synced (mirrorSyncAge's synced=false)
//     counts as stale, same as spaceSyncStale's own convention.
//
//     Sharing the statusline's TTL was a real defect, found end-to-end on
//     2026-07-26 against a live space: an artifact whose pull request had
//     just merged was INVISIBLE to `a2a outbox` and `a2a show`, which
//     answered "artifact not found", and one explicit `a2a sync` revealed
//     it immediately. The mirror had been fetched moments earlier by the
//     submit, so its age was inside DefaultStatuslineTTL (5 minutes) and
//     this function skipped it as fresh.
//
//     Five minutes is exactly the window in which a counterparty's merge
//     matters most, and the documented session-start loop says an empty
//     inbox means proceed — so this reproduced the very blocker the read
//     refresh was written to close, just narrower. The two callers were
//     never the same question: the statusline renders on every shell prompt
//     and needs a cheap window, while a read verb is a deliberate question
//     asked seconds apart at worst.
//
//  3. Bounded: each attempted mirror gets its own context.WithTimeout(ctx,
//     perMirrorTimeout) derived from ctx; the remaining totalBudget is
//     checked BEFORE starting each mirror, and once it is spent,
//     SyncIfStale stops and returns one error per mirror it never
//     attempted, naming it.
//
//  4. Errors are returned, never logged (rails: log-or-return, never
//     both) — one error per failed mirror, each naming the space id and
//     wrapped so errors.Is still resolves the underlying cause.
//
//  5. Never fatal to the read: a failure here never panics and never
//     leaves the mirror less usable than it was — see this method's own
//     doc trailer on what a timed-out fetch does to the mirror on disk.
//
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
	results := s.SyncIfStaleResults(ctx)
	errs := make([]error, 0, len(results))
	for _, result := range results {
		if result.Err != nil {
			errs = append(errs, result.Err)
		}
	}
	return errs
}

// SyncIfStaleResults is SyncIfStale's typed production seam. It returns one
// row for every refresh attempted or refused by the global admission budget,
// including successful rows, so callers can attribute degradation without
// inspecting human-readable errors.
func (s *Store) SyncIfStaleResults(ctx context.Context) []SpaceSyncResult {
	// Indices into s.spaces, not copies — a successful refresh below
	// writes the reloaded manifest straight back into s.spaces[idx]
	// (clause 6), which a copied SpaceMirror value could not do.
	var staleIdx []int
	for i, sm := range s.spaces {
		if !isRefreshableMirror(sm.Dir) {
			continue // poisoning guard: never first-clone from a read verb
		}
		age, synced := mirrorSyncAge(s.now(), sm.Dir)
		if synced && age <= readRefreshTTL {
			continue // fresh
		}
		staleIdx = append(staleIdx, i)
	}
	if len(staleIdx) == 0 {
		return nil
	}

	start := time.Now()
	var results []SpaceSyncResult
	for i, idx := range staleIdx {
		if time.Since(start) >= totalBudget {
			for _, restIdx := range staleIdx[i:] {
				spaceID := s.spaces[restIdx].SpaceID
				results = append(results, SpaceSyncResult{
					Space: spaceID,
					Err:   fmt.Errorf("cache: SyncIfStale: space %s: %w", spaceID, ErrSyncBudgetExhausted),
				})
			}
			break
		}
		sm := s.spaces[idx]
		mctx, cancel := context.WithTimeout(ctx, perMirrorTimeout)
		err := s.cloneOrFetch(mctx, sm.Dir, sm.RepoURL)
		cancel()
		if err != nil {
			results = append(results, SpaceSyncResult{
				Space: sm.SpaceID,
				Err:   fmt.Errorf("cache: SyncIfStale: space %s: %w", sm.SpaceID, err),
			})
			continue
		}
		s.refreshManifestAfterFetch(idx)
		if err := ReconcilePendingMarkers(s.cacheDir, sm.SpaceID, sm.Dir); err != nil {
			results = append(results, SpaceSyncResult{
				Space: sm.SpaceID,
				Err:   fmt.Errorf("cache: SyncIfStale: space %s: reconcile pending markers: %w", sm.SpaceID, err),
			})
			continue
		}
		results = append(results, SpaceSyncResult{Space: sm.SpaceID})
	}
	return results
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
