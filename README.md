# a2ahub

[![Latest release](https://img.shields.io/github/v/release/ydnikolaev/a2ahub?label=release)](https://github.com/ydnikolaev/a2ahub/releases/latest)
[![CodeQL](https://github.com/ydnikolaev/a2ahub/actions/workflows/codeql.yml/badge.svg)](https://github.com/ydnikolaev/a2ahub/actions/workflows/codeql.yml)
[![License](https://img.shields.io/github/license/ydnikolaev/a2ahub)](LICENSE)
**Reliable handoffs between autonomous agents, using a Git repository both
sides can inspect.**

## Who this is for

AI-first developers whose systems hand work to each other — your agents and a
partner's, two teams in one company, or a client and a contractor. The boundary
changes; the problem does not.

Two agents can exchange work in chat, but chat is a poor system of record:
requests get buried, contracts drift, nobody knows whose move is next, and a
handoff that worked once is hard to reproduce.

**A concrete case.** Your agent needs a data export from a partner's agent,
against an agreed shape. The request, its acceptance criteria, the payload, the
verdict on whether it met them and the sign-off all land in one Git repository
both sides can read — with neither company opening a system to the other.

Each system runs the local `a2a` CLI and connects to a GitHub repository called
a *space*. Changes go through pull requests and the space's validation gate.
There is no hosted a2ahub service, database, or public agent endpoint to keep
alive.

## What it gives you

- **Typed artifacts instead of loose messages.** Questions, requirements, work
  requests, decisions, responses and handoffs, each with the fields its kind
  needs, connected into one chain either side can replay in order.
- **A computed inbox.** Open work and whose move is next, derived from shared
  history rather than a private to-do list. Agents report what they are
  implementing separately from protocol completion, so a closed thread never
  means nobody is working — and a missing report stays unknown, not idle.
- **Contracts you can pin and reproduce.** Publish an immutable carried set,
  materialize any historical version exactly, offline, and deliver against a
  pinned version with a verdict derived from the contract's own declared checks.
- **A safe write funnel.** Drafts are validated locally, submitted as pull
  requests, checked again in CI, and merged as an auditable Git commit. Inbound
  artifact text is treated as data, never as instructions.
- **A refusal instead of a silent yes.** An unrecognised field, an id it cannot
  place, a rule it cannot evaluate — each is named and refused rather than
  accepted and reported as success. An unknown answer is reported as unknown.
- **Local surfaces, and one that reaches you.** Work through the CLI or local
  stdio MCP tools, read the state as a bounded dashboard, and let a space tell
  you when a move is yours — on your machine, in your editor, or on Telegram.

Both machines can be offline at different times: Git holds the durable state.

## How it works

1. One agent drafts and submits a typed request or contract.
2. The space validates ownership, schema, lifecycle, and contract rules.
3. The other agent syncs, sees the next move, and responds through the same
   funnel.
4. Both sides read the folded current state and the immutable history behind it.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ydnikolaev/a2ahub/main/scripts/install.sh | sh
```

The installer downloads the latest release and verifies its checksum. Windows
archives and manual downloads are on the
[releases page](https://github.com/ydnikolaev/a2ahub/releases/latest).

## Start a project

```sh
a2a init
a2a connect <owner/space-repo>
a2a new question
a2a submit <artifact>
a2a inbox
```

Run `a2a` for the current command list. Run `a2a html -demo` to explore the
dashboard without connecting a real space.

## Release confidence

Release candidates are tested against a protected public GitHub space using two
independent identities, at a depth chosen for what the release changes. Green
proves that scope—not every state, and each release's notes say what was not.

## Documentation

[Project site](https://a2ahub.dev/) ·
[onboarding](skill/a2ahub/onboarding.md) ·
[command and MCP reference](skill/a2ahub/reference/commands.md) ·
[security and release verification](SECURITY.md) ·
[release notes](releasenotes/)

Apache-2.0 licensed. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
