# P4 — Lint hardening + coverage-policy SSOT — Specification

**Slug**: `publish-prep-2026-07`  ·  **Track**: ci  ·  **Status**: draft
**Plan**: (created at dispatch)
**Created**: 2026-07-22  ·  **Owner**: yura
**Footprint**: `.golangci.yml`, `internal/coveragepolicy/` (new — a coverage-threshold SSOT package + its checker) OR a reconciliation with the existing 70% floor, `Makefile` (coverage target). Reference: sporo `.golangci.yml`, `internal/coveragepolicy/policy.go`, `Makefile` coverage target.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-1 | As the operator, the public repo's lint config is the hardened sporo set (gosec, errorlint, bodyclose, nolintlint with required explanations), so public contributions meet the bar. |
| US-2 | As the operator, the coverage threshold is a single source of truth consumed identically by local `make` and CI, so it can never drift between them. |
| US-3 | As the operator, a2ahub's existing rails (`-race`, the 70% `internal/...` floor) are RETAINED, not regressed toward sporo's weaker settings (sporo has no `-race`, a 60% global). |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Sporo lint config | `/Users/yuranikolaev/Developer/projects/sporo/.golangci.yml` | v2 schema, `default: none` + explicit enable list; gosec `excludes` each with a rationale; `nolintlint: require-explanation/require-specific` |
| Sporo coverage SSOT | `/Users/yuranikolaev/Developer/projects/sporo/internal/coveragepolicy/policy.go` | one Go package holding the threshold, consumed by both `make coverage` and CI — "a threshold can never drift" |
| a2ahub current rails | [.golangci.yml](../../../../.golangci.yml), [AGENTS.md](../../../../AGENTS.md) §Testing rails, [.claude/rules/check-convention.md](../../../../.claude/rules/check-convention.md) | current lint set + the 70% floor + `-race`; DO NOT regress these |

---

## T5. CI pipeline (track: ci)

| Artifact | Purpose | Contract |
|----------|---------|----------|
| `.golangci.yml` | hardened lint | reconcile the current config toward sporo's explicit-enable set (add gosec, errorlint, bodyclose, copyloopvar, nakedret, nilerr, nolintlint require-explanation, etc.), keeping any a2ahub-specific linters already enabled. Every `gosec`/`errcheck` exclusion carries an inline rationale. |
| `internal/coveragepolicy/` | coverage SSOT | a small Go package exposing the threshold(s) (global + per-package overrides), plus a `covercheck` entrypoint that parses `coverage.out` and fails below threshold. RETAIN the 70% `internal/...` floor (do not drop to sporo's 60%). Both `make coverage` and CI call this SAME code path. |
| `Makefile` coverage target | run coverage with the SSOT gate | `go test ./... -race -covermode=atomic -coverprofile=coverage.out` (KEEP `-race`) then `covercheck`. |

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Sporo `.golangci.yml` enable-list + settings — port, then union with a2ahub's current config; never drop an a2ahub linter.
- [ ] Sporo `internal/coveragepolicy` package pattern (SSOT consumed by local + CI) — port the shape; set thresholds to a2ahub's values (70% floor), not sporo's.
- [ ] a2ahub's existing `-race -count=1` test invocation — retain in the coverage target.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| lint config validity | `golangci-lint run` passes on the current product code with the hardened config | a newly-enabled linter that flags existing code → fix or justify with an explained `//nolint` |
| coverage SSOT | `make coverage` and the CI step read the SAME threshold; a package below floor reds | dropping a package below 70% → red |
| no-regression | `-race` still runs; the 70% floor is not lowered | grep the config + Makefile |

## 7. Schema / contract delta

None (CI + a test-only Go package).

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1 | `.golangci.yml` enables the hardened sporo linter set (incl. gosec/errorlint/bodyclose/nolintlint), each exclusion justified; `golangci-lint run` is green | run the linter on product code |
| 2 | US-2 | one coverage-threshold SSOT is consumed by both local `make` and CI | inspect: both call the same `covercheck`; a below-floor package reds both |
| 3 | US-3 | `-race` retained; the `internal/...` floor stays ≥70% (not lowered to sporo's 60%) | grep `.golangci.yml`/Makefile/policy |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | a per-package coverage override is one entry in the policy package; a new linter is one enable-list line. |
| Coupling | soft — the policy is a leaf package with no product dependency. |
| Migration path | raising the floor is a normal commit; lowering requires an explicit user-approved decision (existing rail). |

## 10. Implementor entry point

`blocked_by: [P1]` (shares the Makefile/CI product lane P1 splits). Harden `.golangci.yml` by UNION with sporo's set (never regress a2ahub's); port the coverage-policy SSOT keeping a2ahub's 70%/`-race`. Fix or explicitly justify any new lint hit. Private repo only.

## 11. Amendments

> Append-only.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
