# §6 Hub Service (Go)

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).

## 6.1 Responsibilities

| Does | Does NOT |
|---|---|
| Mirror connected spaces (fetch on webhook + reconcile timer) | Write to any space (v1; hub-write is the designed public-mode extension, D-002) |
| Validate (V4, flag-only) and fold lifecycle states | Hold any normative artifact state (D-001) |
| Maintain indexes: per-system inbox/outbox, threads, staleness, graph | Transport runtime data payloads (NG-1) |
| Fan out notifications (statusline feed, optional chat webhooks) | Orchestrate agents (NG-2) |
| Serve the realtime dashboard (R-009) | Host project trackers (NG-4) |
| Serve read APIs for clients (remote inbox checks, graph) | |

## 6.2 API surface (v1)

All endpoints read-only, token-authenticated (§10.5), JSON. Declarative
catalog — exact wire format is an implementation detail; semantics are not.

| OP | Operation | Input | Output |
|---|---|---|---|
| OP-101 | list spaces visible to token | — | spaces + sync status/lag |
| OP-102 | inbox for a system | system, filters (state, type, priority, since) | artifacts + folded states, sorted by priority/staleness |
| OP-103 | outbox for a system | same | same, from-perspective |
| OP-104 | artifact detail | artifact ref | envelope + body + event history + folded state + validation flags |
| OP-105 | thread view | thread ID | ordered artifacts + events |
| OP-106 | exchange graph | space? / fleet | nodes (systems), edges (open/closed exchanges, contract dependencies) — feeds dashboard and local HTML |
| OP-107 | staleness report | space? | open items past needed_by / silent past SLA defaults |
| OP-108 | statusline summary | system | the compact payload the binary's statusline mode renders (7.5) |
| OP-110 | validation flags | space | current V4 violations |
| OP-111 | health/metrics | — | sync lag per space, last webhook, fold errors |

Server-Sent Events stream mirrors OP-106/OP-108 updates for the dashboard's
realtime view. Subscriptions (which events notify which channel) are hub
config (`hub.yaml`), operator-managed in v1 — not per-user state.

## 6.3 Ingest pipeline

```
GitHub webhook (push on space repo)
  → verify webhook signature
  → git fetch (mirror clone)
  → diff changed paths
  → validation engine (V4, flag-only)
  → fold affected exchanges (same engine as binary, §3.5)
  → update indexes (SQLite)
  → notification fan-out (per hub.yaml routes)
```

- **Reconcile timer** (default 5 min) runs the same pipeline from `git
  fetch`, catching missed webhooks; pipeline is idempotent by commit SHA
  (processing the same SHA twice is a no-op).
- Webhook-triggered fetches are debounced/rate-limited per space (webhook
  floods and force-push loops cannot pin the hub in re-index — DoS
  hardening, T-7); per-push ingest content size is capped.
- Force-push / non-fast-forward on a space: full re-index of that space +
  operator alert — UNLESS a sanctioned redaction was announced beforehand
  (10.7/CC-098), which the operator confirms.
- Fold errors and V4 violations never stop indexing; they become flags.

## 6.4 Storage

SQLite, single file, on the VPS (D-012). Everything is derived (4.1):
tables for artifacts index, folded states, events, refs graph, watermarks,
notification log. `hub rebuild` drops and replays from git mirrors.
RATIONALE: one operator, ~10 systems, low write rate — a DB server would be
overengineering (R-013); SQLite + rebuildability beats backup discipline.
A daily SQLite snapshot is kept only to speed recovery, never for truth.

## 6.5 Notifications

| Channel | Payload | Default routing |
|---|---|---|
| statusline feed (pull: OP-108) | counts + top item per system | always on |
| chat webhook (push: generic JSON POST, adapters for Telegram first) | event summaries | operator-configured per space: gate-relevant events (G1–G5), p1 items, validation flags |
| space-CI fallback (optional template feature: a GitHub Actions step POSTing to Telegram on push) | bare push notice | OFF by default; a degraded-mode supplement for when the hub is down — the hub stays the primary router (dedup + noise budget live there, R-004) |
| none | — | everything else (noise budget: §11.3) |

Delivery is at-least-once with per-channel internal cursors (an
implementation detail of this section — there is no public events-since API
in v1; one can be added later without migration, R-012); duplicate
suppression by event ULID at the receiver side where possible.

## 6.6 Deployment & ops (R-020)

- Single static Go binary (`a2ahub-server`), systemd unit, on the existing
  Timeweb VPS; TLS terminated by the VPS's existing reverse proxy (or Caddy
  if none). Config: `hub.yaml` (spaces + credentials refs + notification
  routes + tokens).
- Read credentials to spaces: GitHub fine-grained PAT, read-only, per §10.5.
- Upgrades: replace binary, restart; DB schema migrations run on start;
  worst case `hub rebuild`.
- Monitoring: OP-111 scraped by anything (even a cron + chat webhook);
  minimum alerts: webhook silence > 1h while commits exist (reconcile
  detects), fold error count growth, disk.

## 6.7 Failure modes (normative behaviors; tests in §13)

| # | Failure | Behavior |
|---|---|---|
| F-1 | missed webhook | reconcile timer catches within 5 min; no data loss (git is SSOT) |
| F-2 | duplicate webhook | idempotent by SHA; no double notifications (dedup by event ULID) |
| F-3 | hub down | agents fall back to direct git sync (§8 watch loop); on restart, hub catches up from git; queued notifications collapse into current-state summaries (no stale flood) |
| F-4 | DB corruption/loss | `hub rebuild` from git mirrors; snapshot only accelerates it |
| F-5 | space repo unreachable | space marked stale on dashboard + OP-111; other spaces unaffected |
| F-6 | invalid artifact merged despite V3 (e.g. CI bypass) | V4 flags it; dashboard + notification; never crashes fold (3.5 rule 2) |
| F-7 | force-push on space | full re-index + operator alert |
| F-8 | webhook signature invalid | reject + log + alert (possible probe) |
| F-9 | notification channel down | cursor holds; redelivery on recovery; statusline unaffected (pull) |
| F-10 | required-check (V3) pipeline outage | ALL space writes freeze (PRs can't merge) — loud by design; `a2a doctor` diagnoses; operator MAY temporarily lift the required check per runbook §9.4 (logged, followed by an announcement to the space) |
