package release

import (
	"context"
	"time"
)

// NewChecker builds the T3 background update-check refresher: a func that,
// invoked, asks source for the latest release and writes the result to the
// cache at cachePath. now is injected (the same Clock DI precedent
// internal/cache uses) so tests never depend on a wall clock.
//
// The returned func closes over Source ONLY — it has no reference to any
// Verifier, Download, or Swap value, so it is structurally unable to reach
// the swap path (D-021: proactive display, explicit act). Both source.Latest
// and WriteCheck failures are swallowed: this is the same "detached,
// recover-guarded, cache-for-next-render" background pattern
// Store.triggerRefreshIfStale already uses (spec 19 §5) — a failed
// best-effort refresh must never surface noise, it just leaves the
// existing cache (or its absence) for the next render to read.
func NewChecker(source Source, cachePath string, now func() time.Time) func(context.Context) {
	return func(ctx context.Context) {
		rel, err := source.Latest(ctx)
		if err != nil {
			return
		}
		_ = WriteCheck(cachePath, CheckState{
			CheckedAt: now(),
			Latest:    rel.Version,
			Source:    source.Name(),
		})
	}
}
