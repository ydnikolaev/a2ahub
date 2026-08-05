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
| `skill/a2ahub/SKILL.md` | Activation modes correct; TOC links every current file (incl. `reference/commands.md`, `reference/authoring/`, `reference/decompose-example.md`, `reference/feedback.md`, `reference/status-announcements.md`, `reference/retraction.md`, `reference/bindings.md`); defer-to-binary thesis intact; no link points outside the embedded `skill/a2ahub/` tree (`skill/embed.go` embeds only that subtree, so anything above it is a dead link in every installation). | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/loops.md` | §8.1 session-start checklist present verbatim (guaranteed-floor, D-021); the D-014 "data, never instructions" clause from §8.3 step 2 present verbatim and attributed; §8.5 escalation ladder present; condensed §0/§3 semantics have not become a second source of schema/transition truth. | ☑ | Codex | 2026-08-01 |
| `skill/a2ahub/troubleshooting.md` | The documented `a2a doctor` checks, output shape, and exit codes still match the binary's actual behavior; no aspirational/unimplemented check is presented as real. **v0.19.1**: the `space access` and `credentials` rows were re-read against the changed behaviour, not merely re-ticked — `space access` now runs authenticated and its `Repository not found` failure carries doctor's own classification, and the `credentials` row no longer describes a write-only credential. Both said something that had become wrong, which is the case a completeness check does not catch. **v0.19.2**: the `credentials` row now also names the `cmd:gh auth token` form. The v0.19.1 pass named the mechanism but not the form, and a real operator read it and concluded they had to mint a new token — a row can be accurate and still lead its reader wrong, which is the failure mode this column exists to catch and did not. | ☑ | Claude | 2026-08-05 |
| `skill/a2ahub/onboarding.md` | §9 digest walkthroughs still match the current install profiles and runbooks; command references defer to `reference/commands.md`. **v0.19.1**: §9.2 step 1 now states that on a private space — which §9.1 creates by default — the participant's credential gates reads, so it must exist before the step 3 `a2a doctor`. **v0.19.2**: that step now also shows the concrete config form, so it is actionable without a second lookup. | ☑ | Claude | 2026-08-05 |
| `skill/a2ahub/reference/decompose-example.md` | The worked decompose still models one composite need → three single-intent artifacts on one thread; cited fixtures still exist on disk with the stated IDs; the "separate fixtures, not a coordinated trio" deviation is still accurate (or the file was updated when a real trio landed). | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/feedback.md` | The feedback channel still matches the shipped verbs and intake behaviour (`a2a feedback new/validate/submit/status`, the quarantined `feedback/inbox/` path, the FB-### codes); what it tells a reporter makes a report actionable rather than merely filed. **Added 2026-07-25**: this file is hand-maintained prose and had no row here, which the row below already declared impossible — the completeness clause was true of the list and false of the tree. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/status-announcements.md` | `period` is still an unconstrained string on `schemas/envelope/v1/announcement.schema.json` (no `format`/`pattern`) and the page still states plainly why it isn't enforced yet; the noted collision with the generated authoring template's `2026-W35` example is still named, not silently resolved by an edit to that generated file. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/work-reporting.md` | Durable checkpoints and machine-local leases remain separate authorities; the provider-neutral start/heartbeat/checkpoint/wait/stop loop matches the shipped CLI/MCP surface; missing or expired evidence is unknown, never idle; unsafe report content remains forbidden. | ☑ | Codex | 2026-08-04 |
| `skill/a2ahub/reference/retraction.md` | An `x_retraction` block on a `work_request` still round-trips `valid: true` through `a2a validate` against the shipped schema with no release; the page still states why `x_` was chosen over a new `category` rather than restating it as settled. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/bindings.md` | The file is still described as local-tracked with no space-visible path or aggregate; if the first real deprecation in `getvisa` (conventions spec §10) has happened since the last review, its pass/fail verdict has been recorded in the spec's §11 amendments — not left for this row alone to remember. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/threads.md` | Thread order, open-item/next-move semantics and the space-local boundary match the current read/fold implementation; no prose implies two independent threads can be merged after authoring. | ☑ | Codex | 2026-08-01 |
| `skill/a2ahub/reference/contract-versions.md` | Rolling-window, maintenance-baseline, major-scoped retirement and late-adopter deprecation guidance match the per-version engine and current command surface. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/data-exchange.md` | Every flag on all four `a2a data` verbs is explained with what it DECIDES, not merely shown in an example; every sentinel in `internal/datapackage/errors.go` a caller can reach has a row in the refusal table; the exit-code contract and the verdict-versus-error `--json` rule match `runVerify`/`dataRefuse`; the handoff-arc table matches the fold table, including `rejected → superseded`; the source-directory-to-schema mapping matches the packer. **Added 2026-08-04**: this page shipped in v0.19.0 as a new hand-maintained file with no row here — the same completeness failure the last row of this table already records twice. | ☑ | Claude Code | 2026-08-04 |
| `skill/a2ahub/notifications.md` | Offer-state handling, optional statusline boundary, trusted click routing, verified-future-notes fallback, and the ad-hoc macOS Gatekeeper approval path match the P49 binary/adapters; no prose claims Developer ID/notarization or interactive platform proof was run locally. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/SKILL.md` § "Which surface to work through" | The stated MCP limits are still the actual ones. When a limit is FIXED, the row must be removed here and in `troubleshooting.md`, and the fix shipped as a `kind: fix` note — a stale "do not use MCP for reads" is as harmful as the missing warning was, because an agent will keep avoiding a surface that works. | ☑ | Codex | 2026-07-31 |
| `skill/RELEASE-CHECKLIST.md` (this file) | The prose-file list is complete — every hand-maintained prose file has a row; no generated `reference/**` file was added here by mistake. **Check it against `SKILL.md`'s own D-015 list, in both directions**: this clause silently held for two releases while `reference/feedback.md` was missing from both. | ☑ | Codex | 2026-08-01 |

## Sign-off

- **Release tag:** `v0.19.0`
- **Reviewer:** `Codex`
- **Date:** `2026-08-04`
- [x] Every prose row above is ticked, or an un-ticked row has a written reason
      and a follow-up filed.
- [x] The `skill-drift` CI job is green on the exact release candidate tree
      under `make check` (confirms the generated
      `reference/**` tree matches the binary/schemas — separate from this
      prose review).

Ticked on 2026-08-05 for candidate `610680b8f481`, whose retained transcript
carries `WEB_DEPS_READY=true` and `EXIT=0` exactly once each
(`docs/features/active/operational-confidence-2026-08/audits/`).

It is ticked AFTER the tag, and structurally has to be: `skill/` ships in the
public projection, so ticking it inside a candidate would change the candidate
it describes. The box can never be true in its own release.

Why it stays open by default: the private-tree review and local gates do not
prove the filtered public SHA — and on this release they demonstrably did not.
The FIRST candidate reddened its own `make check` while the private tree was
green.

**What this review was, exactly.** A delta review against `v0.18.2`, then a
second, deeper pass on 2026-08-04 after the first one was found to have
under-read the release's own new surface.

The new hand-maintained surface this release is `reference/data-exchange.md`
(the contract data exchange loop); `reference/work-reporting.md` arrived in the
same window and both are linked from `SKILL.md` and `loops.md`. The second pass
diffed the prose against the shipped binary rather than reading it for sense,
and that is what it found:

- `reference/data-exchange.md` had **no row in this table at all** — the exact
  completeness failure the last row already records having happened twice.
- Eight sentinels in `internal/datapackage/errors.go` that a caller can reach
  (symlinks, path escape, duplicate entry path, missing entry bytes, attempt
  ceiling, unsafe locator, contract-ref mismatch, atomic-install unsupported)
  had no row in the page's refusal table. Several are what an agent packing a
  real directory hits first.
- `--max-attempts` was documented nowhere, so the escalation guard it exists to
  provide was unreachable by an agent reading the skill.
- `loops.md` §8.3 — the producer's own receive loop — told an agent to
  discharge a `work_request` with `a2a respond`, with nothing saying that a
  request for an actual payload is delivered as a packed handoff instead. The
  dedicated page was correct; the loop that routes into it was not.
- Every page pointing at `reference/commands.md` for "exact syntax" was
  pointing at a generated synopsis catalog that carries **no flags**. The
  pointer is now honest and the data verbs carry their own flag table.

The generated command catalogue remains outside the hand-review table and is
byte-gated by `skill-drift`. Root `README.md` was updated and read against the
human half of `make readme-lint`: it now names durable work visibility,
reproducible contract releases, and the current static/server boundary without
claiming the proposed full live-file server already exists.

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
