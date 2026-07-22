# P1 — Publish boundary gate + product/harness split — Specification

**Slug**: `publish-prep-2026-07`  ·  **Track**: ci  ·  **Status**: draft
**Plan**: (created at dispatch)
**Created**: 2026-07-22  ·  **Owner**: yura
**Footprint**: `.gitignore`, `scripts/classify-guard.sh` (new), `.github/workflows/classify-guard.yml` (new), `Makefile`, `.github/workflows/ci.yml` — the two-layer publish boundary + the product/harness gate split. NON-DESTRUCTIVE: nothing is deleted or made public here; this phase only DEFINES and ENFORCES the line. Reference: sporo `.gitignore:16-27`, `scripts/classify-guard.sh`, `.github/workflows/classify-guard.yml`, `Makefile`.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-1 | As the operator, I want a fail-closed gate that refuses to track any harness/planning path, so a public push can never leak the harness. |
| US-2 | As the operator, I want the gate enforced BOTH locally (make check) and in CI, so a local `--no-verify` cannot bypass it. |
| US-3 | As a contributor on the public repo, I want `make check` to run only product gates (Go + release + security), never the private harness gates, so a public checkout's CI is self-contained and green. |
| US-4 | As the operator, I want the boundary list to have ONE source of truth shared by `.gitignore` and the guard, so the two can never silently disagree. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Sporo boundary model | `/Users/yuranikolaev/Developer/projects/sporo/.gitignore` (bottom block), `scripts/classify-guard.sh`, `.github/workflows/classify-guard.yml` | port the two-layer allow/deny gate; the CI backstop uses self-contained bash `git ls-files | grep -E` so it needs no private script |
| a2ahub current gates | [Makefile](../../../../Makefile), [.github/workflows/ci.yml](../../../../.github/workflows/ci.yml) | `check` mixes REPO_GATES (feature-lint, epic-drift — HARNESS) with the Go gates; the public lane must drop the harness gates |
| a2ahub mate boundary | [.claude/rules/harness-discipline.md](../../../../.claude/rules/harness-discipline.md) | which artifacts are mate-managed (re-deliverable via `mate pull`) vs project-owned |

---

## T5. CI pipeline (track: ci)

| Artifact | Purpose | Behavior |
|----------|---------|----------|
| `.gitignore` publish-boundary block | untrack every harness/planning path | denies `.agents/ .claude/ .codex/ .mate/ docs/ AGENTS.md CLAUDE.md scripts/*` with `!scripts/install.sh` (and `!scripts/e2e-authoring-smoke.sh` — a product script). SSOT comment points at `classify-guard.sh`. |
| `scripts/classify-guard.sh` | local fail-closed gate | ALLOW_DIRS/ALLOW_FILES/DENY_DIRS/DENY_FILES/IGNORE arrays; three checks: (a) no DENY path is `git ls-files`-tracked, (b) every top-level entry is classified (allow or deny), (c) DENY set ↔ `.gitignore` coherence. Degrades gracefully (exit 0 + note) if run in a public checkout where the harness is simply absent. Wired into the PRODUCT `make check` lane. |
| `.github/workflows/classify-guard.yml` | CI backstop | self-contained bash re-asserting the denylist (`git ls-files | grep -E '^(\.agents|\.claude|\.codex|\.mate|docs)/|^(AGENTS\.md|CLAUDE\.md)$'` with a `scripts/*` exception for the product scripts); fails the build if any private path is tracked. Runs on push + PR. |
| `Makefile` split | product vs harness gates | a PRODUCT lane (`make check` when harness gates are absent) runs gofmt/vet/lint/`go test -race`/classify-guard only; the harness gates (feature-lint, epic-drift) run ONLY when their scripts are present (private checkout). The Makefile must detect presence and not hard-fail on absence — a public checkout has no `scripts/check-feature-lint.sh`. |
| `.github/workflows/ci.yml` | product CI | the public CI job runs the product `make check` (Go gates + classify-guard), never feature-lint/epic-drift. |

**Boundary SSOT**: the DENY list lives in `scripts/classify-guard.sh`; `.gitignore` mirrors it and check (c) proves they agree — exactly sporo's design.

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Sporo's `classify-guard.sh` array structure + three-check design — port, adjust ALLOW/DENY to a2ahub's tree (add `space-template/`, `testkit/`, `schemas/`, `cc-coverage.yaml` to ALLOW; `docs/` etc. to DENY).
- [ ] The existing Makefile's `REPO_GATES` variable + presence-detection idiom (`if [ -f … ]`) — extend it so absence of harness scripts is a clean skip, not a failure.
- [ ] The existing `ci.yml` structure (setup-go, golangci-lint provision, `make check`) — keep; only ensure `make check` degrades to the product lane in a public checkout.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| classify-guard positive | with the harness present + gitignored, the guard passes | a harness path accidentally `git add`ed → guard FAILS loudly |
| classify-guard coherence | DENY set and `.gitignore` agree | a DENY entry missing from `.gitignore` (or vice versa) → FAIL |
| public-checkout degrade | simulate a checkout with no `.claude/`/`docs/` and no `scripts/check-feature-lint.sh` | guard exits 0 with a note; `make check` runs product gates only, no harness-gate failure |
| CI backstop | the `git ls-files | grep` denylist catches a tracked private path even when the local guard is skipped | — |

## 7. Schema / contract delta

None. This phase adds no product schema — it is gate + build-config only.

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1/US-4 | `.gitignore` denies every harness/planning path; `scripts/classify-guard.sh` is the DENY SSOT and passes on the current (private) tree | `bash scripts/classify-guard.sh`; deliberately `git add -f .claude/rules/…` in a throwaway index → guard FAILS |
| 2 | US-2 | `.github/workflows/classify-guard.yml` re-asserts the denylist independently (no reliance on the local script) | inspect the workflow; its grep catches a tracked private path |
| 3 | US-3 | the product `make check` lane runs Go gates + classify-guard and SKIPS feature-lint/epic-drift when their scripts are absent | rename `scripts/check-feature-lint.sh` in a throwaway copy → `make check` still green on product gates |
| 4 | US-4 | DENY↔`.gitignore` coherence check fails on a deliberate mismatch | remove one `.gitignore` line → guard check (c) reds |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | a new private path = one line in the classify-guard DENY array + `.gitignore`; the coherence check forces both. |
| Coupling | soft — the guard reads `git ls-files`, not internal state; the Makefile presence-detection means the same Makefile works in both private and public checkouts. |
| Migration path | this phase is reversible (no deletion/flip); it only adds the gate the later phases rely on. |

## 10. Implementor entry point

First phase, `blocked_by: []`. Port sporo's `classify-guard.sh` + `classify-guard.yml` + `.gitignore` block, adapting the ALLOW/DENY to a2ahub's tree. Split the Makefile so harness gates are presence-gated. Do NOT delete or gitignore-away anything yet in a way that breaks the private dev loop — the harness must still work locally (it is gitignored but present on disk; `mate pull` re-delivers it). Verify with `make check` staying green in the current private tree.

## 11. Amendments

> Append-only.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
