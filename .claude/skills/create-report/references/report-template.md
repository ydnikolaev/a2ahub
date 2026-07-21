<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
# The `report-<date>.md` template — the machine-readable record

Copy the shape below. It is **English**, always: the HTML is for people, this is for the
next system that reads this project's history. The file is named `report-<date>.md` 
(e.g. `report-2026-07-16.md`); older reports with legacy names still work.

Every `{placeholder}` is filled from `facts.json`. If a number is not in `facts.json`, it
does not go in the report — there is no third source.

---

```markdown
---
project: {project}
date: {date}
authors:
  - alice@example.com
  - bob@example.com
tags:
  - project
  - progress-report
  - {epic-slug}
reviewed: true
---

# {project} — progress report, {date}

> **This document is written for an AI system to consume.** It is the canonical,
> machine-readable record of one reporting window: the facts as the repository states
> them, and the judgment a human-facing report was built on. The rendered HTML beside it
> (`report.html`) is the same content composed for people. Where they differ in wording,
> this file is the record.

## Window

- Period: {window.since} → {window.until} ({window.label})
- Facts: `facts.json` in this directory — every number below is read from it.
- Epic state source: {epics_source}

## Numbers

| metric | value |
|---|---|
| commits | {git.commits} ({git.code_commits} of type feat/fix/refactor/perf) |
| lines | +{git.insertions} / −{git.deletions} |
| files changed | {git.files} ({git.test_files} test files) |
| hours (derived) | {git.hours} across {len(git.sessions)} session(s) |
| hours on tests | {git.test_hours} |

**Method.** {method.hours_note} Excluded from the line counts: {method.exclude_paths}.

## Epics

For each epic in `facts.json`:

### {slug} — {percent}% ({phases.done}/{phases.total} phases)

- **Status:** {status}
- **Shipped in this window:** one bullet per commit attributed to this epic, each naming
  the commit hash and what it changed. If none: "no commits in this window".
- **Why it matters:** one or two sentences, in the reader's terms.
- **Remaining:** the pending phases, by id and title.
- **Blocked by:** {blocked_by} — or "nothing; {len(actionable)} phase(s) can start now".
- **Estimate:** a RANGE with its basis, e.g. "2–3 days: {waves} dependency wave(s)
  remain, the first of which is {len(actionable)} parallel phases". Never a point number.
  If there is no basis, write what it depends on instead of inventing one.

## Shipped

One bullet per product commit worth naming, each with *why it matters*. Group by epic
where the attribution exists (`commit.epic`), and say how it was attributed
(`commit.via`: `trailer` — the commit said so; `tracker` — the epic's own list claims it;
`scope` — inferred from the commit scope, which undercounts).

## Harness upkeep

The commits under `git.harness` — the tooling that keeps the machine running. Listed, not
elaborated.

## Added by the operator

Anything the repository cannot see (a demo, a call, an arriving dependency) that the user
asked to include. Omit the section entirely if there is none — never pad it.
```

---

## Frontmatter

The YAML block at the top of the file (between `---` fences) **is emitted by `mate report frontmatter`** and
never hand-typed. It carries the project, date, distinct commit authors, tags for indexing (project name +
"progress-report" + each epic that landed commits), and a `reviewed` flag. Do not edit it yourself.

---

## What must never appear here

- A number that is not in `facts.json`.
- A point estimate with no stated basis.
- A percentage derived from markdown checkboxes. The phase counts are the SSOT; a real
  epic here had 0 of 41 boxes ticked while 7 of its 17 phases were done.
- A claim about work that has no commit behind it — unless the operator supplied it in
  §"Added by the operator", where its source is explicit.
