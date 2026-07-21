---
slug: v1-min-2026-07
phase: P8
spec: ../specs/08-lifecycle-contract-verbs.md
wave: 4
status: verified
---

# Phase plan — P8 Lifecycle & contract verbs

## Goal

Ship the OP-211 generic lifecycle verbs (`cmd_lifecycle.go`), the OP-212/213
+ `contract diff` verbs (`cmd_contract.go`), the retire-precondition policy
check (`internal/validate/policy_retire.go` + registry rows), and close the
verify/dispute legality gap in `internal/fold` (the wave-3 backlog item, now
with a real consumer). Spec 08 AC rows AC-302.1/202.2/202.3/1001.1 + P8-1..4.

## Allowlist (repo-relative)

- `internal/cli/cmd_lifecycle.go` (all OP-211 verbs)
- `internal/cli/cmd_contract.go` (OP-212/213 + `contract diff`)
- `internal/validate/policy_retire.go` (retire-precondition policy hook)
- `internal/fold/legality.go` + `internal/fold/legality_test.go` (ADD the
  verify/dispute response-scoped legality case — see placement)
- `schemas/errors/v1/registry.yaml` (APPEND retire/lifecycle POL/LFC rows only)

## Lead-reserved / off-limits deltas

- `internal/cli/cli.go`, `adapters.go`, all P6 verb files, all P7 verb files
  + `internal/cache/**` (parallel agent — never touch/import), `cmd/a2a/**`
  (lead wires dispatch), `go.mod`, other `internal/*` core sources.

## Placement decisions (lead, binding)

- **verify/dispute legality closes in `internal/fold`** (wave-3 backlog,
  now consumed by P8's verify/dispute verbs): `CheckLegality` currently has
  no verify/dispute case (they target a RESPONSE, not the parent — need
  Kind=KindResponse + the response's substate + the parent's envelope). Add
  that case to `internal/fold/legality.go` with tests. P8 is the sole writer
  of internal/fold this wave (P7 only imports it) — safe. Keep fold pure.
- **Every verb goes through the P5 write funnel, uniform** (auto-merge always
  on) — NO verb passes a gate/review parameter. Gating = advisory PR marker
  + CODEOWNERS + V3 (downstream, P9). Reuse the SubmitValidatorAdapter /
  LegalityAdapter / write-funnel wiring pattern P6 established (read
  adapters.go + cmd_submit.go — do NOT duplicate the funnel call).
- **`contract new` translates its positional `<slug>` into `--slug`** when
  delegating to P6's `a2a new contract` path (wave-3 backlog) — do not
  forward args verbatim; P6's NewCommand takes the slug as a flag.
- **retire-precondition** = registered consumers (satisfied requirements ∪
  `consumes.yaml` entries, §5.2.3/D-022; `left` systems excluded, §5.4)
  must all be acked; the check lives in policy_retire.go, returns a
  policy-class violation with a NEW POL-### code (append to registry).
  Override path (AC-202.3): refused locally unless sunset passed AND ≥1
  `note` reminder on the deprecation thread AND actor is human.
- **digest trees** (publish/diff/verify-export) all call the SAME
  `internal/artifact` multi-file digest helper (§5.7/D-029) — one impl.
- Each verb is a `cli.Command`; no shared package-level state with P7 (both
  write into internal/cli — disjoint files, file-private helpers only).

## Brief

```
Stack: Go 1.26 stdlib (flag verbs, encoding/json). NO new deps. Imports per
ADR-001 cli row + internal/fold (legality) + internal/validate (pipeline +
new policy hook) + internal/space (funnel + mirror read) + internal/artifact
(digests) + internal/schema. NO internal/cache, NO internal/mcp, NO direct
internal/host. All file paths REPO-RELATIVE.

## Goal
Implement the lifecycle + contract verbs + the retire policy hook + the
fold verify/dispute legality case per
docs/features/v1-min-2026-07/specs/08-lifecycle-contract-verbs.md — read END
TO END first (T1 both verb tables incl. every flag/gate-posture note, §5
reuse, §6 tests, §8 ACs, the 3 Open questions), then the Placement decisions
in docs/features/v1-min-2026-07/plans/08-lifecycle-contract-verbs.plan.md
(BINDING).

## Context (read in order)
- The lead seam internal/cli/cli.go; P6's cmd_submit.go + adapters.go (the
  funnel-call + LegalityAdapter/SubmitValidatorAdapter pattern you REUSE,
  never duplicate — every verb here authors an event/v1 and ships via the
  same write funnel).
- Root AGENTS.md rails (idempotency by design, one write shape, error flow,
  testing rails).
- internal/fold source (Kind/State/Event/Envelope, CheckLegality, the
  closure model helpers) — you EXTEND legality.go for verify/dispute.
- internal/validate source (the V2 pipeline + Class/Violation/registry
  loader) — policy_retire.go plugs a new policy-class check.
- internal/artifact digest helpers; internal/space funnel + mirror read.
- Plan corpus: 03-domain.md §3.4 (EVERY transition — §3.4.6 closure model
  is load-bearing for verify/dispute/close), §3.5, §3.7 (G1-G3);
  05-schemas.md §5.3/§5.4/§5.4a/§5.4b/§5.7, D-010/D-022/D-023/D-029;
  07-client.md §7.2 OP-211/212/213/221.

## Allowed files — REPO-RELATIVE ONLY
- internal/cli/cmd_lifecycle.go, internal/cli/cmd_contract.go
- internal/validate/policy_retire.go
- internal/fold/legality.go, internal/fold/legality_test.go (verify/dispute
  case ADDED — do not rewrite existing cases)
- schemas/errors/v1/registry.yaml (APPEND new POL-/LFC- rows only; never
  edit an existing row)

## Off-limits (NEVER touch)
- internal/cache + all P7 verb files (cmd_inbox/outbox/show/thread/search/
  statusline) — a parallel agent owns them; never touch or import.
- internal/cli/cli.go, adapters.go, P6 verb files, cmd/a2a, go.mod, other
  internal/* core sources (import, never edit — except the two fold files
  and policy_retire.go granted above).

## What to do
1. internal/fold/legality.go: add the verify/dispute response-scoped case
   to CheckLegality (Kind=KindResponse, response substate, parent envelope),
   with legality_test.go cases (legal verify from responded-response,
   illegal from wrong state, unauthorized actor). Pure, no I/O.
2. internal/validate/policy_retire.go: the retire-precondition policy-class
   check (registered-consumer ack set per §5.4/D-022; `left` excluded);
   returns a Violation with a new POL-### code; add the row(s) to
   registry.yaml (+ any LFC- rows a lifecycle-legality violation needs that
   don't exist yet). Closure test: unacked → violation; all-acked → clean.
3. internal/cli/cmd_lifecycle.go: one cli.Command dispatching the OP-211
   verbs (ack/accept/decline/start/block/unblock/cancel/respond/verify/
   dispute/close/supersede/withdraw/satisfy/approve/reject/verify-pass/
   verify-fail/note) with the flags in T1; every mutating verb: batch (N
   IDs → one commit/one PR), V2 before ship (foreign-section + legality
   refusal BEFORE funnel, reuse P6's guard pattern), uniform funnel call
   (no gate param); approve/reject/publish add an advisory PR marker only.
4. internal/cli/cmd_contract.go: contract new (slug→--slug delegate to P6
   new-path), publish (--version/--bump, records SHA + multi-file digest
   tree, G1/G2 advisory marker), deprecate (event + linked deprecation
   announcement in one PR), retire (calls policy_retire hook; override
   path), diff (read-only, resolve versions via publish-event → SHA, mirror
   read, path-level delta, --json), verify-export --local (multi-file
   digest compare, exit 0/nonzero).
5. Sanity: gofmt; go vet ./internal/cli/... ./internal/validate/...
   ./internal/fold/...; go test for the three packages -race -count=1;
   go list -deps ./internal/cli/... must NOT show internal/cache.

## Constraints
- Reuse internal/fold legality + internal/validate pipeline — NEVER
  re-derive §3.4 transition logic in a verb.
- One digest-tree helper (internal/artifact) for publish/diff/verify-export.
- cli.Command.Run returns exit code; only cli.IO; snake_case --json.
- Copy the P1 error idiom; log-or-return; idempotent re-run test per
  mutating verb; t.Parallel() (or // reason:); coverage floor 70%;
  registry stays SSOT (append only, code mirrors data).

## DO NOT
- DO NOT commit / run git / run make check / repo-wide go build|test.
  Scope tests to your own packages.
- DO NOT touch internal/cache, P6/P7 verb files, cli.go, adapters.go,
  cmd/a2a, or rewrite existing fold/registry entries.

## Acceptance
- Spec 08 §8 rows (AC-302.1, AC-202.2, AC-202.3, AC-1001.1, P8-1..4), each
  with a named go test target against fixture spaces (reuse testkit/
  spacefixture; host via host.NewFakeHost).

## Report back
- Files, tests, scoped output; the verb constructor signatures + the
  policy_retire + fold-legality additions (cmd/a2a wiring + P9/P12 consume
  — feeds the probe).
- Deviations — REQUIRED (esp. the 3 Open questions' resolutions, any funnel/
  adapter reuse friction, new registry codes added).
- Anything skipped + why.
```

## Acceptance

- [ ] Spec 08 §8 rows green (lead re-runs scoped tests; live contract/
      lifecycle flows exercised in the P10/P11 integration pass).

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- cmd/a2a wiring of the new verbs = lead, post-wave.
- V3 CI wiring of the retire hook = P9 (already shipped the workflow; P10
  reconciles the actual verb/flags).
- axon CI wiring of verify-export = P12.
