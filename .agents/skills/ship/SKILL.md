---
name: ship
description: Composite finalizer — run the gate for the touched stacks, review the session diff, then commit. Never ship a red gate.
user-invocable: true
argument-hint: "[optional commit message hint]"
requires_capabilities: [run-shell]
mate_synced: v0.98.0
---

# ship

> **Thesis.** `ship` is the safe end-of-work step: it *composes* three smaller
> skills into one finalizer — **gate** (`check`), **review the session diff**
> (`self-review`), then **commit** (`commit`) — so a feature lands verified,
> reviewed, and isolated in one move. The universal part is the sequence and its
> invariants: **never ship a red gate**, **review before you commit**, **docs are
> part of the change** (not an afterthought), and **capture-don't-fix** any harness
> signal the session tripped. The project-specific parts are **values** — and `ship`
> does not invent a new rule for them: it defers to the surfaces the project (and the
> skills `ship` composes) already own — its **anti-pattern surface** (via
> `self-review`), its **docs rule**, its **harness-signal / backlog convention**, and
> its **commit convention** (via `commit`). This skill runs the sequence and defers
> each value to the surface that already holds it.

## Procedure

1. **Gate.** Run the project's quality gate for the stack(s) this session touched —
   this is the `check` skill's loop (detect touched stacks → run each one's gate).
   **A red gate aborts the ship**: report the failures and stop. Never commit red.

2. **Review the session diff.** Run an inline `self-review` of what this session
   changed (`git diff --stat` then `git diff`), walking each file against the
   **project's anti-pattern surface** (whatever rule/doc its `self-review` names).
   **Flag — don't auto-fix — substantive issues** (placement misses, hardcoded
   names, `any`/`@ts-ignore`/`--no-verify`, `git add -A` leaks, swallowed errors,
   dead code, WHAT-not-WHY comments). Trivial issues you can fix without scope creep
   → fix them, then re-run the gate.

3. **Docs obligation.** If the session changed *behavior* (a route, schema/migration,
   env var, config key, UI component, CLI flag, deploy step — whatever the project's
   docs rule enumerates), update the matching doc **before** committing. Nothing
   behavior-changing → skip explicitly (say so in the report).

4. **Capture a harness signal — only if the session tripped one** (usually nothing →
   skip). If the work revealed a *harness-level* rough edge, **capture, don't fix**:
   file it per the project's backlog convention for a later batch pass. Do **not**
   run a harness audit inline here.

5. **Commit** via the `commit` skill — session-isolated staging, conventional
   message, a subject that covers the feature/fix (not the file list).

6. **Report** in ≤5 lines: gate result (PASS + stacks) · review outcome (clean / N
   fixed / N flagged) · commit `<hash> <subject>` · harness signal (captured 1-line /
   none) · suggested next (e.g. `git push`, open a PR, or the next phase).

## Stop conditions

- The gate is red → fix, don't ship. Never commit over a red gate.
- Review found session-scope creep (files touched beyond the task) → report and ask
  before committing.
- Uncommitted work from another session is present → the `commit` step isolates it
  (stages only this session's paths), but warn the user in the report.

## Composition

`ship` rests on three neutral Tier-0 skills — `check`, `self-review`, `commit`. If a
consumer names its equivalents differently, follow the same loop with its own skills;
the sequence and its invariants are what `ship` guarantees, not the specific names.
For a small bug a single `quick-fix` may already cover the whole loop — `ship` is the
finalizer for a completed feature.
