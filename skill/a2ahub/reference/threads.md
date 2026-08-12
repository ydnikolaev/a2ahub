# Threads — one intent, one chain, both sides

> **Answers:** what a thread IS, why it is the unit you read rather than the
> individual artifact, how it is ordered, and how "whose move is it" is
> computed.
>
> **Read it when:** you are trying to establish the state of one piece of
> work, or you need to know which side owes the next act.
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
