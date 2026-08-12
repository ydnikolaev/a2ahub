# decompose-example.md — one composite need, three single-intent artifacts

> **Answers:** one worked decomposition: a single composite need split into
> three single-intent artifacts on one shared thread, submitted as one batch.
>
> **Read it when:** you have a need that wants to be several things at once
> and the single-intent rule is refusing it.
>
> **Not here:** the rule itself (§3.2, condensed in [loops.md](../loops.md))
> and the send loop that classifies each part
> ([loops/send.md](../loops/send.md)); per-type skeletons
> ([authoring/](authoring/)).

> **The rule this demonstrates** (§3.2, [loops.md](../loops.md)): an artifact
> MUST carry exactly one intent of exactly one type. A composite need — "here's
> a notice, and a question, and some work I need" — is the NORMAL case, not an
> edge case. You decompose it into single-intent artifacts linked by a shared
> `thread`, and submit them together as one batch (one commit, one PR). You do
> NOT smuggle a second intent into one artifact's body; the receiver may decline
> the whole thing with `reason_code: split-required`.
>
> **Defer for syntax and shape.** Command invocation is in
> [commands.md](commands.md); each type's full skeleton and field guidance is in
> [authoring/](authoring/). This file narrates the *decomposition*, not the
> command grammar.

## The scenario

Suppose `axon` is rolling out a new ingest major version and, in the same work
session, needs three distinct things from `seomatrix`. Written as one document
it would read:

> "We're sunsetting ingest v1's destination handle on 2026-10-01 (heads up).
> Also, your factory's 422 error shape contradicts our §4.3 example — is that a
> defect on your side? And separately, we need a currency dictionary keyed by
> real ISO-4217 codes."

That is three intents of three types. It is a protocol violation as a single
artifact. Decomposed, it becomes:

| Part of the need | Type | `category` | Direction |
|------------------|------|-----------|-----------|
| "We're sunsetting ingest v1's destination handle" | `announcement` (`XA`) | `deprecation` | one-way notice, no response expected |
| "Your 422 error shape contradicts our §4.3 example — defect?" | `question` (`XQ`) | `defect` | needs an answer |
| "We need a currency dictionary keyed by ISO-4217 codes" | `work_request` (`XW`) | `data` | asks the target to perform work |

Each gets its own lifecycle state, its own closure, and unambiguous tracking —
which is exactly what a single bundled document cannot give (the core failure of
the manual-relay era was items with no individual state).

## The decompose flow

1. **Classify** each part into exactly one type (the table above). One intent
   per artifact.
2. **Draft each on one shared thread.** The first draft via `a2a new <type>`
   mints a thread automatically. For subsequent drafts on the same thread,
   pass `--thread <that value>` (or use `a2a_new`'s `items[]` batch call via
   the MCP surface, which handles all three on one thread in a single call).
   Per-type skeletons are in [authoring/](authoring/).
3. **Validate** each draft with `a2a validate`.
4. **Submit as one batch.** `a2a submit --batch` lands all three in one commit /
   one PR (see [commands.md](commands.md) for exact flag grammar). The
   announcement expects no response; the question and the work_request each
   track to their own closure.

The `blocking` field is per-artifact and honest: the announcement is
`blocking: false` (a notice); the question here is genuinely `blocking: true`
(axon's work waits on the answer); the work_request may be `blocking: false`
with an `interim_behavior`.

## The coordinated fixture trio

The product-repo fixture set under `schemas/envelope/v1/fixtures/valid/`
contains the exact three documents below. They share
`thread:axon-20260729-d3c0` and are validated together by the fixture guard;
they are one decomposition, not three unrelated shape examples.

| Type in this scenario | Closest real fixture | Notes |
|-----------------------|----------------------|-------|
| `announcement` / `deprecation` | `XA-axon-20260729-d3c1.md` | One-way sunset notice with the machine-readable successor. |
| `question` / `defect` | `XQ-axon-20260729-d3c2.md` | One blocking request for an authoritative answer about the 422 shape. |
| `work_request` / `data` | `XW-axon-20260729-d3c3.md` | One executable dictionary request with its own acceptance criteria. |
