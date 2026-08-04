# Data exchange — packing, delivering, fetching and verifying a payload

> **This adds no new loop.** The delivery cycle rides the exact exchange
> lifecycle [loops.md](../loops.md) already describes: a `work_request`
> (`category: data`) gets accepted, the producer sends a `handoff`, the
> consumer verifies it, and on failure the producer supersedes. The four
> `a2a data` verbs below are how that handoff's payload is produced, moved
> and judged — they are not a second protocol next to the one you already
> know. **Command syntax is generated in
> [commands.md](commands.md); this page is the how-to and the one thing
> most likely to trip an agent — mapping a source directory onto a
> contract's schemas — that a flag list cannot show.**

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

**Verifying the handoff is not the same as closing the work_request.**
`data verify --record` only ever moves the *handoff*. The `work_request` the
delivery answers still goes through the ordinary loop once the accepted
delivery is in hand: the producer discharges it with
`a2a respond --result delivered <XW-id>`, and the consumer finishes with
`a2a close <XW-id>`. Skipping this leaves the original request open even
after a passing verdict is recorded.

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
   untouched and stay readable (§ below).

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

5. **Once accepted**, discharge the original request:

   ```sh
   a2a respond --result delivered XW-axon-20260804-w001
   ```

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
   the report's own `checks[]` derive — see the section below. Two runs over
   the same package and contract produce byte-identical `checks[]` and
   `result`; only `started_at`/`finished_at` differ, so re-running
   judge-only after `--record` (or vice versa) is not a way to get a second
   opinion.

   With `--json`, distinguish a **failing verdict** from a **command
   error**: a failing verdict still emits the full result with a top-level
   `report` key (and exits `1`); a command error (a bad package id, an
   illegal transition because the handoff was never acked, a funnel
   failure) emits `{"error": "..."}` instead, with no `report` key.

5. **On fail**, wait for the producer's superseding attempt and repeat from
   step 2 against the new package id — it is a different `DP-` id, not a
   new version of the old one.

6. **On pass**, close the original request once the producer has responded:

   ```sh
   a2a close XW-axon-20260804-w001
   ```

## How your source directory maps to the contract's schemas

`pack` needs to know, for every file, which of the pinned contract's schema
entries it is checked against (`conforms_to`). The rule (one file is walked
recursively per top-level directory whose name matches a schema's own
**stem** — the schema's own file name with its directory and
`.schema.json`/`.json` suffix stripped):

```
<source>/
├── order/                      # matches contract schema "schema/order.schema.json" (stem "order")
│   ├── 2026-08-01.json         # conforms to that schema
│   └── nested/2026-08-02.json  # also conforms — any depth under order/ counts
├── shipment/                   # matches "schema/shipment.schema.json" (stem "shipment")
│   └── 2026-08-01.json
├── README.md                   # role=readme — ONLY recognized at the source ROOT
└── index.json                  # role=index — ONLY recognized at the source ROOT
```

- **A file under `<source>/<schema-stem>/…`, at any depth, conforms to that
  schema entry.** This is the layout to use whenever the contract declares
  more than one schema.
- **A flat source (files directly at the root, no per-schema
  subdirectories) works only when the contract declares exactly one
  schema** — every dataset file at the root then conforms to that sole
  schema.
- **A flat root file against a multi-schema contract is refused**, naming
  every schema the contract declares and the stem each one expects, e.g.
  *"entry `orders.json` is at the source root, but the pinned contract
  declares 2 schema entries (`schema/order.schema.json`,
  `schema/shipment.schema.json`) — place it under one of
  `<source>/<schema-stem>/` instead"*. This is deliberate: a pack that
  silently guessed one of several schemas would produce a package whose
  verdict means nothing.
- **A top-level directory that matches no schema's stem is refused the same
  way**, naming the schemas the contract does declare.
- **Two of the contract's schemas whose stems collide** (say
  `schema/order.schema.json` and `orders/order.json` — both stem `order`)
  refuse the pack outright, because a directory name cannot then say which
  one is meant. This is a problem with the CONTRACT, not with your source
  tree: the producer cannot fix it by rearranging files, and it needs a
  contract version whose schema entries have distinct stems.
- **`README.md` and `index.json`/`index.ndjson` are role-recognized only at
  the source root.** A `README.md` placed *inside* `order/` is not treated
  specially — everything under a matched schema directory becomes a
  `dataset` entry checked against that schema, so a stray README or index
  file nested under a schema directory will fail conformance rather than be
  classified as documentation. Keep them at the top level.

## What each refusal means, and what to do

Every one of these is a hard refusal, not a warning — nothing is staged or
written on the way to it.

| Refusal (sentinel) | Fires at | What it means | What to do |
|---|---|---|---|
| `data_profile production is refused` | `pack` | You set `--profile production`. Not negotiable — real end-user data may not enter the shared space. | Re-pack with `--profile synthetic` or `--profile sanitized`, sanitizing the source first if needed. |
| `entry does not conform to its contract schema` | `pack` (before staging), `verify` (against delivered bytes) | One file failed its `conforms_to` schema. The message names the file; for an `ndjson` entry it also names the failing **record number**. | Fix that file (or that record) and re-pack. `verify` naming this after delivery means fix it and pack/deliver a **superseding** attempt — the failed one is not editable in place. |
| `declared digest does not match computed digest` | `fetch`, `verify` — before parsing | The bytes you have do not hash to what the manifest declares, checked per entry before assembly (never a matching aggregate masking a tampered entry). | Re-`fetch` (or re-sync the mirror) rather than trust the local copy; if it recurs, the package itself is corrupt and needs a new attempt from the producer. |
| `--expires must be positive` | `pack` | You passed a zero or negative `--expires`. The attempt would be expired the moment it existed. | Drop the flag to take the one-week default, or pass a real duration. |
| `package has expired` | `fetch` only | `now` is past the manifest's `expires_at`. `verify` does **not** check this — a package already fetched, or resolved straight from the mirror, can still be verified after its nominal expiry. | Ask the producer for a fresh attempt with a longer `--expires`; there is no override. |
| `configured bound exceeded` | `pack` and `fetch` | Per-entry bytes, total package bytes, entry count or record count is over the configured limit — the message names which one and both the observed and configured values. | Split the payload across more, smaller entries/attempts, or ask the operator to raise the bound if it is genuinely undersized for the dataset. |
| `cannot pass a report whose result is not pass` | never surfaced to a caller | This sentinel exists only as the internal guard `--record` checks to pick `verify-pass` vs `verify-fail` — no input makes it appear as an error message; it is what makes a forced pass unrepresentable rather than merely refused. See the next section. | N/A — there is no flag to work around, because there is nothing to work around. |
| illegal transition (e.g. `verify-fail refused: ...`) | `verify --record` | The handoff is not in `acknowledged` state yet (or is in a state the fold table does not permit this transition from). | `a2a ack <XH-id>` the handoff first, then retry `--record`. |
| `no committed handoff carries package …` | `verify` | The package id you gave was never referenced by any handoff's `deliverables[]` — you likely typed the wrong `DP-` id, or fetched from a different space than the one the handoff was submitted to. | Re-check the id against `a2a inbox`/`a2a show <XH-id>`. |
| `--supersedes is set at pack time, not here` | `deliver` | You put `--supersedes` on `deliver`. The chain lives in the manifest `pack` produced, so `deliver` cannot honour it — and ignoring it silently would let you believe attempt 2 was linked when it was not. | Re-run `a2a data pack ... --supersedes <prior-id>` and deliver the staging root it prints. |
| a divergent fetch destination | `fetch` | The destination directory already holds content that disagrees with the package. It is left completely untouched. | Fetch into a clean directory, or delete the local copy first if you are certain you want to overwrite it — `fetch` will not do that for you. |

## Do not drive the handoff by hand

`a2a verify-pass <XH-id>` and `a2a verify-fail <XH-id>` are the generic
lifecycle verbs and they WILL move a data handoff. Nothing stops you. Do not
use them here.

They record a verdict bound to nothing: no verification report exists
afterwards, no digest was re-proven, no conformance check ran against the
pinned contract, and the producer has no way to reproduce or contest what
you decided. The whole point of this loop is that a verdict names the exact
bytes it judged — a hand-driven transition is that guarantee thrown away
while still looking, in the thread, exactly like the real thing.

Use `a2a data verify <package-id> --record` instead. Reserve the generic
verbs for a handoff whose acceptance genuinely is a human judgement — code
review, a design sign-off — rather than a schema check.

## The verdict is derived — there is no flag to force a pass

`a2a data verify` has no `--pass` flag, deliberately. The report's `result`
is computed from its own `checks[]`; `--record`'s write direction
(`verify-pass` vs `verify-fail`) is read straight off that computed value,
never off anything the caller supplied. Do not look for a way to mark a
delivery as passing — if the payload is right, `verify` computes `pass` on
its own; if it is wrong, fix the payload and deliver a new attempt. There is
no manual override path, by design.

## A superseding attempt never erases what failed

Packing attempt 2 with `--supersedes <attempt-1-id>` does not touch attempt
1's manifest, payload or verification report — they remain committed and
readable at their original ids, and attempt 1's handoff stays `rejected`
rather than being deleted or rewritten. `data pack` refuses if the new
attempt's number is inconsistent with the one it supersedes (it must be
exactly `supersedes.attempt + 1`), so the chain is always reconstructible
from `manifest.supersedes` alone, one link at a time, back to attempt 1.
