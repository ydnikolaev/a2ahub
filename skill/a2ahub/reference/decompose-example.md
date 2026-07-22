# decompose-example.md — one composite need, three single-intent artifacts

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
2. **Draft each on one shared thread.** Mint a thread ID on the first artifact
   (`thread:<system>-<YYYYMMDD>-<rand4>`, §3.8) and carry the same `thread` on
   all three so tooling renders them as one conversation. Draft each with
   `a2a new <type>` (per-type skeletons in [authoring/](authoring/)); via the
   MCP surface, `a2a_new` accepts an `items[]` array that drafts several
   artifacts on one thread in a single call.
3. **Validate** each draft with `a2a validate`.
4. **Submit as one batch.** `a2a submit --batch` lands all three in one commit /
   one PR (see [commands.md](commands.md) for exact flag grammar). The
   announcement expects no response; the question and the work_request each
   track to their own closure.

The `blocking` field is per-artifact and honest: the announcement is
`blocking: false` (a notice); the question here is genuinely `blocking: true`
(axon's work waits on the answer); the work_request may be `blocking: false`
with an `interim_behavior`.

## Fixtures illustrating each type's shape

The product-repo fixture set under `schemas/envelope/v1/fixtures/valid/`
contains a **valid, on-disk exemplar of each of the three types** in this
scenario. Use them as *shape* references for the frontmatter and body of each
part:

| Type in this scenario | Closest real fixture | Notes |
|-----------------------|----------------------|-------|
| `announcement` / `deprecation` | `XA-axon-20260901-d8k1.md` | axon → seomatrix; "Deprecation: ingest v1 destination handle sunset 2026-10-01"; carries the machine-readable successor `ref` (`XC-axon-ingest@2.0.0`). |
| `question` / `defect` | `XQ-seomatrix-20260730-h2k8.md` | "Ingest 422 error shape contradicts §4.3 example"; `blocking: true`. (Authored seomatrix → axon; mirror the direction for an axon-authored part.) |
| `work_request` / `data` | `XW-axon-20260731-p9d3.md` | axon → seomatrix; "Currency dictionary keyed by real ISO-4217 codes". |

> **Deviation — read this.** These three fixtures are **separate, independent
> fixtures; they do NOT share a `thread` on disk.** No coordinated
> announcement + question + work_request trio on one thread exists in the
> fixture set today. The only threaded pair in the fixtures is
> `thread:axon-20260729-c7q2`, which links `XR-axon-country-vocabulary`
> (requirement) and `XS-seomatrix-20260805-b6n2` (its response) — a
> requirement→response pair, not the decompose trio §8.7 describes. This file
> therefore cites each type's fixture as a *shape exemplar* and narrates the
> shared-thread decompose against them; it deliberately does **not** invent a
> thread ID linking the three real fixtures, and it invents no fixture IDs. If a
> coordinated single-thread trio is later added to the fixture set, replace the
> table above with its actual IDs and thread.
