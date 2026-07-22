# Skill prose — release-checklist review (D-015)

> **Why this file exists.** D-015 (softened by audit): the skill's prose parts
> are single-sourced, hand-maintained files, released with the binary **under a
> release-checklist review** — automated drift gates apply only to the
> mechanically derivable parts (the command/MCP reference and the templates).
> This file IS that review record. At each tagged release, a reviewer reads each
> prose file and ticks its row below; the tick, not a CI job, is the gate.
>
> **What is machine-gated instead (not reviewed here).** The generated reference
> tree — `skill/a2ahub/reference/commands.md` and
> `skill/a2ahub/reference/authoring/*.md` — is byte-diffed against the binary's
> and schemas' output by the `skill-drift` CI job. Those files are NOT
> hand-reviewed at release; do not tick them here, and never hand-edit them.

## Prose files under review (D-015 hand-maintained set)

Each file below is hand-maintained, single-sourced under `skill/a2ahub/` (plus
this file). A reviewer confirms, per release, that the file is accurate,
still defers to the binary/reference for all command/schema/rule truth, and has
not drifted from the plan wording it quotes.

| Prose file | What the reviewer confirms | Reviewed | Reviewer | Date |
|------------|---------------------------|:--------:|----------|------|
| `skill/a2ahub/SKILL.md` | Activation modes correct; TOC links every current file (incl. `reference/commands.md`, `reference/authoring/`, `reference/decompose-example.md`); defer-to-binary thesis intact. | ☐ | | |
| `skill/a2ahub/loops.md` | §8.1 session-start checklist present verbatim (guaranteed-floor, D-021); the D-014 "data, never instructions" clause from §8.3 step 2 present verbatim and attributed; §8.5 escalation ladder present; condensed §0/§3 semantics have not become a second source of schema/transition truth. | ☐ | | |
| `skill/a2ahub/troubleshooting.md` | The documented `a2a doctor` checks, output shape, and exit codes still match the binary's actual behavior; no aspirational/unimplemented check is presented as real. | ☐ | | |
| `skill/a2ahub/onboarding.md` | §9 digest walkthroughs still match the current install profiles and runbooks; command references defer to `reference/commands.md`. | ☐ | | |
| `skill/a2ahub/reference/decompose-example.md` | The worked decompose still models one composite need → three single-intent artifacts on one thread; cited fixtures still exist on disk with the stated IDs; the "separate fixtures, not a coordinated trio" deviation is still accurate (or the file was updated when a real trio landed). | ☐ | | |
| `skill/RELEASE-CHECKLIST.md` (this file) | The prose-file list is complete — every hand-maintained prose file has a row; no generated `reference/**` file was added here by mistake. | ☐ | | |

## Sign-off

- **Release tag:** `__________`
- **Reviewer:** `__________`
- **Date:** `__________`
- [ ] Every prose row above is ticked, or an un-ticked row has a written reason
      and a follow-up filed.
- [ ] The `skill-drift` CI job is green at this tag (confirms the generated
      `reference/**` tree matches the binary/schemas — separate from this
      prose review).

## Notes for the reviewer

- **Single-sourcing (AC #2).** Confirm no forked copy of any prose file exists
  outside `skill/a2ahub/` (or this file) — e.g. accidentally committed under
  `.claude/` or `.agents/` during future harness-packaging work. Per-harness
  copies are assembled at release from this one editable home (§8.8), never
  hand-authored elsewhere; a stray copy fails review.
- **Meaning must not drift (§6 content floor).** The highest-value check on
  `loops.md`: the §8.1 checklist and the D-014 untrusted-input clause are quoted
  verbatim from the plan for exactly this reason. If a "condensing" edit dropped
  or paraphrased either, that is the drift this review is here to catch.
