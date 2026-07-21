---
name: create-report
description: Produce the day's progress report — machine-collected facts, an agent-written narrative, one HTML page for people and one Markdown record for machines, plus the date-navigable aggregate. Use when the user asks for a "отчёт", "daily report", "progress report", or wants to show someone what has been shipped.
user-invocable: true
argument-hint: "[optional: window (24h, 72h) and which epics to cover]"
requires_capabilities: [run-shell, read-files, edit-files]
model_tier: reasoning
cites: [code/reporting.md, code/documentation.md]
mate_synced: v0.98.0
---

# create-report

> **Thesis.** A progress report is **derived, never authored**. Every number in it
> comes from `mate report facts` — the commits, the lines, the hours, each epic's
> phase count — and your entire job is the part a machine cannot do: what the work
> MEANS, why it matters to whoever is reading, and what is honestly left. The moment
> you type a number you did not read out of `facts.json`, the report becomes a
> fabrication pointed at the person least able to check it. That is the one failure
> this skill exists to make impossible.

## When to run

- The user wants the day's (or week's) progress made visible — to partners, to a
  client, to themselves.
- NOT for: a release note (that is the CHANGELOG), a status file update (that is the
  status home), or an epic's own tracker (that belongs to whoever closes the phase).

## Procedure

### 1. Preflight — let the machine speak first

```
mate report facts --stdout --since 24h
```

Read what it found and **say it back to the user in one short block**: the window, the
commit and line counts, the hours, the epics it can see with their percentages, and —
just as important — anything it **cannot** see (`epics_source` starting with `none:`
means the project publishes no machine-readable epic state, so the report will carry no
progress bars and will say why).

Do not "fix" a number you disagree with. If it looks wrong, the collector's method is
wrong and the fix is `.mate/config.yaml` (`report:` — the session gap, the excluded
paths, the code types), not the prose.

### 2. Confirm the scope — the first human checkpoint

Propose the epics the report should cover (the active ones, plus any that took commits
in the window) and **ask the user to confirm or amend**. An epic the user does not want
in front of this audience is their call, not yours.

Also confirm the repositories: the report covers the primary repository by default, plus
any standing repositories configured in `.mate/config.yaml` under `report.repos`. To
include additional repositories for today only (without modifying the config), the user
can add them with `mate report facts --repo <path>` (repeatable) — this is the per-day
opt-in for a one-off inclusion.

### 3. Understand each epic — one cheap agent apiece

For each confirmed epic, read its tracker/spec home and the commits attributed to it in
`facts.json`, and produce four things:

- **what shipped** in this window (grounded in the listed commits — no others),
- **why it matters**, in the reader's language, not the codebase's,
- **what is left**, from the phase list,
- **an estimate as a RANGE with its basis** — "2–3 days: two dependency waves remain, of
  which the first is four parallel phases". Never a point number. If you cannot state a
  basis, you do not have an estimate; say what it depends on instead.

If your runtime offers parallel subagents, fan these out — one per epic, on a **cheap**
tier (this is reading and summarizing, not judgment) — and synthesize their results
yourself. If it does not, do them in sequence. Either way the synthesis is yours.

### 4. Ask what the machine cannot see — the second human checkpoint

A demo happened. A partner call unblocked something. A dependency arrived. None of that
is in `git`, and all of it belongs in the report. **Ask the user whether to add
anything** before you freeze the prose.

### 5. Write `report-<date>.md` — the machine-readable record

Follow [references/report-template.md](references/report-template.md). It is **English**,
always — it is the canonical record, and the audience is an AI system reading this
project's history later. The template says so in its own first line, on purpose. The file
is named `report-<date>.md` (e.g. `report-2026-07-16.md`); older reports with legacy names
still work.

### 6. Write `report-<date>.html` — the document for people

This is a doc-presentation: the **`presentation` skill owns the design, the skeleton and
the voice, and this skill does not restate them** — read it and follow it. Two things
are specific to a report:

- **Inline BOTH stylesheets**: `.mate/design/presentation.css` first, then
  `.mate/design/report.css`. The second is the report tier — the KPI grid, the epic
  barline, the checklist, the commit small-print. [references/components.md](references/components.md)
  is the markup cookbook for them; use those class names exactly, because the aggregate
  page is generated against the same vocabulary and the two must look like one product.
- **The order is fixed** (it is what makes the page readable in thirty seconds):
  1. a short human overview — what happened, in plain words;
  2. the **KPI numbers** — commits, lines, tests, hours, hours on tests. When `facts.json`
     carries a `runtime` block, the hours it MEASURED (from the agent runtimes' own logs)
     lead, and the commit-derived figure is the corroborating one — but each keeps its own
     label. Two numbers are printed there and they are different questions: `wall_clock_hours`
     is how long it took, `agent_hours` is how much machine effort went in (parallel sessions
     make the second larger, legitimately). Tokens: print **fresh input and output**. Never
     print `processed` as "tokens" — it is dominated by cache re-reads and reads as a
     spectacular number that means nothing;
  3. the **epic barline** — every epic with its percentage, *including the ones that have
     not started*, each with a plain-language reason (an unstarted epic explains what it
     waits on; a blank row invites suspicion);
  4. **what shipped**, each item with a green check and *why it matters*;
  5. other epics' commits, in small print;
  6. **the harness's own commits, last** — the tooling upkeep is real work and is shown,
     but it never crowds the product story.
- **The one honesty note** the standard mandates has an obvious occupant here: the method.
  `facts.json` carries the sentences — `method.hours_note` for the commit-derived proxy,
  `method.runtime_note` for the measured hours (and for what they do NOT measure: a runtime's
  engagement is not a person's working day). Use them; do not soften them. When there is no
  `runtime` block — a CI machine, a colleague's laptop, no logs kept — say the measurement was
  not available and that the figure is the proxy. `runtime.absences` carries the reason, and a
  silent zero in its place would be a lie told with a number.

Language follows `report.lang` in `.mate/config.yaml` (default English). The Markdown
record stays English regardless — one is for people, one is for machines.

### 7. Freeze the summary, then build the aggregate

Write the narrative back into the day's `facts.json` as a `summary` block — `headline`,
`body`, and one `{slug, line, estimate}` per epic. **This is not bookkeeping**: the
aggregate is generated from `facts.json` and never by parsing your HTML, so a summary
that does not land there simply does not exist in the history.

```
mate report build
```

One self-contained, zero-JavaScript page with a date rail. Then stamp the machine-derived frontmatter onto the report:

```
mate report frontmatter --write --reviewed
```

This emits the YAML header (project, date, authors, tags, reviewed flag) atop `report-<date>.md`, derived from the frozen summary in `facts.json`. Open and review the built pages.

**`--reviewed` is load-bearing, not decoration.** You are a human-run report — a person triggered you and reviews the built pages — so the honest stamp is `reviewed: true`. It is also the signal the unattended nightly (`mate report daily`) reads to hold back: a day already carrying `reviewed: true` is one a human finalized, so the numbers-only run leaves it alone rather than overwriting it with a `reviewed: false` draft (which, under the brain's latest-wins upsert, would silently downgrade the confirmed report). Stamp `--write` **without** `--reviewed` only when this genuinely was not human-confirmed.

### 8. Commit

Stage **exactly** the files this run wrote — the day's directory and the aggregate —
never `git add -A`. The `commit` skill owns the rest.

### 9. Offer to send the reviewed report (optional)

A human-confirmed report supersedes any nightly auto-draft in the brain — the brain
deduplicates on project and date, latest wins — so ask the user whether to publish
this day:

```
mate report send --date <date> --reviewed
```

This surfaces the report to the brain, making it visible to systems that read the
project's history. **Publishing is irreversible** — the brain keeps a record — so
ask before running the command.

Note: this requires `report.sink` to be configured in `.mate/config.yaml`. See
the operator's `report-sink` operations note for setup.

## The rules you cannot bend

1. **Progress comes from the work-item SSOT, never from prose.** Not from markdown
   checkboxes: a real epic here had 0 of 41 acceptance boxes ticked while 7 of its 17
   phases were done. A checkbox-counting report would have told the reader **0%** — not
   merely imprecise, but the opposite of true.
2. **Every number names its method** where the method is a proxy.
3. **Estimates are ranges with a basis.** A point estimate nobody can defend is the
   fastest way to lose the trust the report exists to build.
4. **A published day is immutable.** `mate report facts` refuses to overwrite one; do not
   reach for `--force` to "improve" a report someone has already read. The aggregate is
   deterministic and is rebuilt freely — that is the difference.
5. **Show the harness's own work, and show it last.** Hiding it would be dishonest;
   leading with it would be self-absorbed.

## Output

`<reports home>/<dd.mm.yyyy>/{facts.json, report-<date>.md, report-<date>.html}` and the regenerated
`<reports home>/index.html` (facts.json and index.html keep fixed names). Plus a one-line report: 
the window, the headline numbers, and what to open.
