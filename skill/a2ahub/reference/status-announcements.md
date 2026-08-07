# status-announcements.md — feed liveness, without a new type

> **The rule this demonstrates**: a data-feed producer's silence reads
> identical to "nothing changed" unless something says otherwise. a2ahub does
> not need a new envelope type to say it — `announcement` with `category:
> status` already ships (schema: `schemas/envelope/v1/announcement.schema.json`,
> optional `period` field). This page is the convention layered on top of
> that shipped mechanism: what a status announcement looks like, when an
> agent writes one, and how a consumer turns it into "is this producer
> overdue".

## Why this is not a new type

The backlog row this page closes proposed a missing `event` envelope type
for "the feed ran at T, produced N units, last successful sync S". `event/v1`
already exists as the lifecycle-transition FILE schema (§3.5) — reusing its
name for a different concept would collide. The plan already answered the
actual design question (`docs/the-plan/plan/03-domain.md` §22–27): a
periodic/state snapshot is `announcement` with `category: status` and an
optional `period`, both shipped today. Nothing needed to be added to the
`type` enum. What was actually missing was a *read surface* — something that
answers "when did this contract's producer last report, and is that
overdue" — and a read surface is a `doctor`-style check, not a schema
change; see "The guard, deliberately deferred" below.

## Template

```yaml
---
schema: envelope/v1
id: XA-<system>-<YYYYMMDD>-<rand4>
type: announcement
title: <feed name> feed — status for <run timestamp>
space: <space-id>
from: <producing-system>
to: all                                # a status is a feed-level fact, not addressed to one consumer
actor: {kind: agent, name: <written by a2a — never hand-author>}
created: <RFC-3339 UTC — the moment THIS run finished, not when the file was drafted>
category: status
priority: p4
blocking: false
period: <ISO-8601 duration, e.g. P1D | PT1H | P7D>   # see "fixing a form for period" below
refs:
  - {ref: "<XC-id>@<version>"}         # pins the contract this status is about
classification: internal
thread: <thread:system-YYYYMMDD-rand4 — a2a new mints it>
x_units_processed: <int>               # counts live under x_ — free-form, not schema-modelled
x_units_changed: <int>
x_last_error: null
---
Body: one line — what changed since the previous status announcement on this
thread, or "no change" if none.
```

This round-trips against the shipped `envelope/v1/announcement.schema.json`
today (`a2a validate` on a filled copy of this template returns `valid:
true`) — nothing here needs a release.

## Fixing a form for `period`

`period` ships as an unconstrained string (`{"type": "string"}`, no
`format`, no `pattern`) — any producer can write anything there today, and
the schema will not stop them. This page fixes the convention: **`period`
is an ISO-8601 duration expressing cadence** — how often this producer is
expected to report — not a calendar window. `P1D` means "expect another one
about a day from `created`"; a consumer computes overdue as
`created + period < now`.

**Read this against the shipped authoring template, not around it.** The
generated skeleton at `reference/authoring/announcement.md` carries the
comment `# period: <e.g. 2026-W35>` — an ISO-8601 *week designation*, i.e. a
coverage window ("this status covers the week of 2026-W35"), not a cadence.
Both shapes validate; the schema constrains neither. This page picks the
cadence reading deliberately, because cadence is the one fact "is the
producer overdue" needs computed — a coverage window alone doesn't tell a
consumer when to expect the next one. If a producer also wants to state
which window a status covers, put it under an `x_` key (e.g.
`x_covers: 2026-W35`) rather than overloading `period` with two meanings.

**Why the schema does not enforce this yet.** Adding a `pattern` to
`period` is the same move §2b of the conventions spec makes for
`x_retraction`, run in reverse: tightening a schema is a one-way
compatibility decision, and a binary older than the change would refuse a
newly-valid artifact that used the shape this page is *proposing*, not yet
established. The floor of what an old binary accepts, and the space's
pinned validator, would both have to move first — for a form that has not
yet been used by a second real producer. This page is that evidence-
gathering step: once real producers have used the ISO-8601-duration form
without needing a different one, tightening `period` into the schema is a
normal additive change, backed by usage rather than a guess.

## When an agent writes one — the anchored moments

Not "keep it current" — two concrete triggers:

1. **At the end of every scheduled run that touches this contract's data** —
   whether or not the run produced a change. A silent successful run still
   proves liveness; skipping the announcement because "nothing happened" is
   exactly the ambiguity this convention exists to remove.
2. **On the same thread as the previous status announcement**, so a reader
   (or `a2a thread <thread-id>`) can walk the history as one conversation
   rather than a scatter of unrelated broadcasts. Do not `supersede` the
   prior one — each run's status is its own fact; superseding would erase
   the run history the consumer needs to see the cadence actually held.

A consumer finds the latest one by searching `category: status`
announcements whose `refs` pins the contract (`a2a search --type
announcement`, then match `refs`) and reading its `created` + `period`.

## Measurement moment, and what would falsify this

There is no doctor row for this yet (deliberately — see below), so the
measurement moment is empirical: **the first time one of the two pilot
repos' data-feed producers actually misses a scheduled run** while a
consumer is watching for it.

This convention is falsified if either of these happens:

- **The consumer never notices.** No read surface actually computes
  `created + period` yet — this page ships the convention, not the check.
  If a real miss goes unnoticed because nobody wrote the read side, that is
  evidence the read surface is the actual missing piece (as §1 above
  argues), not evidence the convention is wrong — but it means the doctor
  row moves up in priority.
- **The cadence itself is unreliable.** If a real producer's `period`
  turns out to vary run to run (batch reruns, backfills, retries) such that
  `created + period` never lines up with a stable expectation, the
  ISO-8601-duration reading of `period` is the wrong fix for this producer
  and the page's recommendation needs revision before any schema pattern
  ships.

## The guard, deliberately deferred

A `doctor`-style check that reads the latest status announcement per
consumed contract and flags "overdue" is the natural next step, and it is
explicitly **not** built here (per the conventions spec §3/§9: schema and
doctor rows follow evidence, not a guess). Building it before a real
producer has used this convention would freeze a read algorithm against a
`period` form nobody has field-tested.
