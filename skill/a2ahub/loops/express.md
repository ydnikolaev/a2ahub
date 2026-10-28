# Express data loop — a data exchange in three commands

> **Answers:** how a `category: data` exchange runs in a space whose `flow` is
> `express`: which of the three composite commands each side runs, what they
> leave in the history, and when to step back into the full loop.
>
> **Read it when:** `a2a version --json` reports `"flow": "express"` for the
> space, and you want data from another system or owe it data.
>
> **Not here:** what each command sequences, how a re-run resumes, when it
> stops, and the flag combinations it refuses — that is "Express flow" in
> [data exchange](../reference/data-exchange.md). The flags themselves are in
> each command's `--help`. The full loop this shortens is the [send
> loop](send.md) and the [receive loop](receive.md).

## The express loop

A space's `flow` key decides which data loop this skill teaches there: `full`
is the send and receive loops, step by step; `express` is this page. A
*composite* command is one that runs several existing verbs in order for you.
The three below exist in every space, and the full loop stays legal in an
express space: choosing the wrong one costs extra commands, never a wrong
state.

| Step | Who | Command |
|---|---|---|
| 1. Ask | consumer (wants the data) | `a2a data request` |
| 2. Deliver | producer (owes the data) | `a2a data deliver --for <XW-id>` |
| 3. Judge and finish | consumer | `a2a data verify --for <XW-id> --record` |

**Consumer.**

1. Run `a2a data request` with the producer, the pinned contract and what you
   do while you wait. It submits a `work_request` with `category: data` and
   prints its `XW-` id and the command the producer runs next. The producer's
   agent finds the request in its inbox, as it would any other.
2. Once the producer has delivered, run
   `a2a data verify --for <XW-id> --record`. On a pass it closes the request
   and the exchange is done. On a fail it records the failure, disputes the
   producer's response with the failing findings and exits `1`; the move is
   then the producer's. If the producer has not delivered yet, it writes
   nothing, exits `0` and names the producer's command.

**Producer.**

1. Run `a2a data deliver --for <XW-id>` with the source directory, profile and
   format. It acknowledges and accepts the request, packs and delivers the
   payload, and responds naming the package. After a failed verify, run the
   same command again: it packs a new attempt that supersedes the rejected
   one.

A stopped command is re-run as it was: it skips every step already in the
space.

**What the history shows.** Each artifact goes through the same transitions,
in the same order, as in the full loop, and a passing exchange folds to the
same thread state. The order *between* artifacts differs: the producer's
response lands before the consumer's verify. A failing verify also disputes
the response, so each failed attempt leaves one response in `disputed` and one
dispute that the full loop would not have written.

**`--for` is what makes verify composite.** Bare
`a2a data verify <DP-package-id> --record` is the full loop's single step,
unchanged: it records the verdict on one package and closes nothing.

## When to step back into the full loop

The three commands cover one path: the producer delivers, the consumer judges.
Any other move is made with the full loop's verbs, on the same thread. The
thread does not record which commands moved it, so you can switch at any step.

- **The producer will not or cannot deliver** — declining, or blocking on a
  question first: the [receive loop](receive.md) (§8.3).
- **The producer contests a failed verify** instead of delivering again: the
  dispute step of the [receive loop](receive.md), and the [escalation
  ladder](escalation.md) once a dispute has run twice.
- **The producer replaces an attempt nobody has judged yet**:
  `deliver --for` supersedes only a rejected attempt, so pack the replacement
  with `--supersedes` as in the producer sequence of [data
  exchange](../reference/data-exchange.md).
