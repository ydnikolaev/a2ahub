---
name: implement
description: Execute a persisted spec from docs/features/ in a single context — research, plan-mode brief, inline self-evaluate, TDD, two-lane verification, spec amendments, tracker write-back. No sub-agent code waves; escalates to /teamlead when the work is really ≥3 independent file-disjoint phases.
user-invocable: true
argument-hint: "<slug> [phase]"
---

# /implement

Execute one persisted spec in one head. You are the **sole writer** — unlike a `/teamlead` fan-out worker, you may run the repo-wide gates yourself.

Siblings: `/discover` writes the spec this skill executes; `/teamlead` orchestrates when the work doesn't fit one context; `/quick-fix` handles small bugs without a spec.

## Procedure

1. **Read the spec end-to-end.** `docs/features/<slug>/README.md` (`kind: spec`), or for an epic phase: `docs/features/<slug>/specs/<NN>-*.md` named by the `[phase]` argument. Missing spec → stop: "run `/discover` first." Don't trust the spec's file-path claims blindly — Grep to verify; the code may have moved.

2. **Orient in the governing corpus.** The architecture plan (`docs/the-plan/plan/`) is normative: pull the R-### / AC-### / CC-### / D-### IDs the spec cites and read those sections. A spec that contradicts a D-### decision → pause and surface before writing code.

3. **Research.** Read/Grep every file the spec names plus its neighbours and tests. If the spec spans many unfamiliar areas, fan the *research only* out — template A in [_shared/cc-workflow.md](../_shared/cc-workflow.md), parallel `scout` probes returning schemas. The implementation itself stays single-context — that's what separates `/implement` from `/teamlead`.

   **Escalation gate**: research reveals ≥3 independent, file-disjoint phases → stop and suggest `/teamlead <slug>`.

4. **Plan Mode brief.** Enter Plan Mode before editing. Brief = Goal · Placement (validator core / CLI surface / schema corpus / space template — per plan §4/§5) · Constraints · Acceptance criteria (from the spec's AC table) · Step-by-step plan · Risks. Wait for approval; persist the approved brief to `docs/features/<slug>/plan.md`.

   4.5 **Inline self-evaluate.** Walk the `/self-evaluate` checklist inline (never `Skill('self-evaluate')` — that cedes control). Critical: ground truth (paths/symbols exist), spec compliance, placement, SSOT & DRY (one validator core, no second copy), roadmap (no collision with `docs/status.md` in-flight items). ⚠️ ADJUST → revise once; 🔴 STOP → surface.

5. **TDD.** Red → green → refactor. Lifecycle/state-machine work: the spec's transition table IS the test-case list. Schema work: golden fixtures (valid + invalid) first. Bugfix: reproduce → fix → regression test.

6. **Verify — two lanes** (per [.claude/rules/check-convention.md](../../rules/check-convention.md)):
   - inner loop: `make check-validators` (static/doc gates, fast);
   - THE GATE: `make check`. A static-lane pass is not a gate pass. Don't commit red; 3 failed fix attempts on the same gate → stop and surface (Round-3).

7. **Amendments (anti-drift).** If reality deviated from the spec — a different shape, path, or a silently-closed open question — append to the spec's `## Amendments` (`### <YYYY-MM-DD> — <what changed & why>`) and grep the epic's sibling specs for downstream claims your change just made false; amend them in place. A stale spec is a lie the next agent will faithfully implement.

8. **Docs & tracker.** Update `docs/status.md` if a feature completed; epic phase → flip the phase in `tracker.yaml` (`status: done`, `commits:`, bump `updated:`) — `make epic-drift` gates this.

9. **Commit** via the `/commit` conventions ([.claude/rules/commit-convention.md](../../rules/commit-convention.md)): scope = epic slug minus date suffix, session-isolated staging.

10. **Report**: what shipped · tests added · gate result · commit hash · amendments made · suggested next step.

## Stop conditions

- Ambiguous spec → ask, don't guess.
- Spec conflicts with the plan corpus or with code reality → pause, surface options.
- Scope ballooned mid-flight → escalate to `/teamlead` at a clean commit boundary.
