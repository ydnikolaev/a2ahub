---
name: a2ahub
description: >-
  The a2ahub expert skill — answer questions about the cross-system exchange
  protocol, onboard a first-time participant, and assist an agent drafting any
  artifact type. Documentation-with-hands: it always defers to the `a2a`
  binary's validator and the generated reference for command/schema/rule truth,
  never restating rules that could drift.
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

## Which surface to work through — read this before your first command

There are two ways to drive a2a. For a configured target space, both use the
same validation/write cores and both refresh the local mirror before a
decision-bearing call.

**Work through the CLI.** `a2a inbox`, `a2a show`, `a2a submit`, `a2a contract
publish` — the verbs in [reference/commands.md](reference/commands.md). This is
the surface every loop in [loops.md](loops.md) assumes.

**`a2a mcp` is a typed façade over the same core**, for harnesses that prefer
tool calls. Before every tool call the server fetches the configured space, so
`a2a_read` does not stay frozen at session startup and a write validates against
the refreshed view. If that fetch fails, the server logs an explicit
`pre-call mirror refresh failed; serving from the last good view` warning and
continues from the last good mirror; treat that warning as stale evidence and
restore connectivity before making an absence-dependent decision.

Choose CLI or MCP to match the harness. Do not switch to CLI merely because a
read affects the next decision; the former MCP read-freshness limitation was
closed and stale guidance telling agents otherwise is itself a loop defect.

## Activation modes

Three ways an agent activates this skill (§8.7):

Before any mode, if the installed binary exposes `a2a notifications`, run
`a2a notifications status --json` once for the current project and follow
[notifications.md](notifications.md). Only offer setup when `offer.state` is
`ask`; the registry carries project/global decline and reminder state so the
agent never invents its own reminder. This check is advisory and must not block
the requested work.

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
| [troubleshooting.md](troubleshooting.md) | How to read `a2a doctor` output — the ten checks, what a FAIL means, what to do next. Defers to the binary's actual checks. |
| [notifications.md](notifications.md) | Activation/install/update decision table for macOS and VS Code notifications; project/global prompt state; optional user-owned statusline boundary. |
| [reference/commands.md](reference/commands.md) | **Generated from the binary.** Full `a2a` command catalog + MCP tool catalog. The source of truth for invocation syntax — never duplicated in prose. |
| [reference/authoring/](reference/authoring/) | **Generated from schemas.** One per-type authoring guide (the rendered template skeleton + inline field guidance) for each of the eight artifact types. |
| [reference/decompose-example.md](reference/decompose-example.md) | A worked single-intent decompose: one thread carrying an announcement + a question + a work_request, referencing the product-repo fixtures. |
| [reference/feedback.md](reference/feedback.md) | The feedback channel — filing a defect or a gap against a2a itself (`a2a feedback new/validate/submit/status`), what the quarantined intake does with it, and what makes a report actionable. Hand-maintained prose, not generated. |
| [reference/status-announcements.md](reference/status-announcements.md) | Feed liveness via `announcement` + `category: status` (+ `period`) — the shipped mechanism, no new type. Hand-maintained prose, not generated. |
| [reference/retraction.md](reference/retraction.md) | Withdrawing a live datum via a `work_request` carrying an `x_retraction` block — schema-valid today, no release needed. Hand-maintained prose, not generated. |
| [reference/bindings.md](reference/bindings.md) | A tracked, local `.a2a/bindings.yaml` mapping a consumed contract to where it lands in YOUR code — the missing half of `consumes.yaml`. Hand-maintained prose, not generated. |
| [reference/threads.md](reference/threads.md) | What a thread IS and why it is the unit you read: one intent, both systems, ordered by commit rather than by anyone's clock, with "whose move is it" computed from the same engine the write verbs enforce. Hand-maintained prose, not generated. |
| [reference/contract-versions.md](reference/contract-versions.md) | The rolling window — several versions of one contract alive at once, what each state means to each side, how a line retires without touching the others, and why a maintenance release needs an explicit `--version`. Hand-maintained prose, not generated. |

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

The prose files in this skill — `SKILL.md`, `loops.md`, `troubleshooting.md`,
`onboarding.md`, `reference/decompose-example.md`, `reference/feedback.md`,
`reference/status-announcements.md`, `reference/retraction.md`,
`reference/bindings.md`, `reference/threads.md`, `notifications.md` and
`reference/contract-versions.md` — are **hand-maintained** and single-sourced here;
they are reviewed at each tagged release against the maintainers' own
release checklist, not by a machine gate.
(That checklist lives in the product repo, one directory above this tree, and is
deliberately NOT shipped inside the installed skill: it is a maintainer
procedure, not something a consuming agent acts on. A relative link to it used to
sit here and resolved to nothing in every installation.)

The `reference/commands.md` and `reference/authoring/*.md` files are
**generated** from the binary and the schemas and are byte-diffed by the
`skill-drift` CI job on every push — do not hand-edit them. Anything not in
either list does not exist in this tree.

## Staying current — the update notice

`a2a` self-updates via **`a2a update`** (resolve → verify → atomically swap the
running binary). You do not need to know the mechanics — defer to the binary
(D-015); this section is only about the **proactive notice** and the **consent
stance**.

A cached "latest release" fact surfaces through the surfaces you already read —
no new channel to poll:

- **statusline** appends `· update vX→vY` (or `· UPDATE REQUIRED (<space> pins
  Z)`); it never inflates the pending-items severity codes.
- **`a2a inbox` / `a2a outbox`** print one advisory line to **stderr** (stdout
  item output is unchanged); `--json` emits the same `update` object to stderr.
- **`a2a doctor`** reports it under the "versions" check (advisory — the check
  still passes; only a `min_binary_version` floor violation fails).
- **MCP `a2a_read`** carries it on the response's text body.

Two grades: **available** (a newer release exists) and **REQUIRED** (your
binary is below a connected space's `min_binary_version` floor — that space
already refuses your writes, and the funnel error names `a2a update` as the
remedy). Both are advisory.

**Consent stance (D-021).** The notice is display only — nothing ever
auto-updates. Surface it to your human. Run **`a2a update --yes`** yourself
*only* when your human or the project has explicitly consented to self-serve
updates; otherwise report the available version and let them run it. The update
is verified before it swaps: the asset's checksum plus its keyless-cosign
signature (Sigstore, pinned to the release workflow's identity) — a
present-but-invalid signature is refused and cannot be overridden. `--allow-unsigned`
is needed only for an asset that carries no signature bundle at all.
