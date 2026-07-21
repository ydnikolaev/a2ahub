# P14 — MCP surface (`a2a mcp` façade) & CLI/MCP parity suite — Specification

> Unified spec template projection. Track: cli only (§T1); other track sections deleted per template rule.

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Plan**: [plans/14-mcp-surface.plan.md](../plans/14-mcp-surface.plan.md)
**Created**: 2026-07-21  ·  **Owner**: yura
**Footprint**: `internal/mcp/` (new package — stdio MCP server, tool registry
mapping 1:1 to the §7.7-enumerated OP subset: `a2a_inbox`/`a2a_outbox`/
`a2a_show`/`a2a_thread`/`a2a_search`/`a2a_contracts`, `a2a_new` with
`items[]`, `a2a_submit` with ID arrays, lifecycle verb tools, `a2a_contract_*`
family), `cmd/a2a` wiring line only (register the `mcp` subcommand — no other
`cmd/a2a` file besides the parity test below), one new parity test file
living in the `cmd/a2a` package, one CI job edit adding the T3 CLI/MCP
parity suite run to the product-repo CI workflow (workflow file itself is
P1's creation, not this phase's — this phase adds one job to it).

**May import** (ADR-001): `internal/mcp` → core packages only (`artifact`,
`schema`, `fold`, `validate`, `host`, `space`, `cache`, `template`) — **never
`internal/cli`**. `cmd/a2a` (the wiring line + the parity test file) → anything
under `internal/` (ADR-001's `cmd/a2a` row grants this) — which is precisely
*why* the parity test sits in `cmd/a2a` and not inside `internal/mcp`: the
bijection check must see both the CLI's OP registry and the MCP tool
registry, and `internal/mcp` is structurally forbidden from importing
`internal/cli`. Placing the check anywhere inside `internal/mcp` would
violate this phase's own footprint.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-301 | As an agent, I operate the whole exchange from one binary with idempotent commands — feature parity between the CLI and the MCP façade means the surface I use never changes what I can do. |
| US-701 | As an MCP-capable agent, I use the exchange through typed tools with structured results — same core, same funnel, same idempotency guarantees as the CLI. |
| US-P14-1 | As an MCP-capable agent in an MCP-native harness, when I call `a2a_submit`/a lifecycle verb tool, the write lands through the exact same V2-validate → PR funnel as `a2a submit` — no MCP-only shortcut, no MCP-only capability (R-018). |
| US-P14-2 | As an agent running the CLI and the MCP server in the same session, my ops stay idempotent regardless of which surface issued them — same core, same cache, same fold (CC-093). |
| US-P14-3 | As the operator, the OP↔tool mapping can never silently drift — CI fails the build the moment a CLI verb and its MCP tool diverge. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| MCP tool surface & structured returns | [07-client.md §7.7](../../../the-plan/plan/07-client.md) | tool list + returns contract is normative, quote verbatim, do not reparaphrase |
| Parity invariant | [07-client.md §7.1](../../../the-plan/plan/07-client.md) | "The CLI and MCP surfaces are thin frontends over one core; feature parity between them is a CI-checked invariant." — the sentence this phase implements mechanically |
| v1-min MCP-tail placement | [15-rollout.md V1-min re-cut](../../../the-plan/plan/15-rollout.md) | MCP ships IN v1, as a non-critical-path tail — quoted below |
| MCP-in-v1 rationale | [17-decisions.md D-030](../../../the-plan/plan/17-decisions.md) | "MCP kept in v1 (operator call): thin façade over the same core, ~1 day, typed tools in the v1 release" |
| Package boundaries | [ADR-001](../../../decisions.md) | `internal/mcp` import row; this phase's footprint and import line are derived from it |
| T3 parity suite | [13-testing.md](../../../the-plan/plan/13-testing.md) T3 row, §13.2 | "CLI/MCP parity suite (7.1)"; coverage grid's 4th dimension is explicitly "surface (CLI, MCP)" |
| Same-session safety | [12-corner-cases.md CC-093](../../../the-plan/plan/12-corner-cases.md) | "MCP server and CLI used in same session" → "same core, same cache, idempotent ops — safe by construction" |
| AC/US source rows | [14-us-ac.md](../../../the-plan/plan/14-us-ac.md) US-301/AC-301.2, US-701 | quoted verbatim in §8, not modified here |

**v1-min MCP-tail clause (15-rollout.md, quoted verbatim):**

> the **MCP surface (OP-216 / 7.7) as a TAIL item** — built after the core is
> green, before v1 is declared done (thin façade over the same core, ~1 day
> incl. the parity suite; never on the critical path)

Non-critical-path is a sequencing fact, not a scope cut — every clause in
this spec (parity, structured returns, same V2 pipeline) ships in v1-min;
only the *ordering* is constrained: this phase starts after P5–P8's core is
green.

---

## Track-specific contract (§T1 — cli)

### T1. CLI surface

| Command | Flags | Input | Output | Notes |
|---------|-------|-------|--------|-------|
| `a2a mcp` | none | none (JSON-RPC 2.0 over stdin/stdout) | serves tool calls for the life of the stdio session | (OP-216); thin façade — a harness (Claude Code, Codex, or any MCP client) spawns this as a subprocess |

### Generated OP↔tool mapping table (§7.7 normative subset)

> Scope note: 7.7 enumerates a specific tool set, not the full OP-2xx
> catalog. This table is exactly that enumeration; OPs not listed here
> (init/connect/disconnect/validate/sync/html/statusline/update/doctor/
> template) have no MCP tool in v1 and stay CLI-only — see Open questions
> for why this scoping, not the AC-301.2 wording alone, is load-bearing.

| OP | CLI command | MCP tool | Notes |
|---|---|---|---|
| OP-207 | `a2a inbox` | `a2a_inbox` | — |
| OP-208 | `a2a outbox` | `a2a_outbox` | — |
| OP-209 | `a2a show` | `a2a_show` | — |
| OP-210 | `a2a thread` | `a2a_thread` | — |
| OP-221 (1st clause) | `a2a search` | `a2a_search` | hub-less by design (§7.2) |
| OP-221 (2nd clause) | `a2a contracts` | `a2a_contracts` | — |
| OP-221 (3rd clause) | `a2a contract diff` | `a2a_contract_diff` | part of the `a2a_contract_*` family |
| OP-203 | `a2a new` | `a2a_new` | accepts `items[]` for batch drafting on one thread |
| OP-205 | `a2a submit` | `a2a_submit` | single ID |
| OP-220 | `a2a submit --batch` | `a2a_submit` | same tool, ID array input → OP-220 all-or-nothing batch semantics |
| OP-211 | lifecycle verbs (`ack`/`accept`/.../`note`) | lifecycle verb tools (`a2a_ack`, `a2a_accept`, ... one tool per §3.4 transition name) | verbs accept multiple IDs; batch triage: one commit, one PR |
| OP-212 | `a2a contract new/publish/deprecate/retire` | `a2a_contract_new`/`_publish`/`_deprecate`/`_retire` | gate awareness unchanged (G1/G2/§5.4 retire-override) |
| OP-213 | `a2a contract verify-export` | `a2a_contract_verify_export` | part of the `a2a_contract_*` family; multi-file digest compare |

**Structured returns (§7.7, quoted verbatim):**

> Read tools return envelope + folded state as structured data (not raw
> markdown) plus the body verbatim; write tools take structured inputs and
> run the same V2 pipeline. NOTE: MCP is a convenience façade; anything it
> can do, the CLI can do — no MCP-only capabilities (R-018).

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `internal/cache` (P7) — `a2a_inbox`/`a2a_outbox`/`a2a_show`/`a2a_thread`/
      `a2a_search`/`a2a_contracts` compose over the same folded-view queries
      the CLI verbs already call; `internal/mcp` adds zero new read logic.
- [ ] `internal/space`'s write funnel (P5, D-002) — `a2a_submit`/lifecycle
      verb tools call the identical validate → ephemeral branch → PR path
      the CLI's `cmd_submit.go`/lifecycle files (P6/P8) call; no second
      commit/PR mechanism.
- [ ] `internal/validate`'s V2 pipeline (P3) — every write tool runs the
      same pipeline as its CLI counterpart; this phase adds zero new
      validation rules or a parallel machine-code registry.
- [ ] `internal/fold` (P4) — folded-state values returned by read tools are
      the same fold output the CLI's `show`/`inbox` already compute; no
      second fold implementation for the MCP shape.
- [ ] `cmd/a2a`'s existing DI wiring pattern (P1 skeleton, extended by every
      phase that registers a verb) — this phase adds one `mcp` registration
      line, following the same convention P6/P7/P8 used for their verbs.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| `internal/mcp` tool registry | 1:1 coverage against the §7.7-enumerated OP set (table above) | a new §3.4 lifecycle transition added without a matching tool (CI catch, not runtime) |
| Parity suite (T3, AC-301.2) | bijection in both directions: every enumerated OP row has a tool; every tool maps to an enumerated OP row (no MCP-only capability, R-018) | a tool present with no OP row; an OP row present with no tool, within the 7.7-scoped set |
| Read tool structured returns | `a2a_show`/`a2a_inbox`/etc. return envelope + folded state as structured JSON plus body verbatim, never markdown-only | folded state from the tool call byte-matches the CLI `--json` equivalent for the same artifact |
| Write tool same funnel | `a2a_submit`/lifecycle verb tools produce identical commit/event shape to their CLI counterparts | same artifact submitted once via CLI, once via tool (different fixture ID) — resulting commit message/event file structure match |
| CC-093 same-session safety | CLI and MCP interleaved against the same cache/mirror stay idempotent | `a2a submit <id>` then `a2a_submit` (MCP) on the same already-submitted ID — second call is a no-op "already done", not a duplicate PR |
| `a2a mcp` stdio session | malformed JSON-RPC request handling; concurrent tool calls in one session | harness closes stdin mid-call; two tool calls issued before the first responds |
| CI parity gate | the job fails the build on drift, not just at test time locally | add a lifecycle verb to §3.4 without a tool → CI red; remove a tool without removing its OP row → CI red |

## 7. Schema / contract delta

No product JSON Schema changes — envelope/event/manifest/consumes are
frozen by P2 (D-030: zero migrations). The only cross-boundary contract this
phase adds is the MCP tool input/output JSON shapes, which are direct
projections of the already-frozen envelope/event schemas (§7.7 "schemas
embedded") — described as data via the OP↔tool table above, not restated as
a schema delta since no new field is introduced anywhere.

## 8. Acceptance criteria

> AC-301.2 and the US-701 clause are copied verbatim from
> [14-us-ac.md](../../../the-plan/plan/14-us-ac.md); implementors do not
> modify them. Extra phase-local rows carry US "—".

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-301 | AC-301.2 Given the CLI and MCP surfaces, when the parity suite runs, then every capability exists in both. [T3, 7.1] | CI job runs the `cmd/a2a` parity test against the §7.7-enumerated OP set; green means every row in the mapping table above has both a CLI command and a tool |
| 2 | US-701 | As an MCP-capable agent, I use the exchange through typed tools with structured results. [ACs: parity AC-301.2; structured envelope + folded state responses per 7.7; T3] | invoke `a2a_show` over stdio against a fixture artifact; assert the response is structured JSON (envelope + folded state fields), body field carries the verbatim markdown body, not embedded in prose |
| 3 | — | Bijection is checked in both directions, not just CLI→tool | parity test fixture includes a decoy tool registered with no OP row and a decoy OP row with no tool; both fail the suite independently |
| 4 | — | Write tools run the identical V2 pipeline as their CLI counterparts, not a shortcut | submit the same fixture artifact shape via `a2a_submit` and via `a2a submit`; resulting event file and commit message shape are structurally identical (differ only by artifact ID) |
| 5 | — | CC-093: interleaved CLI + MCP use on one already-submitted ID is idempotent, not a duplicate PR | `a2a submit <id>` then `a2a_submit` on the same ID in one test session; second call returns "already done", mirror shows exactly one PR/commit |
| 6 | — | Tools outside the §7.7 enumeration (init/connect/validate/sync/html/statusline/update/doctor/template) are not present in the MCP tool registry | list registered tools at server start; assert none of the excluded OP names appear |
| 7 | — | `a2a mcp` never blocks the harness beyond a normal tool round-trip; malformed input returns a JSON-RPC error, not a crash | send a malformed request over stdio; process stays alive, well-formed JSON-RPC error response returned |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | a new §3.4 transition or a new OP added to the catalog gets one new tool entry + one mapping-table row; the parity test's bijection check is the mechanism that forces this to stay in sync, not a manual doc update. |
| Coupling | soft — `internal/mcp` depends only on the same core package outputs (`artifact`/`fold`/`validate`/`space`/`cache` types) the CLI already depends on; it never reaches into `internal/cli` internals, and `internal/cli` never depends on `internal/mcp` either. |
| Migration path | low — MCP tool schemas are projections of the frozen product schemas (D-030 zero-migrations); a v2 hub-backed MCP surface (if ever) would reuse this same tool registry unchanged, only swapping the core package's data source. |
| Roadmap conflicts | this phase is `blocked_by` P10 per `tracker.yaml` (P10 transitively covers P1–P9, including the `internal/space`/`internal/cache`/`internal/fold`/`internal/validate` stability and the P6/P7/P8 CLI verbs the parity comparison needs, plus P1's `cmd/a2a` skeleton + the product-repo CI workflow this phase adds one job to); P13's skill (also a v1-min phase) may document the MCP tool list but does not import this package. |

## 10. Implementor entry point

Execute as the tail wave of the v1-min epic (`blocked_by` P10 per
`tracker.yaml`, which transitively covers P1–P9 — the core must be
green first, per the 15-rollout.md tail clause quoted above; never start
this before those are stable). TDD
default: red (parity test written first, failing because `internal/mcp`
doesn't exist yet) → green (tool registry implemented) → refactor. Framework-
first: stdio JSON-RPC 2.0 transport via Go stdlib (`encoding/json`, `bufio`,
`os.Stdin`/`os.Stdout`) — if MCP protocol compliance genuinely cannot be met
with stdlib alone, adding a dependency (e.g. an MCP SDK) is a **lead-level
`go.mod` decision**; the implementor raises it, never adds it unilaterally.
Log-or-return per
[.claude/rules/go-conventions.md](../../../../.claude/rules/go-conventions.md).
Full loop: [docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. When shipped reality deviates from this spec, record it here
> AND amend any downstream spec.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — from coherence audit (pre-implementation): corrected the §9 "Roadmap conflicts" row and the §10 implementor entry point, both of which incorrectly stated this phase is `blocked_by` P1, P5, P6, P7, P8 per `tracker.yaml`; `tracker.yaml` actually lists `blocked_by: [P10]` only, with P10 transitively covering P1–P9 — both spots now cite P10 and note the transitive chain.

---

## Open questions

- **Q-P14-A** (phase-local, not a plan Q-###): AC-301.2 ([14-us-ac.md](../../../the-plan/plan/14-us-ac.md))
  reads "every capability exists in both" with no explicit qualifier, which
  taken literally could mean full parity across the entire OP-2xx catalog
  (including local/admin ops like `init`/`connect`/`validate`/`sync`/`html`/
  `statusline`/`update`/`doctor`/`template`). §7.7 ([07-client.md](../../../the-plan/plan/07-client.md))
  instead gives an explicit, closed enumeration of tools ("Tools map 1:1 to
  the OP catalog... `a2a_inbox`, `a2a_outbox`, ... `a2a_contract_*`") that
  excludes those local ops. This spec resolves the tension by treating
  §7.7's enumeration as the normative scope of "capability" for AC-301.2 —
  local/admin ops have no natural remote-tool analog and are not listed —
  and builds the parity suite against that scope only. The plan itself does
  not state this narrowing explicitly; flagged per this brief's instruction
  not to resolve plan ambiguity silently. Does not block this phase's start;
  would only require widening the tool registry + mapping table if the
  operator later confirms the broader literal reading.
