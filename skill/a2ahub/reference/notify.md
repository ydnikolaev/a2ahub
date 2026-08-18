# Space notifications — every flag, and what each verb is for

> **Answers:** what each flag on `a2a notify render`, `send`, `setup`,
> `discover` and `verify` DECIDES (not merely that it exists), the exit-code
> contract for all five, and which event classes a route may subscribe to.
>
> **Read it when:** you are composing an `a2a notify` command, a route was
> refused, or a channel has gone quiet and you need to tell rendering from
> delivery.
>
> **Not here:** whether to turn the channel on and how the setup conversation
> goes ([notifications.md](../notifications.md)); rotating or tearing down a
> token (`docs/runbooks/space-notify.md`); what a failing `notify-*` doctor
> check means ([troubleshooting.md](../troubleshooting.md)).

## The two planes, so you are reading about the right one

`a2a notifications` (plural) is the LOCAL plane — macOS and VS Code toasts on
your own machine. `a2a notify` (singular) is the SPACE plane — the space's own
CI messaging a human on Telegram. This page is only the second.

## The five verbs

| Verb | What it is for |
|---|---|
| `render` | Reads the space **checkout** and prints the messages a push would produce, as JSON. Sends nothing. Never reads `.a2a/` — a CI runner has no cache. |
| `send` | Reads a JSON message array on **stdin** — what `render` prints — and delivers it, printing one delivery record per message. This is the verb the space's workflow runs. |
| `setup` | Walks a human through creating the bot, handing over its token, choosing a chat, and proving the path works. Prints a route stanza to paste into `space.yaml`. |
| `discover` | Waits for a message to the bot and prints the chat id it came from. The step that turns "I made a bot" into a routable address. |
| `verify` | Checks the three facts a working channel needs: the route is well-formed, the secret it names exists, the last delivery succeeded. |

## Flags, and what each one decides

| Verb | Flag | Required | What it decides |
|---|---|---|---|
| `render` | `--base <sha>` | one of three | The push range. Everything `<sha>..HEAD` changed becomes the candidate set. |
| `render` | `--all` | one of three | Every qualifying artifact in the space, ignoring any push range. Cost grows with the space, not with the push. |
| `render` | `--only <id,id>` | one of three | Exactly these artifacts, **bypassing the push range, the route's event filter and the digest cap**. This is the one for reviewing layout: a document settled last month renders exactly as it would have when it was new. |
| `render` | `--limit <n>` | no (default `5`) | The digest-coalescing threshold on the `--base`/`--all` paths: a route sees this many artifacts as individual messages and everything beyond folds into one digest message, so a large push cannot become a wall. `--only` ignores it. |
| `render` | `--json` | no | **Inert.** JSON is the only output this verb has; the flag exists for symmetry with other verbs. |
| `send` | `--dry-run` | no | Performs no API call and prints each delivery record with the full payload it WOULD have sent, through the same renderer the live path uses. The sanctioned rehearsal. |
| `setup` | `--for <participant>` | no | The `for:` field of the printed route stanza — which participant this route serves. |
| `setup` | `--events <list>` | no (default `human-gate,blocking`) | The `events:` list of the printed stanza. Legal classes are `human-gate`, `blocking`, `published`; anything else, a duplicate, or an empty list is refused by name. The default is the two classes that mean a human is actually needed — `published` is opt-in per route, deliberately, because plan §11.3 marks ordinary publication chat-✘. |
| `setup` | `--locale <ru\|en>` | no (default `en`) | The language of the printed BotFather instructions. Nothing else in the flow changes. |
| `setup` | `--non-interactive` | no | Refuses to prompt for a token and prints the exact `gh secret set` one-liner instead. **Use this when an agent is driving the session** — it is what keeps a token out of a conversation. |
| `setup` | `--space <id>` | no | **Inert.** Cosmetic; the repo identity comes from the local git remote. |
| `discover` | `--timeout <duration>` | no (default `10s`) | How long to wait for an update before giving up. |
| `verify` | `--space <id>` | no | **Inert.** Same as `setup --space`. |

**Exactly one of `--base`, `--all`, `--only` is required.** They are alternative
ways to choose the candidate set, not options that combine; naming none, or
two, is a usage error.

## Exit codes

Two of these are easy to misread, and a script that treats every non-zero code
as an outage will get both wrong.

| Verb | 0 | 1 | 2 |
|---|---|---|---|
| `render` | messages printed | a refusal (unknown `--only` id, a route naming an undeclared secret) or an unreadable checkout | usage — no range selector, two of them, or a bad `--limit` |
| `send` | every record sent, or dry-run | **at least one delivery failed** — the others still went | usage, or an invalid message array on stdin |
| `setup` | the step reached its end | any named error, **including `--non-interactive`'s deliberate refusal to prompt** — the intended path, not a fault | usage |
| `discover` | a chat id was found | no update arrived within `--timeout`, or the API refused | usage |
| `verify` | all three facts check out | at least one did not | usage |

## Event classes

A route subscribes to classes, not to document types. There are three, and a
fourth name that is not subscribable:

| Class | What lands in it |
|---|---|
| `human-gate` | A move that needs a person — the case the channel exists for. |
| `blocking` | Something that stops work until it is answered. |
| `published` | Ordinary publication. Off by default per route; §11.3 marks this chat-✘ and calls widening to it a decision somebody takes, not a default. |
| `digest` | **Not subscribable.** The class of the coalesced message `--limit` produces; naming it in a route is refused. |

## What is not wired yet

`a2a_notify` is registered on the MCP surface with the same five actions, so an
agent listing tools will find it — and **every call answers that notify
operations are not available**. It is registered with a zero-value dependency
set on purpose; the follow-up is named in `internal/mcp/tools_notify.go`'s own
doc comment. Use the CLI verbs above.

This is written down because a tool that is present and always refuses, with
nothing saying why, costs an agent a debugging session it cannot win.
