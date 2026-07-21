---
slug: v1-min-2026-07
phase: P5
spec: ../specs/05-space-and-host.md
wave: 2
status: planned
---

# Phase plan — P5 Space model, mirrors & GitHub host adapter

## Goal

Ship `internal/host` (5-primitive host interface + GitHub REST impl) and
`internal/space` (§4.2 layout builder, manifest load, §7.4 config files,
credential resolution, mirror clones, the D-002 write funnel with the
idempotent FindPRByHeadBranch short-circuit), plus the first cut of the
shared `testkit/spacefixture` git-fixture builder — spec 05 AC rows 1–7.

## Allowlist (repo-relative)

- `internal/host/**`
- `internal/space/**`
- `testkit/spacefixture/**` (NEW — see placement note)

## Lead-reserved / off-limits deltas

- `internal/validate/**`, `internal/schema/**` (P3, parallel),
  `internal/fold/**` (P4, parallel), `cmd/a2a/**`, `go.mod`/`go.sum`,
  `schemas/**`, `.github/**`, Makefile.

## Placement decisions (lead, binding)

- **Validate/schema seams = consumer-side interfaces, NOT imports.** P3
  writes `internal/validate`/`internal/schema` in parallel; importing them
  would compile half-written siblings. `internal/space` defines its own
  1–3-method interfaces where used (e.g. a submit-validator seam for funnel
  step 1, a manifest-validator seam for space.yaml checks), takes them in
  constructors (rails ISP/DI), and tests with fakes. cmd/a2a (P6) wires the
  real engine. ADR-001's import grant is a ceiling, not a mandate.
- **`testkit/spacefixture`** (rails pre-flight #6): P5 is the first consumer
  of throwaway git space fixtures, so it CREATES the minimal shared builder
  (local bare repo + clone, seeded §4.2 tree); P10 extends it for e2e. No
  hand-rolled per-test git plumbing.
- **Binary version for the CC-085 guard** comes via constructor DI (the
  version stamp lives in cmd/a2a; space never reads build info itself).
- **Credential references**: machine config carries `env:<VAR>` or
  `cmd:<argv...>` helper references (portable superset of "keychain");
  resolution precedence per spec Open Q1 RESOLVED: explicit A2A_* env >
  configured reference > actionable error. Secrets never persisted/logged.

## Brief

```
Stack: Go 1.26, stdlib + gopkg.in/yaml.v3 (config/manifest parse) ONLY.
Git plumbing = os/exec of system git with EXPLICIT ARGV (never sh -c);
GitHub API = net/http REST. NO new deps.
All file paths are REPO-RELATIVE.

## Goal
Implement internal/host + internal/space + testkit/spacefixture per
docs/features/v1-min-2026-07/specs/05-space-and-host.md — read END TO END
first (§T1 both API tables + "Gating needs no OpenPR parameter", §6 test
matrix, §7 funnel steps + config-file contract, §8 ACs, Open questions
1–2 RESOLVED notes), plus the Placement decisions in
docs/features/v1-min-2026-07/plans/05-space-and-host.plan.md (binding).
Then: docs/the-plan/plan/04-topology.md §4.1–4.5, 07-client.md §7.3/7.4,
10-security.md §10.3–10.5, 17-decisions.md D-001/D-002/D-019/D-026;
root AGENTS.md rails (idempotency-by-design, one write shape, config &
secrets, security rows are ALL load-bearing here); internal/artifact's
exported API (IDs, digests, frontmatter — reuse, never re-implement).

## Allowed files — REPO-RELATIVE ONLY
- internal/host/**, internal/space/**, testkit/spacefixture/**

## Off-limits (NEVER touch)
- internal/validate, internal/schema, internal/fold (parallel agents —
  do not import, do not read), cmd/a2a, go.mod, go.sum, schemas/**,
  Makefile, docs/**, .github/**

## What to do
1. internal/host: a 5-method host interface (PushBranch, OpenPR
   [UNIFORM, auto-merge always], CheckStatus, ReviewStatus,
   FindPRByHeadBranch) + GitHub impl over net/http (REST; token in
   Authorization header; io.LimitReader-bounded responses; explicit
   context.Context). No space.yaml knowledge here. Unit tests against an
   interface-level fake + an httptest server for the GitHub impl; push
   rejection (CC-061) surfaces as a typed error, no partial state.
2. testkit/spacefixture: minimal builder — bare origin repo + working
   clone(s), seeded §4.2 tree for N systems; used by space tests;
   t.TempDir-based, t.Helper, no network.
3. internal/space:
   - layout builder: §4.2 path constructors (8 type locations +
     consumes.yaml + decisions/ + vendored/), invalid system-id rejected
     via internal/artifact parsing;
   - config: .a2a/config.yaml (project: own system, connected spaces:
     repo URL + mirror-location key) and ~/.config/a2a/config.yaml
     (machine: credential REFERENCES env:<VAR>|cmd:<argv>, mirror root,
     personal defaults). os.Getenv ONLY here (config layer). Round-trip
     test asserts no secret-shaped field ever serializes;
   - credential resolution: A2A_* env > configured reference > actionable
     error naming what was checked; resolved secret passed to host calls,
     never persisted, never logged (slog redaction is cmd/a2a's, but you
     never put a secret into any struct that serializes);
   - mirror clone: clone if absent, fetch if present; non-git non-empty
     target → typed error;
   - manifest load: YAML parse + schema/policy checks via the
     consumer-side manifest-validator seam (fake in tests);
   - write funnel (D-002/D-026): (0) FindPRByHeadBranch short-circuit
     for a2a/<system>/<id> — "already done" result (idempotency,
     AC-301.1); (1) validate via the submit-validator seam + the
     min_binary_version guard (version via constructor DI; refuse write,
     stay read-only, loud warning — CC-085); (2) ONE commit = artifact
     file + its first lifecycle event (assert tree contents in test);
     (3) PushBranch; (4) OpenPR uniform; (5) return write-result
     {branch, pr number/url, commit sha, state: pending-merge} — cache
     persistence is P7's, not yours.
4. Sanity: gofmt; go vet ./internal/host/... ./internal/space/...
   ./testkit/...; go test ./internal/host/... ./internal/space/...
   -race -count=1; go list -deps for both — no a2ahub imports beyond
   artifact + own packages.

## Constraints
- Idempotency-by-design on every mutating path (rails): keyed by
  deterministic branch name, check-then-act, "already done" result.
- Data stays data (D-014): nothing from artifact content reaches argv or
  a template unsanitized; git argv is explicit slices.
- Copy the P1 error idiom; log-or-return (space/host return, transport
  logs); t.Parallel() (or // reason: for env-mutating config tests);
  coverage floor 70%; bounded reads everywhere.

## DO NOT
- DO NOT commit / run git AGAINST THIS REPO (your os/exec git targets
  ONLY t.TempDir fixtures) / run make check / repo-wide go build|test.
- DO NOT import internal/validate, internal/schema, or internal/fold.
- DO NOT make live network calls in tests (httptest only).
- DO NOT touch files outside the allowlist.

## Acceptance
- Spec 05 §8 rows 1–7, each with its named go test -run target.

## Report back
- Files, tests, scoped output; the EXACT exported shapes of: the 5-method
  host interface, the write-result struct, the two consumer-side seams
  (submit-validator, manifest-validator) — P6/P7/P8 build against these
  (feeds the propagation probe).
- Deviations — REQUIRED. Anything skipped + why.
```

## Acceptance

- [ ] Spec 05 §8 rows 1–7 green (lead re-runs scoped tests + import check).

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- Real-engine wiring of the two seams at P6 (cmd/a2a DI); live-GitHub
  integration is P10/P11.
- P10 extends testkit/spacefixture (three-system e2e space).
