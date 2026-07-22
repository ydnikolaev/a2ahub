# P6 — History remediation + guarded public flip — Specification

**Slug**: `publish-prep-2026-07`  ·  **Track**: ci  ·  **Status**: draft
**Plan**: (created at dispatch)
**Created**: 2026-07-22  ·  **Owner**: yura
**Footprint**: git history of `ydnikolaev/a2ahub` (re-root/rewrite), a private archive of the pre-flip repo, the harness/planning private-delivery setup, the GitHub visibility flip, and the first `v*` release tag. THE ONE-WAY PHASE — every step is user-approved before execution. Reference: sporo's model (harness delivered locally, never in the public history).

---

## 0. User stories

| ID | User story |
|----|------------|
| US-1 | As the operator, the FULL pre-publication dev history (harness + planning + every epic commit) is preserved in a PRIVATE archive before anything is rewritten — nothing is lost. |
| US-2 | As the operator, the PUBLIC repo's history contains ZERO harness/planning — not just gitignored going forward, but absent from every reachable commit. |
| US-3 | As the operator, after the flip my dev loop still works: the harness is re-delivered locally (`mate pull`) and planning lives in the private archive; I keep developing, the public repo only ever receives the product. |
| US-4 | As the operator, the public flip + first release happen only after P1–P5 are green in the private repo, and only on my explicit go. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| The publish boundary | [specs/01-publish-boundary.md](01-publish-boundary.md) | the classify-guard DENY set defines exactly what must be absent from history |
| mate delivery | [.mate/handbook/src/guides/onboard-a-project.md](../../../../.mate/handbook/src/guides/onboard-a-project.md) | `mate pull` re-delivers the mate-synced harness into a fresh checkout — the mechanism that lets the harness be gitignored yet locally present |
| Sporo end-state | `/Users/yuranikolaev/Developer/projects/sporo/.gitignore` (comment) | "these live in a separate private repo… delivered by mate fleet pull… the PUBLIC repo must never track them" — the target topology |

---

## T5. CI / process (track: ci)

> This phase is a PROCESS, executed as an explicit, user-approved runbook. Each row below is a gated step, not code the implementor writes autonomously.

| Step | Action | Gate |
|------|--------|------|
| 1. Pre-flight | confirm classify-guard (P1) is green on the current tree; confirm P1–P5 all done + green | `make check` product lane green; tracker P1–P5 `done` |
| 2. Private archive | push the CURRENT full repo (all history, harness, planning) to a PRIVATE archive remote (e.g. a private `ydnikolaev/a2ahub-archive` or a protected private branch) | operator confirms the archive exists + is private + complete |
| 3. Choose remediation | pin re-root (single clean product-only initial commit) vs `filter-repo` path-strip — recommended: re-root (simplest, guarantees no harness in ANY commit) | operator approves the technique in the plan |
| 4. Harness/planning private delivery | verify `mate pull` re-delivers the harness into a fresh checkout; move planning (`docs/the-plan`, `docs/features`, trackers) to its private home (the archive) | a fresh clean checkout + `mate pull` yields a working dev loop |
| 5. Build the public tree | on a clean branch: only the product subset tracked, harness/planning gitignored, all P1–P5 artifacts present; classify-guard + product `make check` green | guard green; `git log --all --name-only | grep <deny>` empty |
| 6. Visibility flip | make `ydnikolaev/a2ahub` PUBLIC (or push the clean history to it) | operator's explicit go |
| 7. First release | tag `v0.1.0` → the P2 release workflow runs → signed/checksummed/SBOM'd/attested archives on GitHub Releases | release assets present + verifiable |

**Product subset (tracked in public):** `cmd/ internal/ testkit/ schemas/ space-template/ go.mod go.sum .golangci.yml cc-coverage.yaml scripts/{install.sh,e2e-authoring-smoke.sh} skill/a2ahub/` (when P13 lands) + all P1–P5 artifacts (`.goreleaser.yaml`, workflows, `LICENSE`, `README.md`, `SECURITY.md`, `.gitignore`, `classify-guard.sh`, etc.).

**Never in public history:** `.claude/ .mate/ .agents/ .codex/ AGENTS.md CLAUDE.md docs/{the-plan,features,backlog,decisions,status,validator-backlog,reports,_templates} scripts/check-feature-lint.sh` + harness gate scripts.

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `mate pull` (existing) — the harness re-delivery mechanism; do NOT invent a new one.
- [ ] classify-guard (P1) — the exact DENY set that step 5's history grep must return empty for.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| history cleanliness | `git log --all --name-only | grep -E '(^|/)(\.claude|\.mate|\.agents|\.codex|AGENTS\.md|CLAUDE\.md)|(^|/)docs/(the-plan|features)/'` returns NOTHING on the public history | a stray tag/branch still referencing old history |
| dev-loop survival | a fresh clone of the PRIVATE archive + `mate pull` reconstitutes a working harness + planning | `mate pull` fails closed → resolve before flip |
| public checkout | a fresh clone of the PUBLIC repo builds (`go build ./...`), `make check` product lane green, contains no harness | — |
| archive completeness | the private archive contains every pre-flip commit (SHA count matches) | — |

## 7. Schema / contract delta

None.

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1 | full pre-flip history preserved in a private archive before any rewrite | archive remote exists, private, commit count matches pre-flip HEAD |
| 2 | US-2 | public history has ZERO harness/planning in any reachable commit | the `git log --all` grep returns empty |
| 3 | US-3 | post-flip dev loop works: `mate pull` re-delivers harness; planning lives in the archive | fresh checkout + `mate pull` → working; documented in the archive's README |
| 4 | US-4 | flip + first release only after P1–P5 done and on explicit operator go; first release is signed/checksummed/attested | tracker P1–P5 done; release assets verifiable |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Reversibility | up to step 5 everything is reversible (private); steps 6–7 are one-way — hence the archive (step 2) and the explicit operator gates. |
| Coupling | this phase consumes P1–P5; it produces nothing new except the topology change. |
| Migration path | future planning stays private (archive); future product changes flow to public normally; the dual-home dev loop is the steady state. |

## 10. Implementor entry point

`blocked_by: [P1, P2, P3, P4, P5]`. Execute as a user-approved runbook, NOT autonomously — every irreversible step (archive, re-root, flip, tag) is presented as a concrete plan and waits for the operator's explicit go. The lead investigates `.mate/` delivery deeper and pins the planning private-home mechanism before step 4. Surface the exact commands for each step; never run a history rewrite or a visibility flip without confirmation.

## 11. Amendments

> Append-only.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
