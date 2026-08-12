# Data exchange — what each refusal means, and how a verdict is derived

> **Answers:** every hard refusal the four `a2a data` verbs can raise and what
> to do about each, why a failing verdict is NOT one of them, and why there is
> no flag that forces a pass.
>
> **Read it when:** a data command stopped and named a condition, or you are
> deciding how to record a verdict on a delivered package.
>
> **Not here:** the producer and consumer sequences ([data
> exchange](data-exchange.md)); what each flag decides ([flags and source
> mapping](data-exchange-flags.md)).

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
| `contract version is not in the local mirror; run a2a sync` | `pack` | Your checkout has not synced far enough to see the pinned version. This is **your stale copy, not the producer's omission** — and it is deliberately the DEFAULT answer, because absence in a local mirror is not evidence of absence in the space. | `a2a sync`, then re-`pack`. Do not report it to the contract's author; there is nothing wrong on their side. |
| `contract version is not published` | `pack` | The mirror's own descriptor already reaches AT OR PAST the version you asked for and still carries no publish event naming it — positive evidence that the version was never published, not merely unseen. | Check the version against `a2a contracts` / `a2a show <XC-id>`; you pinned something that does not exist. This one IS the producer's business if you believe it should exist. **Do not confuse these two rows:** they used to be one sentence, and packing against an unsynced mirror blamed the contract's author for the reader's own stale copy. |
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
