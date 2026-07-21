# P10 — Integration Tests: T3 Harness, E2E-1/E2E-4, cc-coverage Seed

**Slug**: `v1-min-2026-07`  ·  **Track**: ci  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Footprint**: `internal/cli/*_test.go`, `internal/space/*_test.go` (T3
additions to the per-verb packages P6/P7/P8 already own), `internal/e2e/`
(new — throwaway git space fixture builder, `internal/e2e/testdata/`
sanitization fixtures, E2E-1 and E2E-4 scripts), `cc-coverage.yaml` (product
repo root), `.github/workflows/ci.yml` (test-job step additions only — the
job itself is P1's). May import: `internal/e2e` may import any
`internal/{artifact,schema,fold,validate,host,space,cache,template,cli}`
package (ADR-001 core-package rules apply to production code; test-only
harness code reusing `space`'s mirror-clone helpers is not a boundary
violation) plus stdlib `testing`/`os/exec`/`net/http/httptest` for the
simulated host, plus `github.com/rogpeppe/go-internal/testscript` (ADR-002,
test-only) — the normative T3/T5-lite driver. Never imports `internal/mcp` —
parity is P14 (tail item).

---

## 0. User stories

| ID | User story |
|----|------------|
| US-1 | As the operator (OP), I want the §1.3 north-star cascade proven end to end in product CI (not just described) so I can trust a release before it reaches the real getvisa space. |
| US-2 | As an implementer agent (IA), I want every v1-min-shipped OP-2xx verb exercised against a throwaway git space fixture so a regression in P6/P7/P8/P9 is caught before merge, not after a real team hits it. |
| US-3 | As a system owner (HL), I want the breaking-change gate (G2, deprecation, consumer acks, blocked retire) scripted and green so I know CC-080/081/082 actually hold, not just that the plan describes them. |
| US-4 | As the operator (OP), I want every corner case the shipped tests claim tracked in `cc-coverage.yaml` and CI-enforced so §12's catalog can't silently drift from test reality (13.2). |
| US-5 | As an agent (IA), I want the statusline's <100 ms render budget checked in CI so a regression there doesn't quietly break R-001's proactivity promise. |
| US-6 | As a system owner (HL), I want the secret-scan corpus, compat goldens, and sanitization fixtures wired into the same CI run they're specified for (13.4), not left as prose. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Test tiers T1–T6, E2E minimum set, non-functional checks | [13-testing.md](../../../the-plan/plan/13-testing.md) | this phase builds T3 + the T5-lite pair E2E-1/E2E-4 only; T1/T2/T6 are owned by P2/P3/P4/P9 |
| v1-min test cut | [15-rollout.md](../../../the-plan/plan/15-rollout.md) L1 exit + "V1-min re-cut" | "core tests (T1 goldens, T2 fold, slim T3, E2E-1 + E2E-4)" is the literal v1-min scope; L4 owns the full §13 matrix + cc-coverage full enforcement |
| North-star cascade | [01-vision.md](../../../the-plan/plan/01-vision.md) §1.3 | the 8-step script E2E-1 must assert against; steps 2 (hub webhook) and 7 (dashboard/local HTML) are v2 surfaces — see §11 Amendments |
| Breaking-change corner cases | [12-corner-cases.md](../../../the-plan/plan/12-corner-cases.md) CC-080/081/082 | the exact assertions E2E-4 must make |
| Versioning & compat policy | [05-schemas.md](../../../the-plan/plan/05-schemas.md) §5.4 | retire preconditions, sunset, gated-override — what E2E-4's validator half exercises |
| Validation matrix | [05-schemas.md](../../../the-plan/plan/05-schemas.md) §5.5 (D-011) | V1–V5 invocation points; T3 exercises V1/V2 via the CLI, V3-equivalent logic is P9's space-template CI, not re-tested here |
| OP-2xx verb catalog | [07-client.md](../../../the-plan/plan/07-client.md) §7.1 table | source for "each shipped OP-2xx"; §7.1 also states the CLI/MCP parity invariant this phase does NOT test (P14 owns it) |
| Package boundaries | [decisions.md](../../../decisions.md) ADR-001 | footprint packages and import rules above map 1:1 to this table |
| AC rows exercised (cited, not owned) | [14-us-ac.md](../../../the-plan/plan/14-us-ac.md) E1–E4/E6 | AC-301.1/.3, AC-401.1, AC-202.1–.4, AC-601.2, AC-203.1 — all owned by their phases; P10 is the CI wiring that proves them, ownership stays with P6–P9. NOTE: AC-502.1 (E5 hub inbox latency) is NOT cited — E5 is not in the README epic mapping at all, hub is deferred to v2 in full (15-rollout.md), not merely narrowed |

---

## T5. CI pipeline (track: ci)

| Workflow | Trigger | Steps (added to the existing test-job) | Gate it enforces |
|----------|---------|------------------------------------------|-------------------|
| `.github/workflows/ci.yml` test-job | push, PR (same triggers as P1's existing job) | run the `internal/e2e` fixture-builder-backed T3 suite | every v1-min-shipped OP-2xx round-trips against a throwaway local git space fixture without error (AC-301.1/.3, AC-401.1) |
| `.github/workflows/ci.yml` test-job | push, PR | run the E2E-1 script | the §1.3 cascade folds correctly end to end on the git-fallback path; every intermediate folded state and statusline signal asserted (13.3 E2E-1, AC-601.2) |
| `.github/workflows/ci.yml` test-job | push, PR | run the E2E-4 script | G2 gate + deprecation + consumer acks + blocked retire hold per CC-080/081/082 (13.3 E2E-4, AC-202.1–.4) |
| `.github/workflows/ci.yml` test-job | push, PR | statusline perf check | render <100 ms from cache (13.4, AC-601.2) |
| `.github/workflows/ci.yml` test-job | push, PR | `cc-coverage.yaml` gate | a CC listed in the file without a resolvable named test fails the build (13.2 exit criterion, quoted below) |

> §13.2: "every CC-### in §12 mapped to ≥1 named test (traceability file
> `cc-coverage.yaml` in the product repo, CI-enforced: a CC without a test
> fails the build — this makes §12's append-only growth self-enforcing)."

`cc-coverage.yaml` scope this phase seeds: only the CCs the T3/E2E-1/E2E-4
tests this phase ships actually exercise (a subset of §12) — the full
matrix is explicitly deferred to v2 per the README epic-level AC and
L4's exit criterion ("every CC mapped to a green test", 15-rollout.md).

### cc-coverage.yaml shape (described, not pasted — this is a contract file, not code)

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `cc_id` | string | yes | matches a §12 `CC-###` exactly |
| `test_ref` | string | yes | package path + test name the CI gate resolves and runs |
| `tier` | string | yes | one of `T1`\|`T2`\|`T3`\|`E2E-1`\|`E2E-4` (the tiers this phase and its predecessors actually ship) |
| `status` | string | yes | `covered` (default check) — a row with no matching resolvable test fails the gate |

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `internal/space`'s mirror-clone / local bare-repo helpers (P5) — the
  fixture builder wraps these, it does not reimplement git plumbing.
- [ ] P1's existing product-repo CI job structure (`ci.yml`) — this phase
  adds steps to the existing test-job, it does not create a second workflow.
- [ ] P9's space-template V3 CI workflow shape as the model for what "PR
  gate" mechanics the fixture's simulated host must reproduce (diff-authz,
  compat 5.4b) — reused conceptually; P9 owns the real V3 workflow, P10's
  fixture only needs enough of a host stand-in to drive CC-081/082/080.
- [ ] Grep P2/P3's secret-scan corpus and P9's compat-goldens fixtures
  before adding new ones — this phase wires them into the CI run, it does
  not re-author fixtures those phases already own (see §11 Amendments,
  footprint boundary note).

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| T3 fixture builder | a local bare repo + N clones simulating three systems (per SCOPE); builds/tears down per test | clone reuse across cases must not leak state between tests |
| T3 mechanism (normative) | per-verb suites are `testscript` `.txtar` scripts (`rogpeppe/go-internal/testscript` per ADR-002 and AGENTS.md §Testing rails): `testscript.Main` registers the `a2a` binary once; each script runs verbs against a fixture space wired in via `Setup`; assertions on stdout/stderr/exit codes + `cmp` against golden files | scripts must stay OP-contract-level (exit codes, JSON output) — never reach into internal package state; fixture path injection via env, not cwd assumptions |
| T3 per-verb suite | each v1-min-shipped OP-2xx run against the fixture: `init connect new validate submit sync inbox outbox show thread lifecycle-verbs contract-new/publish/deprecate/retire verify-export statusline doctor template-list submit-batch search contracts contract-diff` (OP-201–213, 215, 218–221) | idempotent re-run is a no-op (AC-301.1); offline/no-hub path works via direct git (AC-301.3, CC-042); `from` mismatch refused locally (CC-002); draft passes V1 before edits (AC-401.1) |
| T3 exclusions | OP-214 (`html`, v2 per 15-rollout.md), OP-216 (`mcp`, P14 tail), OP-217 (`update`, v2), `doctor --space` (v2 per README) — none are shipped in v1-min, none are exercised here | — |
| E2E-1 | full §1.3 cascade: requirement → ack → downstream requirement → contract version → response → verify → `satisfy`; assert every intermediate folded state and the statusline signal at each step | asserts the git-fallback (no-hub) path only — see §11 Amendment on step 2/7 scope |
| E2E-4 | major contract PR requires G2 + linked `deprecation` with `ack_requested` (AC-202.1); retire blocked while consumers un-acked (AC-202.2, CC-081); retire blocked pre-sunset / no reminder / agent-actor, succeeds via human-reviewed override PR with `retired-unacked` flag (AC-202.3, CC-082); mislabeled-minor fixture fails V3-equivalent compat check, major required (AC-202.4, CC-080) | see §11 Amendment on which half is validator-real vs host-simulated |
| statusline perf | render <100 ms from cache, warm-cache path (13.4, AC-601.2) | cold-cache first-run path is out of scope for the perf budget (13.4 says "from cache") |
| secret-scan corpus (wired, not authored) | P2/P3's known-pattern fixtures block; benign lookalikes pass in the same CI run (13.4, AC-203.1) | false-positive budget documented by the owning phase, referenced here |
| compat goldens (wired, not authored) | P9's mislabeled-minor-fails / additive-minor-passes fixture pairs run in the same CI job (13.4, 5.4b, CC-080) | — |
| sanitization fixtures (authored here — no upstream owner, homed under `internal/e2e/testdata/`) | malicious titles/bodies/notes (script tags, raw HTML, control chars) render inert on statusline (13.4, narrowed — see §11) | v1-min has no dashboard/local-HTML render surface to also assert against |

## 7. Schema / contract delta

None. This phase adds no product schema fields; `cc-coverage.yaml` is a
test-traceability file, not a product artifact schema (its shape is the
table in §T5 above, not a `schemas/` addition).

## 8. Acceptance criteria

> Written by the spec author; the implementor does NOT modify them.

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | — | T3 harness runs each v1-min-shipped OP-2xx (OP-201–213, 215, 218–221) against a throwaway local git space fixture (bare repo + 3-system clones) and every run is green, idempotent-safe | `go test ./internal/e2e/... ./internal/cli/... -run T3 -race -count=1`; exercises AC-301.1, AC-301.3, AC-401.1 |
| 2 | — | E2E-1 (full §1.3 cascade, git-fallback path) runs green in product CI, asserting folded state and statusline signal at every intermediate step | `go test ./internal/e2e/... -run E2E1 -race -count=1`; exercises AC-601.2, 13.3 E2E-1 |
| 3 | — | E2E-4 (G2 gate, deprecation, consumer acks, blocked retire, override, mislabeled-minor backstop) runs green in product CI | `go test ./internal/e2e/... -run E2E4 -race -count=1`; exercises AC-202.1, AC-202.2, AC-202.3, AC-202.4, CC-080, CC-081, CC-082 |
| 4 | — | statusline render completes <100 ms from warm cache, measured in the CI job | `go test ./internal/cli/... -run StatuslinePerf -race -count=1`; exercises AC-601.2, 13.4 |
| 5 | — | `cc-coverage.yaml` exists at repo root, lists every CC-### the T3/E2E-1/E2E-4 tests in this phase (and P2/P3/P9's wired-in T1 fixtures) claim, and the CI test-job fails the build if any listed CC has no resolvable `test_ref` | inspect `cc-coverage.yaml`; `go test ./internal/e2e/... -run CCCoverageGate -count=1` (or the equivalent CI step) fails on a deliberately-broken `test_ref` in a throwaway copy |
| 6 | — | P2/P3's secret-scan corpus and P9's compat-goldens fixtures are invoked from the same CI test-job this phase edits (not merely present in their own packages, unrun) | `.github/workflows/ci.yml` test-job step list includes both; `go test` output shows both suites executed on the same CI run |
| 7 | — | Sanitization fixtures (script tags, raw HTML, control chars in title/body/notes) render inert on the statusline output | `go test ./internal/e2e/... -run Sanitization -race -count=1`; exercises 13.4 narrowed to the statusline surface (see §11) |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | `cc-coverage.yaml`'s flat `cc_id`/`test_ref`/`tier` shape absorbs new CCs and new tiers (T4, T6, E2E-2/3/5–10) without a format change — v2's full-matrix enforcement (L4) is additive rows, not a rewrite. |
| Coupling | Soft — the fixture builder depends on `internal/space`'s public clone API, not its internals; E2E scripts depend on CLI verb exit codes/output shape (OP-2xx contracts), not on internal package state. |
| Migration path | low — v2 adding T4/T6/E2E-2/3/5–10 and full cc-coverage enforcement extends this harness, doesn't replace it. |
| Roadmap conflicts | P14 (MCP parity, AC-301.2) is `blocked_by: [P10]` and deliberately does not reuse this phase's fixture for parity assertions until MCP exists — no overlap risk. |

## 10. Implementor entry point

Execute as one wave of the `v1-min-2026-07` epic, `blocked_by: [P7, P8, P9]`
per `tracker.yaml` (needs the read surface, lifecycle/contract verbs, and
the space-template V3 CI shape to exist first). TDD default: write the
fixture builder and the failing T3/E2E-1/E2E-4 tests against the already-
shipped P6–P9 verbs first, then wire the CI steps. Framework-first;
log-or-return. Full loop: [docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. Deviations from plan §13/§15 wording, disclosed up front per
> the epic SSOT rule (README.md "a deliberate narrowing must name itself").

- **Sanitization surface narrowed.** 13.4 specifies fixtures MUST render
  inert "on dashboard, local HTML, and statusline." Dashboard (11.1) and
  local HTML (7.6/OP-214) are v2-deferred (15-rollout.md V1-min re-cut).
  v1-min asserts render-inert on the **statusline only** — the sole
  v1-min-shipped render surface. Re-verified against dashboard/local HTML
  when v2 ships them.
- **E2E-1 narrowed to the git-fallback path.** §1.3 step 2 is a hub webhook
  notification and step 7 is the hub dashboard / local HTML view — both v2
  surfaces (hub entirely deferred per 15-rollout.md; E5 hub service is not
  in the README epic mapping at all, so its AC-502.1 hub-latency clause is
  not a v1-min AC to begin with, not merely a branch to skip). This phase's
  E2E-1 asserts the cascade's folded states and the statusline signal via
  the git-fallback / TTL-default path (AC-601.2), never the hub-webhook-
  driven seconds-latency path.
- **T3 excludes CLI/MCP parity.** 13.1 bundles "CLI/MCP parity suite (7.1)"
  into T3's definition. MCP does not exist until P14 (`blocked_by: [P10]`,
  AC-301.2). This phase's T3 is CLI-only; the parity suite is P14's.
- **E2E-4's host half is simulated, not real GitHub.** The fixture is a
  local bare repo + clones (no real host). The validator-enforced
  assertions (retire-block CC-081/082, compat-fail CC-080, AC-202.2/.3/.4)
  run against the real validation engine and are fully real. The G2
  required-human-review / CODEOWNERS mechanics (AC-202.1's merge-requires-
  review clause) are exercised via the `internal/host` adapter's test
  double, standing in for the GitHub-side gate — not a live PR review.
- **Fixture authorship boundary.** Secret-scan corpus fixtures are P2/P3's
  (T1, AC-203.1); compat-goldens fixtures are P9's (V3 compat 5.4b,
  AC-202.4/CC-080). This phase wires both into the same CI test-job it
  edits and maps them into `cc-coverage.yaml`; it does not author or
  duplicate them. Sanitization fixtures have no upstream owner in the plan
  and are authored here under `internal/e2e/testdata/`.

### 2026-07-21 — testing environment made normative (pre-implementation)

- **`testscript` is the T3/T5-lite mechanism**, not an implementation choice:
  `rogpeppe/go-internal/testscript` (the harness `cmd/go`'s own tests use)
  added to ADR-002 as test-only; per-verb suites are `.txtar` scripts under
  `internal/e2e/testdata/`, asserting on the OP contract (exit codes,
  JSON/stdout, golden `cmp`) against the throwaway git space fixture. See
  AGENTS.md §Testing rails; footprint and §6 updated to match.
