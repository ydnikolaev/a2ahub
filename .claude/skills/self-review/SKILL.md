---
name: self-review
description: >-
  Before declaring any non-trivial task done, step back and adversarially
  critique your own work against the original request. Use when you are about to
  say "done", open a PR, or hand work back — catches the gap between "tests pass"
  and "the thing the user asked for actually works".
mate_synced: v0.98.0
---

# self-review

> **Thesis.** "Done" is a claim about the *user's goal*, not about your last
> command exiting zero. Before you assert it, become your own harshest reviewer:
> re-read the request, list what you actually changed, and hunt for the gap
> between the two. This skill is deliberately **seam-free** — it needs no project
> config, no stack knowledge, no tools beyond reading your own transcript. It
> runs identically in every project, which is why it is the first thing the
> harness syncs.

## When to run

- You are about to say **"done"**, "this works", "ready for review", or open a PR.
- You just made a claim you have not *observed* to be true (a test you didn't run,
  a flow you didn't drive, an edge case you reasoned about but didn't exercise).
- The task was non-trivial (3+ steps, or any change with a runtime surface).

Skip it for pure conversation, a one-line typo fix, or a change with nothing to
observe.

## The evaluation — five passes, in order

Answer each honestly in your own reasoning before you reply to the user. Any
"no" or "not sure" is work, not a footnote.

1. **Restate the goal.** In one sentence, what did the user actually ask for —
   the outcome, not the mechanism? If you can't state it crisply, re-read the
   request before touching anything else.
2. **Diff intent vs. change.** List what you changed. For each item: does it
   serve the goal, or did it drift (an unasked-for refactor, scope creep, a
   feature nobody requested)? Unrequested changes are a finding.
3. **Evidence, not vibes.** For every claim of "works", name the observation
   that backs it — a command you ran, output you read, a flow you drove. A
   passing type-check or unit test is *evidence about a slice*, not proof the
   feature works end-to-end. If the only evidence is "it compiles", you have not
   verified the feature.
4. **Adversarial pass.** Attack your own work: the empty input, the second call,
   the error path, the boundary, the concurrent case. What would a skeptical
   reviewer try first? If you haven't checked it, say so out loud.
5. **Loose ends.** Anything left `TODO`, silenced (`--no-verify`, `@ts-ignore`,
   a disabled lint), stubbed, or "will fix later"? Surface it — do not let it
   ride out under a "done".

## The verdict

State one of three, explicitly, to the user:

- **Done + verified** — goal met, and you name the observation that proves it.
- **Done, unverified** — change made but not exercised end-to-end; say *what* is
  unverified and why (couldn't run it, no environment, out of scope to drive).
- **Not done** — a pass turned up a gap; fix it or report it as open. Never
  paper over a "not sure" with a confident "done".

Honesty here is the whole point: a truthful "unverified" beats a false "done"
every time. Surfacing your own gap is a feature, not an admission of failure.
