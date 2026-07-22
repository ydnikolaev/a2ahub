---
slug: v1-min-2026-07
mode: epic
started: 2026-07-21 17:05
status: in-progress
budget_cap: 65 commits (epic-wide; raised from 30→50 at wave-3, 50→65 at wave-5 by operator standing permission "Поднимать кап коммитов разрешаю" — audit FIX-AND-REAUDIT loops + the P10/P13 splits cost more commits than the ~40–45 estimate; re-ask at >70)
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
| 2 | Engines + host: validation, fold, space/host | P3, P4, P5 | 3 disjoint footprints AND compile-decoupled (consumer-side interfaces at the fold/validate/schema seams — no cross-sibling imports; wiring at P6); `registry.yaml` + `schemas/embed.go` granted to P3, `testkit/spacefixture` to P5; jsonschema/v6 pre-added by lead | coder/sonnet ×3 | high | [plans/03](plans/03-validation-engine.plan.md), [plans/04](plans/04-fold-engine.plan.md), [plans/05](plans/05-space-and-host.plan.md) | cross-agent interface mismatch → STOP, reconcile specs |
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
- Wave 1.1 — plan-corpus repair — 2026-07-21 — P2 done (operator ceded fork; self-evaluate PROCEED option a: B.7 fields added, B.10 title quoted, B.11 ULID fixed, fixtures re-synced byte-for-byte) — commit 3fd4621 — make check green — details: plans/02-product-schemas.plan.md
- Wave 1.2 — early audit + fixes — 2026-07-21 — go-auditor FIX-AND-REAUDIT → fix c5d521e (CC-011 conditional + SCH-008 + fixtures + CI hardening) → re-audit found own regression → fix fb33d53 (fixture isolation) → re-audit 2: PASS (ajv cross-check). P1/P2 audit: done. Forward note for P3 recorded in its plan (annotation-propagation edge). — make check green
- Wave 2 — engines + host — 2026-07-21 — P3, P4, P5 done — commits 5fa17f0, 3309521, d0e6386 (+ haiku lint batch, fold.Result rename, manifest-seam doc fix folded in) — make check green (8 packages, lint 0) — propagation: specs 03/04/05 amended, 4 backlog rows (manifest ref/policy checks unowned, Result JSON tags, format-assertion decision, decision-supersede gap), P6-adapter weight flagged in spec 03 — details: plans/03,04,05 *.plan.md
- Wave 2.1 — audit + fixes — 2026-07-21 — go-auditor FIX-AND-REAUDIT: 1 HIGH (funnel path-traversal in sectionOK, empirically confirmed) + 2 security MED (authz fail-open, referential can't-verify) fixed lead-inline with tests (c0eb80f); 2 fold-consumer ergonomics MED backlogged as wave-3 P7/P8 inputs (no current consumer) — bf77094 — make check green — P3/P4/P5 audit: done
- Wave 3 — author verbs ∥ space-template/CI/doctor — 2026-07-21 — P6, P9 done — commits c20082b (cli.go seam), d28ea5f (P6), 8dcd8a3 (P9), 2942644 (cmd/a2a wiring + JSON-tag fix) — make check green (lint 0), 176 tests — LIVE e2e cascade init→new→validate→template proven (advisor-mandated gate); specs 06/09 amended, 6 backlog rows (fold CandidateEvent envelope, host reachability primitive, contract-slug passthrough, credential expiry, V3-workflow flag reconcile, init space-id heuristic) — details: plans/06,09 *.plan.md
- Wave 3.1 — advisor + e2e-driven integration — 2026-07-21 — advisor pre-wiring review (closure-per-verb ✓; AC-201.3 clone-before-refuse trap caught + fixed in the closure; "green make check ≠ working binary" → mandated live run). Live run surfaced the JSON casing gap (fixed, §7 snake_case) and the init-space-id-from-URL heuristic (backlog). submit's GitHub path deferred to P11 by design.
- Wave 3.2 — audit + fix — 2026-07-21 — go-auditor FIX-AND-REAUDIT: 2 HIGH in my cmd/a2a/wire.go (submit --drafts + bare-id dead — drifted target-resolution copy) + MED (cross-space batch / falsified space field) + testing HIGH (no wiring-seam tests) — fixed 69a475a (one shared ResolveSubmitTargets, cross-space guard, config-only guards before clone, wire+resolver tests) → re-audit PASS (auditor built the binary, both repros now reach space resolution; funnel-never-called on cross-space proven). P6/P9 audit: done. Wave 3 CLOSED.
- Wave 4 — read surface ∥ lifecycle/contract — 2026-07-21 — FIRST dispatch died on infra (both agents "Connection closed mid-response", zero code); stray build binary cleaned + gitignored (1bf787f); re-dispatched → P7, P8 done — commits 79dfe0d (P7 cache+read verbs), e753e6e (P8 lifecycle/contract+fold-legality+retire POL-006), 5d005fc (cmd/a2a wiring: all 27 verbs), e13fc26 (read verbs tolerate missing config) — make check green (lint 0, 406+ tests) — lead fixed POL-006 closure test (registry_test off P8 allowlist); specs 07/08 amended, 3 backlog rows (§5.7 contract-root digest, D-023 no publish SHA, fold response sub-lifecycle) — details: plans/07,08 *.plan.md
- Wave 4.1 — audit + fix + re-audit — 2026-07-21 — go-auditor FIX-AND-REAUDIT: 2 HIGH (respond/deprecate non-idempotent — freshly-minted secondary id folded into the funnel dedup branch → duplicate PR on retry; P7's cache-backed markers built but never wired into cmd/a2a) + 3 MED (buildStore swallowed malformed config, digest helper mis-placed for P12 reuse, statusline goroutine unreliable in one-shot process) + 3 LOW. Fix-wave (coder, advisor-designed): deterministic content-derived ids (bef5b7b) via existing entropy seam — no funnel/buildRequest change, multi-response preserved; lead wired CacheBacked markers + surfaced malformed config + .git-skip (1fe953f); docs/amendments/backlog (fcd829f); MED-4 regression test (340f99b). MED-3 statusline-subprocess + midnight-crossing edge backlogged. Re-audit over 79dfe0d^..fcd829f: PASS (all 4 flips confirmed, discriminating tests under -race). P7/P8 audit: done. **Wave 4 CLOSED.** — make check green — details: plans/07,08 *.plan.md

- Wave 5 — integration harness (P10) — 2026-07-22 — P10 done — commits d694c9c (T3 testscript + E2E-1/E2E-4 + cc-coverage + statusline perf, 48 tests), a0ffc38 (doctor bare-version wiring fix the harness surfaced) — make check green — P13 SPLIT OUT to wave 6 (needs a lead-built binary command-catalog entry point — none exists; §7.7 generate-then-diff mechanism also absent). T3 shipped as a two-mode split (txtar for read verbs, direct-construction Go tests for write verbs — built binary not FakeHost-injectable); zero ci.yml edit (runs under `go test ./...`); specs 10 §11 amended; 2 backlog rows (spacefixture participants seed, doctor dev-build version UX). Audit rides S8 (test-only). — details: plans/10-integration-tests.plan.md

- Wave 6 — MCP surface (P14) — 2026-07-22 — P14 done — commits a92cf2c (ContractSubcommands SSOT export), 73a5aed (internal/mcp stdio JSON-RPC server + §7.7 tool set + cmd/a2a parity/equivalence + wire mcp line), 7dc6327 (statusline perf gate robust under -race) — make check green — 75 mcp + 43 cmd/a2a tests, coverage 71.2%, internal/mcp NEVER imports internal/cli (proven); parity bijection (two-level subset-map) + per-write-verb byte-equivalence (23 funnel writers modulo volatile tokens) + CC-093 all green; live `a2a mcp` serves stdio JSON-RPC. Deviations in spec 14 §11 (no ci.yml narrowing, parity-is-a-test-not-generator → spec 13 amended, equivalence-modulo-tokens, first-space wiring); 3 backlog rows (mcp first-space, eager-clone, funnel version-stamp). Audit dispatched. — details: plans/14-mcp-surface.plan.md

## Revisions (user feedback loop)

## Closeout

- Final commits: —
- Audit findings: —
- Deferred: —
- Status: draft
