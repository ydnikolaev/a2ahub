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

### v0.25.7 — ten gaps, and every one of them was a flag, a refusal or a printed line

**This is the release the column exists for.** `answers-that-hold-2026-08`
ships three new verbs, four new flags, two new doctor advisory families and one
new funnel refusal. Enumerated against the shipped surface — `internal/cli`'s
own `fs.Bool`/`fs.String`/`fs.Var` registrations, the sentinels each path can
return, and `internal/mcp/tools.go`'s parameter names — rather than sampled.
Ten gaps, all fixed. Six the lead found first:

1. **`a2a validate --ci --space <id>` (RN-02507-3) was in no hand-maintained
   page.** It appeared only in the GENERATED `reference/commands.md` synopsis,
   which this file's own header says is not the place to look for flag syntax.
   Worse, `troubleshooting.md` said `--space` twice — an exit-code row and a
   resolution row — as "the v2 admin host-drift diff, explicitly rejected in
   v1-min". Both are `doctor`'s flag and both sat inside doctor's own sections,
   so they were correct by position and wrong by wording: an agent grepping the
   page for the flag it had just been given learned the flag does not exist.
   Both rows are now scoped to `doctor --space` by name and point at the new
   `validate --ci` row.
2. **`--allow-empty-bump` (RN-02507-1) was documented nowhere at all** — not a
   row, not an example, not a mention. Its refusal ("this bump's N mutation(s)
   … touch no normative artifact") was equally absent, so the flag existed only
   as a string in a refusal an agent had no page to look up.
3. **`local_subjects` was undocumented while `--local` was.** RN-02507-7's own
   detail calls this asymmetry out: the CLI flag is `--local <XC-id>=<path>`
   and the MCP input is the `local_subjects` OBJECT, because `local` is already
   published as a string for `action=verify-export`. Since v0.25.6 every MCP
   input schema is closed, so an agent carrying the CLI name across to MCP is
   now refused BY NAME rather than quietly ignored — a gap that got sharper,
   not softer, because of the previous release.
4. **Two new doctor advisory families printed lines no row explained.**
   `observed consumer [<space>]` (RN-02507-4) and `notify selectivity
   [<space>]` (RN-02507-5) both print on `a2a doctor`, both WARN-only, and
   `troubleshooting.md`'s checks table carried a row for their older sibling
   `consumed contract` and none for either of them. The count in the heading
   was not wrong — the tripwire derives it from the FIXED roster and these are
   advisories — which is exactly why the machine gate could not see this.
5. **`a2a adapt --json` and `--done --json` had no shape written down.** The
   verb was documented; its machine-readable route, including
   `obligations_remain` carrying the same verdict as the exit code, was not.
6. **`notify render`'s unreadable-manifest refusal had no exit-code row.** A
   `space.yaml` whose `notification_routes[]` bytes do not re-decode — or
   re-decode to a different route count — now refuses rather than being read as
   "declares no selectors", which is the widest possible routing. The exit-code
   table listed `render`'s other two refusals and not this one.

**What step 7 checked and found sound**, stated so the six above are not read
as the whole diff: REF-027 — this release's write-funnel refusal, the sharpest
thing in it — already carried a complete `troubleshooting.md` row naming the
batch-not-the-file distinction and the `a2a respond --response <RS-id>` repair.
`reference/contract-versions.md` already covered the `--staging` refusal,
`verify-published` with its zero-contracts/stale-mirror asymmetry, the
`--json` field reference and the descriptor-only `frontmatter <field>` line.
`loops/receive.md` already routes every response through `a2a respond`, so no
loop step sends an agent into the new refusal. And no prose page reproduces a
verb's usage output verbatim, so RN-02507-10's extra `workflow:` line breaks
nothing that was written down.

**Four more, found by the scouts, after the lead had already stopped looking:**

7. **`adapt --done` refuses TWICE and the page described one of them.** A
   detect that STILL FIRES and a detect that COULD NOT BE RUN are different
   answers — `cmd_adapt.go`'s own doc comment says they must never be
   conflated, because a check that cannot be measured is never treated as
   clean — and only the first was written down. Its config-write refusal had
   no row at all.
8. **`a2a docs`'s unknown-topic refusal was in the release note and nowhere in
   the corpus that note describes.** It refuses by naming the vocabulary it
   holds, which is the one thing a reader who cannot find the docs can act on.
9. **`bindings.md` pointed at "§5.3".** The section exists — in
   `docs/the-plan/plan/05-schemas.md`, which `skill/embed.go` does not embed,
   so the pointer resolves to nothing in every installed skill. The scout
   reported it as a section that does not exist; it is worse than that, and
   the correction matters: it exists and is unreachable.
10. **Two `ContractSubcommands` synopses omitted flags this release added**,
    while the verb added beside them names both of its own. That is a gap in a
    GENERATOR, not in a page: `reference/commands.md` is byte-diffed against
    `a2a __catalog`, so the fix is the literal in `cmd_contract.go` and the
    page is regenerated.

Also fixed, not a prose gap: `cmd_adapt.go` and `cmd_docs.go` both opened with
"NOT WIRED into cmd/a2a/wire.go/catalog.go/help.go". Both are wired. A
dispatch-time note that outlived its dispatch.

**How this pass was run, recorded because the first version of this paragraph
was wrong.** Three scouts were dispatched to split the enumeration; their
reports did not arrive, the lead enumerated alone, and this file was written
saying so. The reports then landed — late, complete, and carrying four gaps the
lead's own pass had missed, one of which is a defect in a generator rather than
in a page. Both halves are kept: an enumeration done alone found six, and a
second reader found four more on the same diff. That ratio is the argument for
the second reader, and it is not one this column could have made from a green
verdict.

### v0.25.5 — no row moves; the release does not touch the agent's surface

**The shipped surface this release changes is the published website.** Step 7's
diff against `cmd/`, `internal/cli/`, `internal/mcp/`, `schemas/` and `skill/`
is one file — the regenerated demo-JSON golden, which moves because it embeds
the release-notes corpus. No verb, no flag, no reachable sentinel, no MCP
parameter, no exit code. There is nothing for a prose page to have gone stale
against.

The skill is how an AGENT learns to operate `a2a`, and nothing an agent calls
moved. The exchange map, the home page copy and the space filter this release
fixes are read by people, in a browser, on a page the skill never points an
agent at.

The separation was checked rather than assumed: `grep -rn "a2ahub.dev" skill/`
returns nothing. The prose set does not point an agent at the public site on
any page, so no row can have gone stale against a site change — which makes
this a stronger answer than "the pages still read correctly", not a weaker one.

Recorded rather than ticked, for the reason the v0.25.4 entry gives: a tick
asserts a page was read against a diff, and these rows have no diff to be read
against. The dates and reviewers in the table below stand.

### v0.25.4 — no row moves, and the reason is written rather than ticked

**Not one prose row changed, and that is a finding rather than an omission.**
Step 7 asks for the diff between the shipped surface and the prose. This
release's diff against `cmd/`, `internal/cli/`, `internal/mcp/`, `schemas/` and
`skill/` is one file — a regenerated demo-JSON golden. No verb, no flag, no
reachable sentinel, no MCP parameter, no exit code. There is nothing for a
prose page to have gone stale against.

What the release DOES change lives entirely inside `a2a-validate-reusable.yml`
and `a2a-notify-reusable.yml`, in the step that fetches the binary before a
space's check begins. Two pages mention reusable workflows —
`onboarding.md` and `troubleshooting.md` — and both were read against the
change:

- `onboarding.md` describes wiring a space's callers to the reusable workflows.
  Unchanged by this release: the caller's inputs, the pin form and the setup
  sequence are all identical.
- `troubleshooting.md` documents `a2a doctor`'s checks and says nothing about a
  space's CI check going red on a failed download. **It said nothing before
  this release either**, so nothing is stale — and the message this release
  ships is self-describing by design: "THIS PULL REQUEST WAS NOT VALIDATED …
  the a2ahub release CDN did not answer" names the cause and the remedy in the
  place the reader is already looking. A troubleshooting row would be a second
  copy of a sentence that arrives on its own.

Recorded here rather than ticked, because a tick asserts a page was read
against the diff and these rows have no diff to be read against. The dates and
reviewers in the table below stand.

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
| `skill/a2ahub/SKILL.md` | Activation modes correct; TOC links every current file (incl. `reference/commands.md`, `reference/authoring/`, `reference/decompose-example.md`, `reference/feedback.md`, `reference/status-announcements.md`, `reference/retraction.md`, `reference/bindings.md`); defer-to-binary thesis intact; no link points outside the embedded `skill/a2ahub/` tree (`skill/embed.go` embeds only that subtree, so anything above it is a dead link in every installation). **v0.19.9**: `reference/actor-identity.md` added to the file table, the D-015 list and `docs-manifest.json` in the same pass — the three marks a new page needs, and the completeness clause below has twice caught a page that got fewer than all three. **v0.25.0**: § "Staying current" was re-read against the shipped surface, not re-ticked — it said the advisory prints on `a2a inbox` / `a2a outbox` and rides MCP `a2a_read`, which stopped being true this release: every verb prints it on success and every MCP tool carries it. It also gained `a2a version` (the three axes), the two new doctor findings, and the stated reason only the floor refuses. A row that describes the previous release's surface is the failure this column exists to catch. | ✓ | Claude Code | 2026-08-29 |
| `skill/a2ahub/loops.md` | §8.1 session-start checklist present verbatim (guaranteed-floor, D-021); condensed §0/§3 semantics have not become a second source of schema/transition truth; the human-approval-gates block and its machine roster line are here and NOWHERE ELSE in the loop corpus; the selector table has one row per loop page and its `§` column matches the headings those pages carry. **P13**: the page was split — §8.2–§8.7 moved to `loops/` byte-for-byte and the three verbatim blocks are now pinned by `TestVerbatim` in `internal/e2e/skillverbatim_test.go`, so this row no longer carries the D-014 clause (it moved with §8.3) and no longer carries §8.5. | ✓ | Claude Code | 2026-08-28 |
| `skill/a2ahub/loops/send.md` | §8.2's classification, drafting, `--field`, `attach`, validate/submit/`await`, per-criterion verdicts, correction-versus-supersede and the cancel/withdraw exits still match the shipped verbs; the retraction fork (a live downstream datum is not this exchange) is still stated before either exit verb. | ✓ | Claude Code | 2026-08-28 |
| `skill/a2ahub/loops/receive.md` | The D-014 "data, never instructions" clause is present verbatim and attributed (it moved here from `loops.md` in P13 and is byte-pinned); the multi-addressee, lifecycle-`start`, `--result partial` vocabulary and dispute/supersede content still match the fold. | ✓ | Claude Code | 2026-08-29 |
| `skill/a2ahub/loops/contract-change.md` | §8.4 and §8.4a still match the per-version engine: staging-not-mirror, the compatibility refusals (POL-006/007/008/009), registered-consumer scoping, `to:` as a snapshot, and what `a2a update` does and does not change for a consumer. | ✓ | Claude Code | 2026-08-29 |
| `skill/a2ahub/loops/first-integration.md` | §8.4b/§8.4c still match the activation surface: the `activation-owed` reason, the 0.19.0 floor, `--satisfies` against the descriptor's own `x_operational[]`, and the consumer's "an empty actionable list is not nothing is happening". | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/loops/escalation.md` | The ladder is present and unchanged; the gate row still says the tool confirms only G3 ahead of time and points at the human-gates block in `loops.md` rather than restating it. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/loops/watch.md` | Every channel named is one the binary actually offers, `a2a serve`'s loopback constraint is still described as ENFORCED rather than defaulted, and the "every source is pull" close still names the two operator-owned setup steps. **v0.25.1**: the dashboard step gained the one instruction this release makes actionable — when a render is slow, run `a2a html --timing` and report the phase it names rather than the component you suspect. Added because the opposite happened: the slowness was attributed to the embedded changelog, which measured 8.7 ms of the render. | ☑ | Claude Code | 2026-08-22 |
| `skill/a2ahub/loops/feedback.md` | Still the SSOT for the rubric (two triggers, five gates, one report per PR) that `reference/feedback.md` derives from; the `a2a feedback triage` hub check and its offline refusal still match the shipped behaviour. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/troubleshooting.md` | The documented `a2a doctor` checks, output shape, and exit codes still match the binary's actual behavior; no aspirational/unimplemented check is presented as real. **v0.19.1**: the `space access` and `credentials` rows were re-read against the changed behaviour, not merely re-ticked — `space access` now runs authenticated and its `Repository not found` failure carries doctor's own classification, and the `credentials` row no longer describes a write-only credential. Both said something that had become wrong, which is the case a completeness check does not catch. **v0.19.2**: the `credentials` row now also names the `cmd:gh auth token` form. The v0.19.1 pass named the mechanism but not the form, and a real operator read it and concluded they had to mint a new token — a row can be accurate and still lead its reader wrong, which is the failure mode this column exists to catch and did not. **v0.25.0**: doctor gained two advisory findings — "no release check has ever run on this machine" and a connected space pinning an older a2ahub template — both reported under the existing `versions` check, neither able to fail it. | ✓ | Claude Code | 2026-08-29 |
| `skill/a2ahub/onboarding.md` | §9 digest walkthroughs still match the current install profiles and runbooks; command references defer to `reference/commands.md`. **v0.19.1**: §9.2 step 1 now states that on a private space — which §9.1 creates by default — the participant's credential gates reads, so it must exist before the step 3 `a2a doctor`. **v0.19.2**: that step now also shows the concrete config form, so it is actionable without a second lookup. | ☑ | Claude | 2026-08-05 |
| `skill/a2ahub/reference/decompose-example.md` | The worked decompose still models one composite need → three single-intent artifacts on one thread; cited fixtures still exist on disk with the stated IDs; the "separate fixtures, not a coordinated trio" deviation is still accurate (or the file was updated when a real trio landed). | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/feedback.md` | The feedback channel still matches the shipped verbs and intake behaviour (`a2a feedback new/validate/submit/status`, the quarantined `feedback/inbox/` path, the FB-### codes); what it tells a reporter makes a report actionable rather than merely filed. **Added 2026-07-25**: this file is hand-maintained prose and had no row here, which the row below already declared impossible — the completeness clause was true of the list and false of the tree. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/status-announcements.md` | `period` is still an unconstrained string on `schemas/envelope/v1/announcement.schema.json` (no `format`/`pattern`) and the page still states plainly why it isn't enforced yet; the noted collision with the generated authoring template's `2026-W35` example is still named, not silently resolved by an edit to that generated file. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/work-reporting.md` | Durable checkpoints and machine-local leases remain separate authorities; the provider-neutral start/heartbeat/checkpoint/wait/stop loop matches the shipped CLI/MCP surface; missing or expired evidence is unknown, never idle; unsafe report content remains forbidden. **v0.19.9**: the page stated that a work stream is owned by its original system, actor name and session, without saying that the ownership is ENFORCED — the refusal text and the `--actor-name <the original>` escape were nowhere, and this release changes what `actor.name` resolves to, so a stream held open across the upgrade meets that refusal. | ☑ | Claude Code | 2026-08-07 |
| `skill/a2ahub/reference/notify.md` | Every flag on all five `a2a notify` sub-verbs still exists with the semantics stated, checked against `internal/cli/cmd_notify.go` and `cmd_notify_setup.go` rather than against `-h` output; the exit-code table still matches each verb's own `Run`; the legal event classes still match `internal/validate`'s route policy AND `internal/spacenotify`'s constants (they are two copies held together only by a test); the two inert flags (`setup --space`, `verify --space`) are still inert and still labelled so; and the "not wired yet" section still describes `a2a_notify`'s real state — **delete that section the release it becomes functional**, because a stale "this does not work" is as harmful as the missing warning was. **Added 2026-08-18**: this page exists because enumerating the notify surface found the whole `send` verb documented nowhere and two shipped defects behind it; `a2a notify` had no reference page while `loops.md` states the convention that a family's flags live in one. | ✓ | Claude Code | 2026-08-29 |
| `skill/a2ahub/reference/retraction.md` | An `x_retraction` block on a `work_request` still round-trips `valid: true` through `a2a validate` against the shipped schema with no release; the page still states why `x_` was chosen over a new `category` rather than restating it as settled. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/bindings.md` | The file is still described as local-tracked with no space-visible path or aggregate; if the first real deprecation in `getvisa` (conventions spec §10) has happened since the last review, its pass/fail verdict has been recorded in the spec's §11 amendments — not left for this row alone to remember. | ☑ | Codex | 2026-07-31 |
| `skill/a2ahub/reference/threads.md` | Thread order, open-item/next-move semantics and the space-local boundary match the current read/fold implementation; no prose implies two independent threads can be merged after authoring. **v0.19.9**: the page had told agents that `a2a inbox --actionable` is "this same computation projected onto your own system's inbox". Both surfaces do ask ONE relation, which is worth saying — but `--actionable` also surfaces an urgent item whose next move is the other side's, so a thread settled on you can legitimately appear there. An agent told the two are identical reads that as a contradiction in the tool. | ✓ | Claude Code | 2026-08-28 |
| `skill/a2ahub/reference/contract-versions.md` | Rolling-window, maintenance-baseline, major-scoped retirement and late-adopter deprecation guidance match the per-version engine and current command surface; the carried-set section names the profile each `min_binary_version` selects, the required roles, the non-JSON-Schema limit, and the `--staging` route for a descriptor that already merged. **Added 2026-08-06**: this page had no carried-set section at all while the publication planner refused on exactly that shape — the omission is what fb-20260806-3539ac cost. **v0.25.8**: the `--local` paragraph was CORRECTED, not re-ticked. Its wording — "a local subject that is not where the layout expects it" — is quoted verbatim in fb-20260829-e756c0 as the reason a reporter pointed the flag at a generator's output and got a diagnostic blaming the space. The page now says the subject must BE a published-shaped tree (envelope `contract.md` plus carried files), names the producer who therefore cannot use the verb directly, and records both v0.25.8 refusal changes: the not-published-shaped refusal and the `local:`/`space:` side label. A prose line a user cites as the cause of their mistake is the failure this column exists to catch. |  ✓ |  Claude Code |  2026-08-29 |
| `skill/a2ahub/reference/data-exchange.md` | The handoff-arc table matches the fold table, including `rejected → superseded`; the producer and consumer sequences match `runVerify`/`dataRefuse` and the shipped output lines; the response-cannot-claim-a-delivery section still describes the check the write path actually performs. **Added 2026-08-04**: this page shipped in v0.19.0 as a new hand-maintained file with no row here — the same completeness failure the last row of this table already records twice. **P13**: the flag table, the source-directory mapping and the refusal table moved to the two sibling pages below; check those rows, not this one, for them. | ✓ | Claude Code | 2026-08-21 |
| `skill/a2ahub/reference/data-exchange-flags.md` | Every flag on all four `a2a data` verbs is explained with what it DECIDES, not merely shown in an example; the exit-code contract matches the binary; the source-directory-to-schema mapping matches the packer. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/reference/data-exchange-refusals.md` | Every sentinel in `internal/datapackage/errors.go` a caller can reach has a row; the verdict-versus-error `--json` rule matches `runVerify`; the "no flag forces a pass" claim still holds against the code that derives the write direction. | ☑ | Claude Code | 2026-08-12 |
| `skill/a2ahub/reference/actor-identity.md` | The resolution order matches `resolveActorFrom` on BOTH surfaces (`internal/cli/adapters.go`, `internal/mcp/adapters.go`) — detection ahead of every explicit rung, `kind: human` suppressing it, `Contradicts` overruling only a rival AGENT name; the recognized-id list and its aliases match `internal/agentid`'s registry exactly, with no id present here that the icon set cannot key; the `ErrNoActorName` text is the shipped one; the work-reporter immutability escape names the refusal `internal/validate/work_checkpoint.go` actually emits. **Added 2026-08-07 (v0.19.9)**: this release inverted who decides the actor and shipped `A2A_ACTOR_AGENT`, and NO prose page described any of it — the release note was the only place a user could have learned it existed. | ☑ | Claude Code | 2026-08-07 |
| `skill/a2ahub/notifications.md` | Offer-state handling, optional statusline boundary, trusted click routing, verified-future-notes fallback, and the ad-hoc macOS Gatekeeper approval path match the P49 binary/adapters; no prose claims Developer ID/notarization or interactive platform proof was run locally. | ✓ | Claude Code | 2026-08-21 |
| `skill/a2ahub/SKILL.md` § "Which surface to work through" | The stated MCP limits are still the actual ones. When a limit is FIXED, the row must be removed here and in `troubleshooting.md`, and the fix shipped as a `kind: fix` note — a stale "do not use MCP for reads" is as harmful as the missing warning was, because an agent will keep avoiding a surface that works. | ☑ | Codex | 2026-07-31 |
| `skill/RELEASE-CHECKLIST.md` (this file) | The prose-file list is complete — every hand-maintained prose file has a row; no generated `reference/**` file was added here by mistake. **Check it against `SKILL.md`'s own D-015 list, in both directions**: this clause silently held for two releases while `reference/feedback.md` was missing from both. **v0.19.9**: checked by enumerating `skill/a2ahub/**/*.md` minus the generated tree and differencing it against both lists — 15 files, four empty differences. That it took a script to answer honestly is the argument for a gate — a clause that has escaped twice will not be caught by reading harder the third time, and the check is a dozen lines of shell over `skill/a2ahub/**/*.md` minus the generated tree. It is NOT filed in `docs/validator-backlog.md`: that file records itself at 15 open rows against a `wip-limit=8` with no gate enforcing the brake, and says plainly that the honest move there is to drain, not to capture a 16th. So this sits here, in the row it would guard, until someone drains that queue or builds this one. | ☑ | Claude Code | 2026-08-07 |

## Sign-off

- **Release tag:** `v0.25.7`
- **Reviewer:** `Claude Code`
- **Date:** `2026-08-29`

> **Ten gaps, and the section above names each one against the surface it came
> from.** This release ships three new verbs, four new flags and two new doctor
> advisory families; the enumeration found something missing for five of the
> six shipped `feat` entries and two of the five `fix` entries. Every gap was a
> flag, a refusal or a printed line — never a paragraph that read badly — which
> is the shape this column keeps finding and the reason "the pages still read
> correctly" is not an answer to it. Six were found by the lead alone and four
> more by a second reader on the same diff.
>
> **Rows ticked below are only the ones re-read against THIS release's diff** —
> `troubleshooting.md`, `reference/contract-versions.md`, `reference/notify.md`,
> `loops/contract-change.md`, `SKILL.md` and `loops/receive.md`. Every other row
> keeps its earlier date. `reference/authoring/response.md` carries this
> release's REF-027 guidance and is deliberately NOT ticked: it is GENERATED
> from `schemas/templates/{v1,v2}/response.md` and byte-diffed by `skill-drift`,
> so a tick here would assert a hand review of a file nobody may hand-edit.

- **Release tag:** `v0.25.6`
- **Reviewer:** `Claude Code`
- **Date:** `2026-08-28`

> **FOUR RELEASES BETWEEN THIS SIGN-OFF AND THE ONE BELOW IT CARRY NONE.**
> v0.25.2, v0.25.3, v0.25.4 and v0.25.5 shipped with no entry here. That is
> recorded rather than quietly stepped over, and nothing is reconstructed for
> them: what was not reviewed was not reviewed, and back-filling ticks would
> make this file worse than the gap does. It is worth noticing on a file whose
> own last row exists because completeness silently failed twice.
>
> **What this release changed, and what step 7 found.** Enumerated against the
> shipped surface, not sampled, and it found three things — the point being
> that a formality would have found none:
>
> - `reference/threads.md`'s `SCH-012` section was **backward**. It taught that
>   a left-in-place `<YYYY-MM-DD>` placeholder is "the single most common way"
>   to trip the new format assertion. A placeholder is deliberately EXEMPT from
>   `SCH-012` at every tier and is refused by `POL-010` at submit instead —
>   verified by driving the binary, not by reading the schema: `<YYYY-MM-DD>`
>   produced no violation, `next tuesday` and `2026-02-30` produced one. An
>   agent following that page would have misdiagnosed the refusal it actually
>   hits and expected draft-time `validate` to catch what only submit catches.
> - `troubleshooting.md`'s `default branch healthy` row still asserted the
>   branch is literally `main`, in the release that stopped assuming it — and
>   it contradicted the `REF-026` row four rows below, which points AT it as
>   the row naming each space's derived branch.
> - The **breaking** change — an unknown MCP field is now refused rather than
>   silently dropped — was documented on **no page at all**. Grepped for
>   "unknown field", "additionalProperties", "invalid input", "extra field",
>   "closed schema": zero hits across the whole skill tree.
>
> `loops.md` also gained the routing row it lacked: it claims to be the single
> routing root, and a reader whose write was refused had no row to follow.
>
> **Rows ticked below are only the ones re-read against THIS release's diff**
> — `SKILL.md`, `loops.md`, `loops/send.md`, `troubleshooting.md` and
> `reference/threads.md`. Every other row keeps its earlier date, because this
> pass did not re-read it and a tick asserting otherwise is the thing this
> file exists to prevent.

### Earlier sign-offs

- **Release tag:** `v0.25.1`
- **Reviewer:** `Claude Code`
- **Date:** `2026-08-22`

> **v0.25.1 changes one shipped surface and one prose file, and they are the
> same subject.** `a2a html --timing` existed from v0.25.0 and no page told an
> agent to reach for it, so the prose was not wrong — it was silent about the
> one thing that turns "the dashboard is slow" into a report somebody can act
> on. `loops/watch.md` now carries it.
>
> Enumerated rather than sampled, per step 7: the release adds no verb, no
> flag beyond that one already-shipped diagnostic, no sentinel, no exit code
> and no MCP parameter. The behaviour change an agent can observe is that the
> render is roughly three times faster, and there is nothing to instruct about
> that.

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
`docs/features/archive/agent-exchange-2026-08/`, and whose subject was that no
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
