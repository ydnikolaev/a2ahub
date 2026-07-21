---
name: quick-fix
description: Triage → reproduce → fix the root cause → verify → commit loop for a small bug; escalate (don't expand) the moment scope grows.
user-invocable: true
argument-hint: "<bug description>"
requires_capabilities: [run-shell, read-files, edit-files]
mate_synced: v0.98.0
---

# quick-fix

> **Thesis.** `quick-fix` is the fast, *bounded* loop for a small bug: triage first
> (don't implement blindly), fix the **root cause** not the symptom, verify with the
> gate, commit. The bound is the whole point — the moment the work grows past a small
> fix (ambiguous repro, several files, a real design choice), **stop and escalate to
> a proper spec**; never expand scope mid-fix. It composes two neutral Tier-0 skills —
> **verify** (`check`) and **commit** (`commit`) — and defers its project-specific
> surfaces (the **docs rule**, the **spec / escalation workflow**) to whatever the
> project already owns.

## Procedure

1. **Triage** the description (+ any repro) before touching code:

   | Verdict | Signal | Action |
   |---|---|---|
   | Trivial | single-file typo, obvious one-liner | proceed |
   | Small | localized bug, clear fix, no API change | proceed |
   | Needs spec | ambiguous repro, touches several files, unclear scope, crosses domains | **stop → escalate** |

   *Needs spec* → tell the user this is bigger than a quick-fix and point them at the
   project's spec/planning workflow (whatever it calls it). If the project has no such
   path, ask them to scope it first. Then **stop** — do not start fixing.

2. **Reproduce** (for trivial/small): read the failing code path (Grep/Glob → Read
   the file). If the user gave a failing test, run it; if none exists and the behavior
   is testable, **write a failing regression test first** (the test-after exception for
   bugfixes: reproduce → fix → test proves the fix).

3. **Fix the root cause.** Don't patch around it, don't refactor beyond the fix, no
   "while we're here" extras.

4. **Verify** — run the project's gate for the affected stack (the `check` skill's
   loop). Anything red → fix it before moving on; never commit over a red gate.

5. **Docs** — only if the fix changed *documented* behavior (route, schema, env,
   public API, CLI flag), update the matching doc per the project's docs rule. Most
   quick-fixes don't → skip explicitly.

6. **Commit** via the `commit` skill — type `fix`, scope the narrowest meaningful
   label.

7. **Report** in ≤3 lines: the bug (root cause) · the fix (`file:line`) · gate PASS +
   commit hash.

## Stop conditions & don'ts

- *Needs spec* verdict, or the fix reveals a deeper structural issue mid-way → note it
  in the report and suggest a follow-up spec; **do not** expand scope inside the fix.
- Don't add logging "just in case", rename variables "while here", upgrade
  dependencies, or touch files unrelated to the bug.
- The gate stays red → keep fixing; never declare done or bypass on a red gate.

## Composition

`quick-fix` rests on `check` (step 4) and `commit` (step 6). For a *completed feature*
(not a small bug) the finalizer is `ship`; for anything the triage marks *needs spec*,
the project's own discovery/planning workflow owns it — `quick-fix` escalates, it does
not grow into one.
