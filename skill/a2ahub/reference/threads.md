# Threads — one intent, one chain, both sides

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
watch it — which `a2a inbox --actionable` will also tell you, because that
listing is this same computation projected onto your own system's inbox.

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
