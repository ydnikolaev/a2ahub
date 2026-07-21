# §14 User Stories & Acceptance Criteria

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Grouped by epic candidate (E#; §15 maps E# to phases). Personas:
> **IA** implementer agent (member of own system) · **PA** partner agent
> (other system) · **HL** human lead (system owner / space admin) ·
> **OP** operator. ACs cite corner cases (CC) and test tiers (T#/E2E-#).

## E1 — Space foundation

**US-101** (OP): As the operator, I create a new space from a template so a
circle can start exchanging in minutes.
- AC-101.1 Given the space template, when the repo is created, then layout
  4.2, CI workflow (V3), CODEOWNERS skeleton and a schema-valid `space.yaml`
  exist and CI is green on the empty space. [T6, E2E-9]
- AC-101.2 Given branch protection configured per §4.2/§10.3, when any
  actor pushes directly to `main`, then the push is rejected; when a member
  PRs into a foreign section, then V3 diff-authz fails and the PR is
  unmergeable; when an ungated own-section PR is green, then it auto-merges
  with zero human involvement. [CC-060, CC-095]

**US-102** (HL): As a space admin, I add a participant via one manifest PR
so onboarding is explicit and reviewable.
- AC-102.1 Given a join PR (manifest + section scaffold), when merged (G4),
  then the section is writable by that system only, and the hub serves it
  with no hub-side config change. [E2E-9]
- AC-102.2 Given a participant set to `left`, when any actor addresses it,
  then V2 rejects new `to:` references and open items flag
  `orphaned-counterparty`. [CC-008, CC-062, E2E-8]

## E2 — Schemas & validation engine

**US-201** (IA): As an agent, every artifact I draft is validated before it
can leave my machine, so I can never publish garbage.
- AC-201.1 Given any of the invalid golden fixtures, when `a2a validate`
  runs, then it fails with the expected machine-readable code. [T1, CC-001…011]
- AC-201.2 Given the same content, when validated locally (V2), in space CI
  (V3), and by the hub (V4), then results are identical. [T1/T4/T6]
- AC-201.3 Given an artifact with `from` not matching my configured system,
  when I submit, then the write is refused locally. [CC-002]

**US-202** (HL): As a system owner, no breaking contract change can reach
consumers without my gate and their awareness.
- AC-202.1 Given a major-version contract PR, when CI runs, then merge
  requires human review (G2) and a linked deprecation announcement with
  `ack_requested`. [E2E-4]
- AC-202.2 Given un-acked registered consumers (per the space-visible
  registry: satisfied requirements + `consumes.yaml`, §5.4), when retire is
  attempted, then the validator blocks it. [CC-081, CC-082]
- AC-202.3 Given un-acked consumers and sunset passed, when retire is
  attempted by an agent actor or without a recorded reminder, then the
  validator blocks it; when submitted as a human-reviewed override PR
  meeting all §5.4 preconditions, then retire succeeds and each overridden
  consumer is flagged `retired-unacked` and notified. [CC-082, CC-086]
- AC-202.4 Given a mislabeled-minor breaking change (prior-version valid
  fixture fails against the new schema), when V3 runs, then the merge is
  blocked with a major-required error. [CC-080]

**US-203** (IA): As an agent, secret-looking content in an outbound artifact
is blocked before it crosses the boundary.
- AC-203.1 Given the secret-pattern corpus, when submitted, then V2 blocks
  each, overridable only via the G5 flow. [CC-010, T1 corpus 13.4]

## E3 — Client binary core

**US-301** (IA): As an agent, I operate the whole exchange from one binary
with idempotent commands.
- AC-301.1 Given any OP-2xx mutating command re-run after success, then the
  second run is a no-op with a clear "already done" result. [T3]
- AC-301.2 Given the CLI and MCP surfaces, when the parity suite runs, then
  every capability exists in both. [T3, 7.1]
- AC-301.3 Given no hub configured, when I run inbox/submit/sync, then
  everything works via direct git. [CC-042, E2E-7]

**US-302** (IA): As an agent, lifecycle verbs write correct events that fold
identically everywhere.
- AC-302.1 Given every legal transition of §3.4, when performed via CLI,
  then the folded state matches the transition tables on binary and hub.
  [T2, T4]
- AC-302.2 Given illegal or unauthorized events injected into a section,
  when folded, then they are ignored and flagged, never crash. [CC-020…022]

## E4 — Templates & authoring

**US-401** (IA): As an agent, `a2a new` gives me a template that cannot
drift from the schema.
- AC-401.1 Given any type, when drafted, then the draft passes V1 before
  any edits except placeholder fills. [T3]
- AC-401.2 Given product-repo CI, when a schema changes without its
  template, then the build fails. [5.6]

## E5 — Hub service

**US-501** (OP): As the operator, the hub stays truthful and rebuildable.
- AC-501.1 Given `hub rebuild` after arbitrary DB loss, then indexes equal
  the incrementally-built ones. [T4, E2E-7]
- AC-501.2 Given a missed webhook, then reconcile catches changes ≤5 min.
  [CC-040]
- AC-501.3 Given a force-push on a space, then full re-index + operator
  alert. [CC-045]

**US-502** (PA): As a partner agent, my inbox query reflects new items
addressed to me within the freshness budget.
- AC-502.1 Given a pushed artifact `to:` my system, when I query OP-102 (or
  statusline via OP-108), then it appears ≤ webhook+pipeline latency
  (seconds) with hub, ≤ TTL (5 min default) without. [E2E-1]

## E6 — Statusline, adapters, skill

**US-601** (IA): As an agent in Claude Code, I see pending exchange signals
without doing anything.
- AC-601.1 Given nothing actionable, then statusline prints nothing, exit 0.
  [CC-092]
- AC-601.2 Given a p1/blocking inbound, then the line + severity exit code
  reflect it ≤ freshness budget; render <100 ms from cache. [13.4]

**US-602** (IA): As any agent, activating the `a2ahub` skill answers my
questions about the system and walks me through any flow.
- AC-602.1 Given a release, when the binary's command/MCP reference or the
  templates changed, then their generated projections in the skill are
  regenerated or the build fails; prose sections are covered by the release
  checklist review, not a machine gate (8.7, D-015). [8.7]

## E7 — MCP surface

**US-701** (IA): As an MCP-capable agent, I use the exchange through typed
tools with structured results. [ACs: parity AC-301.2; structured envelope +
folded state responses per 7.7; T3]

## E8 — Dashboard & local HTML

**US-801** (HL): As a human, the dashboard shows me the live graph and where
things are stuck.
- AC-801.1 Given open exchanges across spaces, then the graph shows nodes/
  edges with state, staleness and gate markers, live-updating on new events.
  [E2E-1 assertions]
- AC-801.2 Given validation flags (V4), then they are visible per space and
  per section. [CC-020, CC-084]

**US-802** (IA/HL): As a participant, `a2a html` gives me my system's
offline view. [AC: renders from cache with no network; content per 7.6; T3]

## E9 — Onboarding & ops

**US-901** (HL): As a new team's lead, I onboard by runbook alone in ≤30 min.
- AC-901.1 Scripted E2E-9 completes with no steps outside the runbook. [S-6]

**US-902** (OP): As the operator, I can hand over the whole installation in
one sitting following the succession note. [9.5; verified by doc review +
dry run]

## E10 — Migration (day one)

**US-1001** (IA-axon): As the axon agent, the `ingest` and `todo-feed`
contracts live in the space, code-backed.
- AC-1001.1 Given the axon export, when `a2a contract verify-export` runs in
  axon CI, then digest match is enforced; the space copies carry
  `generated_from`. [CC-084]

**US-1002** (HL): As both leads, the producer-outbox backlog is migrated or
consciously closed.
- AC-1002.1 Given the 15 legacy files, when migration completes, then each
  is either a typed artifact (with `migrated_from` carrying its legacy
  filename, §5.2) or recorded as intentionally-dropped in a migration note;
  none silently lost. [S-5]
- AC-1002.2 Given migration cutoff, then the chat-relay channel is declared
  deprecated in an `announcement`. [S-1]

## E11 — Handoff directive

**US-1101** (IA): As a producing agent, I can hand implemented+tested work
to another system's zero-context agents.
- AC-1101.1 Given a handoff draft missing §16 required evidence, when
  submitted, then V2 rejects with the specific missing blocks. [E2E-6]
- AC-1101.2 Given verify-fail findings, when resubmitted on the same thread,
  then the new revision links its predecessor. [E2E-6]
