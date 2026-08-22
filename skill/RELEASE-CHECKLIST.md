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

### v0.23.0 — what was reviewed, and what was not

**Three rows carry today's date because three pages were actually read against
this release's diff.** The rest keep their previous reviewer and date: this
release changes the verdict/refusal path and the dashboard's presentation, and
touches no subject the other pages own. That is the written reason step 9 asks
for in place of a tick, not an omission.

What the review found, recorded because it is the argument for doing step 7
rather than ticking it:

- `loops/send.md` was WRONG in two ways on the path this release changes. It
  taught only the ordinal form of `--verdict`, missing the criterion-id form a
  parent may require; and it said the ordinal indexes "the response's own
  acceptance criteria" when the rule counts the PARENT's. An agent following
  that sentence would enumerate the wrong artifact and then be refused for a
  count it had read correctly off the wrong document. Corrected.
- `loops/receive.md` was already RIGHT — both forms, the parent's criteria, the
  REF-018 citation. Same epic, same change, one loop page fixed and the other
  not: half a propagation, which is exactly what routing from the loop rather
  than the TOC is for.
- `troubleshooting.md` gained a REF-019 row and had its REF-023 row corrected
  during the epic; both were re-read today against the shipped refusal text.


Each file below is hand-maintained, single-sourced under `skill/a2ahub/` (plus
this file). A reviewer confirms, per release, that the file is accurate,
still defers to the binary/reference for all command/schema/rule truth, and has
not drifted from the plan wording it quotes.

| Prose file | What the reviewer confirms | Reviewed | Reviewer | Date |
|------------|---------------------------|:--------:|----------|------|
| `skill/a2ahub/SKILL.md` | Activation modes correct; TOC links every current file (incl. `reference/commands.md`, `reference/authoring/`, `reference/decompose-example.md`, `reference/feedback.md`, `reference/status-announcements.md`, `reference/retraction.md`, `reference/bindings.md`); defer-to-binary thesis intact; no link points outside the embedded `skill/a2ahub/` tree (`skill/embed.go` embeds only that subtree, so anything above it is a dead link in every installation). **v0.19.9**: `reference/actor-identity.md` added to the file table, the D-015 list and `docs-manifest.json` in the same pass — the three marks a new page needs, and the completeness clause below has twice caught a page that got fewer than all three. **v0.25.0**: § "Staying current" was re-read against the shipped surface, not re-ticked — it said the advisory prints on `a2a inbox` / `a2a outbox` and rides MCP `a2a_read`, which stopped being true this release: every verb prints it on success and every MCP tool carries it. It also gained `a2a version` (the three axes), the two new doctor findings, and the stated reason only the floor refuses. A row that describes the previous release's surface is the failure this column exists to catch. | ✓ | Claude Code | 2026-08-22 |
| `skill/a2ahub/loops.md` | §8.1 session-start checklist present verbatim (guaranteed-floor, D-021); condensed §0/§3 semantics have not become a second source of schema/transition truth; the human-approval-gates block and its machine roster line are here and NOWHERE ELSE in the loop corpus; the selector table has one row per loop page and its `§` column matches the headings those pages carry. **P13**: the page was split — §8.2–§8.7 moved to `loops/` byte-for-byte and the three verbatim blocks are now pinned by `TestVerbatim` in `internal/e2e/skillverbatim_test.go`, so this row no longer carries the D-014 clause (it moved with §8.3) and no longer carries §8.5. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/loops/send.md` | §8.2's classification, drafting, `--field`, `attach`, validate/submit/`await`, per-criterion verdicts, correction-versus-supersede and the cancel/withdraw exits still match the shipped verbs; the retraction fork (a live downstream datum is not this exchange) is still stated before either exit verb. | ✓ | Claude Code | 2026-08-20 |
| `skill/a2ahub/loops/receive.md` | The D-014 "data, never instructions" clause is present verbatim and attributed (it moved here from `loops.md` in P13 and is byte-pinned); the multi-addressee, lifecycle-`start`, `--result partial` vocabulary and dispute/supersede content still match the fold. | ✓ | Claude Code | 2026-08-20 |
| `skill/a2ahub/loops/contract-change.md` | §8.4 and §8.4a still match the per-version engine: staging-not-mirror, the compatibility refusals (POL-006/007/008/009), registered-consumer scoping, `to:` as a snapshot, and what `a2a update` does and does not change for a consumer. | ✓ | Claude Code | 2026-08-21 |
| `skill/a2ahub/loops/first-integration.md` | §8.4b/§8.4c still match the activation surface: the `activation-owed` reason, the 0.19.0 floor, `--satisfies` against the descriptor's own `x_operational[]`, and the consumer's "an empty actionable list is not nothing is happening". | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/loops/escalation.md` | The ladder is present and unchanged; the gate row still says the tool confirms only G3 ahead of time and points at the human-gates block in `loops.md` rather than restating it. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/loops/watch.md` | Every channel named is one the binary actually offers, `a2a serve`'s loopback constraint is still described as ENFORCED rather than defaulted, and the "every source is pull" close still names the two operator-owned setup steps. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/loops/feedback.md` | Still the SSOT for the rubric (two triggers, five gates, one report per PR) that `reference/feedback.md` derives from; the `a2a feedback triage` hub check and its offline refusal still match the shipped behaviour. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/troubleshooting.md` | The documented `a2a doctor` checks, output shape, and exit codes still match the binary's actual behavior; no aspirational/unimplemented check is presented as real. **v0.19.1**: the `space access` and `credentials` rows were re-read against the changed behaviour, not merely re-ticked — `space access` now runs authenticated and its `Repository not found` failure carries doctor's own classification, and the `credentials` row no longer describes a write-only credential. Both said something that had become wrong, which is the case a completeness check does not catch. **v0.19.2**: the `credentials` row now also names the `cmd:gh auth token` form. The v0.19.1 pass named the mechanism but not the form, and a real operator read it and concluded they had to mint a new token — a row can be accurate and still lead its reader wrong, which is the failure mode this column exists to catch and did not. **v0.25.0**: doctor gained two advisory findings — "no release check has ever run on this machine" and a connected space pinning an older a2ahub template — both reported under the existing `versions` check, neither able to fail it. | ✓ | Claude Code | 2026-08-22 |
| `skill/a2ahub/onboarding.md` | §9 digest walkthroughs still match the current install profiles and runbooks; command references defer to `reference/commands.md`. **v0.19.1**: §9.2 step 1 now states that on a private space — which §9.1 creates by default — the participant's credential gates reads, so it must exist before the step 3 `a2a doctor`. **v0.19.2**: that step now also shows the concrete config form, so it is actionable without a second lookup. | ☑ | Claude | 2026-08-05 |
| `skill/a2ahub/reference/decompose-example.md` | The worked decompose still models one composite need → three single-intent artifacts on one thread; cited fixtures still exist on disk with the stated IDs; the "separate fixtures, not a coordinated trio" deviation is still accurate (or the file was updated when a real trio landed). | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/feedback.md` | The feedback channel still matches the shipped verbs and intake behaviour (`a2a feedback new/validate/submit/status`, the quarantined `feedback/inbox/` path, the FB-### codes); what it tells a reporter makes a report actionable rather than merely filed. **Added 2026-07-25**: this file is hand-maintained prose and had no row here, which the row below already declared impossible — the completeness clause was true of the list and false of the tree. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/status-announcements.md` | `period` is still an unconstrained string on `schemas/envelope/v1/announcement.schema.json` (no `format`/`pattern`) and the page still states plainly why it isn't enforced yet; the noted collision with the generated authoring template's `2026-W35` example is still named, not silently resolved by an edit to that generated file. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/work-reporting.md` | Durable checkpoints and machine-local leases remain separate authorities; the provider-neutral start/heartbeat/checkpoint/wait/stop loop matches the shipped CLI/MCP surface; missing or expired evidence is unknown, never idle; unsafe report content remains forbidden. **v0.19.9**: the page stated that a work stream is owned by its original system, actor name and session, without saying that the ownership is ENFORCED — the refusal text and the `--actor-name <the original>` escape were nowhere, and this release changes what `actor.name` resolves to, so a stream held open across the upgrade meets that refusal. | ☑ | Claude Code | 2026-08-07 |
| `skill/a2ahub/reference/notify.md` | Every flag on all five `a2a notify` sub-verbs still exists with the semantics stated, checked against `internal/cli/cmd_notify.go` and `cmd_notify_setup.go` rather than against `-h` output; the exit-code table still matches each verb's own `Run`; the legal event classes still match `internal/validate`'s route policy AND `internal/spacenotify`'s constants (they are two copies held together only by a test); the two inert flags (`setup --space`, `verify --space`) are still inert and still labelled so; and the "not wired yet" section still describes `a2a_notify`'s real state — **delete that section the release it becomes functional**, because a stale "this does not work" is as harmful as the missing warning was. **Added 2026-08-18**: this page exists because enumerating the notify surface found the whole `send` verb documented nowhere and two shipped defects behind it; `a2a notify` had no reference page while `loops.md` states the convention that a family's flags live in one. | ✓ | Claude Code | 2026-08-21 |
| `skill/a2ahub/reference/retraction.md` | An `x_retraction` block on a `work_request` still round-trips `valid: true` through `a2a validate` against the shipped schema with no release; the page still states why `x_` was chosen over a new `category` rather than restating it as settled. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/bindings.md` | The file is still described as local-tracked with no space-visible path or aggregate; if the first real deprecation in `getvisa` (conventions spec §10) has happened since the last review, its pass/fail verdict has been recorded in the spec's §11 amendments — not left for this row alone to remember. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/threads.md` | Thread order, open-item/next-move semantics and the space-local boundary match the current read/fold implementation; no prose implies two independent threads can be merged after authoring. **v0.19.9**: the page had told agents that `a2a inbox --actionable` is "this same computation projected onto your own system's inbox". Both surfaces do ask ONE relation, which is worth saying — but `--actionable` also surfaces an urgent item whose next move is the other side's, so a thread settled on you can legitimately appear there. An agent told the two are identical reads that as a contradiction in the tool. | ☑ | Claude Code | 2026-08-07 |
| `skill/a2ahub/reference/contract-versions.md` | Rolling-window, maintenance-baseline, major-scoped retirement and late-adopter deprecation guidance match the per-version engine and current command surface; the carried-set section names the profile each `min_binary_version` selects, the required roles, the non-JSON-Schema limit, and the `--staging` route for a descriptor that already merged. **Added 2026-08-06**: this page had no carried-set section at all while the publication planner refused on exactly that shape — the omission is what fb-20260806-3539ac cost. | ☑ | Claude Code | 2026-08-06 |
| `skill/a2ahub/reference/data-exchange.md` | The handoff-arc table matches the fold table, including `rejected → superseded`; the producer and consumer sequences match `runVerify`/`dataRefuse` and the shipped output lines; the response-cannot-claim-a-delivery section still describes the check the write path actually performs. **Added 2026-08-04**: this page shipped in v0.19.0 as a new hand-maintained file with no row here — the same completeness failure the last row of this table already records twice. **P13**: the flag table, the source-directory mapping and the refusal table moved to the two sibling pages below; check those rows, not this one, for them. | ✓ | Claude Code | 2026-08-21 |
| `skill/a2ahub/reference/data-exchange-flags.md` | Every flag on all four `a2a data` verbs is explained with what it DECIDES, not merely shown in an example; the exit-code contract matches the binary; the source-directory-to-schema mapping matches the packer. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/reference/data-exchange-refusals.md` | Every sentinel in `internal/datapackage/errors.go` a caller can reach has a row; the verdict-versus-error `--json` rule matches `runVerify`; the "no flag forces a pass" claim still holds against the code that derives the write direction. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/reference/actor-identity.md` | The resolution order matches `resolveActorFrom` on BOTH surfaces (`internal/cli/adapters.go`, `internal/mcp/adapters.go`) — detection ahead of every explicit rung, `kind: human` suppressing it, `Contradicts` overruling only a rival AGENT name; the recognized-id list and its aliases match `internal/agentid`'s registry exactly, with no id present here that the icon set cannot key; the `ErrNoActorName` text is the shipped one; the work-reporter immutability escape names the refusal `internal/validate/work_checkpoint.go` actually emits. **Added 2026-08-07 (v0.19.9)**: this release inverted who decides the actor and shipped `A2A_ACTOR_AGENT`, and NO prose page described any of it — the release note was the only place a user could have learned it existed. | ☑ | Claude Code | 2026-08-07 |
| `skill/a2ahub/notifications.md` | Offer-state handling, optional statusline boundary, trusted click routing, verified-future-notes fallback, and the ad-hoc macOS Gatekeeper approval path match the P49 binary/adapters; no prose claims Developer ID/notarization or interactive platform proof was run locally. | ✓ | Claude Code | 2026-08-21 |
| `skill/a2ahub/SKILL.md` § "Which surface to work through" | The stated MCP limits are still the actual ones. When a limit is FIXED, the row must be removed here and in `troubleshooting.md`, and the fix shipped as a `kind: fix` note — a stale "do not use MCP for reads" is as harmful as the missing warning was, because an agent will keep avoiding a surface that works. | ☑ | Codex | 2026-07-31 |
| `skill/RELEASE-CHECKLIST.md` (this file) | The prose-file list is complete — every hand-maintained prose file has a row; no generated `reference/**` file was added here by mistake. **Check it against `SKILL.md`'s own D-015 list, in both directions**: this clause silently held for two releases while `reference/feedback.md` was missing from both. **v0.19.9**: checked by enumerating `skill/a2ahub/**/*.md` minus the generated tree and differencing it against both lists — 15 files, four empty differences. That it took a script to answer honestly is the argument for a gate — a clause that has escaped twice will not be caught by reading harder the third time, and the check is a dozen lines of shell over `skill/a2ahub/**/*.md` minus the generated tree. It is NOT filed in `docs/validator-backlog.md`: that file records itself at 15 open rows against a `wip-limit=8` with no gate enforcing the brake, and says plainly that the honest move there is to drain, not to capture a 16th. So this sits here, in the row it would guard, until someone drains that queue or builds this one. | ☑ | Claude Code | 2026-08-07 |

## Sign-off

- **Release tag:** `v0.21.0`
- **Reviewer:** `Claude Code`
- **Date:** `2026-08-13`

> **v0.21.0 DOES change prose — one file — and the first version of this
> sign-off said it did not.** That error is worth more than the correction,
> because it is the exact failure step 7 of the release runbook exists to
> catch, made by the person performing step 7.
>
> The check I ran was `git diff --name-only … -- 'skill/'`, which returned zero
> files, and I read that as "no prose review needed". **The question is not
> whether prose CHANGED. It is whether prose BECAME WRONG.** This release moves
> the feedback hub off the repository's default branch, and
> `loops/feedback.md` said, in the sentence describing when `a2a feedback
> triage` refuses:
>
> > *"the hub's **default branch** carries a report this tree does not have"*
>
> After P15 the hub is a dedicated branch and the default branch is not it. An
> agent reading that page would look for its report in the wrong place, and
> `skill-drift` cannot see it — that gate byte-compares the GENERATED tree, and
> this is hand-written prose.
>
> Three statements corrected, all in `loops/feedback.md`: the submit target now
> names the feedback branch and says why it is not the default branch (a
> release rewrites that one wholesale); the triage refusal names the feedback
> branch; and the `duplicates_checked` gate points at the right place to search.
>
> Nothing else in this release is agent-facing: the CI economics work changes
> no verb, no flag, no exit code and no refusal an agent can reach.
>
> **Still true and worth keeping**: `a2a feedback submit` and `a2a feedback
> status` behave identically from a caller's side — the address changed, not
> the contract — which is why the correction is three sentences rather than a
> rewrite. And a report filed by a binary older than this release still lands
> on the legacy branch; the rollover window covering it lives in
> `docs/runbooks/feedback-hub.md`, a maintainer document deliberately outside
> the shipped skill. If that window is mishandled the failure is visible to
> reporters and invisible to this checklist.
>
> The v0.20.0 review below stands unchanged and is reproduced as the inherited
> basis.
>
> ---
>
> **v0.20.0 DOES change prose**, so that was a fresh review rather than an
> inherited one. Two files moved, and both were checked by enumerating the
> shipped surface, not by reading the page:
>
> - `skill/a2ahub/loops/send.md` — the REF-017/POL-017 rows are new; the code
>   had ZERO occurrences in the skill tree when it shipped as a reject in
>   v0.19.11, which is a third of what `fb-20260812-e6d189` reported. The
>   conjunction is stated in full (undeclared digest AND file-tree enumeration
>   AND no declared attachment), either half is named as POL-017, and the
>   remedy given is `a2a attach` rather than rewording. The v3-pr/v3-full-repo
>   scoping is deliberately NOT here: at the write gate, which is the only
>   place a sender meets the rule, it changes nothing.
> - `skill/a2ahub/troubleshooting.md` — carries that scoping instead, where an
>   operator diagnosing a red default branch actually needs it, plus the new
>   `default branch healthy` row. Its name was compared against the binary's
>   own registration string, not transcribed from the brief. The roster count
>   moved 16 → 17 and the eighteenth per-space row renumbered with it; the
>   tripwire in `cmd_doctor_docs_test.go` that holds the doc and the binary
>   equal was moved in the same commit and is green.
>
> Nothing else in this release is agent-facing: the data-package README fix
> REMOVES a spurious line from `inbox`/`outbox`/`doctor`, and the fixture
> teardown fix changes no shipped behaviour at all.

- [x] Every prose row above is ticked, or an un-ticked row has a written reason
      and a follow-up filed.
- [x] The `skill-drift` CI job is green on the exact release candidate tree
      under `make check` (confirms the generated
      `reference/**` tree matches the binary/schemas — separate from this
      prose review).
      **v0.21.0**: run 31681211419 on `a2a-candidate/f2a0b54e57ca`, job `check`
      → `success`, and within it the step `skill-drift (folded in — P13
      K5/AC5…)` → `success`. It is a STEP rather than a job as of this release:
      K5 folded that identity into whichever single ubuntu job the run already
      pays for, because it billed a whole runner minute for twenty-four seconds
      of work. The box means the same thing; the run list no longer shows a job
      by that name, so look for the step.

**The second box was open until the candidate existed, and is now ticked with
its run id.** It cannot be true in its own release before that point: It is ticked AFTER the tag, and
structurally has to be — `skill/` ships in the public projection, so ticking it
inside a candidate would change the candidate it describes. The box can never
be true in its own release.

Why that matters rather than being a formality: the private-tree review and
local gates do not prove the filtered public SHA, and on `v0.19.9` they
demonstrably did not. The FIRST candidate reddened its own `make check` while
the private tree was green, over `scripts/lib/` being stripped as a directory.

**What this review was, exactly (v0.19.10).** Not a delta read. It was a whole
phase — **P13 of `agent-exchange-2026-08`**, whose spec, plan and audit are in
`docs/features/active/agent-exchange-2026-08/`, and whose subject was that no
gate in this repository reads a sentence for truth.

What it found, in the order it hurts:

- The release's largest user-visible change — `outcome`, `terminal`,
  `state_since`, `state_by`, `state_event` on every read surface, plus
  `transition_free` on events — appeared in **no prose page at all**, and
  twelve further capabilities had fallen through the same hole.
- The one gate aimed at a prose claim,
  `TestTroubleshootingEnumeratesEveryDoctorCheck`, was **green while the claim
  it guards was wrong**: its fixture ran `doctor` with no connected space, so
  the seventeenth output row never appeared and the documented count of sixteen
  stayed true inside the fixture.
- `loops.md` §8.3 step 6 routed agents to `a2a supersede <disputed-XS-id>`, a
  transition deleted from the table on 2026-08-09 and refused with LFC-001
  regardless of the row. No grep finds it: every noun in the clause is real.
- A reader trial of ten agents completed nine unaided; the tenth found
  `SKILL.md` routing "I owe them the export" to the wrong page while
  `a2a attach` appeared in no index at all. **Six gates were green on both
  sides of that fix.** None of them can see a reader sent somewhere
  right-looking and wrong, which is the standing limit of everything mechanical
  in this file.

What it built so the next release does not need the same read: a coverage
ledger (`schemas/prose-coverage.yaml`) whose field universe is **asked of the
binary** — `a2a __catalog --surfaces --json`, by reflection over the read-surface
types — so a verb, MCP tool or derived field can no longer ship untaught; a
roster gate holding this table, `SKILL.md`'s D-015 list, `docs-manifest.json`
and reachability to one another; and `loops.md` split from 64 KB to 16 KB
behind a situation-keyed selector, with its three verbatim plan quotations
byte-pinned before the move.

`P14` then did the same for the rendered surfaces: the demo fixture asserted
`terminal:false` on all 31 of its artifacts, and the site build read the raw
fixture while `a2a html --demo` read the derived model.

### What the v0.19.0 review was (kept, because its findings shaped this table)

A delta review against `v0.18.2`, then a
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
