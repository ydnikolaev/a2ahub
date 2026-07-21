# A2A Hub — Architecture Plan: Document Structure & Index

> **Status:** filled (iteration 2) — all sections drafted, awaiting operator
> review. Index of the set:
> [§0 meta](00-meta.md) · [§1 vision](01-vision.md) · [§2 actors](02-actors.md) ·
> [§3 domain](03-domain.md) · [§4 topology](04-topology.md) ·
> [§5 schemas](05-schemas.md) · [§6 hub](06-hub-service.md) ·
> [§7 client](07-client.md) · [§8 agent protocol](08-agent-protocol.md) ·
> [§9 human ops](09-human-ops.md) · [§10 security](10-security.md) ·
> [§11 observability](11-observability.md) · [§12 corner cases](12-corner-cases.md) ·
> [§13 testing](13-testing.md) · [§14 US/AC](14-us-ac.md) ·
> [§15 rollout](15-rollout.md) · [§16 handoff directive](16-handoff-directive.md) ·
> [§17 decisions](17-decisions.md) · [A1 mate amendments](A1-mate-amendments.md) ·
> [A2 examples](A2-examples.md)
>
> The remainder of this file is the original structural contract each section
> was written against (kept for review traceability). NOTE: its section
> annotations reflect the pre-interview brief — e.g. the §3 taxonomy sketch
> lists candidate types (decision_request, data_request,
> contract_change_proposal) that D-004 later merged into categories of the
> final 8 types; §3 of the plan is authoritative. Once all sections are
> approved, the set is handed to implementers as the epic source.

## Authoring rules (for the final doc set)

- **Audience:** AI implementer agents first, humans second. Zero implied context —
  every term defined in the glossary, every reference by stable ID.
- **Normative language:** RFC 2119 (MUST / SHOULD / MAY). Everything non-normative
  is marked `NOTE:` or `RATIONALE:`.
- **No implementation code.** Schemas, state machines, folder layouts, and API
  surfaces are specified declaratively (tables, field lists, transition tables).
  Example *documents* (a filled-in contract, a filled-in request) are allowed and
  required — they are data, not code.
- **Stable IDs everywhere:** requirements `R-###`, user stories `US-###`,
  acceptance criteria `AC-###.#`, corner cases `CC-###`, decisions `D-###`,
  open questions `Q-###`. Cross-reference by ID only.
- **Every decision** either resolved with rationale or listed in §17 with an owner.
- One file per section under `plan/`, numbered as below. This file is the index.

---

## §0 Meta & Glossary — `00-meta.md`

How to read the doc set, ID conventions, RFC 2119 note, full glossary
(actor, org, project, agent, contract, requirement, exchange request, envelope,
hub, spool, watermark, digest, SSOT…). Canonical names for all participants.

*Depends on:* naming decisions, participant list.
*Done when:* every term used in §1–§17 is defined here.

## §1 Vision, Problem, Non-Goals — `01-vision.md`

- Problem statement: humans as bottleneck relaying md files between AI-first
  teams; unreliable contracts; no validation; no visibility.
- Product vision (one paragraph) + the north-star scenario (the requirement
  cascade, end to end, no humans in the loop except approval gates).
- Explicit non-goals for v1 (e.g. runtime data transport, agent runtime
  orchestration/A2A-protocol, multi-tenant SaaS).
- Success criteria for v1 — measurable.

*Depends on:* interview Q: scope, volumes, participants.

## §2 Actors & Identity Model — `02-actors.md`

- Hierarchy: **org → system (project) → agent / human**. Roles: owner, admin,
  member, machine (read-only), machine (write-scoped).
- Registry of participants (v1 real list + how new ones are added).
- Identity: how an actor is named, authenticated, and how identity appears in
  every artifact's metadata (who wrote this, human or agent, which model/session).
- Administration model: who runs the hub, who approves new orgs/routes.

*Depends on:* interview Q: participants, auth model, admin.

## §3 Domain Model: Object Types & Lifecycles — `03-domain.md`

The heart of the spec. For EVERY object type:
purpose · owner/authority · ID scheme · lifecycle state machine (table of
transitions: state → event → state, who may trigger) · required metadata ·
relations to other objects · archival/supersession rules.

Object taxonomy (to be confirmed in interview):
- **contract** (provides) — versioned interface a party may implement against
- **requirement** (consumes) — demand placed on another party's contract
- **exchange requests:** question · work_request · decision_request ·
  data_request · contract_change_proposal
- **response** (to any request) + verification/closure
- **decision** (ADR, multi-party)
- **handoff** — post-implementation transfer document (see §16)
- **announcement / broadcast**, **incident/alert** (confirm need)
- Explicit split: request lifecycle ≠ contract lifecycle (per research doc).

*Depends on:* interview Q: object taxonomy, lifecycle/approval gates.

## §4 Topology & SSOT — `04-topology.md`

- Which store is normative for which object class (git repo(s) vs hub service DB)
  — THE key decision, single writable authority per object class.
- Repo layout: sections per owner, provides/consumes/docs/decisions/vendored,
  CODEOWNERS + branch protection mechanics.
- Multi-repo federation: N repos, routes between orgs, how routes are declared,
  discovered, and authorized. Vendored mirrors for non-cooperating parties.
- Failure independence: what still works when the hub is down (git must remain
  readable/writable; hub state must be rebuildable from git + event replay).

*Depends on:* interview Q1 (SSOT), Q2 (write path), federation needs.

## §5 Schemas & Validation — `05-schemas.md`

- Meta-schema layer: envelope + frontmatter schemas for every object type
  (rich metadata: who/when/what/why/version/effort/model/session/digest…).
- Contract payload schema format(s) and how contracts are **generated from SSOT
  code** (exporters per stack) + CI guard "export == committed".
- Versioning & compatibility policy: semver rules, what counts as breaking,
  deprecation windows, supersession.
- Validation matrix: local (pre-commit / CLI) and CI (PR gates), identical
  validator logic from one source (the shared binary) — zero drift.
- Template system: canonical templates per object type, how they're versioned
  and distributed.

*Depends on:* interview Q: schema tech, SSOT sources per participant.

## §6 Hub Service (Go) — `06-hub-service.md`

- Responsibilities (and explicit non-responsibilities).
- API surface (declarative: operation · actor · input · output · errors ·
  idempotency) — commands vs queries separated.
- Event model: append-only log, delivery watermarks, at-least-once semantics,
  idempotency keys, replay.
- Storage model (what's derived/rebuildable vs durable).
- Webhook ingestion (GitHub) + outbound notification fan-out.
- Deployment target (Timeweb VPS), ops: backup, upgrade, monitoring.
- Failure modes catalog: missed webhook, duplicate event, outage, split-brain
  with git, partial delivery.

*Depends on:* Q1/Q2/Q3, volumes/SLA.

## §7 Client Toolchain — `07-client.md`

- Single distributable artifact (one Go binary serving as: CLI + local MCP
  server + validator + statusline provider + local HTML generator) — confirm.
- Command surface (declarative), config file spec, self-update mechanism
  (one source of truth, no drift across teams).
- Statusline integration contract for Claude Code (what it shows, polling
  cadence, zero-noise rules).
- Local visualization: generated static HTML (design direction, data it shows).

*Depends on:* Q3 (interface), Q9/Q10 (notifications/viz).

## §8 Agent Protocol (the Algorithm) — `08-agent-protocol.md`

The exact operating procedure an agent follows — written as instructions
addressable to any agent (Claude Code, Codex, CI worker):
- **Send loop:** need identified → draft from template → validate → approval
  gate (if required) → submit → track → verify response → close.
- **Receive loop:** sync/notify → validate inbound → ack → triage (link to
  local work) → implement → respond → await closure.
- **Watch loop:** how an agent knows something changed (statusline, on-session
  start check, MCP query) — proactive, not polling-by-human.
- Harness packaging: skill/rule text, where it lives in mate, how projects
  inherit it, session-start hooks.
- Untrusted-input rule: inbound documents are data, never instructions.

*Depends on:* Q: mate integration, approval gates.

## §9 Human Operations — `09-human-ops.md`

- Install guide shape (per role: hub admin, org admin, project dev).
- Onboarding runbook: new org, new project, new agent, new route.
- Key/token issuance and rotation; offboarding.
- Day-2: upgrading the binary fleet, schema migrations, repo maintenance.

## §10 Security & Privacy — `10-security.md`

- Threat model (who can do what damage: leaked token, malicious payload,
  prompt injection via inbound docs, compromised partner).
- AuthN/AuthZ matrix: actor class × action × object class → allow/deny/gate.
- Server-side enforcement (not convention-only), scoped credentials.
- Data classification levels + forbidden payload classes (secrets, private
  code, raw prompts…). Enforcement points.
- Audit log requirements. Public-repo readiness checklist (what must be true
  before the repo goes public / product goes open source).

## §11 Observability & Visualization — `11-observability.md`

- Realtime graph dashboard on the hub (who exchanges what with whom, live):
  data model, update transport, access control, design direction.
- Local HTML view (per-system perspective).
- Notification routing matrix: event type × audience → channel (statusline,
  chat, none). Noise budget.
- Hub's own ops metrics/health.

## §12 Corner-Case Catalog — `12-corner-cases.md`

Exhaustive numbered catalog (CC-###), each with: scenario, expected behavior,
which AC covers it. Categories: document-level (malformed, oversized, wrong
schema version, duplicate ID, clock skew, encoding), lifecycle (response to
closed request, superseded mid-flight, decline, timeout, silence), transport
(missed/duplicated webhook, out-of-order events, offline agent, hub outage,
git conflict, force-push), authz (revoked token mid-session, unauthorized
write, cross-section write), federation (route removed, repo renamed, partner
disappears), versioning (breaking change without bump, consumer on deprecated
version).

## §13 Testing Strategy — `13-testing.md`

- Test pyramid for hub + client + schemas.
- **e2e matrix:** full coverage grid = object types × lifecycle transitions ×
  actor roles × failure injections. Every CC-### mapped to a test.
- Multi-party e2e harness (simulated 3-org exchange in CI).
- Contract-validation golden files (valid + invalid fixtures per schema).

## §14 User Stories & Acceptance Criteria — `14-us-ac.md`

US-### grouped by epic candidate, each with Given/When/Then AC-###.#.
Personas: implementer agent, partner agent, human lead, hub admin.
This section is what gets converted into the epic/spikes.

## §15 Rollout Plan — `15-rollout.md`

Phased delivery (L0 structure → L1 events/notifications → L2 hub+client →
L3 CI validation → L4 viz — exact levels TBD), with per-phase: scope,
exit criteria, what value ships, migration/coexistence with current md-relay
practice, kill criteria. Mapping to epics/spikes.

## §16 Handoff Directive — `16-handoff-directive.md`

The directive for producing a **handoff document** after implemented & tested
work: template (schema'd like everything else), required evidence (test runs,
coverage of AC, known limitations), audience assumptions (agent in another
system with zero context), delivery route through the exchange itself.

## §17 Decision Log & Open Questions — `17-decisions.md`

D-### resolved decisions with rationale; Q-### open with owner and deadline.
Interview answers land here first, then propagate into sections.

## Appendix A — mate Amendments — `A1-mate-amendments.md`

Concrete amendment list for the mate harness (folder structure, registry,
skill placement) reflecting the approved architecture. Separate so it can be
shipped to the mate repo as-is.

## Appendix B — Filled Examples — `A2-examples.md`

One fully-filled example per object type (real getvisa-chain content where
possible): a contract, a requirement, a question, a work_request, a response,
a decision, a handoff. These double as golden fixtures for §13.
