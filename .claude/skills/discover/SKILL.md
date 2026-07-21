---
name: discover
description: Interrogation-style discovery to draft and persist a spec or epic under docs/features/<slug>/. Asks one question at a time, no assumptions. Owns the documentation system — README/tracker/specs shapes, feature-lint conformance, status.md registration.
user-invocable: true
argument-hint: "<feature description | plan section to convert>"
---

# /discover

Transform a rough idea — or a section of the architecture plan — into a persisted spec the pipeline can execute. Interrogative: do not draft until the gating questions are answered. This skill **owns** `docs/features/` — its shapes, its lint conformance, its registration in `docs/status.md`.

## Output routing (the core decision)

Home + format are enforced by `make feature-lint` (`scripts/check-feature-lint.sh`). **Never** a flat `docs/features/<slug>.md` — the linter only scans `*/README.md`, so a flat file ships unlinted.

- **Simple feature** (single area, ≤1 week) → `docs/features/<slug>/README.md`, `kind: spec`. No tracker.
- **Epic** (multi-phase, needs sub-specs) → `docs/features/<slug>-YYYY-MM/README.md` (`kind: epic`) + `tracker.yaml` + `specs/<NN>-<area>.md` per phase.

Templates: `docs/_templates/feature/{README.template.md, spec.template.md, tracker.template.yaml}`. The README frontmatter must carry every required field (`slug title kind status owner created`) — feature-lint hard-fails otherwise; `slug` must equal the dir name.

## Algorithm

1. **Orient.** Simple scope: read `docs/status.md`, `docs/backlog.md`, and the plan index (`docs/the-plan/plan/00-STRUCTURE.md`) serially. Epic scope: read-only fan-out (template A in [_shared/cc-workflow.md](../_shared/cc-workflow.md)) — parallel `scout` probes on three lenses (`inflight`: open specs + status.md; `plan`: the governing plan sections + their D-###/R-### IDs; `history`: recent commits + backlog) returning `{overlaps[], notes}`.

2. **Scan for duplicates.** `ls docs/features/` + grep for the concept. Overlap found → surface it, ask extend-vs-new.

3. **Interrogate — one question at a time, no drafting, no assumptions.** Priority order (skip what's already known — and in this repo the plan corpus already answers much; cite the ID instead of asking):
   1. Goal & success metric
   2. Scope boundary (explicitly OUT)
   3. Primary actor (IA implementer agent / PA partner agent / HL human lead / OP operator — plan §14 personas)
   4. Data/schema delta (which object types, which envelope fields)
   5. Surface delta (CLI verbs, CI gates, space layout)
   6. Dependencies (other phases, external repos, decisions still open — Q-###)
   7. Placement (validator core / CLI surface / schema corpus / space template — plan §4/§5)
   8. Rollout & migration (which §15 phase this belongs to)

   Style: each question under two sentences; "I assume X" is banned; don't draft in chat — persist to disk.

4. **Draft** only after the gating questions are answered. README body: `## Goal`, `## Scope` (In/Out), `## User stories` (or cite plan US-### directly), `## Placement`, `## Data model / schemas`, `## Surface (CLI/CI)`, `## Acceptance criteria` (checkboxes citing plan AC-### where they exist), `## Phases` (epic), `## Open questions`.

   **Converting the plan**: when the input is the architecture plan itself (e.g. "make the v1-min epic"), the plan's §14 US/AC set and §15 phase cut are the SSOT — the epic README cites their IDs rather than restating them, each phase spec carries the relevant AC rows verbatim in its AC table, and any deliberate narrowing is recorded as an explicit deviation, never silent.

   4.5 **Decompose for parallelism (epic only).** Phase boundaries follow file/module footprints — each spec states a `**Footprint:**` line; `/teamlead` derives file-disjoint waves from footprints ∩ the `blocked_by` DAG. `blocked_by` = true data/interface deps only (every edge serializes a wave); an unbroken linear chain is a smell. Name the critical path + parallel groups in the tracker's `strategy:`. Cross-epic deps as `<slug>:PN` (the target epic must be listed in the README's `related:` — lint-checked). Suggest an epic-coherence audit right after persisting.

5. **Persist** (always a dir): simple → README only; epic → README + tracker (from the template — canonical keys only) + one spec per phase. Register the slug in `docs/status.md` §In flight; for an epic add the machine stamp `<!-- epic-state: <slug> phases=0/<total> -->` (the `epic-drift` gate holds it true from then on). Run `make feature-lint` and fix to green before reporting.

   **Spec→epic escalation** is in-place: flip `kind: spec`→`epic`, add the tracker, move phase bodies into `specs/`.

6. **Report**: spec path · phase list (if epic) · open questions · suggested next step (`/implement <slug>` or `/teamlead <slug>`).

## Don't

- Don't batch questions; don't assume; don't draft in chat.
- Don't restate plan content that has a stable ID — cite it (the plan is the SSOT; the spec is its executable projection).
- Don't create a tracker without specs on disk — feature-lint fails on a `spec:` path that doesn't exist.
- Don't leave a new slug unregistered in `docs/status.md`.
