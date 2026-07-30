# a2ahub

[![Latest release](https://img.shields.io/github/v/release/ydnikolaev/a2ahub?label=release)](https://github.com/ydnikolaev/a2ahub/releases/latest)
[![CodeQL](https://github.com/ydnikolaev/a2ahub/actions/workflows/codeql.yml/badge.svg)](https://github.com/ydnikolaev/a2ahub/actions/workflows/codeql.yml)
[![License](https://img.shields.io/github/license/ydnikolaev/a2ahub)](LICENSE)

**Reliable handoffs between autonomous agents, using a Git repository both
sides can inspect.**

Two agents can exchange work in chat, but chat is a poor system of record.
Requests get buried, contracts drift, nobody knows whose move is next, and a
handoff that worked once is hard to reproduce. a2ahub turns that conversation
into typed, validated artifacts with an explicit lifecycle.

Each system runs the local `a2a` CLI and connects to a GitHub repository called
a *space*. Changes go through pull requests and the space's validation gate.
There is no hosted a2ahub service, database, or public agent endpoint to keep
alive.

## What it gives you

- **Structured requests instead of loose messages.** Exchange questions,
  requirements, work requests, decisions, responses, handoffs, and
  announcements with the fields and acceptance criteria each kind needs.
- **One readable work chain.** A request, acknowledgement, response, evidence,
  verification, and decision stay connected. `a2a thread` reconstructs the
  ordered transcript for either side.
- **A computed inbox.** `a2a inbox`, `outbox`, and the local HTML dashboard show
  open work and whose move is next from the shared history—not from somebody's
  private to-do list.
- **Versioned data contracts.** Publish schemas with valid and invalid fixtures,
  compare versions, register consumers, announce deprecations, and prevent
  retirement until the right consumers acknowledge it.
- **A safe write funnel.** Drafts are validated locally, submitted as pull
  requests, checked again in CI, and merged as an auditable Git commit. Inbound
  artifact text is treated as data, never as instructions.
- **Useful local surfaces.** Work through the CLI or the local stdio MCP tools;
  open a self-contained graph/inbox/contracts dashboard with `a2a html`; enable
  macOS or VS Code notifications; or embed `a2a statusline` in a terminal
  prompt.

Both machines can be offline at different times. Git holds the durable state,
and either agent can rebuild its view from the repository.

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
independent identities. The latest full gate, for
[v0.16.0](https://github.com/ydnikolaev/a2ahub/releases/tag/v0.16.0), passed all
**50 of 50 declared live cells** across CLI, MCP, lifecycle, contracts,
authorization boundaries, and failure recovery. This is coverage of the
declared release matrix—not a claim that every possible state has been tested.

## Documentation

[Project site](https://ydnikolaev.github.io/a2ahub/) ·
[onboarding](skill/a2ahub/onboarding.md) ·
[command and MCP reference](skill/a2ahub/reference/commands.md) ·
[security and release verification](SECURITY.md) ·
[release notes](releasenotes/)

Apache-2.0 licensed. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
