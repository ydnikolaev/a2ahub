# Read surface — cache, inbox/outbox/show/thread/search, statusline — Specification

> Unified spec template projection. Track: cli only (§T1); other track sections deleted per template rule.

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Plan**: [plans/07-read-surface-statusline.plan.md](../plans/07-read-surface-statusline.plan.md)
**Footprint**: `internal/cache/` (new package — folded views, per-system read
cursors, staleness), `internal/cli/cmd_inbox.go`, `internal/cli/cmd_outbox.go`,
`internal/cli/cmd_show.go`, `internal/cli/cmd_thread.go`,
`internal/cli/cmd_search.go`, `internal/cli/cmd_statusline.go`, `cmd/a2a`
wiring lines only (register these six verbs) — no other `cmd/a2a` file.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-301 | As an agent (IA), I operate the whole exchange from one binary with idempotent commands — read verbs work with no hub configured, straight off the git mirror. |
| US-601 | As an agent in Claude Code (IA), I see pending exchange signals without doing anything — a fast, cache-only statusline that stays silent when there is nothing to say. |
| US-P7-1 | As an agent, I run `a2a inbox --actionable` and get exactly the items the plan's normative set defines, computed the same way every time. |
| US-P7-2 | As an agent, I run `a2a outbox --attention` and see my own open items that need a second look, without re-deriving the rule myself. |
| US-P7-3 | As an agent, I run `a2a show <ref>` and get the artifact, its folded state, its event history, and any V5 staleness/digest warning in one place. |
| US-P7-4 | As an agent, I run `a2a search`/`a2a contracts` against my local cache and get useful results with zero network, zero hub. |
| US-P7-5 | As a human wiring my own statusline, `a2a statusline` never blocks my prompt and never touches my harness config on its own. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Command surface & statusline contract | [07-client.md §7.2, §7.5](../../../the-plan/plan/07-client.md) | OP-207..OP-210, OP-215, OP-221; `--actionable`/`--attention` definitions are normative, copy verbatim into implementation, do not reparaphrase |
| Inbox-is-a-query | [04-topology.md §4.2](../../../the-plan/plan/04-topology.md) | no spool copies; inbox/outbox are computed joins over artifacts + folded state, never a folder |
| Package boundaries | [ADR-001](../../../decisions.md) | `internal/cache` imports `artifact`, `fold`, `space` only; never `cli`/`mcp` reads it back |
| Validation matrix (V5 row) | [05-schemas.md §5.5](../../../the-plan/plan/05-schemas.md) | `show`/MCP read triggers V5: digest check of pinned refs + staleness, warning only, never blocks |
| Statusline severity model | [17-decisions.md D-020, D-021](../../../the-plan/plan/17-decisions.md) | pull, cache-first, zero-noise; embedding is advisory, never invasive |
| v1-min statusline scope cut | [15-rollout.md](../../../the-plan/plan/15-rollout.md) | statusline ships on the git-fallback path only; hub refresh (OP-108) is v2 |

---

## Track-specific contract (§T1 — cli)

### T1. CLI surface

| Command | Flags | Input | Output | Notes |
|---------|-------|-------|--------|-------|
| `a2a inbox` | `[filters]`, `--actionable`, `--json` | none (reads config + mirrors + cache) | JSON array guaranteed (OP-207); text rendering is a projection of the same JSON | computed across ALL connected spaces (§4.3 federation); works offline from mirror+cache with sync-age flagged when stale |
| `a2a outbox` | `[filters]`, `--attention`, `--json` | none | JSON array, own open items + states | (OP-208) |
| `a2a show` | `<ref>` | artifact ref (ID or `space:id[@version]`) | artifact body + folded state + event list + validation flags (incl. V5 digest/staleness warning) | (OP-209); ref not found → clear not-found error, no crash |
| `a2a thread` | `<thread-id>` | thread ID | ordered conversation view (all artifacts on the thread, folded states) | (OP-210) |
| `a2a search` | `<query> [--type --space --state]` | free-text query + filters | ranked match list over local cache | (OP-221 first clause); hub-less by design |
| `a2a contracts` | `[--provider <sys>]` | optional filter | list of known contracts (provider, version, state) from local cache | (OP-221 second clause); `a2a contract diff <id> <v1> <v2>` is OP-221's third clause but ships under **P8** (contract lifecycle phase) — out of this phase's footprint, not implemented here |
| `a2a statusline` | (none; reads config only) | none | at most one line + exit code | (OP-215/§7.5); reads cache ONLY, <100 ms budget; see §7.5 contract below |

**`--actionable` (OP-207, normative, copy verbatim):**

> `--actionable` is normatively defined: {addressed to me with no ack by me}
> ∪ {responded awaiting my verify/close} ∪ {disputed toward me} ∪ {p1 or
> blocking, any open state} ∪ {gate pending on me}. "New" is computed against
> a per-system read cursor in `.a2a/cache/`, advanced by explicit inbox
> reads

**`--attention` (OP-208, normative, copy verbatim):**

> `--attention` = {folded state changed since read cursor} ∪ {declined} ∪
> {disputed} ∪ {stale: no event for the space's staleness SLA (space.yaml,
> default 7 days) or `needed_by` passed}

**Statusline contract (§7.5, quoted verbatim, normative):**

> `a2a statusline` prints at most one line, designed for embedding: counts +
> the single most urgent item, e.g. `a2a: 2 new · 1 p1 XW-seomatrix-… "todo
> feed pagination" · 1 stale`. Prints nothing (exit 0) when there is nothing
> actionable — zero-noise rule.
>
> Exit code communicates severity (0 quiet / 10 items pending / 11 p1 or
> gate pending) so harnesses can style without parsing.
>
> Refresh model (explicit, single-owner): a statusline render READS CACHE
> ONLY (<100 ms budget, never blocks the prompt). If cache age > TTL
> (default 5 min), the render spawns one detached background refresh — hub
> OP-108 when configured (~seconds fresh), else git fetch — whose result
> lands in the cache for the NEXT render. Worst-case staleness = TTL + one
> render interval; S-3/AC-502.1 numbers are defined against this.
>
> **Integration is advisory, never invasive (D-021):** every user owns their
> statusline. `a2a statusline` is a data source designed for *embedding into*
> an existing statusline (one segment among the user's own). During
> onboarding the agent/`a2a doctor` PROPOSES the addition and shows the
> snippet; it MUST NOT replace the user's statusline or edit their harness
> config without explicit consent. Declining is fully supported — the 8.1
> session-start checklist remains the guaranteed floor.

**v1-min scope cut (D-030, §15):**

> hub service (§6 entirely — statusline runs on the git-fallback path, 7.5,
> which already satisfies S-3 at the 5-min TTL)

So the background refresh this phase implements is `git fetch` against the
connected space mirrors — the OP-108 hub-refresh branch quoted above is v2,
not built here; do not stub a hub client. Integration remains advisory only
(D-021, quoted above): this phase ships the data source (`a2a statusline`)
and nothing that writes to a user's harness config — the onboarding
snippet/proposal flow (`a2a doctor` PROPOSES) is out of this phase's
footprint.

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `internal/fold` (P4) — the ONE folded-state computation; `internal/cache`
      composes over it, never reimplements transition logic.
- [ ] `internal/space` (P5) — mirror clone locations, space config; `cache`
      reads mirrors, it does not manage clones or the write funnel.
- [ ] `internal/artifact` (P1) — ID parsing, digest computation (D-029);
      reuse for the V5 digest check in `show`, do not recompute hashing.
- [ ] `.a2a/cache/` on-disk layout (§7.4) is the machine-level cache root
      (gitignored); the pending-merge marker written by OP-205 submit (P6,
      via `internal/space`'s write funnel, which does not import `cache`)
      is a raw file this package's fold-composition step must read and
      overlay onto folded state — do not require P6 to import `internal/cache`.
- [ ] Machine-readable error/warning codes come from the single registry in
      `internal/validate` (ADR-001) — do not mint new ad hoc codes for V5
      staleness/digest warnings surfaced by `show`. `internal/cache` stays
      validate-free per its ADR-001 import row (`artifact`, `fold`, `space`
      only): it supplies folded state plus staleness/digest-mismatch facts
      only, never a registry code. The V5 code lookup itself happens in
      `internal/cli` (`cmd_show.go`), which is allowed to import
      `internal/validate` and maps `cache`'s facts to the registry code
      before rendering.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| `internal/cache` folded views | inbox/outbox computed sets match §7.2 set definitions exactly | empty cache, single space, N connected spaces (federation, §4.3) |
| `internal/cache` read cursors | cursor advances only on explicit inbox read; "new" recomputes correctly after advance | cursor missing (first run), cursor from a stale schema version |
| `internal/cache` staleness | sync-age flag set correctly when mirror fetch age > TTL | mirror never synced, mirror mid-fetch |
| `a2a inbox --actionable` | each of the 5 unioned conditions independently and in combination | item matching zero conditions excluded; item matching 2+ not double-counted |
| `a2a outbox --attention` | each of the 4 unioned conditions independently | `needed_by` exactly at boundary; SLA default (7 days) vs `space.yaml` override |
| `a2a show` | folded state + events + V5 warning rendering | pinned ref digest mismatch, stale pinned ref, ref not found |
| `a2a search` / `a2a contracts` | filter combinations, empty local cache | query with zero hits (empty result, not error) |
| `a2a statusline` | zero-noise silence, severity exit codes, <100 ms render, TTL-triggered detached refresh via git fetch | no config/space (CC-092: silent exit 0); p1/blocking item present (exit 11); TTL exceeded mid-render (render still reads stale cache, refresh detached) |
| OP-301.3 offline path | inbox/submit/sync all function via direct git with no hub configured | mirror present but never fetched since connect |

## 7. Schema / contract delta

No product JSON Schema changes (envelope/event/manifest/consumes are frozen
by P2). This phase's only cross-boundary contract is the on-disk cache
format under `.a2a/cache/` (machine-level, never committed, not a product
schema) and the CLI JSON output shape for `inbox`/`outbox` (guaranteed by
OP-207/OP-208, T1 `cli` track — described in the T1 table above, not restated
here as YAML).

## 8. Acceptance criteria

> AC rows below are copied verbatim from `docs/the-plan/plan/14-us-ac.md`;
> implementors do not modify them. Extra phase-local rows carry US "—".

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-301 | AC-301.3 Given no hub configured, when I run inbox/submit/sync, then everything works via direct git. [CC-042, E2E-7] | `a2a inbox`/`a2a outbox`/`a2a show`/`a2a thread`/`a2a search` against a connected space mirror with no hub config present; all return correct data via git fetch only |
| 2 | US-601 | AC-601.1 Given nothing actionable, then statusline prints nothing, exit 0. [CC-092] | `a2a statusline` against a cache with zero matching items (or no config/space at all, CC-092) → empty stdout, exit code 0 |
| 3 | US-601 | AC-601.2 Given a p1/blocking inbound, then the line + severity exit code reflect it ≤ freshness budget; render <100 ms from cache. [13.4] | seed cache with a p1/blocking inbound item; `a2a statusline` → exit 11, line names the item; wall-clock render time asserted <100 ms in T3 perf test |
| 4 | — | `a2a inbox --actionable` returns exactly the union of the 5 OP-207 conditions, no more, no fewer, over a fixture space with one item per condition plus one non-matching control item | T3 fixture: 6 items, assert set membership item-by-item |
| 5 | — | `a2a outbox --attention` returns exactly the union of the 4 OP-208 conditions | T3 fixture: 5 items (4 matching + 1 control), assert set membership |
| 6 | — | `a2a show` surfaces a V5 digest-mismatch or staleness warning as a non-blocking flag, never a hard error | T3 fixture: pinned ref with stale/mismatched digest → `show` succeeds, warning flag present |
| 7 | — | `a2a inbox`/`a2a outbox` JSON output is stable and parses under the documented shape (OP-207/OP-208 "JSON output guaranteed") | `a2a inbox --json \| jq .` round-trips without error in T3 |
| 8 | — | Federated inbox: a system connected to 2 spaces sees one aggregated inbox, each item attributable to its origin space | T3 fixture: 2 space mirrors, 1 item each addressed to the system, `a2a inbox` returns 2 items with distinct `space` fields |
| 9 | — | Statusline background refresh is git-fetch only in v1-min (no hub client code path reachable) | code review / grep: no hub RPC symbol referenced from `cmd_statusline.go` or the refresh path in `internal/cache` |
| 10 | — | Read cursor persistence: a second `a2a inbox` run against an unchanged cache does not re-flag already-read items as "new" | T3: run inbox twice, assert cursor advanced and "new" count is 0 on the second run |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | v2's hub-backed refresh (OP-108) slots into the same `internal/cache` refresh interface this phase defines for git-fetch — the statusline render path (cache read, <100 ms) is unchanged; only the background-refresh strategy gains a second implementation (D-030: zero migrations). |
| Coupling | Soft — `internal/cache` depends only on `artifact`/`fold`/`space` outputs (IDs, folded state, mirror paths), never on `cli` internals; CLI verbs are thin callers. |
| Migration path | low — on-disk cache format is machine-local and gitignored; a v2 format change is a local cache rebuild, not a migration (D-012-style rebuildability). |
| Roadmap conflicts | P8 (lifecycle + contract verbs) writes events this phase's fold-composition reads; P13 (skill) documents these verbs' usage; P14 (MCP) wraps `a2a_inbox`/`a2a_outbox`/`a2a_show`/`a2a_thread`/`a2a_search`/`a2a_contracts` 1:1 over this same package (7.7 parity) — no new read logic there. |

## 10. Implementor entry point

Execute as one wave of the v1-min epic, blocked_by P6 per `tracker.yaml`
(needs `internal/space` mirrors and the author verbs' on-disk conventions
live). TDD default: red (fixture space + expected set membership) → green
→ refactor. Framework-first: `encoding/json` for output, stdlib `time` for
TTL/staleness math, no new dependencies. Log-or-return per
[.claude/rules/go-conventions.md](../../../../.claude/rules/go-conventions.md).
Full loop: [docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. When shipped reality deviates from this spec, record it here
> AND amend any downstream spec.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — from wave 4: shipped-reality deltas
- **"reads cache ONLY" is more precisely "reads the local mirror + cache
  ONLY, no network in the render path".** The statusline render composes
  fold over the on-disk mirror (not a separate pre-baked cache blob); the
  <100ms/no-block/zero-noise/severity-exit-code contract holds exactly. The
  TTL-triggered detached `git fetch` is the only network, spawned in an
  owned goroutine (recover) whose result lands for the NEXT render — never
  the current one. Wording of §T1/§7.5 lines "reads cache ONLY" reads as if
  a distinct cache store is required; the shipped design reads mirror+cache.
- **`internal/cache` transitively pulls `internal/host`** via
  `internal/space` (space's write funnel imports host). No file in
  internal/cache imports host directly — the ADR-001 rule (cache's own
  imports = artifact/fold/space) holds; the raw `go list -deps` showing
  `host` is the transitive-via-space closure, not a violation.

### 2026-07-21 — from wave 4 audit-fix — interpretation calls recorded

> These P7 reading decisions shipped in code comments citing a nonexistent
> "Deviations report"; recorded here (the real referent) per the audit. The
> `internal/cache` code comments still say "Deviations report" — a LOW
> wording cleanup backlogged, not a behavior change.

- **OP-207 `--actionable` condition 4 ("any open state I'm a party to")** is
  read as: applies to any item where `me` is a party (`from` OR `to`), not
  scoped by addressed-to-me alone. Condition 2 keys off `from == me` (owner
  awaiting own verify/close). `actionableReasons` evaluates all five
  normative conditions unscoped by `addressedToMe`.
- **D-017 membership is resolved once per space against the manifest**, not
  per-commit — the cache reads `fold.MembershipView` from the current
  manifest rather than replaying membership at each historical commit. A
  known simplification: a mid-history membership change is not retro-applied
  to old commits' authorization in the read view (the write funnel + V2/V3
  enforce authorization at write time regardless).

### 2026-07-21 — from coherence audit (pre-implementation)
- Clarified §5 cache-package bullet: `internal/cache` stays validate-free
  (its ADR-001 import row is `artifact`/`fold`/`space` only); the V5
  digest/staleness registry-code lookup happens in `internal/cli`
  (`cmd_show.go`), which is allowed to import `internal/validate` — `cache`
  only supplies folded state and staleness/digest-mismatch facts.
