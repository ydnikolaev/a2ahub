---
slug: v1-min-2026-07
phase: P4
spec: ../specs/04-fold-engine.md
wave: 2
status: verified
---

# Phase plan — P4 Fold engine

## Goal

Ship the pure `internal/fold` package: §3.4 transition tables encoded as
data, full + incremental fold, the 3-valued pre-write legality check, the
closure model (D-024), transition-free kinds (D-025), the `expired` overlay,
non-fatal flag semantics (CC-020…022), and the T2 determinism properties —
spec 04 AC rows 1–9.

## Allowlist (repo-relative)

- `internal/fold/**`

## Lead-reserved / off-limits deltas

- Everything else. In particular `internal/validate`/`internal/schema` (P3,
  parallel) and `internal/space`/`internal/host`/`testkit` (P5, parallel).

## Placement decisions (lead, binding)

- Purity is absolute: imports = stdlib + `internal/artifact` only. No yaml,
  no schema types — callers translate `event/v1` documents into fold's own
  input structs (spec 04 §7).
- P3 consumes the legality check via ITS OWN consumer-side interface + DI at
  cmd/a2a (P6) — you export a plain function/method matching the spec §T1
  contract; you do not need to know P3's interface.

## Brief

```
Stack: Go 1.26, STDLIB + internal/artifact ONLY (ADR-001 purity row — this
is the spec's own AC row 9, gated by go list -deps).
All file paths are REPO-RELATIVE.

## Goal
Implement internal/fold per
docs/features/v1-min-2026-07/specs/04-fold-engine.md — read it END TO END
first (§T1 surface table, §T1.1 table list, §T1.2 flag semantics, §6 T2
matrix, §8 ACs), then the normative plan sections it cites:
docs/the-plan/plan/03-domain.md §3.4 (EVERY row of every table §3.4.1–.7 —
this IS your test list), §3.5 rules 1–5, §3.7; 17-decisions.md
D-017/D-024/D-025/D-027; 12-corner-cases.md CC-020..022;
05-schemas.md §5.2.2. Also root AGENTS.md "a2ahub engineering rails" and
internal/artifact's exported API (reuse ID parsing; copy the error idiom
for genuinely-invalid-input errors — but remember: flagged events are NOT
errors, fold never fails on them).

## Allowed files — REPO-RELATIVE ONLY
- internal/fold/**

## Off-limits (NEVER touch)
- EVERYTHING else: internal/validate, internal/schema, internal/space,
  internal/host, testkit, cmd/a2a, schemas/**, go.mod, docs/**. Parallel
  agents own their packages — do not read their in-progress code.

## What to do
1. Encode §3.4.1–§3.4.7 as DATA: (type, fromState, transition) →
   (toState, requiredActorRole). The table drives BOTH the fold and the
   test fixtures (spec §5: never hand-duplicate rows into test code). A
   meta-test asserts row-count == exercised-subtest-count (AC 3).
2. Types: fold's own minimal input/output structs (event, envelope facts,
   ordering key = caller-supplied commit-seq + ULID tiebreak,
   manifest-membership view member|left|unknown as caller-supplied
   function/map — fold NEVER reads git/space.yaml/clock).
3. Full fold + incremental fold (must agree — T2 property); flags
   {illegal-transition, unauthorized-actor, state-claim-mismatch} NON-FATAL
   (ignore+flag+retain, never error/panic — CC-020..022); one shared flag
   type.
4. Closure model (D-024): per-response sub-state keyed by XS id; verify/
   dispute target the RESPONSE; dispute reopens parent responded→
   in_progress; close only from responded (subject = parent).
5. Transition-free kinds (D-025): note + broadcast-acknowledge — exempt
   from rule 2, never flagged, never change state; broadcast acks
   accumulate per-recipient (dedup by system) — the structure P8's retire
   precondition reads.
6. Legality check (pre-write): (current folded state, candidate transition,
   actor block, actor system, membership view) → verdict in
   {legal, illegal-transition, unauthorized-actor} — same table data, the
   ONLY rejecting surface; D-027 makes exchange role checks a single
   to[0] comparison.
7. Expired overlay (3.4.7): caller-supplied reference instant; bool
   overlay, never a state, absent from the transition enum.
8. T2 properties: order-independence over valid interleavings (all-at-once
   vs incremental vs chunked) and idempotent replay (duplicate ULID
   re-application is a no-op) — property-style tests with a deterministic
   seeded shuffler (no naked rand; seed logged via t.Logf).
9. Sanity: gofmt; go vet ./internal/fold/...; go test ./internal/fold/...
   -race -count=1; go list -deps ./internal/fold/... | grep a2ahub →
   only internal/artifact (+ own).

## Constraints
- Pure: no I/O, no time.Now(), no logging, no goroutines, no maps with
  nondeterministic iteration leaking into output ordering (sort flag/ack
  output deterministically).
- t.Parallel() everywhere (or // reason:); coverage floor 70%; unblock
  recovers pre-block state FROM THE EVENT SEQUENCE (recompute, not stored).

## DO NOT
- DO NOT commit / run git / run make check / repo-wide go build|test.
- DO NOT import anything beyond stdlib + internal/artifact.
- DO NOT touch files outside internal/fold/.

## Acceptance
- Spec 04 §8 rows 1–9, each with its named go test -run target.

## Report back
- Files, tests, scoped output; the EXACT exported names/signatures of the
  fold + incremental + legality-check surfaces (P3/P6/P7/P8 build against
  them — this feeds the propagation probe).
- Deviations — REQUIRED. Anything skipped + why.
```

## Acceptance

- [ ] Spec 04 §8 rows 1–9 green (lead re-runs scoped tests + purity check).

## Phase log

### Wave 2 — 2026-07-21

- Agent: coder/sonnet/high. Files/Commits: 15 / 3309521 (+ lead rename
  FoldResult→Result before commit, package-local, pre-consumer).
- Verify: lead re-ran scoped tests post-rename + full `make check`;
  purity confirmed (go list -deps = artifact only). 106 table rows, meta-
  test pins coverage; T2 properties green.
- Deviations adjudicated → spec 04 §Amendments "from wave 2" block
  (respond self-loop, zero-events asymmetry, decision-supersede
  membership-only gap → backlog, note rule-3 extension, fail-closed
  unknown membership, single contract state, ExpiredOverlay signature).
- Epic-direction reconcile: still-serves.

## Deferred / follow-ups

- Seam-shape reconciliation with P3's LegalityChecker interface happens at
  the wave-2 propagation probe + P6 wiring.
