# Notifications

> **Answers:** the activation, install and update decision table for macOS and
> VS Code notifications, the project/global prompt state a2a keeps, and the
> boundary around the user's own statusline.
>
> **Read it when:** `a2a notifications status --json` reported an offer state
> you have to act on, or a human asked to be notified outside the terminal.
>
> **Not here:** what each `a2a notify` flag decides, the exit codes and the
> event classes — that is [reference/notify.md](reference/notify.md), including
> the MCP twin's current state; the pull-based channels an agent watches for
> itself — that is the watch loop ([loops/watch.md](loops/watch.md)); what a
> failing notifications check means ([troubleshooting.md](troubleshooting.md)).

Use the binary as the source of truth.
[reference/commands.md](reference/commands.md) catalogues which verbs exist —
synopsis only, no flags.

## Activation preflight

Run once for the current project:

```text
a2a notifications status --json
```

This is a passive read: it must not register the project, consume the human
inbox cursor, launch UI, or run platform subprocess probes. It may spend up to
the bounded update-check deadline refreshing the shared release cache so users
without a terminal statusline still discover a2a updates.

| `offer.state` | Agent action |
|---|---|
| `enabled` | Say nothing unless setup or health is relevant to the request. |
| `ask` | Ask whether to enable notifications. Offer only channels in `available_channels`: macOS, VS Code, or both. Also offer “not now; remind me in N days” and “no; do not remind me for this project.” |
| `snoozed`, `never` | Do not ask again. |
| `unavailable` | Do not ask; mention the missing supported surface only when relevant. |

Act only after the human chooses:

```text
a2a notifications install --channel macos
a2a notifications install --channel vscode
a2a notifications install --channel all
a2a notifications preference --remind-in 7
a2a notifications preference --never
```

Use `--global` with `--never` only when the human explicitly means every a2a
project on this machine. A project choice must remain project-scoped. Reset a
previous choice only on request.

Installation is agent-free: the same commands are intended for a human to run
directly. They install or repair the cohort-verified macOS app or
version-matched VSIX, enrol only the current project in the chosen channels,
and keep other project enrolments intact. The macOS app currently uses an
ad-hoc code signature rather than a paid Developer ID. If the result is
`approval-required`, tell the human to open the installed **A2A Notifier**
once, choose **System Settings → Privacy & Security → Open Anyway**, and repeat
the same install command. Never run `xattr`, clear quarantine, or disable
Gatekeeper for them. Use `status` to diagnose and `test` for a synthetic
readiness notification.

## Statusline boundary

The terminal statusline is separate, optional, and user-owned.
`a2a notifications install` never reads or edits it.

If asked to add terminal status, inspect the existing provider first. Preserve
its bytes, JSON, and exit contract and propose an additive `a2a statusline`
segment. Do not replace a custom statusline and do not modify it without
explicit consent.

## Click and update behavior

macOS banners and VS Code items invoke only an opaque route through
`a2a notifications open`; they never construct a path, URL, or command from
artifact text. The route opens the local a2a HTML view and focuses the item.

Update notifications focus the local What’s New card. An older binary can show
future prose only when the standalone `release-notes/v1` asset from the signed
release cohort was verified and cached. Otherwise the card must show the
target version and a clear version-only fallback. Never claim that the old
binary’s embedded notes describe a future release, and never auto-update
without the consent described in `SKILL.md`.

## Two planes, two verbs

`a2a notifications` and `a2a notify` are different commands, and the names are
close enough to confuse. Read the plane, not the spelling.

| | `a2a notifications` | `a2a notify` |
|---|---|---|
| Plane | **local** — this machine | **space-side** — the space repo's CI |
| Surfaces | macOS Notification Center, VS Code | a chat adapter (Telegram in v1) |
| Runs when | the human's machine is awake and a2a is installed | a push lands on the space's `main`, on GitHub's runner |
| Configured by | `a2a notifications install` and the offer state above | `notification_routes` in the space's own `space.yaml`, reviewed in a pull request |
| Silent when | the offer was declined, or nothing is installed | no route is configured — the workflow completes and says so |

The split exists because the local plane cannot help a human whose laptop is
asleep, and the plan assigns that job to a hub that does not exist yet. Until it
does, the space's own CI is the only always-on party, so it is what speaks.

## `a2a notify render` — what a push would say

`render` reads the space **checkout** and prints the messages a push would
produce, as JSON, and sends nothing. It never reads `.a2a/` — a CI runner has no
cache, so the facts come from the repository itself, folded the same way every
other read surface folds them.

```sh
a2a notify render --base <sha>     # everything the range changed
a2a notify render --all            # the whole space
a2a notify render --only XW-…,XC-… # exactly these, whatever their state
```

**Exactly one of those three is required** — they choose the candidate set,
they do not combine. What each one and `--limit` decide is
[reference/notify.md](reference/notify.md).

Each message carries the facts a decision needs — type, who to whom, whose turn,
priority and deadline, the artifact's own body — and, for a route that names a
participant, the copy-paste block that hands the move to an agent.

`--only` is the one that matters when reviewing layout: it ignores the push
range, the route's event filter and the digest cap, so a settled document from
last month renders exactly as it would have when it was new.

A route naming a secret this version does not declare is refused here, by name,
rather than discovered as a missing environment variable inside the sender.

## `a2a notify send` — the verb CI actually runs

`send` reads a JSON message array on **stdin** — the array `render` prints — and
delivers it, printing one delivery record per message. It is what the space's
workflow runs; you rarely type it, but you do want it when a channel has gone
quiet and you need to know whether the failure is rendering or delivery.

```sh
a2a notify render --base <sha> | a2a notify send --dry-run
```

`--dry-run` performs no API call and prints each record with the full payload
it WOULD have sent, through the same renderer the live path uses. That is the
sanctioned rehearsal: a layout can be reviewed, and a failure read, without a
chat receiving anything.

Every flag on all five verbs, the exit codes, and the event classes a route may
subscribe to are [reference/notify.md](reference/notify.md). Two exit codes are
worth knowing before a script treats non-zero as an outage: `send` exiting 1
means at least one delivery failed while the others went, and `setup
--non-interactive` exiting 1 is its intended refusal to prompt.

## `a2a notify setup` — the flow that never sees the token

`a2a notify setup` walks a human from "no bot" to a proven, delivering route.
**The agent never asks for the token and never receives it** — the tool reads
it on the human's own TTY with echo disabled, holds it only in memory, and
feeds `gh secret set` over stdin. If an agent is driving a non-interactive
session, run `a2a notify setup --non-interactive`: it refuses to prompt and
prints the exact one-liner for the human to run themselves. Never ask a human
to paste a bot token into chat, and never accept one if offered — point them
at the command instead.

The flow, once the human runs it in a terminal:

1. Prints a four-line BotFather instruction (open @BotFather, `/newbot`, copy
   the token).
2. Reads the token on the TTY, checks the caller has **admin** on the space
   repo (needed for the secret) and separately reports the **`workflow`**
   token scope (needed only if the same human later runs `a2a space update`) —
   two distinct facts, never conflated.
3. Sets `TG_BOT_TOKEN` on the space repo via `gh secret set`, then
   immediately calls `a2a notify discover` with the same in-memory token so
   the human sees which chats the bot can already see — no group privacy mode
   change required, one `getUpdates` call restricted to `my_chat_member`.
4. Prints the exact `notification_routes` stanza to add to `space.yaml` — a
   normal, reviewable pull request through the same write funnel every other
   manifest change uses. This tool never opens that PR itself.
5. Once the route has merged, re-running `a2a notify setup` triggers the
   space's `a2a-notify.yml` via `workflow_dispatch`, naming a real artifact id
   through the `artifacts` input (never the bare dispatch, which would prove
   nothing — see the runbook), and reports one of three outcomes:
   `configured` (the run was green **and delivered at least one message**),
   `unproven` (green, but sent nothing — a green run that sent nothing is
   never reported as configured), or the run's own conclusion on failure.

`a2a notify discover` and `a2a notify verify` are the read-only halves:
`discover` lists the chats the bot currently sees; `verify` reports the same
three facts `a2a doctor` does — route validity, secret presence (never the
value), and the last `a2a-notify` run's conclusion — as JSON, for a space
whose route is already live.

Rotation and teardown live in
[../../docs/runbooks/space-notify.md](../../docs/runbooks/space-notify.md),
not here — that is an operator runbook, not agent-facing skill prose.
