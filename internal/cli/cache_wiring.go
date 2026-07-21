// Real internal/cache-backed PendingMarker/CacheRemover (plan 07
// Placement decision, binding): internal/cli's own P6-defined seams
// (adapters.go) filled with the P7 primitives that back them.
// internal/cache CANNOT import internal/cli (ADR-001), so these are
// cli-layer adapters that call internal/cache's exported functions —
// the mirror image of adapters.go's own LegalityAdapter/MirrorResolver
// pattern. cmd/a2a (lead, post-wave) wires these in place of P6's
// NewNoopPendingMarker/NewNoopCacheRemover.
package cli

import (
	"context"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// CacheBackedPendingMarker is P7's real PendingMarker (the cli.
// PendingMarker seam, adapters.go): cmd_submit's per-artifact call
// (non-empty artifactID) persists a cache.PendingMarker file under
// cacheDir, the pending-merge overlay internal/cache.Store reads back
// into Item.PendingMerge. cmd_sync's own calling convention (spaceID
// set, artifactID empty, a zero WriteResult — "refresh local cache",
// adapters.go's PendingMarker doc comment) is a documented no-op here:
// the mirror's own `.git/FETCH_HEAD` mtime (already updated by the
// CloneOrFetch call that precedes this call in cmd_sync.go) IS the
// staleness signal internal/cache.Store reads (mirrorSyncAge) — no
// separate cache-side bookkeeping is needed for a bare refresh.
type CacheBackedPendingMarker struct {
	cacheDir string
	now      func() time.Time
}

// NewCacheBackedPendingMarker constructs a CacheBackedPendingMarker.
// cacheDir is `.a2a/cache/`'s path.
func NewCacheBackedPendingMarker(cacheDir string) *CacheBackedPendingMarker {
	return &CacheBackedPendingMarker{cacheDir: cacheDir, now: time.Now}
}

// MarkPending implements PendingMarker.
func (m *CacheBackedPendingMarker) MarkPending(_ context.Context, spaceID, artifactID string, result space.WriteResult) error {
	if artifactID == "" {
		return nil // cmd_sync's own bare "refresh" call — see doc comment above.
	}
	return cache.WriteMarker(m.cacheDir, spaceID, cache.PendingMarker{
		ArtifactID: artifactID,
		Branch:     result.Branch,
		PRNumber:   result.PRNumber,
		PRURL:      result.PRURL,
		CommitSHA:  result.CommitSHA,
		State:      string(result.State),
		MarkedAt:   m.now(),
	})
}

var _ PendingMarker = (*CacheBackedPendingMarker)(nil)

// CacheBackedCacheRemover is P7's real CacheRemover (the cli.
// CacheRemover seam, adapters.go): `a2a disconnect`'s cache-removal step
// (§7.2 OP-202) clears every pending marker recorded for the
// disconnected space. The read cursor's own item-state entries for that
// space's items are intentionally left as harmless orphans
// (cache.RemoveSpaceMarkers's own doc comment: self-correcting, D-001 —
// a disconnected space's items simply stop appearing in any future
// index).
type CacheBackedCacheRemover struct {
	cacheDir string
}

// NewCacheBackedCacheRemover constructs a CacheBackedCacheRemover.
// cacheDir is `.a2a/cache/`'s path.
func NewCacheBackedCacheRemover(cacheDir string) *CacheBackedCacheRemover {
	return &CacheBackedCacheRemover{cacheDir: cacheDir}
}

// RemoveSpace implements CacheRemover.
func (r *CacheBackedCacheRemover) RemoveSpace(_ context.Context, spaceID string) error {
	return cache.RemoveSpaceMarkers(r.cacheDir, spaceID)
}

var _ CacheRemover = (*CacheBackedCacheRemover)(nil)
