# P15 — MCP surface consolidation (~6 capability-grouped typed tools) — Specification

> Reworks the shipped P14 MCP surface. The plan §7.7 is amended (operator-
> authorized 2026-07-22, ydnikolaev) from a 1:1 verb→tool mapping to
> capability-grouped tools; this spec cites that amendment, it does not
> re-decide it.

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-22  ·  **Owner**: yura
**Plan**: [plans/15-mcp-consolidation.plan.md](../plans/15-mcp-consolidation.plan.md)
**Footprint**: `internal/mcp/**` (registration + new action/view dispatch
handlers over the EXISTING per-verb handlers — no handler business logic
changes), `cmd/a2a/mcp_parity_test.go` + `cmd/a2a/mcp_equivalence_test.go`
(reparameterized by `(tool, action)`), `cmd/a2a/catalog.go` is unaffected but
`skill/a2ahub/reference/commands.md` is regenerated (its MCP section shrinks).
**May import**: `internal/cache`, `internal/fold`, `internal/space` (as P14
already does) — no new dependency. No `internal/cli` import from `internal/mcp`
(ADR-001, still enforced).

---

## 0. User stories

| ID | User story |
|----|------------|
| US-P15-1 | (IA) As an MCP-capable agent, the `a2a` MCP server exposes a small, enable-and-forget set of typed tools (not ~33), so I keep it on alongside other MCP servers without burning tool budget or hitting a host tool-count cap. |
| US-P15-2 | (IA) As that agent, every write still lands through the exact same V2-validate → funnel → PR path as the CLI — grouping tools by capability changes the surface, never the semantics (R-018, no MCP-only capability). |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Amended tool surface | [07-client.md](../../../the-plan/plan/07-client.md) §7.7 (amended 2026-07-22) | the ~6 capability-grouped tools + capability-parity invariant — normative, quoted-by-reference |
| No MCP-only capability | [01-vision.md](../../../the-plan/plan/01-vision.md) R-018 | the constraint is capability-level, not tool-count-level — the whole basis for grouping |
| Shipped P14 surface | [specs/14-mcp-surface.md](14-mcp-surface.md) | the 33-tool 1:1 registry + the bijection/equivalence suites this phase reparameterizes |
| Lifecycle verb table | `internal/mcp/tools_lifecycle.go` `LifecycleVerbTable` | the 15 generic transitions + their per-verb required fields (RequireReason/ReasonCode/Refs/Findings, GateMarker) the `a2a_lifecycle` action enum folds |

---

## T1. The consolidated tool set (track: cli)

> Grouping is by capability; each grouped tool dispatches a CLOSED `action`
> (or `view`) enum to the SAME per-verb handler P14 already ships. Read/write
> split is preserved: `a2a_read` is the only read tool; the five write tools
> all run the existing funnel.

| Tool | Kind | Dispatch enum | Folds (P14 tools) | Input shape |
|------|------|---------------|-------------------|-------------|
| `a2a_read` | read | `view`: inbox \| outbox \| show \| thread \| search \| contracts | 6 read tools | `{view, ...per-view args}` |
| `a2a_new` | write | — | (unchanged) | `{items[], thread}` |
| `a2a_submit` | write | — | (unchanged) | `{ids[]}` |
| `a2a_lifecycle` | write | `action`: ack \| accept \| decline \| start \| block \| unblock \| cancel \| close \| withdraw \| supersede \| satisfy \| approve \| reject \| verify-pass \| verify-fail | 15 generic verb tools | `{action, ids[], reason?, reason_code?, refs?, findings?, actor?}` — per-action requirements enforced by the existing handler |
| `a2a_exchange` | write | `action`: respond \| verify \| dispute \| note | `a2a_respond` / `a2a_verify` / `a2a_dispute` / `a2a_note` | `{action, ...per-action fields}` (parent_ids/result/fields for respond; targets/refs for verify; ids/reason/reason_code for dispute; ids/note for note) |
| `a2a_contract` | write | `action`: new \| publish \| deprecate \| retire \| diff \| verify-export | 6 `a2a_contract_*` tools | `{action, ...per-action fields}` |

> Target: 6 tools (down from 33). **Release valve (implementor call, record in
> §11):** if folding `a2a_respond` into `a2a_exchange` reads as correctness-
> harmful (its `result`+`refs`+`fields` shape is the most distinct), keep
> `a2a_respond` split → 7 tools; the other three (verify/dispute/note) still fold.

## T2. Capability-parity invariant (track: cli)

> Replaces P14's tool-level bijection.

- Every §7.7-designated CLI verb maps to exactly one `(tool, action)`. A
  designated verb with no reachable `(tool, action)`, or a `(tool, action)`
  with no designated verb, fails the parity gate — both directions.
- The mapping table (CLI verb → `(tool, action)`) is enumerated in the parity
  test, derived from `buildCommands()` keys + `cli.ContractSubcommands()` (the
  two-level enumeration P14 established) on one side and the grouped Registry +
  each tool's action enum on the other.

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] The per-verb handlers (`newInboxHandler`, `newLifecycleHandler`,
      `newRespondHandler`, `newContractPublishHandler`, …) are REUSED verbatim —
      the grouped tools add only a thin `action`/`view` dispatch layer that reads
      the discriminator and delegates. No handler business logic is rewritten.
- [ ] `LifecycleVerbTable` stays the SSOT of the 15 generic transitions; the
      `a2a_lifecycle` action enum is derived from it, not re-typed.
- [ ] `cli.ContractSubcommands()` stays the SSOT for the contract action enum.
- [ ] The `a2a __catalog` MCP section (P13/7a) regenerates from `Registry.List()`
      automatically — no catalog code change; just regenerate `commands.md`.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| Capability parity | every designated CLI verb reachable via exactly one `(tool, action)`, both directions | a decoy `(tool, action)` with no CLI verb, and a CLI verb with no `(tool, action)`, each fail independently (carry over P14's decoy tests) |
| CLI ≡ MCP equivalence | for each write verb, the MCP `(tool, action)` path emits byte-identical funnel events (modulo volatile tokens) to the CLI verb — reparameterized by `(tool, action)` | the per-action required-field guards (decline→reason, block/supersede/satisfy→refs, verify-fail→findings, reject→reason) still fire under the grouped tool. NB: `LifecycleVerbTable`'s `RequireReasonCode:true` on `decline` is a pre-existing DEAD field — `newLifecycleHandler` never reads it (a P14 carry-over, handler body off-limits to P15), so there is no reason_code guard to test. |
| Dispatch errors | an unknown `action`/`view`, or a missing discriminator, returns a well-formed `isError` tool result (not a panic, not a JSON-RPC protocol error) | empty `action`; `action` valid but its required field absent |
| No MCP-only capability | no `(tool, action)` exists that the CLI cannot reach (R-018) | — |

## 7. Schema / contract delta

None to the envelope/event/manifest schemas. The change is entirely in the MCP
tool registry's shape (tool names + embedded input-schema descriptors) and its
tests. No `internal/*` core package other than `internal/mcp` is modified.

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-P15-1 | The MCP registry exposes ~6 tools (`a2a_read`, `a2a_new`, `a2a_submit`, `a2a_lifecycle`, `a2a_exchange`, `a2a_contract`; 7 if `a2a_respond` kept split), read/write split preserved. | `Registry.ToolNames()` returns the grouped set; `tools/list` weight drops materially from the P14 ~2.1k-token baseline. |
| 2 | US-P15-2 | Every write verb's MCP `(tool, action)` path emits byte-identical funnel events to its CLI verb (modulo volatile tokens). | the reparameterized equivalence suite is green under `-race`. |
| 3 | — | Capability parity holds both directions (every designated CLI verb ↔ exactly one `(tool, action)`). | the reparameterized parity suite (with decoy tests) is green. |
| 4 | — | `skill/a2ahub/reference/commands.md` is regenerated (MCP section = the grouped tools); `skill-drift` stays green; no removed tool name lingers in `loops.md`/skill prose. | regenerate + `make check`; grep the skill tree for old tool names. |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | a new §3.4 transition adds a row to `LifecycleVerbTable` → automatically a new `a2a_lifecycle` action, no new tool. A new capability family (rare) adds one grouped tool. |
| Coupling | The dispatch layer is thin and reads the SAME registries the CLI does; no second source of the verb set. |
| Roadmap | The v2 hub surface (if it ever exposes tools) inherits the grouped shape; no rework. |

## 10. Implementor entry point

Rework `internal/mcp/tools.go BuildRegistry` to register the ~6 grouped tools,
each with a thin dispatch handler over the existing per-verb handlers (which are
untouched). Reparameterize `cmd/a2a/mcp_parity_test.go` (bijection →
capability-parity by `(tool, action)`) and `cmd/a2a/mcp_equivalence_test.go`
(per-`(tool, action)` byte-equivalence). Regenerate
`skill/a2ahub/reference/commands.md`. The equivalence suite is the real labor
and the bug-farm — verify each CLI verb still emits byte-identical funnel events
through its new `(tool, action)` path. Full loop:
[docs/features/README.md](../../features/README.md).

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it here.

### 2026-07-22 — from wave 8: shipped as 6 tools (respond folded), reason_code note

- **Release valve → folded: 6 tools shipped, not 7.** `a2a_respond` was folded
  into `a2a_exchange` (action=respond) alongside verify/dispute/note. The
  §T1 valve's "correctness-harmful?" concern does not apply: the dispatch layer
  is a transparent passthrough (`return h(ctx, args)` with the ORIGINAL raw
  args), so respond's distinct `parent_ids`+`result`+`fields`+`body_override`
  shape is read only by `newRespondHandler` and cannot collide with another
  action. `TestEquivRespond` proves byte-identical CLI≡MCP output including the
  content-derived response id (`cliResponseID == mcpResponseID`) — the strongest
  evidence the group-tool round-trip perturbs nothing. Final set:
  `a2a_read, a2a_new, a2a_submit, a2a_lifecycle, a2a_exchange, a2a_contract`.
- **`decline` `reason_code` is a pre-existing dead field** (see §6 NB): the row
  in `LifecycleVerbTable` sets `RequireReasonCode:true` but `newLifecycleHandler`
  never reads it. Carried over from P14 unchanged (handler body off-limits to
  P15). Not a P15 defect; flagged so a future reader doesn't expect a guard.
- **`ContractActions` re-typed in `internal/mcp`** (not imported from
  `cli.ContractSubcommands()`): ADR-001 forbids internal/mcp→internal/cli. The
  capability-parity gate (`TestMCPParityBijection` +
  `TestMCPParityContractSubverbsExpanded`) reds on any drift between the two
  lists — the reconciliation is the gate, not the compiler.
- tools/list weight measured 8481 B/~2120 tok → 2803 B/~700 tok (−67%).

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
