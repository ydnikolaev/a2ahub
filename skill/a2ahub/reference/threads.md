# Threads — one intent, one chain, both sides

> **Answers:** what a thread IS, why it is the unit you read rather than the
> individual artifact, how it is ordered, how "whose move is it" is computed,
> and how far a read surface can be trusted — whether the view is current,
> whether your own write is in it, and where a record's claim is compared
> against what the space actually resolves to; also two refusals a submit can
> hit before any of that: a malformed declared date (`SCH-012`) and
> `classification: restricted` on a space that is not bilateral (`POL-024`,
> paired with `POL-026` when the check itself could not be run).
>
> **Read it when:** you are trying to establish the state of one piece of
> work, you need to know which side owes the next act, you are about to act
> on something a read surface did NOT show you, or `a2a submit` refused you
> with one of the codes above.
>
> **Not here:** the loop that acts on the answer
> ([loops/receive.md](../loops/receive.md),
> [loops/send.md](../loops/send.md)); how a thread is folded from events —
> state-as-a-fold is condensed in [loops.md](../loops.md).

A **thread** is every artifact belonging to one intent, in the order it
actually happened, across both systems and both repositories. It is the unit
you read when you want the answer to "what is the state of this piece of work",
and it exists because the alternative — reading a chat log, or a folder, or
your own memory of who said what — is what a2a is here to replace.

Every artifact carries a `thread` field. The first artifact of an intent
starts one and names itself; everything that answers, refines, decides, or
closes it carries the same value. `a2a thread <id>` then reconstructs the
whole chain from the space's committed history.

```sh
a2a thread XW-axon-20260721-abcd     # by any artifact in the chain
a2a thread <thread-id>               # or by the thread id itself
a2a thread <id> --json               # the full ThreadResult, for a harness
```

## Why this is worth having, and not just a nice view

**One question, one place, permanently.** The requirement, the acknowledgement,
the response, the evidence, the verification and the decision are separate
typed artifacts written by two different systems into one repository. The
thread is what makes them one object again. Nothing is reconstructed from
prose, and nothing depends on both sides having kept the same notes.

**Order that survives a disagreement.** The transcript is ordered by COMMIT
sequence, not by anyone's clock. Two systems in two timezones, with two agents
writing concurrently, still read the same sequence — because git's own
first-parent order is the referee, and neither side can quietly re-time its own
message. When commit order is unavailable the view says so (`order: declared`)
and falls back to the declared `created` timestamps, rather than pretending the
guarantee still holds.

**"Whose move is it" is computed, not remembered.** Alongside the transcript,
a thread reports its OPEN ITEMS: every artifact on which the protocol is
waiting for some system to act, naming that system and the act it owes.

Read that definition precisely, because the distinction it draws is the one
that makes the view useful. An artifact can be **live** — still existing, still
able to move — without anything being *pending* on anyone. A published contract
is the standing case: it is alive for as long as it is published, its owner may
always publish a successor or deprecate it, and none of that is a move anyone
is waiting for. Open items are the second thing, never the first. A settled
exchange and a healthy contract both drop off the list, and neither has
disappeared: the transcript keeps every artifact and every event forever, and
`a2a thread <id>` still shows the moves that remain legal — just not as
something owed.

The computation comes from the same lifecycle engine the write verbs enforce,
so the view can never name a move the tool would then refuse — and neither side
has to keep a private to-do list that drifts from the record.

**A cold start costs nothing.** An agent that has never seen this work before
reads one command and has the same picture as the agent that has been on it for
a month. That is the property that makes an unattended loop possible: session
memory stops being load-bearing.

## Reading one

The transcript is chronological and typed — each entry names the artifact, its
type, who wrote it, and the transition it recorded. Open items come last,
because they are what you act on:

```
open: XW-axon-20260721-abcd  work_request  responded
  next: close      by axon
  next: dispute    by axon
```

That reads: the exchange has been responded to, and the only two moves left
belong to `axon`, who asked. If your own system is not named in any `by`, the
thread is waiting on the other side and there is nothing for you to do but
watch it.

`a2a inbox --actionable` will usually agree, because both surfaces ask ONE
relation — "whose move is it" has a single home, and neither re-derives it.
They are **not the same computation**, and the difference is deliberate:
`--actionable` also surfaces an item that is urgent (`p1` or `blocking`) and
still open and yours to be a party to, even when the next move belongs to the
other side. That is a priority filter over LIVENESS, not a claim that anything
is owed by you.

So a thread can be settled on you and still appear in `--actionable`. Read the
open item's own `waiting_on` and `expected_transition` for who owes what; read
`--actionable` for what deserves your attention now. An earlier version of this
page called them one computation, and that sentence was the model defect in one
line.

## What a state means, and which event produced it

`a2a thread --json` carries the derived-state fields on every artifact —
`outcome`, `terminal`, `state_since`, `state_by`, `state_event` — and
`transition_free` on every event. [loops.md](../loops.md) §8.1 step 7 is where
they are taught, including the three wrong readings they invite; the two that
bite hardest when you are reading a THREAD specifically are worth repeating
here, because this is the page you are on when you make the mistake:

- **The transcript's last entry is not what moved the artifact.** A `note` is
  `transition_free` — it appears in the transcript, it is real activity, and
  it changed nothing. `state_since`/`state_by`/`state_event` name the event
  that produced the state you are reading; scanning back to the newest entry
  and calling that "when this last moved" is the exact bug the fields exist to
  end. Read the fields, not the tail of the list.
- **`outcome: refused` is not the end of the thread, and it is not "no open
  items" either.** They are separate answers to separate questions: a rejected
  handoff is `refused` and still carries an open item on its producer, who
  owes the superseding attempt. Whose move it is comes from the open-item
  list, always; `outcome` tells you how the artifact ITSELF ended up, and
  `terminal` tells you whether any move can still follow at all.

## How current is this view, and is your own write in it

Every read surface is served from a LOCAL MIRROR, and these fields say how much
of the space that mirror is showing you. They guard one sentence: **absence in
your copy is not absence in the space.** An agent that reads a short list and
concludes nobody owes anything has made the decision the protocol exists to
prevent — it cannot see what it owes, so it decides it owes nothing.

| Field | Answers | The wrong reading it invites |
|---|---|---|
| `sync_stale` | this mirror is not known to be current | that the listing you hold is the space |
| `sync_age` (on `a2a show`) | how long ago it was last fetched | that a small number means "just fetched" |
| `pending_merge` | a submit made ON THIS MACHINE has not been seen to land | that its absence means the artifact merged |
| `new` | this id was absent from the local read cursor | that it means "arrived since I last looked" |
| `state` | the fold of the events your mirror holds | that some system wrote it |

- **`sync_stale` is a fail-safe, not a measurement.** True when the sync-age
  exceeds the refresh TTL, and also when the mirror was NEVER fetched, and
  also when the space is not in the connected set. It never means "fetched,
  and that was a while ago". One remedy for all three: `a2a sync`, read again.
- **A `sync_age` of `0s` is the reading to distrust.** The age is measured
  from the last fetch, and a mirror that has never been fetched has no such
  instant — so it reports zero. `0s` beside `sync_stale: true` means never
  synced. Read it as an age only when `sync_stale` is false.
- **`pending_merge` is a statement about your machine.** A marker is written
  locally on a successful `a2a submit` and removed only when a later refresh
  finds that artifact in the mirror's canonical tree. `true` means "submitted
  from here, not yet SEEN to land" — it does not separate "the pull request is
  still open" from "it merged and you have not fetched since". `false` is
  weaker: the marker is machine-local and disposable, so a submit from another
  clone, or a cleared cache, leaves none at all. Your own write is not in the
  space until the space says so.
- **`new` is a cursor fact, and the cursor is shared.** The id was absent from
  the read-cursor snapshot when that snapshot last advanced. The snapshot
  covers every artifact in every connected space, and only `a2a inbox` and the
  MCP read tool's inbox view advance it — not `a2a outbox`, not the dashboard.
  So everything reads `new` on the first run after the cache is cleared; an
  item captured earlier and then MOVED is not `new`, because the cursor tracks
  presence, never movement ("state changed since the read cursor" is a
  separate computation, one of the things `a2a outbox --attention` surfaces);
  and an MCP read consumes newness for the CLI. Never build a notification on
  `new` alone.
- **`state` is derived on read.** No envelope field stores it
  ([loops.md](../loops.md) §3.4, state is a fold, not a field), and it folds
  the events YOUR MIRROR HOLDS — an event that has not reached you has not
  moved the state you are reading.

## What the record claims versus what the space resolves to

An event may carry the state its producer believed it was producing; a ref may
carry the digest its author believed they were pointing at. Both are CLAIMS,
recorded as written and never trusted as answers. The fold and the resolver
compute their own result, and where the two are compared the surface reports
BOTH halves rather than the conclusion.

**`claimed_state` is the producer's receipt, not the state.** What the acting
system said the artifact would be in after this event; retained for comparison
and deciding nothing. Act on the folded state.

**`consistency` appears only where the fold contradicted that receipt** —
`claimed` (what the event said), `actual` (what the fold computed, and the
authoritative one) and `cause`. Its ABSENCE is where the wrong reading lives,
because it covers four unlike situations and only one is a check that passed:

- the event carried no receipt — most do not;
- the transition has nothing scalar to have claimed, so nothing is compared: a
  `note`, an `acknowledge` on an announcement, a `respond`, a `dispute`, a
  `deprecate`/`retire` naming no version. **`scope` is the field that decides
  this**, and it is why that list is what it is: it names the ONE scalar fold
  result the receipt is measured against — `kind` is `primary`, `response` or
  `contract-version`, with `version` filled only for the last. A transition
  that moves nothing scalar gets no scope, and a move with no scope can never
  carry `consistency`. So an absent `scope` is not a gap in the record; it is
  the record saying there was nothing here to check;
- the event was ILLEGAL or made by an unauthorized actor. The fold ignored it
  and flagged it separately, and an ignored event's claim is never checked —
  the most wrong claim in a thread is the one least likely to carry this
  field;
- it was compared, and it held.

So "no `consistency`" means no contradiction was RECORDED, which includes
"nothing was compared". Present, it is non-blocking evidence: it changes no
state and refuses nothing. It says the producer's tooling disagreed with the
fold, usually from a stale view on their side — worth a `note`, not by itself
grounds for a dispute.

**`cause` always reads `unknown`.** Both writers set that literal and no
vocabulary for it exists: the tool records THAT a claim diverged, never why.
Do not read it as "we investigated and could not tell", and do not branch on
it.

**A ref's digest fields are the same shape.** On `a2a show`, `pinned_digest`
is the digest the author wrote into the ref (`id#digest`) — their claim;
`resolved_digest` is the digest that id resolves to in your mirror now;
`digest_mismatch` is true only when both exist and differ. So `false` covers
three unlike cases: pinned, resolved and equal (a real check that passed); NOT
pinned, so there was nothing to compare; or pinned but unresolvable here, so
the comparison could not run. `resolved` is what tells them apart. `a2a show`
warns on two of the three — **REF-004** for a pinned digest that differs,
**REF-008** for a pinned ref it could not resolve — and says nothing whatever
for an UNPINNED ref that resolves to nothing: `resolved: false`,
`digest_mismatch: false`, no warning, no verdict.

**A thread resolves no digests.** `a2a thread`'s `refs[]` entries carry the
grammar string and nothing else, deliberately — `a2a show` owns the verdict.
A ref that reads as pinned in a transcript has been checked by nobody.

**`verification_claim` states what was declared, not what happened.** It is
the reader-facing sentence for an attachment's `verification` value on
`a2a show`. "a verdict is required for these bytes" and "a verdict is offered
for these bytes" describe what the producer DECLARED; neither asserts a
verdict exists, or that these bytes passed one. "no verdict is defined for
these bytes" is a deliberate, correctly-formed statement, not a failure. The
verdict itself comes from `a2a data verify --record` —
[data-exchange.md](data-exchange.md).

## Where it shows up

- `a2a thread <id>` — the transcript and open items.
- `a2a html` — the Threads tab, the same data rendered as a chain, with each
  artifact's type and state visible at once.
- `a2a inbox --actionable` — condition 2 and 3 of the actionable union are
  thread facts ("responded, awaiting my verify or close"; "disputed toward
  me"), so a thread that needs you surfaces without you going to look.

## The one thing to get right when authoring

Carry the `thread` field forward. Every `a2a new` fills it for you when you
draft a reply to something (`--parent`, `--thread`), and `a2a validate` refuses
an artifact that claims a thread which does not exist. Where it goes wrong is a
NEW artifact started by hand for work that already had a thread — the record
then holds two chains describing one intent, and no command can merge them
afterwards, because nothing in the data says they were ever the same thing.

If you are unsure whether something belongs to an existing intent or starts a
new one, `reference/decompose-example.md` walks a real case of splitting one
oversized request into single-intent threads.

## Correcting a sent document

Committed artifacts are immutable, but their thread is not frozen. A small
clarification that leaves every commitment intact is an annotation on the
existing artifact: `a2a note --note <clarification> <id>`. A correction to the
deadline, acceptance criteria, requested result, addressee, or meaning is a new
successor artifact on this same thread. Set `supersedes: <old-id>` in the new
artifact, submit it with the complete corrected text, then run
`a2a supersede --refs <new-id> <old-id>`. The thread preserves both the audit
trail and one unambiguous current document; readers never have to treat an old
body plus a note as a silently edited contract.

### One predecessor, one successor — a fork or a cycle is refused

A supersession chain must be LINEAR, and as of `0.19.10` that is enforced
rather than assumed. `a2a validate --ci --mode=v3-full-repo` refuses two
distinct successors claiming the same predecessor (**REF-020**, a fork) and
any chain that loops back on itself, self-supersede included (**REF-021**, a
cycle). Both refusals name the ids involved — for a fork, BOTH claimants,
because a whole-repository walk has no stable notion of which one arrived
second and blaming "the later one" would be an invention.

The reason to care is the cycle: an artifact reachable from itself through
`supersedes` has no state a reader can settle on, so the same history folds
differently depending on where the reader started. A fork is the milder
version of the same defect — two documents both presenting as the current one.

What this changes for an author, concretely:

- **Correcting a correction is a chain, not a second branch.** If successor B
  already supersedes A and B turns out wrong too, author C with
  `supersedes: <B-id>` and run `a2a supersede <B-id> --refs <C-id>`. Pointing
  C back at A instead gives A two claimants and reds the space's full-repo
  validation.
- **The refusal is not on your machine.** The check runs only in full-repo
  mode, deliberately: a pull request carries a slice of the graph, and a
  partial graph would report a fork that does not exist. So a local
  `a2a validate` will not catch this — you meet it at merge, on a red the
  whole space sees. Check what already supersedes the artifact (`a2a thread
  <id>`) before authoring a successor, not after.
- **A space whose history already carries one goes red at the next full-repo
  run.** That history was ambiguous before the check existed; the refusal is
  what makes it visible, not what made it wrong.

## A declared date that is not a real date (`SCH-012`)

`envelope/v1` and `envelope/v2`'s base schema declare four date-shaped
fields: `created` (an RFC-3339 timestamp), `needed_by`/`valid_until`, and
`expected_response.by` (both plain ISO-8601 dates). Before this rule, the
schema's `format` keyword on those fields was annotation-only — present in
the document, checked by nothing — so a string that merely *looked* like a
date validated even if it could not parse as one. `SCH-012` is that gap
closed: the value must now parse as the format it declares, not just match
its shape.

**What actually trips it, in practice:**

- **A value that reads as a date and is not one** — `needed_by: "next tuesday"`
  is the canonical case. It is a string in the shape of an intention, and
  before `SCH-012` it validated clean.

- **NOT a template placeholder.** This is the trap worth stating explicitly,
  because the obvious guess is wrong: `needed_by: <YYYY-MM-DD>` — the literal
  placeholder the authoring templates ship — is **exempt from `SCH-012` at
  every tier**, deliberately. Any value matching `<...>` has its format
  violation dropped before you ever see it, so an unedited draft does not
  drown in refusals about fields you have not filled in yet. A placeholder
  left in place is refused by **`POL-010` instead, and only at submit** —
  which means `a2a validate` on a draft will NOT catch it. Verified against
  the binary rather than read off the schema: `<YYYY-MM-DD>` produces no
  violation, `next tuesday` and `2026-02-30` both produce a `format` one.
- **A calendar date that does not exist** — `2026-02-30`, a 13th month, an
  hour past 23. The shape (`YYYY-MM-DD`) is right; the value it names is not
  a day that ever happens.
- **A timestamp missing its zone or its `T`/`Z` markers** on `created`,
  which needs a full RFC-3339 instant, not a bare calendar date.

**What to do:** fix the value to a real, parseable date in the field's own
format — `created` as a full RFC-3339 UTC timestamp (`2026-08-27T13:00:00Z`),
`needed_by`/`valid_until`/`expected_response.by` as a plain `YYYY-MM-DD`.
There is no way to opt out: a `severity: reject` code refuses the whole
submission, so nothing is staged or written on the way to it.

## Classification: restricted requires a bilateral space (`POL-024`, `POL-026`)

`classification: restricted` is a promise about who may ever see the
artifact: the space's own ACTIVE participants must not exceed `{from} ∪ to`
— the sender plus whoever it is addressed to, and nobody else currently in
the space. `POL-024` is that promise checked at submit time, against the
space manifest's own participant list (`checkClassificationBilateral`,
`internal/validate/classification.go`).

**`POL-024` fires alone** when the space carries at least one ACTIVE
participant outside `{from} ∪ to` on a `restricted` artifact. The fix is one
of: narrow `to` to actually cover everyone active in the space, or
reclassify the artifact (`classification: internal` is the ordinary
default) if it does not truly need to be kept from a third participant. A
`to: all` broadcast is read as "the space's own full active membership" and
is therefore never itself flagged by this rule — which is a narrower
guarantee than it sounds: a broadcast is, in substance, the *least*
restricted audience a message can have, so `restricted` paired with
`to: all` is not a second safety net, just an untested combination as far
as `POL-024` is concerned.

**`POL-024` fires again, paired with `POL-026` (unmeasured)**, on a
different condition: the checker could not even ASK who the space's active
participants are, because the caller it is running inside has no way to
enumerate them. This is never silent — a `restricted` artifact this tool
cannot check is refused (the SAME `POL-024`, the rule's own code, not a
second code minted for this branch) rather than waved through, and
`POL-026` rides beside it to say *why* — "this could not be checked", not
"this failed a check". There is no author-side fix for this pairing:
nothing about the document is wrong. If you meet it, the caller you
submitted through is missing a capability that this tool's own shipped
adapters (`a2a submit`, the MCP `a2a_submit` tool) already carry — retry
from an up-to-date `a2a` binary, or escalate to your operator
([loops/escalation.md](../loops/escalation.md)) if it persists.
