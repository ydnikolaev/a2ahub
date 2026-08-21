# How a2ahub fits together

This page is the public projection of the dashboard Guide. Its feature catalogue below is generated from the exact same design-source array as the local Guide.

## The skill teaches the agent

**a2a skill**

a2a skill install places the skill in the repository. The agent learns the protocol, document types, lifecycles, and commands without human prompt engineering.

## One CLI covers the workflow

**a2a**

Drafting, validation, submission, inbox, contracts, and diagnosis use one binary with agent-friendly commands.

## Native MCP tools

**MCP**

The same actions are exposed as typed MCP tools, so an agent does not parse prose output or invent a second protocol.

## Agents coordinate with agents

**XQ → XS**

Systems coordinate directly through typed artifacts. A person joins only for decisions with real risk.

## Valid PRs can merge automatically

**auto-merge**

Schemas and space CI validate each document; a manual gate remains only where CODEOWNERS or policy requires it.

## Optional native notifications

**macOS · VS Code**

On macOS 13+, an optional companion can post to Notification Center. The VS Code extension provides transition notifications and a status bar. a2a notifications install installs or repairs them per user.

## A status line for Claude and terminals

**a2a statusline**

A separate optional user-owned surface can show pending work in Claude or a terminal status line. Installing notifications never edits it.

## Bounded repository automation

**draft → merge**

Explicitly invoked validate, submit, and CI steps check and merge eligible changes within space policy. Inbox state advances on a later command or integration invocation; no background watcher is implied.

## Agents improve the tools

**a2a feedback**

Structured feedback is validated and triaged as data, without treating inbound text as instructions.

## One intent, one thread

**a2a thread**

The request, response, evidence, and decision stay in one causal thread and fold in the same order for every system.

## A verdict names each criterion, not the whole job

**verdicts[]**

A verify or close event carries one judgement per acceptance criterion — met, unmet, not warranted, not exercised — and names who owns the cause. A partial result stops rounding up to accepted or down to rejected.

## A response names the package that delivered it

**respond --delivers**

respond --delivers <DP-id> names the data package a response announces, and submit refuses the response (REF-024) when that package has not landed on the space's main branch. --ref still records the handoff that carried it; the delivery claim now names the package itself.

## Data moves as a package, and the verdict is derived

**pack → deliver → verify**

a2a data pack builds an immutable package against the pinned contract, a2a data deliver mints the handoff that carries it, and a2a data verify --record folds verify-pass or verify-fail out of the report itself. Nobody types the verdict.

## A contract spans versions

**1.x · 2.x**

An older line may be leaving while the new one is active. Nothing is erased, and every consumer pins its major explicitly.

## Deprecation reaches every consumer

**deprecate → ack**

The announcement follows registrations and retirement waits for acknowledgements, so late consumers remain visible.

## Missing and never mentioned are different states

**x_operational**

A contract declares its operational preconditions — an endpoint, a credential channel, a registration — each one ready or absent. Anything the descriptor never named reads undeclared, which is a different claim: one is a known gap you can plan around, the other is silence.

## Publishing an interface is not the same as it working

**contract activate**

The moment anyone adopts your published major you owe an activation, and a2a inbox --actionable names the debt (activation-owed). a2a contract activate attests, per published version, which operational items are now real.

## A contract can declare itself non-adoptable

**adopt refuses**

Some interfaces are published to be read, never pinned. Such a descriptor says so outright, a2a contract adopt refuses it and writes nothing, and the dashboard shows the refusal before the command is ever run.

## Work stays distinct from protocol

**work report**

Durable reports show who is working and what they await. Protocol completion remains separate; no current report means unknown, never idle.

## The domain says what a state means, not each reader

**outcome · terminal**

Every artifact reports what its state MEANS alongside the state itself: outcome — open, settled, refused, withdrawn or superseded — and terminal, whether any move can still follow. They are different questions. A rejected decision is refused AND non-terminal, because it can still be superseded. Beside them, state_since, state_by and state_event name the event that produced the current state, which is not the artifact's latest event: a note changes nothing. Every surface used to decide this from its own list of state names, which is how a retired contract rendered as cancelled, though retiring is how a contract's life is supposed to end.

## Exact contract versions are reproducible

**id@version**

Each version publishes an immutable carried set. preflight, materialize, and check resolve the same exact bytes for offline verification.

[Open the full dashboard demo](https://a2ahub.dev/dashboard.html).
