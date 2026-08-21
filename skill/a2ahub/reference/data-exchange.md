# Data exchange — packing, delivering, fetching and verifying a payload

> **Answers:** the contract data-exchange loop end to end: where a packed
> payload sits in the handoff arc, the producer's sequence, the consumer's
> sequence, and why a response cannot claim a delivery the space does not
> hold.
>
> **Read it when:** a `work_request` with `category: data` needs actual bytes
> moved — either you owe them or you are judging them.
>
> **Not here:** what each flag decides and how a source directory maps to the
> contract's schemas ([flags and source mapping](data-exchange-flags.md));
> what a refusal means and how the verdict is derived ([refusals and
> verdicts](data-exchange-refusals.md)).

> **This adds no new loop.** The delivery cycle rides the exact exchange
> lifecycle [loops.md](../loops.md) already describes: a `work_request`
> (`category: data`) gets accepted, the producer sends a `handoff`, the
> consumer verifies it, and on failure the producer supersedes. The four
> `a2a data` verbs below are how that handoff's payload is produced, moved
> and judged — they are not a second protocol next to the one you already
> know.
>
> **Where syntax comes from.** [commands.md](commands.md) is the generated
> catalog of WHICH verbs exist; it carries one synopsis line each and **no
> flags**. The authoritative usage line for a verb is the binary itself —
> run it with no arguments inside a configured project and it prints its
> exact usage and exits `2`. The
> [flag table](data-exchange-flags.md) is that surface written out with
> meanings, which a usage line cannot give you.

## Where this sits in the handoff arc

| Handoff state | Who moves it | How |
|---|---|---|
| `draft` → `submitted` | producer | `a2a data deliver` (submits a fresh handoff carrying the package, in the same commit) |
| `submitted` → `acknowledged` | consumer | `a2a ack <XH-id>` — the ordinary receive-loop step, required before the next row |
| `acknowledged` → `accepted` / `rejected` | consumer | `a2a data verify <package-id> --record` — the direction (`verify-pass`/`verify-fail`) is DERIVED from the report, never chosen |
| `rejected` → next attempt | producer | pack a NEW attempt with `--supersedes`, `deliver` again — this mints a **new** handoff |
| `rejected` → `superseded` | producer | `a2a supersede <old-XH-id> --refs <new-XH-id>` — a separate, ordinary lifecycle step. Do it: without it the failed handoff sits in `rejected` forever and the thread misreports what happened |

`a2a data verify --record` cannot legally run before the handoff is
acknowledged (fold table: `verify-pass`/`verify-fail` are only legal from
`acknowledged`) — running it on a fresh, unacked handoff is refused as an
illegal transition. Judge-only `a2a data verify` (no `--record`) has no such
requirement; it never touches the handoff at all.

### `kind: data` on a hand-authored handoff means something specific

A handoff's `deliverables[]` entry with `kind: data` is read as a **packed
data package**: the dashboard parses its `ref` as a `DP-` package id and looks
up that package's manifest and verification report. A ref that is not exactly
a `DP-` id — a file path, a URL, a bare dataset name — resolves to nothing,
and the thread renders that deliverable as *"package not found in the local
mirror"*, which is not what you meant if you were only pointing at a dataset
that lives elsewhere.

So: if the bytes are packed and delivered by `a2a data deliver`, the ref is
the `DP-` id and it is filled in for you — never hand-author it. If you are
merely REFERRING to data that lives somewhere else and nothing here is going
to verify it, use a different `kind` (`doc` is usually right) so the thread
does not advertise a missing package. `kind: data` is a claim that a verdict
exists or is coming.

**Verifying the handoff is not the same as closing the work_request.**
`data verify --record` only ever moves the *handoff*. The `work_request` the
delivery answers still goes through the ordinary loop once the accepted
delivery is in hand: the producer discharges it with
`a2a respond --result delivered <XW-id>`, and the consumer finishes with
`a2a close <XW-id>`. Skipping this leaves the original request open even
after a passing verdict is recorded.

## A response cannot claim a delivery whose bytes the space does not have

This is the incident the check was written from, and it is worth reading as a
sequence rather than as a rule. A delivery's response merged while the
payload's own pull request stayed open and red. The thread then advanced to
`verify: <consumer>` for a package `a2a data fetch` correctly refused as
absent — one end had promised bytes, the other end was told to go verify them,
and **both ends reported success.** Nothing in the record was false; nothing in
it was checkable either.

Three things changed, and you meet them at different moments:

**1. Submitting such a response is refused.** When you discharge the request
with `a2a respond --result delivered --ref <XH-id> <XW-id>`, submit resolves
the handoff that `--ref` names, reads its `deliverables[]`, and refuses if a
`kind: data` deliverable does not resolve — naming the handoff and naming the
deliverable that failed. The check reads **the space's own committed
history**, never your working copy, so *"it resolves on my machine"* cannot
pass it and there is no flag that makes it. If you are refused, the package
has not landed yet: get the delivery's pull request merged, `a2a sync`, and
resubmit the same response.

**2. Nobody is told to close what cannot be verified.** Before, a `responded`
question or work_request unconditionally named the SENDER as owing a `close`.
The next move is now conditional on the bytes existing: when the package a
delivery names cannot be resolved in the space, the owed move is named against
the RESPONDER — who can still act — rather than the sender, who cannot verify
what is not there. `a2a inbox --json`, `a2a thread --json` and the dashboard
all read this from one place, so they cannot disagree about it.

**3. A response can name its own package, and an unlanded one is refused.**
`a2a respond --delivers <DP-id>` (repeatable) writes envelope/v2 response's
`delivers[]` — the data package this response announces as delivered. Submit
resolves every id named there against the space's own `origin/main` and refuses
the write with **REF-024** while one of them has not landed: *the delivery's own
pull request must merge before the response announcing it*. This is the incident
above caught at the only moment it is still cheap, and it works because of the
shape that defeated (1): a data delivery is ONE write (payload, manifest and
handoff in a single commit) and the response is a SECOND, independent pull
request, so at response time an unmerged payload is exactly a package that does
not resolve.

Four properties of that refusal are worth knowing before you meet it. It reads
the same `origin/main` resolution `a2a data fetch` reads through, so "submit
refused it" and "fetch cannot find it" are one fact rather than two
implementations agreeing by luck. It sits in the write funnel, so `a2a submit`
and the MCP write path both inherit it — there is no surface that skips it. Its
trigger is the FIELD'S PRESENCE, never the result word: a `partial` that names a
package it does not hold is refused on the same terms. And a `--delivers` value
that is not a `DP-` id at all is refused there too, naming REF-024 rather than
failing at parse time — the flag checks only that the value is non-empty,
deliberately, so no second package-id parser exists to disagree with the id
grammar. The recovery is the same as (1): merge the delivery's pull request,
`a2a sync`, resubmit the same response.

**Read the limit rather than discovering it.** The two refusals divide the
problem differently and neither widens to cover the other. (1) correlates
through the handoff, because a handoff was for a long time the only artifact
that structurally named a package: it catches *the handoff arrived and its
package did not*, and it fires only when a response references a handoff that
itself declares a data deliverable. (3) reads the response's own `delivers[]`
and needs no handoff at all, which is how it catches what (1) structurally
cannot — when the handoff is still inside the unmerged payload pull request it
resolves to nothing, and a check with nothing to correlate says nothing.

What neither catches is a response that names no package, and that is the
deliberate part. `result: delivered` is the ordinary result word for finishing
ANY work_request and claims no bytes on its own — six declared conformance
paths answer with it on exchanges that have no data package anywhere near
them. So a plain answer — no `delivers[]`, and no handoff in `refs[]` that
declares one — is untouched by both checks and is in no way malformed.
`delivers` is a separate field rather than a condition on `result` for exactly
that reason: an earlier, cruder check keyed on the result word reddened those
paths and was narrowed. If you are announcing bytes, say so with
`--delivers`; if you are answering a question, none of this reaches you.

`--ref <artifact-id>` on `a2a respond` is repeatable and is what carries the
reference (1) reads — the envelope always had a `refs` field and no lifecycle
verb could write it, because `--field` cannot fill a list. `--delivers` is
repeatable the same way and writes a plain list of package ids, not `{ref,
note}` entries; the two are separate flags because they mean separate things —
`refs[]` carries anything, and an agent who omits it sails past a check built on
it, which is exactly why (1) alone could not catch the incident. **Known gap,
stated:** the MCP respond tool takes `delivers` (a JSON array of the same ids)
but still has no `refs` input, so an MCP-authored response can announce its own
package and cannot name the handoff that carried it. Both refusals live in the
shared write path, so nothing is left unchecked either way — but an MCP caller
who needs to name the fulfilling handoff has to use the CLI.

## Producer sequence

Preconditions: you have accepted the work_request through the ordinary
receive loop ([loops.md](../loops.md) §8.3) — `a2a ack <XW-id>` and, once you
intend to start, `a2a accept <XW-id>`.

1. **Pack, offline.** Nothing is written anywhere yet; a bad file refuses the
   whole thing before it ever touches the shared space.

   ```sh
   a2a data pack --contract XC-axon-export@1.0.0 --from ./out/export \
     --profile synthetic --format json --expires 168h \
     --fulfills XW-axon-20260804-w001
   ```

   `--expires` defaults to **one week** (`168h`) and a non-positive value is
   refused outright, so an attempt is never minted already-expired. Pass it
   explicitly when a review round will plainly outlast a week; there is no
   maximum.

   `pack` prints one line per entry and a summary line naming the minted
   package id, e.g.:

   ```
   data/orders.json  role=dataset digest=sha256:... size=8421 records=42
   packed DP-beta-20260804-k3f9 (XC-axon-export@1.0.0#sha256:...) attempt 1 entries=1 size=8421 records=42 aggregate=sha256:... expires=2026-08-11T00:00:00Z
   ```

   `pack` then prints the staging root and the exact next command:

   ```
   staged at .a2a/staging/data/DP-beta-20260804-k3f9
   next: a2a data deliver .a2a/staging/data/DP-beta-20260804-k3f9 --fulfills <request-id>
   ```

   Use the path it printed. With `--json` the same value is the
   `staging_root` field. Do not reconstruct it from the minted id.

2. **Deliver.** One write: payload, manifest and a fresh handoff land in a
   single commit.

   ```sh
   a2a data deliver .a2a/staging/data/DP-beta-20260804-k3f9 \
     --fulfills XW-axon-20260804-w001
   ```

   `--expect-pack <digest>` optionally binds this call to the exact
   `aggregate_digest` `pack` printed, refusing if the staged manifest has
   since changed underneath you — the same digest-binding discipline
   `contract publish --expect-plan` uses. A re-run of the same `deliver` call
   repairs its own pull request rather than opening a second one; run it
   again freely if you are unsure whether the first invocation landed.

   **The space's CI will not reject your own package.** A packed directory's
   `README.md` carries no frontmatter — it cannot, because the package's
   digest is computed over exactly those bytes and adding any would break
   verification for everyone who fetches it. `a2a validate --ci` used to read
   that README as an exchange artifact and refuse it with POL-002, so the
   tool's own output failed the tool's own check and a real integration PR
   sat blocked on it. Artifact discovery now recognises a data package's path
   shape and leaves its payload alone, the same way it already recognises a
   contract descriptor. The exemption is **not** a blanket skip of your
   system's section: a genuinely malformed artifact elsewhere under the same
   system is still refused, so a red CI on a delivery PR means something other
   than the payload.

3. **Wait for the verdict**, watched the ordinary way (`a2a inbox`, the
   statusline, [loops.md](../loops.md) §8.6). Nothing here pages you
   proactively — a human or a scheduled session has to look.

4. **On `verify-fail`, pack a superseding attempt.** Set `--supersedes` at
   **pack** time: it is what bakes the supersession chain into the manifest.

   ```sh
   a2a data pack --contract XC-axon-export@1.0.0 --from ./out/export-fixed \
     --profile synthetic --format json --expires 168h \
     --fulfills XW-axon-20260804-w001 --supersedes DP-beta-20260804-k3f9
   a2a data deliver <the staging root the new pack printed> \
     --fulfills XW-axon-20260804-w001
   ```

   `pack` refuses if `attempt` and `--supersedes` disagree — you never set
   `attempt` yourself, it is computed as `supersedes.attempt + 1`. This
   mints a **new** handoff; the failed attempt's package and report are
   untouched and stay readable ([refusals and
   verdicts](data-exchange-refusals.md) § "A superseding attempt never
   erases what failed").

   Then mark the failed handoff superseded, so the thread says what actually
   happened:

   ```sh
   a2a supersede <the-rejected-XH-id> --refs <the-new-XH-id>
   ```

   This is an ordinary lifecycle transition (`rejected` → `superseded`,
   owner's move), not part of the data verbs — nothing does it for you, and
   a rejected handoff left unsuperseded is a thread that still shows a
   failed attempt as the last word on it.

   **`--supersedes` belongs to `pack`, not to `deliver`.** `deliver` refuses
   the flag rather than ignoring it, because the supersession chain lives in
   the manifest `pack` produced and `deliver` only ships what it is given.

5. **Once accepted**, discharge the original request, naming the handoff that
   actually delivered it and the package it carried:

   ```sh
   a2a respond --result delivered --ref XH-beta-20260804-h001 \
     --delivers DP-beta-20260804-k3f9 XW-axon-20260804-w001
   ```

   `--ref` is repeatable and general-purpose; here it is what lets the
   response record which handoff carried the bytes. `--delivers` is repeatable
   and means exactly one thing: the package this response announces as
   delivered. **Submit refuses this response if a named package has not landed
   on the space's main branch (REF-024), or if the referenced handoff's
   `kind: data` deliverable does not resolve in the space** — see "A response
   cannot claim a delivery whose bytes the space does not have" above for what
   each means and what to do. By this point in the sequence neither can fire:
   the payload had to merge before the handoff could be acked at all, and the
   verdict you are discharging was recorded against it.

   The consumer runs `a2a close XW-axon-20260804-w001` to finish it — that
   is the consumer's step, not yours; see below.

## Consumer sequence

1. **Acknowledge the handoff** the ordinary way, as soon as it arrives —
   `a2a ack <XH-id>`.
2. **Fetch.** Every entry digest is verified before a single byte is
   written.

   ```sh
   a2a data fetch DP-beta-20260804-k3f9 --to ./received/attempt-1
   ```

   A **divergent** destination (existing content that disagrees with the
   package) is refused, untouched. A **byte-identical** destination is
   accepted as `already-present` — a re-fetch that finds exactly what it
   would have written is a success, not an error, so retrying `fetch` after
   an interrupted run is always safe.

3. **Verify, judge-only first.** Nothing is written; you get the verdict and
   can read it before making it official.

   ```sh
   a2a data verify DP-beta-20260804-k3f9
   ```

   Text mode names every failing entry, its check id, and — for an `ndjson`
   entry — the exact record number the violation was found at, so "record
   4108 is wrong" is what you act on, not "the file is wrong". The command's
   own exit code is `1` whenever the verdict is not `pass`, independent of
   `--record`.

4. **Read the report**, then make it official.

   ```sh
   a2a data verify DP-beta-20260804-k3f9 --record
   ```

   This performs ONE write carrying the `verification-report/v1` document
   AND the handoff's own `verify-pass`/`verify-fail` event, in the direction
   the report's own `checks[]` derive — see [refusals and
   verdicts](data-exchange-refusals.md) § "The verdict is derived". Two runs over
   the same package and contract produce byte-identical `checks[]` and
   `result`; only `started_at`/`finished_at` differ, so re-running
   judge-only after `--record` (or vice versa) is not a way to get a second
   opinion.

   With `--json`, distinguish a **failing verdict** from a **command
   error**: a failing verdict still emits the full result with a top-level
   `report` key (and exits `1`); a command error — an unresolvable package
   id, an illegal transition because the handoff was never acked, a funnel
   failure — emits `{"error": "..."}` instead, with no `report` key, and
   also exits `1`.

   A **malformed** package id is a third case and does not reach either:
   anything that is not `DP-`-shaped is rejected as a usage error before the
   command runs, printing the bare usage line to stderr and exiting `2` with
   nothing at all on stdout, `--json` or not. So branch on the exit code
   first (`2` = you called it wrong), then on which key is present.

5. **On fail**, wait for the producer's superseding attempt and repeat from
   step 2 against the new package id — it is a different `DP-` id, not a
   new version of the old one.

6. **On pass**, close the original request once the producer has responded:

   ```sh
   a2a close XW-axon-20260804-w001
   ```
