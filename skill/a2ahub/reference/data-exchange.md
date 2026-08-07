# Data exchange — packing, delivering, fetching and verifying a payload

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
> exact usage and exits `2`. The table below is that surface written out
> with meanings, which a usage line cannot give you.

## Flags, and what each one decides

| Verb | Flag | Required | What it decides |
|---|---|---|---|
| `pack` | `--contract <XC-id>@<version>` | yes | The exact contract version every entry is checked against, pinned into the manifest. A digest suffix (`#sha256:…`) is accepted and re-proven. |
| `pack` | `--from <dir>` | yes | The local source tree. Read-only; nothing under it is modified. See the directory-mapping section below — this is where most packs fail. |
| `pack` | `--profile synthetic\|sanitized` | yes | The data-handling claim recorded in the manifest. `synthetic` asserts the payload was generated and never derived from real records; `sanitized` asserts it was derived from real data with identifying content removed. **Nothing verifies which is true** — it is your assertion, carried to the consumer, so claim the weaker one when unsure: anything derived from production data is `sanitized`, never `synthetic`. `production` is refused outright and is not a third option. |
| `pack` | `--format json\|ndjson` | yes | How every dataset entry is parsed and counted. `ndjson` is one JSON document per line and is what makes a violation report an exact **record number**; `json` is one document per file and reports the file only. Choose `ndjson` for anything row-shaped — the consumer's feedback is only as aimed as this choice. |
| `pack` | `--expires <duration>` | no (default `168h`) | How long the attempt stays fetchable. Non-positive is refused. No maximum. |
| `pack` | `--fulfills <XW-id>` | no | The `work_request` this attempt will answer. Optional here — it only supplies the thread — but **required at `deliver`**, so pass it at both and the two never disagree. |
| `pack` | `--supersedes <DP-id>` | no | The prior attempt this one replaces. **This is the only place supersession is set** — it is baked into the manifest here. |
| `pack` | `--max-attempts <n>` | no (default `0` = no ceiling) | An escalation guard. When set, packing an attempt whose number exceeds `n` is refused with *"escalate rather than retry"* instead of minting attempt N+1. Use it when a loop could otherwise retry forever; leave it unset and nothing is ever refused on this ground. |
| `pack` | `--json` | no | Machine-readable result, including `staging_root`. |
| `deliver` | `<staging-root>` (positional) | yes | The path `pack` printed. Do not reconstruct it from the package id. |
| `deliver` | `--fulfills <XW-id>` | yes | The request this delivery answers. Required here even if `pack` also received it. |
| `deliver` | `--expect-pack <digest>` | no | Binds this call to the exact `aggregate_digest` `pack` printed; refuses if the staged manifest changed underneath you. |
| `deliver` | `--supersedes` | — | **Refused.** Belongs to `pack`. |
| `deliver` | `--json` | no | Machine-readable result. |
| `fetch` | `<DP-id>` (positional) | yes | The package to retrieve. |
| `fetch` | `--to <dir>` | yes | Destination. A divergent existing destination is refused untouched; a byte-identical one succeeds as `already-present`. |
| `fetch` | `--json` | no | Machine-readable result. |
| `verify` | `<DP-id>` (positional) | yes | The package to judge. |
| `verify` | `--record` | no | Performs the ONE funnel write (report + lifecycle event). Without it, nothing is written anywhere. |
| `verify` | `--json` | no | Machine-readable result — see the verdict-vs-error rule below. |

`deliver` and `verify` also accept the ordinary lifecycle actor flags
(`--actor-kind`, `--actor-name`, `--actor-model`), because both write a
lifecycle event; they behave exactly as they do on any other transition verb
and are normally left to the configured identity —
[actor-identity.md](actor-identity.md) owns what that resolves to.

**Exit codes.** `0` success. `1` a failing verdict **or** a refusal — these
are distinguished by content, not by code (see the `--json` rule in the
consumer sequence). `2` a usage error: a missing required flag, an unknown
one, or a malformed package id.

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

Every row below is a hard refusal, not a warning — the command stops and
nothing is staged or written on the way to it.

**A failing verdict is not in this table.** An entry that does not conform to
its contract is a *refusal* at `pack` — nothing is staged, no package exists.
The same condition after delivery is a *verdict*: `verify` returns a complete
report whose `checks[]` carry the violations, exits `1`, and refuses nothing.
That distinction is the whole point of the loop, so do not go looking for an
error envelope when a delivery fails its checks — read the report.

| Refusal (sentinel) | Fires at | What it means | What to do |
|---|---|---|---|
| `data_profile production is refused` | `pack` | You set `--profile production`. Not negotiable — real end-user data may not enter the shared space. | Re-pack with `--profile synthetic` or `--profile sanitized`, sanitizing the source first if needed. |
| `entry does not conform to its contract schema` | `pack` **only** | One file failed its `conforms_to` schema and the whole pack is refused — no package is minted. The message names the file; for an `ndjson` entry it also names the failing **record number**. | Fix that file (or that record) and re-pack. After delivery this same condition is not a refusal at all: `verify` returns a report whose `checks[]` name it, and the fix is a **superseding** attempt — a delivered package is never editable in place. |
| `declared digest does not match computed digest` | `deliver`, `fetch`, `verify` — before parsing | The bytes do not hash to what the manifest declares, checked per entry before assembly (never a matching aggregate masking a tampered entry). At `deliver` this is the guard that reads the staging root the manifest points at; downstream it protects the consumer. | Re-`fetch` (or re-sync the mirror) rather than trust the local copy. At `deliver` it means the staging tree changed under you — re-`pack`. If it recurs after a fetch, the package is corrupt and needs a new attempt from the producer. |
| `--expires must be positive` | `pack` | You passed a zero or negative `--expires`. The attempt would be expired the moment it existed. | Drop the flag to take the one-week default, or pass a real duration. |
| `package has expired` | `fetch` only | `now` is past the manifest's `expires_at`. `verify` does **not** check this — a package already fetched, or resolved straight from the mirror, can still be verified after its nominal expiry. | Ask the producer for a fresh attempt with a longer `--expires`; there is no override. |
| `configured bound exceeded` | `pack` and `fetch` | Per-entry bytes, total package bytes, entry count or record count is over the configured limit — the message names which one and both the observed and configured values. | Split the payload across more, smaller entries/attempts, or ask the operator to raise the bound if it is genuinely undersized for the dataset. |
| `cannot pass a report whose result is not pass` | never surfaced to a caller | This sentinel exists only as the internal guard `--record` checks to pick `verify-pass` vs `verify-fail` — no input makes it appear as an error message; it is what makes a forced pass unrepresentable rather than merely refused. See the next section. | N/A — there is no flag to work around, because there is nothing to work around. |
| illegal transition (e.g. `verify-fail refused: ...`) | `verify --record` | The handoff is not in `acknowledged` state yet (or is in a state the fold table does not permit this transition from). | `a2a ack <XH-id>` the handoff first, then retry `--record`. |
| `handoff submit refused: …` | `deliver` | `deliver` writes a fresh handoff, and that submit goes through the same fold legality check as any other — most often your own system's membership in the space is not current. Nothing was written. | Fix the underlying condition the message names (usually `a2a sync`, or a membership that has not merged yet) and re-run the same `deliver`; it repairs its own pull request rather than opening a second one. |
| `no committed handoff carries package …` | `verify` | The package id you gave was never referenced by any handoff's `deliverables[]` — you likely typed the wrong `DP-` id, or fetched from a different space than the one the handoff was submitted to. | Re-check the id against `a2a inbox`/`a2a show <XH-id>`. |
| `--supersedes is set at pack time, not here` | `deliver` | You put `--supersedes` on `deliver`. The chain lives in the manifest `pack` produced, so `deliver` cannot honour it — and ignoring it silently would let you believe attempt 2 was linked when it was not. | Re-run `a2a data pack ... --supersedes <prior-id>` and deliver the staging root it prints. |
| a divergent fetch destination | `fetch` | The destination directory already holds content that disagrees with the package. It is left completely untouched. | Fetch into a clean directory, or delete the local copy first if you are certain you want to overwrite it — `fetch` will not do that for you. |
| `symlinks are refused` | `pack`, `fetch` | Your source tree (or the delivered set) contains a symlink. A package must be exactly the bytes it declares; a link is a pointer to bytes nobody agreed on, and following it would let a package smuggle in content from outside its own root. | Replace the link with the real file, or exclude it from the source directory. There is no follow-symlinks option. |
| `entry path escapes the package root` | `pack`, `fetch` | A path resolves outside the package root — `..`, an absolute path, or a directory that changed underneath the walk. | Pack from a directory that contains only what you intend to ship. If it recurs on `fetch`, the package itself is malformed and the producer must re-pack. |
| `two entries claim the same path` | `pack` | Two files in the source map to the same package-relative path. Digests are per path, so the package would be ambiguous about which bytes that path means. | De-duplicate the source tree; on a case-insensitive filesystem, check for two names that differ only in case. |
| `declared entry has no delivered bytes` | `pack`, `fetch`, `verify` | The manifest declares an entry that is not present. On `pack` a listed file vanished from the source; on `fetch`/`verify` the delivered set is incomplete. | Re-run `pack` against a stable source tree. After delivery this means the package is broken: it needs a superseding attempt, not a re-fetch. |
| `attempt ceiling reached` | `pack` | You set `--max-attempts <n>` and this attempt would exceed it. The message names the attempt number and the configured maximum. | This is the guard doing its job: **escalate rather than retry** (loops.md §8.5). If the ceiling is genuinely too low for the work, re-pack with a higher `--max-attempts` deliberately, not reflexively. |
| `locator must not be a credential or an absolute path` | `pack` | The payload locator carries a credential or an absolute path. Neither may be recorded in a manifest that lands in a shared repository. | Use a relative, credential-free locator. Never embed a token to make a fetch work — that is a leak into git history, not a configuration step. |
| `resolved contract ref does not match the contract pinned in the manifest` | `verify` | The contract the package pinned is not the contract that resolved locally — a different id, a different version, or a different digest. The verdict is refused rather than computed against the wrong schema. | `a2a sync`, then retry. If it persists, the producer pinned a version your mirror does not have, or the pin and the local contract genuinely disagree — resolve that before any verdict means anything. |
| `atomic no-replace directory install is unsupported on this platform` | `fetch` | The platform cannot install the destination directory atomically-without-replacing, so `fetch` refuses rather than fall back to a non-atomic write that could half-populate a directory. | Fetch to a path on a filesystem that supports it. This is a platform property, not something the package or the flags can change. |

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

## If the pinned contract was superseded while the loop was in flight

A package pins one exact contract version and is always judged against that
version — the one the producer packed against, never whatever is newest. When
`verify` notices that a newer version of the same contract exists, it records
`observed.contract_superseded_by` in the report and **changes nothing about
the verdict**. A package that conforms to the version it pinned passes, even
if that version is no longer current.

This is an observation for you to act on, not a failure:

- **Passing with `contract_superseded_by` set** means the delivery is correct
  and the pin is aging. Finish this exchange normally, then decide separately
  whether the next request should pin the newer version — that is the ordinary
  consumer loop ([loops.md](../loops.md) §8.4a), driven by
  `a2a contract diff <id> <old> <new>`, not by this field.
- **Do not fail a delivery for it.** The producer packed against what was
  pinned when the work was requested; rejecting that is rejecting them for
  your own version drift, and `verify` deliberately gives you no mechanism to
  do it.

A *different* contract entirely — a resolved ref that does not match the
manifest's pin — is not this case: that is refused outright rather than
observed (see `resolved contract ref does not match…` in the refusal table),
because a verdict computed against the wrong schema would mean nothing.

## A superseding attempt never erases what failed

Packing attempt 2 with `--supersedes <attempt-1-id>` does not touch attempt
1's manifest, payload or verification report — they remain committed and
readable at their original ids, and attempt 1's handoff stays `rejected`
rather than being deleted or rewritten. `data pack` refuses if the new
attempt's number is inconsistent with the one it supersedes (it must be
exactly `supersedes.attempt + 1`), so the chain is always reconstructible
from `manifest.supersedes` alone, one link at a time, back to attempt 1.
