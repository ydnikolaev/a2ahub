# §1 Vision, Problem, Non-Goals

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Normative language per RFC 2119 (see §0). Decisions referenced as D-###
> (see §17), requirements as R-### (defined below).

## 1.1 Problem statement

Several independent teams build one product chain with AI-first development:

```
SoT startup (data)  →  SeoMatrix factory (content)  →  Axon/getvisa (web)  →  end users
```

Each team's work is done primarily by AI agents. The teams' agents depend on
each other's interfaces and outputs, but today every cross-team exchange —
a contract draft, a question, a data request, a handoff — travels as a
Markdown file relayed **by humans** through chat.

Consequences (observed, not hypothetical):

1. **Humans are the bottleneck.** Agents finish work in minutes; the exchange
   waits hours or days for a human to relay it.
2. **Contracts are unreliable.** No schemas, no validation, no versioning.
   Producers and consumers drift silently until something breaks at runtime.
3. **No lifecycle.** A relayed file has no status. Nobody knows whether a
   request was seen, accepted, answered, or dropped. Silence is ambiguous.
4. **No history or provenance.** Chat files have no review trail, no diff, no
   record of who changed what and why.
5. **No visibility.** Nobody can see the exchange graph — who waits on whom,
   what is stale, where the chain is blocked.
6. **It multiplies.** Every new integration (more partners are expected)
   multiplies the same manual relay work.

## 1.2 Product vision

**a2ahub** is a git-native exchange layer that lets AI agents of independent
teams exchange persistent, schema-validated documents — contracts,
requirements, requests, responses, decisions, handoffs — **proactively and
without humans in the delivery path**. Humans remain only at explicit
approval gates.

The product consists of:

- **A convention:** the *exchange space* — a git repository shared by a circle
  of participants, with owned sections, strict per-type schemas, and defined
  lifecycles (D-001, D-003).
- **One tool:** the `a2a` binary — CLI, local MCP server, validator, statusline
  provider, and local visualizer in a single self-updating artifact (D-005,
  R-004). It is the only thing a participant installs.
- **An optional hub:** a Go service that indexes spaces, delivers
  notifications, and renders the live exchange graph. The hub owns only
  ephemeral/derived state and is fully rebuildable from git (D-001). A team
  can run without it; the hub is an upgrade, not a prerequisite.

Design stance: **protocol over platform**. a2ahub standardizes formats,
metadata, lifecycles, and validation. It does not prescribe stacks, harnesses,
or agent vendors (D-006, R-018): how a project generates its contracts from
its code is the project's business; Claude Code, Codex, a CI worker, or a
human all speak to the space through the same binary and the same formats.

## 1.3 North-star scenario (the cascade, end to end)

1. The getvisa agent in Axon, mid-implementation, discovers it needs a new
   field from the content factory. It drafts a `requirement` from a template,
   the binary validates it against the schema, and commits it to Axon's
   section of the shared space. No human involved.
2. The hub sees the push (webhook) and flags the SeoMatrix system. Misha's
   agent sees the notification in its statusline at next glance / session
   start — no polling by humans, no chat message.
3. Misha's agent reads the requirement through `a2a` (or MCP), links it to
   local work, acknowledges it — the acknowledgement is itself a committed
   lifecycle event, so the Axon side sees "seen, accepted, ETA".
4. To satisfy it, the factory needs new upstream data: its agent commits its
   own `requirement` toward the SoT startup's section. The cascade continues
   without any human relay.
5. The factory ships, regenerates its `contract` (provides) from its own
   validator code, commits the new version; CI proves the published contract
   matches the code. It commits a `response` referencing the contract version.
6. The Axon agent verifies the response against the acceptance criteria it
   stated in the requirement, and folds the requirement to `satisfied`
   (the `satisfy` event). Completion is explicit — never inferred from
   delivery.
7. Throughout, the hub dashboard shows the live graph: who asked whom for
   what, what state each exchange is in, what is stale. Any participant can
   also generate a local HTML view of their own system's exchanges.
8. The only human touchpoints: approving a breaking contract change and
   multi-party decisions (exact gates specified in §3/§10).

## 1.4 Non-goals (v1)

| # | Non-goal | Rationale |
|---|---|---|
| NG-1 | Runtime data transport | Catalog/content/fact payloads keep flowing through the teams' existing HTTP APIs (ingest, delivery, todo-feed). The space carries *specifications and exchanges about* those flows, never the data itself. |
| NG-2 | Agent runtime orchestration | Live task delegation / streaming between running agents (Google A2A, ACP territory) is out of scope. If ever needed, it arrives as a new hub-native object class (D-001) without disturbing the git layer. |
| NG-3 | Multi-tenant SaaS | v1 serves one operator's circles of trusted teams. Public/open-source hardening is a designed-for future (R-007, §10), not a v1 deliverable. |
| NG-4 | Replacing project-internal trackers | Epics/specs/tasks inside each project stay in each project's harness (mate or other). a2ahub carries only what crosses a team boundary. |
| NG-5 | Replacing human chat | Humans keep chatting. a2ahub removes *artifact relay* from chat, not conversation. |
| NG-6 | General document management | Only the typed objects of §3 live in a space. Arbitrary file sharing is not a feature. |

## 1.5 Requirements register

Source: operator brief (17 wishes) + interview. Every requirement below MUST
be traceable to sections, user stories (§14), and tests (§13). This table is
the traceability root.

| ID | Requirement | Primary sections |
|---|---|---|
| R-001 | Agents learn about relevant new/updated artifacts proactively (statusline signal, session-start check, watch loop) — never rely on a human noticing | §7, §8, §11 |
| R-002 | All exchanges are persistent documents with full history and review trail | §3, §4 |
| R-003 | Every artifact validates against a strict schema, locally before write and in CI | §5 |
| R-004 | One toolchain for all participants, updated from one source, zero drift: single `a2a` binary | §7 |
| R-005 | Rich structured metadata on every artifact: who (org/system/agent/human, model, session), when, what, why, version, priority, effort, expected outcome, digests | §3, §5 |
| R-006 | Multi-space federation: a system participates in N spaces; N systems ↔ N systems; adding a circle never restructures existing ones | §4 |
| R-007 | AuthN/AuthZ and protection: private now, designed so public/open-source mode adds a hub-mediated write path without format changes | §2, §10 |
| R-008 | Any participant can generate a local, modern-looking HTML view of their system's exchanges *(v2 per D-030)* | §7, §11 |
| R-009 | Realtime hosted dashboard: the live exchange graph (who, with whom, what, state) *(v2 per D-030)* | §6, §11 |
| R-010 | Object taxonomy covers real cross-team needs: 8 types per D-004 (revised) | §3 |
| R-011 | Privacy and security are top priorities: classification, forbidden payload classes, audit, server-side enforcement path | §10 |
| R-012 | Extensible foundation for future A2A features (new object classes, new channels) without migrations | §3, §4, §6 |
| R-013 | Open-source-ready quality without overengineering: close today's pain first, foundation second | all |
| R-014 | Exact operating algorithm for agents + install/admin runbooks for humans | §8, §9 |
| R-015 | Canonical templates for every object type, versioned and distributed with the binary | §5, §7 |
| R-016 | Full e2e test matrix: object types × lifecycle × roles × failure injections; every corner case mapped to a test | §12, §13 |
| R-017 | Handoff directive: schema'd document produced from implemented+tested work, consumable by zero-context agents in other systems | §16 |
| R-018 | Agent-agnostic and stack-agnostic core: Claude Code and Codex first-class today, anything speaking the formats tomorrow; contract generation from project code is the project's concern | §3, §5, §7 |
| R-019 | Expert onboarding skill: an activatable skill that knows the whole system and can answer questions and guide any agent or human through it | §8, §9 |
| R-020 | Hub backend in Go on the operator's VPS *(hub v2 per D-030; formats/semantics unchanged)*; MCP as an agent-facing interface *(v1 tail item per D-030)* | §6, §7 |
| R-021 | Explicit, configurable, non-overengineered onboarding of new participants (horizon ≈10 systems) | §2, §9 |
| R-022 | Day-one content: `ingest` and `todo-feed` contracts; migration of the pending producer-outbox exchanges | §15, Appendix B |

## 1.6 Success criteria (v1)

| ID | Criterion |
|---|---|
| S-1 | Zero cross-team artifacts relayed through chat between Axon and SeoMatrix after migration cutoff (D-007); chat relay declared deprecated. |
| S-2 | 100% of artifacts in the space pass schema validation in CI; an invalid artifact cannot merge. |
| S-3 | An agent discovers an inbound item addressed to its system within one work session start or ≤5 minutes while a session is active, with zero human prompting. |
| S-4 | `ingest` and `todo-feed` contracts live in the space, generated from project code, with CI proving export == committed. |
| S-5 | The pending producer-outbox backlog is migrated into typed, tracked exchange objects; none remain untracked. |
| S-6 | Onboarding a new system (docs + tooling only, no human walkthrough) takes ≤1 hour following §9. |
| S-7 | Every exchange has an unambiguous lifecycle state; "silence" is always distinguishable from "declined" and "closed". |
| S-8 | Hub dashboard and local HTML render the real exchange graph; hub can be destroyed and fully rebuilt from git with no durable data loss. |
