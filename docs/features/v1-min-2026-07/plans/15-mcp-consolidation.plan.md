---
slug: v1-min-2026-07
phase: P15
spec: ../specs/15-mcp-consolidation.md
wave: 8
status: closed  # wave 8: shipped 6 grouped tools, lead-verified
---

# Phase plan — P15 MCP surface consolidation (~6 capability-grouped tools)

## Goal

Collapse the shipped 33-tool 1:1 MCP registry into ~6 capability-grouped typed
tools (read/write split preserved), each dispatching a closed `action`/`view`
enum to the EXISTING per-verb handlers, and reparameterize the P14 parity +
equivalence suites from tool-level to `(tool, action)` capability-level. §7.7
amended (operator-authorized 2026-07-22).

## Placement / architecture decisions (lead, binding — advisor-reviewed)

- **Dispatch layer only — handlers untouched.** The grouped tools add a thin
  handler that reads `action`/`view` and delegates to the existing
  `newInboxHandler`/`newLifecycleHandler`/`newRespondHandler`/… No handler
  business logic is rewritten. This keeps the funnel path — and thus the
  byte-equivalence — identical per verb.
- **Target 6, release valve 7.** `a2a_read`, `a2a_new`, `a2a_submit`,
  `a2a_lifecycle`, `a2a_exchange`, `a2a_contract`. If folding `a2a_respond`
  into `a2a_exchange` is correctness-harmful (its result+refs+fields shape),
  keep `a2a_respond` split → 7. Implementor call, recorded in spec §11.
- **Enums derived, not re-typed.** `a2a_lifecycle` actions ← `LifecycleVerbTable`;
  `a2a_contract` actions ← `cli.ContractSubcommands()`.
- **Capability parity replaces the bijection.** Every designated CLI verb ↔
  exactly one `(tool, action)`, both directions, decoy tests carried over.
- **`internal/mcp` still never imports `internal/cli`** (ADR-001). The parity
  test lives in `cmd/a2a` (sees both), as P14 established.
- **Regenerate `commands.md`** in the same change (MCP section shrinks) or
  skill-drift reds; grep `loops.md`/skill for removed tool names.

## Allowlist (repo-relative) — wave 8

- `internal/mcp/tools.go`                 # BuildRegistry → 6 grouped registrations
- `internal/mcp/tools_dispatch.go`        # new: action/view dispatch handlers (thin)
- `internal/mcp/tools_dispatch_test.go`   # new: dispatch-error + enum-coverage tests
- `internal/mcp/*_test.go`                # update any test that constructed a removed tool by name
- `cmd/a2a/mcp_parity_test.go`            # bijection → capability-parity by (tool, action)
- `cmd/a2a/mcp_equivalence_test.go`       # per-(tool, action) byte-equivalence
- `skill/a2ahub/reference/commands.md`    # regenerated from the grouped registry

## Lead-reserved / off-limits deltas

- `internal/mcp` per-verb handler bodies (newInboxHandler, newLifecycleHandler,
  newRespondHandler, newContract*Handler, …) — REUSED, not modified.
- `internal/cli/**`, `internal/fold/**`, `internal/space/**` — no change.
- `cmd/a2a/catalog.go`, `cmd/a2a/wire.go` — unaffected (catalog reads the
  registry; the `mcp` verb wiring is unchanged).
- `.github/workflows/ci.yml`, `go.mod` — untouched.

## Acceptance (spec 15 §8)

- [ ] Registry exposes ~6 grouped tools; read/write split preserved; tools/list
      weight drops materially from the ~2.1k-token P14 baseline.
- [ ] Per-(tool, action) byte-equivalence green under -race (the bug-farm).
- [ ] Capability parity green both directions (decoy tests intact).
- [ ] commands.md regenerated; skill-drift green; no stale tool name in skill.
- [ ] `make check` green.

## Brief

(dispatched agent brief — see §Phase log wave 8)

## Phase log

### Wave 8 — 2026-07-22

- Agent: coder opus/high, 1 disjoint code-wave + scout propagate probe.
- Files / Commits: internal/mcp/tools.go + tools_dispatch.go (new) + tools_dispatch_test.go (new) + tools_test.go; cmd/a2a/mcp_parity_test.go + mcp_equivalence_test.go; skill/a2ahub/reference/commands.md regen / a54260a.
- Verify (lead): footprint = allowlist exactly, NO handler body touched (read tools_dispatch.go: every dispatch is `return h(ctx, args)` transparent passthrough). `make check` exit 0. Read BuildRegistry — 6 grouped Register calls, groupedSchema embeds action/view enum + field union. Token weight independently re-measured: 6 tools, 2803 B / ~700 tok (P14 8481/~2120) = −67%. Equivalence bug-farm: all 15 lifecycle + respond/verify/dispute/note + 6 contract byte-identical (incl. content-derived id match in TestEquivRespond). Capability-parity + decoy tests green. commands.md MCP section = 6 grouped tools; skill grep clean of removed names.
- Deviations + downstream: respond folded → 6 (valve not needed — passthrough can't collide). ContractActions re-typed in mcp (ADR-001), reconciled by parity gate. decline `RequireReasonCode` is a pre-existing dead field (handler off-limits). 3 doc write-backs applied: spec 14 §11 superseded-note, spec 15 §11 valve-decision + §6 reason_code correction.
- Epic-direction reconcile: still-serves — §7.7 amended (operator-authorized), R-018 "no MCP-only capability" preserved (capability parity), core/funnel untouched.
- Notes: P15 → done. audit: open (hand-verified; rides S8 epic-final).

## Deferred / follow-ups

- (none)
