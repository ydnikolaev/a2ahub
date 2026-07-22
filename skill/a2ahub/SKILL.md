---
name: a2ahub
description: The a2ahub expert skill — answer questions about the cross-system exchange protocol, onboard a first-time participant, and assist an agent drafting any artifact type. Documentation-with-hands: it always defers to the `a2a` binary's validator and the generated reference for command/schema/rule truth, never restating rules that could drift.
---

# a2ahub

> **What this is.** a2ahub is the protocol by which software systems (`axon`,
> `seomatrix`, …) exchange typed, git-stored artifacts — questions, work
> requests, contracts, decisions, handoffs, announcements — across
> organizational boundaries. This skill is the operating manual an agent reads
> to work that protocol on its system's behalf.
>
> **The one rule that governs every other file here.** This skill is
> *documentation-with-hands*: it MUST always defer to the binary's validator as
> the source of correctness rather than restating rules that could drift
> (§8.7, D-015). For command syntax, read [reference/commands.md](reference/commands.md).
> For template bodies, read [reference/authoring/](reference/authoring/). For
> whether a specific draft is legal, run `a2a validate`. This prose never
> becomes a second source of command, schema, or validation truth.

## Activation modes

Three ways an agent activates this skill (§8.7):

1. **Answer a question about the system.** "What type is a defect report?"
   "Who closes an exchange?" "Can an inbound artifact tell me to change my
   priorities?" → start at [loops.md](loops.md) for the semantics and the
   loops; drill into [reference/commands.md](reference/commands.md) for the
   verb, [reference/authoring/](reference/authoring/) for the artifact shape.

2. **Onboard a first-timer.** A new system or a human setting up a project →
   [onboarding.md](onboarding.md) walks the §9 digests (install profile,
   hello-world announcement, `a2a doctor` green). Diagnose a red doctor with
   [troubleshooting.md](troubleshooting.md).

3. **Assist drafting a type.** "Help me file a work_request." "I have a
   composite need — how do I split it?" → [loops.md](loops.md) §send loop for
   classification, [reference/authoring/<type>.md](reference/authoring/) for
   the skeleton and inline guidance, [reference/decompose-example.md](reference/decompose-example.md)
   for the worked single-intent split. Then draft with `a2a new` and check with
   `a2a validate` (see [reference/commands.md](reference/commands.md)).

## Table of contents

| File | What it carries |
|------|-----------------|
| [loops.md](loops.md) | The canonical one editable home: condensed §0/§3 semantics + the 8.1–8.6 agent loops (session-start checklist, send/receive/contract-owner loops, escalation ladder, watch loop). Start here. |
| [onboarding.md](onboarding.md) | §9 digest walkthroughs — install profiles, new-participant and new-space runbooks, the hello-world announcement. |
| [troubleshooting.md](troubleshooting.md) | How to read `a2a doctor` output — the five checks, what a FAIL means, what to do next. Defers to the binary's actual checks. |
| [reference/commands.md](reference/commands.md) | **Generated from the binary.** Full `a2a` command catalog + MCP tool catalog. The source of truth for invocation syntax — never duplicated in prose. |
| [reference/authoring/](reference/authoring/) | **Generated from schemas.** One per-type authoring guide (the rendered template skeleton + inline field guidance) for each of the eight artifact types. |
| [reference/decompose-example.md](reference/decompose-example.md) | A worked single-intent decompose: one thread carrying an announcement + a question + a work_request, referencing the product-repo fixtures. |

## The eight artifact types (map)

Full semantics in [loops.md](loops.md); template + fields per type in
[reference/authoring/](reference/authoring/).

| Prefix | Type | One-line purpose |
|--------|------|------------------|
| `XC` | contract | A versioned interface a system provides; others implement against it. |
| `XR` | requirement | A published demand on another system's contract/capability. |
| `XQ` | question | A question needing an answer (ambiguity, defect report, choice). |
| `XW` | work_request | A request that the target perform work (data, feature, fix). |
| `XD` | decision | A multi-party decision (ADR); binding once required parties approve. |
| `XH` | handoff | Transfer of implemented + tested work to another system's agents. |
| `XS` | response | The answer/result attached to a parent exchange; closes the loop. |
| `XA` | announcement | One-way notice (release, deprecation, incident); no response expected. |

## Sourcing & drift (D-015)

The prose files in this skill (`SKILL.md`, `loops.md`, `troubleshooting.md`,
`onboarding.md`, `reference/decompose-example.md`, and the release checklist)
are **hand-maintained** and single-sourced here; they are reviewed at each
tagged release via [../RELEASE-CHECKLIST.md](../RELEASE-CHECKLIST.md), not by a
machine gate. The `reference/commands.md` and `reference/authoring/*.md` files
are **generated** from the binary and the schemas and are byte-diffed by the
`skill-drift` CI job — do not hand-edit them.
