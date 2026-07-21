// Package cache implements the P7 read surface (spec
// docs/features/v1-min-2026-07/specs/07-read-surface-statusline.md): it
// composes internal/fold over the committed history of every connected
// space's local mirror clone (internal/space) into the inbox/outbox
// query sets §4.2/§7.2 defines, plus per-system read cursors, the
// pending-merge overlay, and mirror staleness/sync-age.
//
// ADR-001 import row: internal/artifact, internal/fold, internal/space
// ONLY. This package never imports internal/validate (the V5
// registry-code lookup is internal/cli/cmd_show.go's job — it maps the
// digest/staleness FACTS this package returns to a registry code) and
// never imports internal/cli (P6's PendingMarker/CacheRemover interfaces
// live there; internal/cli/cache_wiring.go adapts this package's
// primitives to satisfy them, not the reverse).
//
// This package is validate-free and mostly I/O-light: it reads mirror
// working-tree files directly (fast, no git subprocess) and uses exactly
// one `git log` invocation per connected mirror to recover first-parent
// commit order (D-017) — never a per-file git call. Its own on-disk
// cache root (cursor.json, pending markers, both under the caller's
// cacheDir, i.e. `.a2a/cache/`) is machine-local, gitignored and fully
// rebuildable (D-001): losing it only costs a "read as new" on the next
// inbox run, never data loss.
//
// Every entry point takes a Clock (never calls time.Now() internally) so
// staleness/TTL/SLA math is deterministic under test.
package cache
