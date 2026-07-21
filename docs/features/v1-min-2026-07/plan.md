---
slug: v1-min-2026-07
mode: epic
started: 2026-07-21 17:05
status: in-progress
budget_cap: 30 commits (epic-wide; re-ask at >37)
---

# Teamlead plan — a2ahub v1-min

Bounded index. Per-phase detail (allowlists, briefs, phase logs) lives in
`plans/<spec-stem>.plan.md` — 1 spec = 1 plan. This file stays navigable.

## Context

- Source: `docs/features/v1-min-2026-07/` (README + tracker.yaml + 14 specs),
  coherence audit `audits/coherence-2026-07-21.md` = PASS after fixes.
  Post-audit commit `2d49baf` (engineering rails + testscript env) touched
  specs 01/10 additively with amendments logged — coherence re-audit not
  required (decision, S1).
- User goal: execute the v1-min epic — space + `a2a` binary core + day-one
  content (plan §15 L0–L2 + E7 tail, D-030).
- Placement (binding for every brief): package layout per ADR-001 — phase
  footprints map 1:1 to packages. Error-code registry SSOT =
  `schemas/errors/v1/registry.yaml` (data); `internal/validate` is the only
  Go reader. Templates SSOT = `schemas/templates/v1/` embedded by
  `internal/template`. Deps frozen by ADR-002 (yaml.v3, jsonschema/v6,
  ulid/v2, testscript test-only). Engineering rails = root `AGENTS.md`
  §a2ahub engineering rails.
- AC-401.2 enforcement placement (from shift-left review): the
  schema↔template pairing + fixture↔registry cross-ref gate ships as an
  embedded Go test in P3's footprint (runs under `go test ./...` in
  `make check` → CI-enforced with no new script surface). Spec 02 AC-1
  amended; spec 03 to be amended at wave-2 planning (S6.a).
- Constraints: stdlib-first; no new deps without a new ADR; `fold`/`validate`
  pure; one write funnel (`internal/space`); idempotency AC-301.1;
  cmd/a2a wiring lines are LEAD-RESERVED across all waves (tracker strategy).
- Acceptance criteria (epic README):
  - [ ] §15 exit criteria green: L0 (AC-101.*, AC-102.1 minus hub, funnel
        proof), L1 (T1–T3; US-201/301/302/401 ACs minus hub; one real loop),
        L2 (S-1, S-4, S-5), E7 tail (AC-301.2 parity).
  - [ ] `cc-coverage.yaml` seeded and CI-enforced for shipped tiers (13.2).
  - [ ] Zero shipped-behavior/plan contradictions, or amendment per deviation.

## Wave plan

Waves = topological layers of tracker `blocked_by` DAG ∩ file-disjointness.
Per-phase allowlists live in the linked plan files.

| # | Wave | Phases | Independence | Model | Effort | Plan file(s) | Stop-cond |
|---|---|---|---|---|---|---|---|
| 0 | Sentinel probe (first fan-out: verify no-isolation write reaches main checkout) | — | solo | coder/haiku | low | — (scratch file, deleted) | write not visible in `git status` → STOP dispatch model |
| 1 | Foundation ∥ product schemas | P1, P2 | disjoint footprints | coder/sonnet ×2 | high | [plans/01-foundation.plan.md](plans/01-foundation.plan.md), [plans/02-product-schemas.plan.md](plans/02-product-schemas.plan.md) | scoped tests red twice / A2 byte-fidelity impossible → STOP |
| — | Early audit (S6.c.3): wave 1 sets every pattern — go-auditor over wave-1 diff before wave 2 | — | read-only | go-auditor/sonnet | — | — | HIGH findings → fix-wave before W2 |
| 2 | Engines + host: validation, fold, space/host | P3, P4, P5 | 3 disjoint footprints; `registry.yaml` additions granted to P3 only; lead pre-adds `jsonschema/v6` to go.mod before dispatch | coder/sonnet ×3 | high | (created at W2 S6.a) | cross-agent interface mismatch → STOP, reconcile specs |
| 3 | Author verbs + templates ∥ space-template + V3 CI + doctor | P6, P9 | disjoint (cli/template vs space-template/); cmd/a2a wiring lead-reserved | coder/sonnet ×2 | high / med | (created at W3 S6.a) | doctor placement overlap → serialize |
| 4 | Read surface ∥ lifecycle/contract verbs | P7, P8 | per-verb cli files disjoint (P8 pinned: `cmd_lifecycle.go`, `cmd_contract.go`, `policy_retire.go`) | coder/sonnet ×2 | med | (created at W4 S6.a) | wiring conflicts → lead serializes wiring commits |
| 5 | Integration tests ∥ agent skill | P10, P13 | disjoint (internal/e2e+testkit vs skill/rules paths); P13 unlocked by P7 (moved out of W4 per shift-left review — DAG violation) ; lead pre-adds `testscript` to go.mod before dispatch | coder/sonnet ×2 | high / med | (created at W5 S6.a) | E2E red on engine bug → fix-wave against owning package |
| 6 | getvisa space bootstrap — L0 exit (ops) | P11 | USER-GATED: real GitHub org/repos/tokens; lead+user driven, not a fan-out | lead | — | (created at W6) | missing GitHub access → STOP, ask user |
| 7 | Day-one content — L2 exit | P12 | after P11; partly ops | coder/sonnet + lead | med | (created at W7 S6.a) | axon/seomatrix repos unavailable → STOP |
| 8 | MCP surface + parity suite (tail) | P14 | solo | coder/sonnet | med | (created at W8 S6.a) | parity gaps vs §7.7 enumeration → amend per Q-P14-A |

Every code wave ends with the built-in propagation probe (scout) per S6.b;
wave-end gate: the ceiling `make check` (golangci-lint verified installed).

## Parallelism plan

- Concurrent sets: {P1, P2}; {P3, P4, P5}; {P6, P9}; {P7, P8}; {P10, P13}.
- Lead-reserved files (never in two allowlists; lead edits between waves):
  `cmd/a2a/main.go` wiring lines (P6/P7/P8/P9/P14), `Makefile`, root
  `AGENTS.md`, `docs/**` (tracker/plans/status), and **`go.mod`/`go.sum`
  after W1** — P1 creates them with yaml.v3 + ulid/v2; the lead runs the
  `go get` for `jsonschema/v6` (before W2) and `testscript` (before W5)
  between waves; no wave agent ever touches them after W1 (shift-left
  review MED-2, option b).
- P3 is the sole writer of `schemas/errors/v1/registry.yaml` in W2.

## Self-evaluate (plan-level)

| # | Criterion | Result | Rationale |
|---|---|---|---|
| 1 | Spec compliance | ✅ | every wave = tracker phase(s); allowlists cite spec footprints verbatim (per-spec plans) |
| 2 | SSOT & DRY | ✅ | one validation engine (P3), registry SSOT in schemas/, templates embedded once |
| 3 | Placement | ✅ | ADR-001 1:1 phase→package; registry/templates/AC-401.2-gate placement pinned in Context |
| 4 | Roadmap awareness | ✅ | docs/status.md shows only this epic in flight |
| 5 | Disjointness | ✅ | pairwise-disjoint allowlists per wave; shared wiring + go.mod lead-reserved; P8 files pinned by coherence audit |
| 6 | Budget | ✅ | 30-commit cap ≈ 2–3/wave + fixes; W6 user-gated stop declared |

Shift-left design review (scout, 2026-07-21): verdict ADJUST — 3 MED / 2 LOW,
all adjudicated and applied: (MED-1) P13 moved W4→W5 (DAG: P13 ← P7);
(MED-2) go.mod freeze replaced with lead-managed `go get` between waves;
(MED-3) AC-401.2 gate assigned to P3 as embedded Go test, spec 02 amended;
(LOW-1) P1 brief pins the typed-error idiom (sentinels + `errors.Is`,
distinct from registry codes); (LOW-2) P2 brief pins the invalid-fixture
annotation format (sidecar `<fixture>.expect.yaml` with `code:`) — byte-pure
fixtures, machine-readable for P3's cross-ref.

Verdict: ✅ PROCEED

## Wave log

(one line per wave — detail in plans/*.plan.md)

- Wave 0 — sentinel — 2026-07-21 — probe confirmed no-isolation agents write the main checkout — no commit
- Wave 1 — foundation ∥ schemas — 2026-07-21 — P1 done, P2 shipped/held — commits 38677dd, 6bf4205 — make check green — STOP raised: 3 A2-examples plan-corpus defects (B.7, B.10, B.11) + rand4 charset fixed lead-side — details: plans/01-foundation.plan.md, plans/02-product-schemas.plan.md

## Revisions (user feedback loop)

## Closeout

- Final commits: —
- Audit findings: —
- Deferred: —
- Status: draft
