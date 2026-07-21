---
slug: v1-min-2026-07
phase: P10
spec: ../specs/10-integration-tests.md
wave: 5
status: verified
---

# Phase plan — P10 Integration tests (T3 harness, E2E-1/E2E-4, cc-coverage)

## Goal

Ship the T3 testscript harness + the T5-lite pair E2E-1/E2E-4 + the
statusline perf check + the `cc-coverage.yaml` traceability gate + wire in
the P3 secret-scan corpus and P9 compat-goldens — all as Go tests that run
under the existing `make check` `go test ./...` step. Spec 10 §8 AC 1-7.

## Placement / narrowing decisions (lead, binding)

- **No `.github/workflows/ci.yml` or Makefile edit.** `make check` already
  runs `gofmt -l · go vet · go test ./... -race -count=1` (check convention).
  Every P10 test — the `internal/e2e` T3/E2E-1/E2E-4 suites, the
  `StatuslinePerf` test in `internal/cli`, the `CCCoverageGate` test — lives
  in a package `go test ./...` already covers, so it runs in CI the moment
  it exists. The spec's §T5 "5 steps added to the test-job" and AC #6
  ("ci.yml step list includes both fixtures") are satisfied by those tests
  running under the existing `go test ./...` step, NOT by literal new YAML
  `- name:` steps. This is a NARROWING recorded in spec 10 §11 — the
  fixtures run in the same CI job; the job just already covers them. (ci.yml
  and Makefile are lead-reserved regardless; the agent never touches them.)
- **testscript drives the BUILT binary via `exec`, not in-process.**
  `cmd/a2a` is `package main` — not importable from `internal/e2e`. So the
  e2e `TestMain` builds `a2a` once (`go build -o <tmpdir>/a2a ./cmd/a2a`,
  the e2e-authoring-smoke.sh idiom) and puts that dir on `$PATH` via
  `testscript.Params.Setup`; `.txtar` scripts then `exec a2a <verb> ...` and
  assert on exit code + stdout/stderr + golden `cmp`. This keeps scripts at
  the OP-contract level (no reach into internal state) and needs no
  cmd/a2a refactor. Do NOT try to `testscript.RunMain` an in-process entry
  point — there is no importable one, by design this wave.
- **The git-space fixture is `testkit/spacefixture`, reused, not
  reimplemented.** `spacefixture.New(t, systems...)` already builds the bare
  origin + per-system clones (§4.2 tree, fixed commit env). `internal/e2e`
  wraps it for the testscript `Setup` (inject the clone/mirror dir + a
  connected `.a2a/config.yaml` + machine config with a fixture credential
  ref into the script's `$WORK`), never re-doing git plumbing.
- **Real funnel + FakeHost for the write path.** Reuse the exact idiom from
  `cmd_submit_test.go:200-245` / `cmd_lifecycle_test.go:496-522`:
  `spacefixture.New` → `fx.Clone(sys)` (mirrorDir) → real `space.NewWriteFunnel`
  over `host.NewFakeHost()` → assert `len(fake.Opens)`/`len(fake.Pushes)` and
  `WriteStateAlreadyOpen` on retry. Lifecycle path wires the funnel with a
  `nil` validator (its local legality gate precedes the funnel); submit path
  wires the `SubmitValidatorAdapter`.

## Allowlist (repo-relative)

- `internal/e2e/**` (NEW package: fixture wrapper, `TestMain`, the T3
  testscript runner, E2E-1 + E2E-4 as Go tests, the compat-golden +
  secret-corpus wiring test, the `CCCoverageGate` test, `testdata/*.txtar`,
  `testdata/` sanitization fixtures)
- `internal/cli/cmd_statusline_test.go` (ADD a `TestStatuslinePerf` — warm
  cache render <100ms; append-only, do not rewrite existing cases)
- `cc-coverage.yaml` (NEW, repo root — the traceability file)

## Lead-reserved / off-limits deltas

- `.github/workflows/ci.yml`, `Makefile`, `go.mod`, `go.sum` (lead runs
  `go mod tidy` at wave close to flip go-internal direct), `cmd/a2a/**`, all
  production `internal/*` sources (import as a black box / build the binary;
  never edit), `schemas/fixtures/**` (the secret-corpus + compat-goldens are
  WIRED, never edited/duplicated), other phases' `*_test.go` except the one
  statusline test file granted above.

## Brief

```
Stack: Go 1.26 stdlib + github.com/rogpeppe/go-internal/testscript (ADR-002,
test-only — already in go.mod as v1.15.0; your import makes it direct, the
lead runs `go mod tidy` at wave close). Reuse testkit/spacefixture,
internal/host.FakeHost, internal/space.WriteFunnel. All paths REPO-RELATIVE.

## Goal
Implement P10 per docs/features/v1-min-2026-07/specs/10-integration-tests.md
— read END TO END first (§0 user stories, §T5 CI table, §6 testing matrix,
§8 the 7 ACs, §11 Amendments incl. the 2026-07-21 testscript-normative
note). Then read the Placement/narrowing decisions in
docs/features/v1-min-2026-07/plans/10-integration-tests.plan.md (BINDING).

## Context (read in this order)
- testkit/spacefixture/spacefixture.go — Fixture API: New(t, systems...),
  RemoteURL(), Clone(system) -> mirrorDir, HeadSHA(dir, ref). This IS the
  throwaway git space fixture; wrap it, never reimplement git plumbing.
- internal/host/fake.go — NewFakeHost(); assert via len(f.Opens)/len(f.Pushes),
  f.byBranch dedup backs WriteStateAlreadyOpen on retry.
- internal/cli/cmd_submit_test.go:200-245 (submit round-trip idiom, real
  funnel + SubmitValidatorAdapter + FakeHost) and
  internal/cli/cmd_lifecycle_test.go:496-522 (lifecycle idiom, funnel with
  nil validator) and :841-879 (idempotent-retry-returns-AlreadyOpen idiom
  with SetClockForTest) — COPY these patterns.
- scripts/e2e-authoring-smoke.sh — the `go build -o $bin ./cmd/a2a` idiom
  your TestMain reuses to build the binary once.
- Plan corpus: 01-vision.md §1.3 (the 8-step north-star cascade E2E-1
  asserts — steps 2 & 7 are hub/dashboard = v2, assert only the git-fallback
  folded states + statusline signal per spec §11); 12-corner-cases.md
  CC-080/081/082 (E2E-4's exact assertions); 05-schemas.md §5.4 (retire
  preconditions/sunset/override); 13-testing.md (T3 + E2E-1/E2E-4 scope).
- Fixtures to WIRE (never author/edit): schemas/fixtures/secret-corpus/
  {positive,negative}/*.md (P3, already run by internal/validate tests —
  confirm they execute) and schemas/fixtures/compat/{additive-minor,
  mislabeled-minor}/ (P9, NOT yet consumed by any test — you author the
  test that invokes them so they run: mislabeled-minor must FAIL the
  compat check / additive-minor PASS, CC-080).

## What to do
1. internal/e2e/main_test.go: TestMain builds `a2a` once (go build -o
   <t.TempDir or a package-level tmp>/a2a ./cmd/a2a — resolve the repo root
   robustly, e.g. via runtime.Caller or a relative ../../ from the test
   file), then `testscript.Main`/`testscript.Run` with a Setup that (a) puts
   the built-binary dir on $PATH, (b) for each script stands up a
   spacefixture-backed connected project in $WORK ($WORK/.a2a/config.yaml
   pointing at the fixture space + a machine config with a cmd:/env:
   credential ref the FakeHost path tolerates OR the direct-git path uses).
   Scripts assert OP-contract level ONLY (exit codes, stdout/stderr, cmp
   against golden) — never reach into internal package state; inject fixture
   paths via env, never cwd assumptions.
2. internal/e2e/testdata/*.txtar: a per-verb T3 script for EACH shipped
   OP-2xx: init connect new validate submit sync inbox outbox show thread
   ack accept decline start block unblock cancel close withdraw supersede
   satisfy approve reject verify-pass verify-fail respond verify dispute note
   contract-new contract-publish contract-deprecate contract-retire
   contract-diff verify-export statusline doctor template-list contracts
   search (OP-201-213,215,218-221). Each: run the verb against the fixture,
   assert exit + output; where the verb mutates, assert idempotent re-run is
   a no-op (AC-301.1); offline/no-hub direct-git path works (AC-301.3,
   CC-042); a `from`-mismatch is refused locally (CC-002); a draft passes V1
   before edits (AC-401.1). EXCLUDE OP-214 html, OP-216 mcp, OP-217 update,
   `doctor --space` (all v2/P14 — spec §6 "T3 exclusions", normative).
   NOTE the exact flags from the plan's ground-truth (submit --batch/--drafts;
   lifecycle --reason/--reason-code/--refs/--findings + --actor-kind/-name/
   -model; contract publish --version/--bump/--generated-from-digest, deprecate
   --version/--successor/--sunset, retire --version/--override, diff --json,
   verify-export --local). Some verbs need a prior verb to set up state
   (e.g. ack needs an artifact to exist) — script the minimal setup inline.
3. internal/e2e/e2e1_test.go (Go test, `-run E2E1`): the full §1.3 cascade
   on the git-fallback path — requirement -> ack -> downstream requirement
   -> contract version -> response -> verify -> satisfy — via the real
   funnel + FakeHost, asserting every intermediate FOLDED state (use the
   binary's `show`/`thread`/`inbox` output as the observable, OP-contract
   level) and the statusline signal at each step. Steps 2 (hub webhook) & 7
   (dashboard) are v2 — assert the git-fallback/TTL path only (spec §11).
4. internal/e2e/e2e4_test.go (`-run E2E4`): G2 gate + linked deprecation
   with ack_requested (AC-202.1, host-simulated via FakeHost review double);
   retire BLOCKED while consumers un-acked (AC-202.2, CC-081); retire blocked
   pre-sunset / no-reminder / agent-actor, SUCCEEDS via human-reviewed
   override with retired-unacked (AC-202.3, CC-082); mislabeled-minor fixture
   FAILS compat, major required (AC-202.4, CC-080 — this is the compat-golden
   wiring). The validator-enforced halves (retire-block, compat-fail) run
   against the REAL engine and are fully real; the G2 required-review half is
   exercised via the FakeHost review double (spec §11).
5. internal/cli/cmd_statusline_test.go: ADD TestStatuslinePerf — render from
   a WARM cache completes <100ms (measure the render call, warm-cache path
   only per 13.4; cold-cache first-run is out of scope). Append-only.
6. internal/e2e/sanitization_test.go + testdata: malicious titles/bodies/
   notes (script tags, raw HTML, control chars) render INERT on the
   statusline output (spec §11 narrowed to the statusline surface — no
   dashboard/HTML in v1-min). Author these fixtures under internal/e2e/testdata/.
7. cc-coverage.yaml (repo root) + internal/e2e/cccoverage_test.go: the file
   lists {cc_id, test_ref, tier, status} rows for every CC-### the T3/
   E2E-1/E2E-4 tests + the wired P3/P9 fixtures actually exercise (CC-042,
   CC-002, CC-080, CC-081, CC-082, CC-085 if hit, CC-092, + others your tests
   touch — only the ones you really cover, a subset of §12). TestCCCoverageGate
   parses the file and FAILS if any row's test_ref does not resolve to a
   real test (resolve by `go test -list` against the named package, or a
   documented equivalent). Prove it: a deliberately-broken test_ref in a
   throwaway copy must fail the gate.
8. Wire-in proof: confirm the secret-corpus fixtures already execute under
   go test (they're consumed by internal/validate); author the compat-golden
   consuming test in step 4 so those run too. Map both into cc-coverage.yaml.
9. Sanity (scoped ONLY): gofmt; go vet ./internal/e2e/... ./internal/cli/...;
   go test ./internal/e2e/... -race -count=1 (and -run StatuslinePerf in
   internal/cli). internal/e2e MUST NOT import internal/mcp (parity is P14).

## Constraints
- No new deps beyond testscript (already in go.mod). No edit to ci.yml,
  Makefile, go.mod, go.sum, cmd/a2a, or any production internal/* source —
  the binary is a black box you BUILD and exec. t.Parallel() where the
  fixture allows (each spacefixture is t.TempDir-isolated); note any test
  that must be serial (the git-heavy ones) with a one-line reason.
- Scripts stay OP-contract-level: exit codes, stdout/stderr, golden cmp.
  Never assert against internal package state.
- Log-or-return; copy the repo's error idiom. Coverage: internal/e2e is
  test-only (no floor applies to a _test package); do not lower any floor.

## DO NOT
- DO NOT commit / run git / run make check / repo-wide go build outside your
  own package's test. Scope self-verify to internal/e2e + the one cli test.
- DO NOT edit ci.yml, Makefile, go.mod, go.sum, cmd/a2a, production
  internal/* sources, or schemas/fixtures/**. DO NOT import internal/mcp.

## Acceptance
- Spec 10 §8 AC 1-7, each with its named go test target green under -race:
  T3 (AC 1), E2E1 (AC 2), E2E4 (AC 3), StatuslinePerf (AC 4), CCCoverageGate
  (AC 5), secret+compat wired-and-run (AC 6), Sanitization (AC 7).

## Report back
- Files, tests, scoped output (the -race run of internal/e2e + the perf test).
- Deviations — REQUIRED: the testscript binary-build/PATH mechanism you
  used, how you injected the fixture into $WORK, any verb whose T3 script
  needed nontrivial state setup, the exact cc-coverage.yaml CC rows you
  claimed, and how CCCoverageGate resolves a test_ref. Any AC you could not
  fully realize (e.g. a folded-state assertion the binary output doesn't
  expose) + why.
- Anything skipped + why; any off-limits file you wished you had.
```

## Acceptance

- [ ] Spec 10 §8 AC 1-7 green (lead re-runs `go test ./internal/e2e/...
      ./internal/cli/... -race`); the harness runs under `make check`'s
      existing `go test ./...` step (no CI-YAML delta).

## Phase log

### Wave 5 — 2026-07-22
- Agent: sonnet/high. 25 files, 48 tests (internal/e2e) + TestStatuslinePerf.
- Commits: d694c9c (harness + cc-coverage + statusline perf), a0ffc38 (doctor bare-version wiring fix the harness surfaced).
- Verify (lead, re-run): `go test ./internal/e2e/... -race` exit 0 (48 pass); `make check` exit 0 after removing 2 agent dead helpers (assertStatuslineExit, cloneFresh — lint unused) + fixing doctor.txtar golden to PASS post-fix; `go mod tidy` flipped go-internal to direct.
- Deviations + downstream amendments: T3 two-mode split (txtar for read verbs / direct-construction Go tests for write verbs — built binary can't reach FakeHost, github URL hardcoded); no ci.yml/Makefile edit (tests run under `go test ./...`); E2E-1 mapped to shipped verbs (satisfy closes, separate question exchange for respond/verify); doctor version-stamp defect FIXED. All recorded in spec 10 §11 (2026-07-22). Backlog: spacefixture map-shaped participants seed; doctor dev-build version UX.
- Audit: DEFERRED to S8 epic-final (test-only phase; the sole production delta is the trivially-correct doctor wiring one-liner). The drift propagation probe returned a malformed dummy — lead ran the corpus sweep manually instead (deviations above).
- Epic-direction reconcile: still-serves (proves the L1-exit test cut).

## Deferred / follow-ups

- `go mod tidy` (flip go-internal direct) = lead, wave close.
- Spec 10 §11 narrowing (no literal ci.yml steps; fixtures run via existing
  `go test ./...`) = lead, wave-end amendment.
- Full §12 cc-coverage matrix + full §13 matrix = v2 (L4), out of scope.
