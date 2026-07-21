---
slug: v1-min-2026-07
phase: P14
spec: ../specs/14-mcp-surface.md
wave: 6
status: dispatched
---

# Phase plan — P14 MCP surface (`a2a mcp`) + CLI/MCP parity suite

## Goal

Ship `internal/mcp` — a stdio JSON-RPC 2.0 MCP server whose tool registry
maps 1:1 to the §7.7 OP subset, every write tool running the SAME
validate→funnel→PR path as its CLI verb — plus the CLI/MCP parity gate and
a per-write-verb CLI≡MCP event-equivalence suite (both in `cmd/a2a`). Spec
14 §8 AC 1-7.

## Placement / architecture decisions (lead, binding — advisor-reviewed)

- **`internal/mcp` re-wires the core; it does NOT import `internal/cli`**
  (ADR-001 forbids it). The config→mirror→funnel/store/engine construction
  is DUPLICATED from `cmd/a2a/wire.go` — this is deliberate and cheap: an
  mcp server is a long-lived stdio session, its wiring legitimately differs
  from the CLI's per-invocation wiring; extracting a shared wiring layer
  buys nothing and couples two things that want to differ.
- **Event-doc construction is duplicated, NOT extracted.** The CLI's
  `lifecycleEventDoc` shape is distributed across ≥4 inline call sites
  (generic lifecycle, respond, deprecate, publish) in `internal/cli`, each
  populated from flags — NOT one clean liftable `buildEvent()`. Lifting it
  would mean surgery on shipped, audited P6/P8 verbs for a tail item —
  rejected. Instead `internal/mcp` builds its OWN event/request docs as a
  projection of the FROZEN `event/v1` schema (P2, D-030 — the real SSOT),
  and the **per-write-verb equivalence suite is the anti-drift gate**: it
  proves byte-identical event/commit output between the two surfaces, so a
  divergence is a red build, not a silent history split.
- **Parity is a SUBSET map, not a bijection over all verbs.** The
  §7.7-designated set = read verbs (inbox/outbox/show/thread/search/
  contracts) + `new` + `submit` + the 19 lifecycle verbs + the 6 contract
  sub-verbs. CLI-only verbs (version/init/connect/disconnect/doctor/
  template/sync/validate/mcp) have NO tool by design. The check: every MCP
  tool → exactly one designated CLI verb, AND every designated verb →
  exactly one tool. The `mcp` verb itself is excluded from the designated
  set (it has no tool, and a future catalog verb self-references — exclude
  it too).
- **Two-level CLI enumeration.** Contract sub-verbs are NOT `buildCommands()`
  keys (bare switch in `ContractCommand.Run`) — the parity test flattens
  both levels: `buildCommands()` keys ∪ `cli.ContractSubcommands()` (the
  exported SSOT, already shipped in a92cf2c), `contract` expanded to its 6.
- **No `ci.yml`/`Makefile` edit** (same as P10): the parity + equivalence +
  CC-093 tests live in the `cmd/a2a` package and run under `make check`'s
  existing `go test ./...`. Spec §T5/§6 "one CI job edit" is satisfied by
  those tests running in the existing job. Narrowing recorded at wave close.
- **Injectable clock + entropy in mcp handlers** — required so the
  equivalence suite can drive both surfaces with the same `now`/entropy and
  get byte-identical event ULIDs. Production defaults to `time.Now`/
  `rand.Reader` (same pattern as `internal/cli`'s lifecycleDeps).
- **stdlib JSON-RPC 2.0** (`encoding/json`, `bufio`, `os.Stdin/Stdout`). If
  genuine MCP protocol compliance cannot be met with stdlib, adding an MCP
  SDK is a LEAD `go.mod` decision — the agent RAISES it, never adds it.

## Allowlist (repo-relative)

- `internal/mcp/**` (NEW package: JSON-RPC stdio server, tool registry, per-
  tool handlers, mcp-side wiring, structured-return shapes, unit tests)
- `cmd/a2a/mcp_parity_test.go` (NEW: the parity bijection gate + decoy
  both-directions + the §7.7-exclusion check)
- `cmd/a2a/mcp_equivalence_test.go` (NEW: per-write-verb CLI≡MCP event/commit
  byte-equivalence + CC-093 interleaved idempotency)

## Lead-reserved / off-limits deltas

- `cmd/a2a/wire.go` — the lead adds the single `m["mcp"] = ...` dispatch line
  POST-wave (the parity/equivalence tests do NOT need it — they call
  `mcp.NewServer()`/the registry + `cli` constructors directly).
- `internal/cli/**` (import as a black box in the `cmd/a2a` tests; the
  ContractSubcommands SSOT already exists), all other `internal/*` production
  sources, `go.mod`/`go.sum` (raise any dep need to the lead), `ci.yml`,
  `Makefile`, `schemas/**`.

## Brief

```
Stack: Go 1.26 stdlib ONLY (encoding/json, bufio, os, context; NO new deps —
if MCP compliance truly needs an SDK, STOP and report it as a lead go.mod
decision, do not add it). Reuse internal/{cache,space,validate,fold,artifact,
schema,template,host} as a black box. All paths REPO-RELATIVE.

## Goal
Implement P14 per docs/features/v1-min-2026-07/specs/14-mcp-surface.md — read
END TO END first (§T1, the §7.7 OP↔tool table, §5 reuse, §6 test matrix, §8
the 7 ACs, §11 Amendments, Open questions). Then read the Placement/
architecture decisions in docs/features/v1-min-2026-07/plans/14-mcp-surface.
plan.md (BINDING). Read docs/the-plan/plan/07-client.md §7.7 (the normative
tool list + structured-returns contract — quote, do not reparaphrase).

## Context (read in this order)
- cmd/a2a/wire.go — the config→mirror→funnel/store/engine wiring you
  DUPLICATE inside internal/mcp (buildStore, resolveLifecycleDeps, runSubmit,
  newEngine, resolveCredential, parseGitHubRepo patterns). mcp re-wires the
  same core; it does NOT import cmd/a2a (package main) or internal/cli.
- internal/cache — the folded-view queries the read tools compose over
  (NewStore + the inbox/outbox/show/thread/search/contracts queries the P7
  verbs already call). Read tools add ZERO new read logic.
- internal/space — WriteFunnel.Submit (the write path every write tool
  calls, IDENTICAL to the CLI) + SubmitRequest/FileWrite shapes + Layout
  (EventFile/Exchange paths). internal/validate — the V2 pipeline the
  SubmitValidatorAdapter runs (write tools run the SAME pipeline).
- internal/cli/cmd_lifecycle.go (lifecycleEventDoc shape ~L649-672 + respond
  ~L768-826) and cmd_contract.go (publish/deprecate event docs) — you READ
  these to MATCH the frozen event/v1 doc shape your handlers emit, then build
  your OWN doc structs (projection of schemas/event/v1). You do NOT import
  them. The equivalence test proves your output matches theirs byte-for-byte.
- schemas/event/v1 + schemas/envelope/v1 — the FROZEN schemas your tool
  input/output shapes project (D-030 zero migrations).
- testkit/spacefixture + internal/host/fake.go — for your tests + the cmd/a2a
  equivalence tests (the same idiom P10/cli tests use).
- cli.ContractSubcommands() (internal/cli, exported) — the 6 contract
  sub-verb names the parity test enumerates.

## What to do
1. internal/mcp: a stdio JSON-RPC 2.0 server (Server type, Serve(ctx, in
   io.Reader, out io.Writer)). Handle the minimal MCP methods: initialize,
   tools/list (returns the registry's tool descriptors + input schemas),
   tools/call (dispatch to the named tool's handler). Malformed request →
   a well-formed JSON-RPC error, process STAYS ALIVE (AC #7). One in-flight
   request at a time over stdio is fine; guard shared state if you spawn any
   goroutine (recover, no leak).
2. Tool registry: name -> {inputSchema (embedded JSON), handler}. Register
   EXACTLY the §7.7 set: a2a_inbox, a2a_outbox, a2a_show, a2a_thread,
   a2a_search, a2a_contracts, a2a_new, a2a_submit, a2a_<lifecycle> for each
   of the 19 §3.4 transition verbs (ack/accept/decline/start/block/unblock/
   cancel/close/withdraw/supersede/satisfy/approve/reject/verify-pass/
   verify-fail/respond/verify/dispute/note — hyphens→underscores), and
   a2a_contract_<sub> for each of the 6 (new/publish/deprecate/retire/diff/
   verify-export). NO tool for version/init/connect/disconnect/doctor/
   template/sync/validate. Expose the registry's tool-name set (e.g.
   ToolNames() []string or an exported registry) so the cmd/a2a parity test
   can enumerate it.
3. Read tool handlers: compose over internal/cache folded views; return
   STRUCTURED JSON (envelope fields + folded state) PLUS the body verbatim
   (never markdown-only) — AC #2. Match the CLI --json shape where one exists
   (the cli read verbs' JSON output is your fidelity reference).
4. Write tool handlers: take STRUCTURED inputs (ids as arrays, fields as
   objects — the structured form replaces the CLI's arg/flag parsing), run
   the SAME internal/validate V2 pipeline + internal/space WriteFunnel.Submit
   as the CLI (D-002 one write shape). Build event/request docs as a
   projection of the frozen event/v1 schema, matching the CLI's emitted
   shape. a2a_submit accepts an ID array (OP-220 all-or-nothing batch) AND a
   single id. Handlers take an injectable now func() time.Time + entropy
   io.Reader (production: time.Now/rand.Reader) so the equivalence test can
   fix them.
5. mcp-side wiring: a constructor that builds the store/funnel/engine from
   the project+machine config (duplicate wire.go's construction — do NOT
   import cmd/a2a or internal/cli). A long-lived session loads config once.
6. cmd/a2a/mcp_parity_test.go: enumerate the DESIGNATED CLI verb set =
   buildCommands() keys MINUS {version,init,connect,disconnect,doctor,
   template,sync,validate,mcp}, with `contract` EXPANDED to
   cli.ContractSubcommands() (a2a_contract_<sub>). Map each to its tool name
   via the a2a_ + name(hyphens→underscores) convention. Assert BIJECTION vs
   the mcp registry's tool names in BOTH directions. Add a decoy tool (no
   verb) and a decoy verb (no tool) in throwaway copies and assert each fails
   the check independently (AC #3). Assert none of the excluded verb names
   appear as tools (AC #6).
7. cmd/a2a/mcp_equivalence_test.go: for EVERY write verb (new, submit, all 19
   lifecycle, all 6 contract) — run the CLI verb (direct construction: real
   space.WriteFunnel + host.FakeHost + testkit/spacefixture, fixed clock +
   fixed entropy) and the equivalent MCP tool call (SAME fixed clock +
   entropy, same fixture shape) and assert the resulting event file bytes +
   commit message are IDENTICAL modulo the artifact/event id (AC #4). This is
   the anti-drift gate — widen beyond the spec's single example to ALL write
   verbs. Plus CC-093 (AC #5): `a2a submit <id>` (CLI) then a2a_submit on the
   SAME id (MCP) in one session → the second returns "already done"
   (WriteStateAlreadyOpen), FakeHost shows exactly one OpenPR.
8. Sanity (scoped ONLY): gofmt; go vet ./internal/mcp/... ./cmd/a2a/...;
   go test ./internal/mcp/... ./cmd/a2a/... -race -count=1;
   go list -deps ./internal/mcp/... MUST NOT show internal/cli.

## Constraints
- internal/mcp NEVER imports internal/cli (ADR-001) — go list -deps proves it.
  No MCP-only capability (R-018): every tool maps to a real CLI verb's core
  op. No new validation rules, no second fold, no second funnel/commit path.
- No new deps (stdlib JSON-RPC). Log-or-return; copy the repo error idiom.
  t.Parallel() where fixtures are t.TempDir-isolated. Coverage floor 70% for
  internal/mcp.

## DO NOT
- DO NOT commit / run git / run make check / repo-wide go build outside your
  own packages' tests. Scope self-verify to internal/mcp + cmd/a2a.
- DO NOT edit cmd/a2a/wire.go (the lead adds the `mcp` dispatch line
  post-wave — your tests don't need it), internal/cli, other internal/*
  production, go.mod, go.sum, ci.yml, Makefile, schemas/**.
- DO NOT import internal/cli from internal/mcp.

## Acceptance
- Spec 14 §8 AC 1-7, each green under -race: parity bijection (AC 1/3/6),
  structured read return (AC 2), per-write-verb event equivalence (AC 4),
  CC-093 interleaved idempotency (AC 5), malformed-input JSON-RPC error +
  process-alive (AC 7).

## Report back
- Files, tests, scoped output (the -race run + go list -deps proving no
  internal/cli import).
- Deviations — REQUIRED: the JSON-RPC/MCP protocol subset you implemented
  (which methods), how mcp re-wired the core (what you duplicated from
  wire.go), the event-doc shape you emit vs the CLI's (any field you had to
  match carefully), any write verb whose equivalence you could NOT make
  byte-identical (+ why), and whether stdlib sufficed for MCP compliance or
  you hit a wall (a lead go.mod decision).
- Anything skipped + why.
```

## Acceptance

- [ ] Spec 14 §8 AC 1-7 green (lead re-runs `go test ./internal/mcp/...
      ./cmd/a2a/... -race`); the parity + equivalence suites run under
      `make check`'s existing `go test ./...`.

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- `wire.go` `m["mcp"]` dispatch line + live `a2a mcp` smoke = lead, post-wave.
- Spec 14 §11 narrowing (no ci.yml edit; equivalence widened to all write
  verbs) = lead, wave-end amendment.
- P13 (skill) commands.md consumes this MCP tool registry — wave 7.
