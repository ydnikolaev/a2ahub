---
slug: v1-min-2026-07
phase: P7
spec: ../specs/07-read-surface-statusline.md
wave: 4
status: verified
---

# Phase plan — P7 Read surface: cache, inbox/outbox/show/thread/search, statusline

## Goal

Ship `internal/cache` (folded views over the mirror, per-system read cursors,
staleness/TTL, pending-merge overlay) and the six read verbs
inbox/outbox/show/thread/search/statusline as `internal/cli` command files,
plus the real cache-backed PendingMarker/CacheRemover impls that fill P6's
seams. Spec 07 AC rows 1–10.

## Allowlist (repo-relative)

- `internal/cache/**`
- `internal/cli/cmd_inbox.go`, `cmd_outbox.go`, `cmd_show.go`,
  `cmd_thread.go`, `cmd_search.go`, `cmd_statusline.go`
- `internal/cli/cache_wiring.go` (NEW — real cache-backed PendingMarker +
  CacheRemover, the cli-layer adapters over internal/cache; see placement)

## Lead-reserved / off-limits deltas

- `internal/cli/cli.go` (lead seam — build against it, never edit),
  `internal/cli/adapters.go` + P6/P8 verb files (never touch),
  `cmd/a2a/**` (lead wires dispatch after the wave), `go.mod`,
  `internal/fold`/`internal/validate`/`internal/space`/`internal/artifact`
  (consume, never edit — cache imports artifact/fold/space ONLY per ADR-001).

## Placement decisions (lead, binding)

- **`internal/cache` is validate-free** (ADR-001 import row: artifact/fold/
  space only). It supplies folded state + staleness/digest-mismatch FACTS;
  the V5 registry-code lookup happens in `cmd_show.go` (which may import
  internal/validate) — cache never mints a code.
- **The parent+response event-gathering gap** (wave-3 backlog): to fold a
  parent exchange correctly, cache must gather events where subject==parent
  AND events where subject is a response attached via that parent's respond
  events (verify/dispute carry the response ID as subject). This composition
  is CACHE's job — do it inside internal/cache when reading the mirror;
  fold stays pure. A naive subject==X-only query silently misses them.
- **Real PendingMarker/CacheRemover** (wave-3 backlog): the interfaces live
  in internal/cli (P6). internal/cache CANNOT import internal/cli (ADR-001),
  so the real impls are cli-layer adapters in `internal/cli/cache_wiring.go`
  that call internal/cache primitives (marker path/format, cursor store).
  cmd/a2a wires them in place of P6's no-ops (lead, post-wave).
- **Statusline**: cache-read ONLY, <100ms, zero-noise; TTL-triggered
  detached refresh is `git fetch` (spawned per-invocation, owned goroutine
  with recover per rails — NEVER breaks the caller's prompt); NO hub client
  symbol anywhere (AC row 9). Exit codes 0/10/11 by severity.
- Each verb is a `cli.Command` (Name/Synopsis/Run) + constructor; no shared
  package-level state; `--json` output is the guaranteed shape (snake_case
  json tags, matching the P6/validate convention).

## Brief

```
Stack: Go 1.26 stdlib (encoding/json, time for TTL/staleness). NO new deps.
internal/cache imports internal/artifact, internal/fold, internal/space ONLY
(ADR-001 — gated by go list -deps). All file paths REPO-RELATIVE.

## Goal
Implement internal/cache + the six read verbs + the real cache-backed
PendingMarker/CacheRemover per
docs/features/v1-min-2026-07/specs/07-read-surface-statusline.md — read END
TO END first (T1 catalog, the VERBATIM --actionable/--attention/statusline
definitions, §6 tests, §8 ACs, §5 reuse), then the Placement decisions in
docs/features/v1-min-2026-07/plans/07-read-surface-statusline.plan.md
(BINDING).

## Context (read in order)
- The lead seam internal/cli/cli.go (cli.Command, cli.IO) — build against.
- Root AGENTS.md rails (concurrency: owned goroutine + recover for the
  statusline refresh; error flow; testing rails; in-memory durable state is
  forbidden — .a2a/cache is disposable, rebuildable from git per D-001).
- Exported APIs you consume (read source, reuse — never re-implement):
  internal/fold (Fold, Apply, Kind/State/Event/Envelope/FoldResult,
  MembershipView, the flag/ack/response accessors), internal/space (Layout,
  LoadProjectConfig, connected spaces, mirror paths, CloneOrFetch),
  internal/artifact (ID parse, Digest for the V5 check), internal/validate
  (ONLY from cmd_show.go, for the V5 registry-code lookup).
- P6's PendingMarker/CacheRemover interfaces in internal/cli/adapters.go
  (read them — your real impls satisfy the same interfaces).
- Plan corpus: 07-client.md §7.2/§7.5, 04-topology.md §4.2/§4.3
  (federation), 05-schemas.md §5.5 (V5 row), 17-decisions.md D-020/D-021.

## Allowed files — REPO-RELATIVE ONLY
- internal/cache/**
- internal/cli/cmd_inbox.go, cmd_outbox.go, cmd_show.go, cmd_thread.go,
  cmd_search.go, cmd_statusline.go
- internal/cli/cache_wiring.go (real PendingMarker + CacheRemover)

## Off-limits (NEVER touch)
- internal/cli/cli.go, adapters.go, any P6 verb file (cmd_init/new/submit/
  sync), any P8 verb file (cmd_lifecycle/cmd_contract) — a parallel agent
  owns P8. cmd/a2a/**, go.mod, and every internal/* core package source
  (import them, never edit).

## What to do
1. internal/cache: a package that, given the connected-space mirrors +
   config, computes: folded state per artifact (compose internal/fold over
   the mirror's committed events — INCLUDING the parent+response gathering
   per the Placement decision), the inbox set (VERBATIM --actionable union
   of 5), the outbox set (VERBATIM --attention union of 4), per-system read
   cursors under .a2a/cache/ (advance ONLY on explicit inbox read),
   staleness/sync-age (mirror fetch age vs TTL), and the pending-merge
   overlay (read the raw marker file P6's submit writes). Federated across
   ALL connected spaces (§4.3), each item attributable to its space.
   Cursor + marker on-disk formats are yours (machine-local, gitignored,
   rebuildable). Provide the primitives cache_wiring.go's markers need.
2. The six verbs (each a cli.Command):
   - inbox (--actionable, --json, [filters]): JSON array guaranteed;
     advances the read cursor on run.
   - outbox (--attention, --json, [filters]).
   - show <ref>: body + folded state + events + V5 warning (cmd_show.go maps
     cache's digest/staleness FACT to the internal/validate registry code);
     ref-not-found → clear error, no crash.
   - thread <thread-id>: ordered conversation view.
   - search <query> [--type --space --state]: ranked local-cache matches;
     zero hits → empty result, not error.
   - contracts [--provider]: known contracts from cache. (contract diff is
     P8 — do NOT implement it here.)
   - statusline: <=1 line, cache-read only, <100ms, zero-noise (empty+exit0
     when nothing actionable / no config CC-092); exit 0/10/11 by severity;
     TTL-exceeded → spawn ONE detached git-fetch refresh (owned goroutine +
     recover), render still reads current cache. NO hub symbol.
3. internal/cli/cache_wiring.go: real PendingMarker + CacheRemover
   satisfying P6's interfaces, backed by internal/cache primitives.
4. Sanity: gofmt; go vet ./internal/cache/... ./internal/cli/...;
   go test ./internal/cache/... ./internal/cli/... -race -count=1;
   go list -deps ./internal/cache/... | grep a2ahub → artifact/fold/space
   only (AC row 9: also grep the statusline path for any hub RPC symbol —
   there must be none).

## Constraints
- cli.Command.Run returns exit code; NEVER os.Exit / real os.Std* — only
  cli.IO. --json output uses snake_case json tags.
- Copy the P1 error idiom; log-or-return; the statusline refresh goroutine
  is the ONE concurrency case — own it (errgroup/WaitGroup + defer recover),
  it must never panic into the caller's prompt.
- No in-memory state that must survive the process (D-001); no time.Now()
  buried in cache logic — take a clock/now for testability.
- t.Parallel() everywhere (or // reason:); coverage floor 70%.

## DO NOT
- DO NOT commit / run git against THIS repo / run make check / repo-wide
  go build|test. Scope to internal/cache + your cli files.
- DO NOT import internal/validate from internal/cache (only cmd_show.go may).
- DO NOT touch cli.go, adapters.go, P6/P8 verb files, cmd/a2a, or core pkgs.

## Acceptance
- Spec 07 §8 rows 1–10, each with a named go test target (T3 fixtures:
  per-condition inbox/outbox items, federation 2-space, V5 stale ref,
  cursor-persistence double-run, statusline severity+silence+<100ms).

## Report back
- Files, tests, scoped output; the cache package's exported surface + the
  cache_wiring adapter constructors (cmd/a2a wires them — feeds the probe).
- Deviations — REQUIRED (esp. the parent+response gather approach, any fold
  accessor you needed that doesn't exist, the marker/cursor formats).
- Anything skipped + why.
```

## Acceptance

- [ ] Spec 07 §8 rows 1–10 green (lead re-runs scoped tests + the statusline
      live check in the integration pass).

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- cmd/a2a wiring of the six verbs + swapping P6's no-op markers for the real
  cache-backed ones = lead, post-wave.
- Hub-backed refresh (OP-108) = v2.
