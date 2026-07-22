---
slug: v1-min-2026-07
title: a2ahub v1-min — space + a2a binary core + day-one content
kind: epic
status: active
owner: yura
created: 2026-07-21
updated: 2026-07-21
related: []
---

# a2ahub v1-min

## Goal

Ship the working tool the operator needs now (D-030): a valid, protected,
reviewable **exchange space** on GitHub + the **`a2a` binary core** (schemas,
one validation engine, fold, lifecycle verbs, submit/sync/inbox, contract
ops, templates, statusline on the git-fallback path) + **day-one content**
(the `ingest`/`todo-feed` contracts and the producer-outbox migration that
ends the md-relay era for the getvisa chain) + the **MCP surface as a
non-critical-path tail**.

**North Star**: the §1 requirement cascade running end to end with no humans
except approval gates — proven here by E2E-1 and the real getvisa space.

## SSOT & consistency rule

The architecture plan (`docs/the-plan/plan/`, §0–§17 + appendices) is the
**normative source**; §17 D-### has top precedence. This epic and its specs
are executable projections: they **cite** stable IDs (R-### / US-### /
AC-###.# / CC-### / D-###) and quote AC rows verbatim — they never restate or
re-decide plan content. A spec found contradicting the plan is a defect in
the spec (fix via `## Amendments`); a deliberate narrowing must name itself
as a deviation and be surfaced to the operator. Scope cut per §15 (D-030):
v1-min = L0 + L1 + L2 + E7 tail; hub (§6), dashboard (11.1), local HTML
(7.6), self-update (OP-217), `doctor --space`, full §13 matrix → v2, with
zero migrations by design.

## Architecture boundaries

Package layout and import rules are fixed by
[ADR-001](../../decisions.md) — phase footprints map 1:1 to packages so
file-disjoint waves are safe. Behavior contracts: §4.2 write funnel (D-002),
§5.5 validation matrix (D-011), §3.4/§3.5 fold (D-017), §7.2 command
semantics (idempotency AC-301.1).

## Epic mapping (plan §14/§15)

| Plan epic | Covered by phases |
|---|---|
| E1 space foundation | P9, P11 |
| E2 schemas & validation | P2, P3 |
| E3 client binary core | P1, P4, P5, P6, P7, P8 |
| E4 templates & authoring | P2, P6 |
| E6 statusline (git-fallback slice only) | P7 |
| E7 MCP surface (tail) | P14 |
| E9 onboarding & ops (partial: P11's runbook seeds it; full E9 = v2) | P11 |
| E10 migration day one | P11, P12 |
| §8 skill + agent rules (D-015 minimal) | P13 |
| §13 test tiers T1–T3 + T5-lite (cross-cutting, not a plan E#) | P10 |

## Phases

| # | Spec | Outcome |
|---|---|---|
| P1 | [specs/01-foundation.md](specs/01-foundation.md) | Go module, `cmd/a2a` skeleton, `internal/artifact` (IDs, frontmatter, digests), product-repo CI |
| P2 | [specs/02-product-schemas.md](specs/02-product-schemas.md) | envelope/event/manifest/consumes JSON Schemas v1 + golden fixtures + error-code registry |
| P3 | [specs/03-validation-engine.md](specs/03-validation-engine.md) | the ONE validation engine (V1/V2 classes, machine codes, secret-scan) |
| P4 | [specs/04-fold-engine.md](specs/04-fold-engine.md) | lifecycle fold: transition tables, determinism, T2 |
| P5 | [specs/05-space-and-host.md](specs/05-space-and-host.md) | space model, mirrors, GitHub host adapter, PR write funnel |
| P6 | [specs/06-author-verbs.md](specs/06-author-verbs.md) | `init/connect/new/validate/submit/--batch/sync` + embedded templates |
| P7 | [specs/07-read-surface-statusline.md](specs/07-read-surface-statusline.md) | cache, computed inbox/outbox, `show/thread/search/contracts`, statusline |
| P8 | [specs/08-lifecycle-contract-verbs.md](specs/08-lifecycle-contract-verbs.md) | lifecycle verbs, contract publish/deprecate/retire/diff/verify-export |
| P9 | [specs/09-space-template-ci.md](specs/09-space-template-ci.md) | space-template scaffold, V3 CI (diff-authz, compat 5.4b), basic doctor |
| P10 | [specs/10-integration-tests.md](specs/10-integration-tests.md) | T3 harness, E2E-1 + E2E-4, cc-coverage seed |
| P11 | [specs/11-getvisa-space-bootstrap.md](specs/11-getvisa-space-bootstrap.md) | the real getvisa space online (axon + seomatrix), L0 exit |
| P12 | [specs/12-day-one-content.md](specs/12-day-one-content.md) | ingest/todo-feed contracts code-backed, producer-outbox migration, L2 exit |
| P13 | [specs/13-agent-skill.md](specs/13-agent-skill.md) | minimal a2ahub expert skill + agent rules (§8, D-015) |
| P14 | [specs/14-mcp-surface.md](specs/14-mcp-surface.md) | `a2a mcp` façade + CLI/MCP parity suite |
| P15 | [specs/15-mcp-consolidation.md](specs/15-mcp-consolidation.md) | MCP surface consolidation — ~6 capability-grouped typed tools (§7.7 amended, capability-parity) |
| P16 | [specs/16-release-mechanics.md](specs/16-release-mechanics.md) | release workflow: `v*` tag → cross-build + publish (unblocks P11 B1); signing deferred |

## Acceptance criteria (epic level)

- [ ] Every §15 v1-min exit criterion green: L0 (AC-101.*, AC-102.1 minus hub
      clauses, hello-world funnel proof), L1 (T1–T3 green; US-201/301/302/401
      ACs minus hub clauses; one real question→response→verify→close loop via
      the binary), L2 (S-1, S-4, S-5), E7 tail (AC-301.2 parity).
- [ ] `cc-coverage.yaml` seeded and CI-enforced for the CCs the shipped tiers
      claim (13.2), full matrix explicitly deferred to v2.
- [ ] Zero contradictions between shipped behavior and plan §3–§5, §7 — or a
      recorded amendment per deviation.

## Open questions

Tracked in plan §17 (Q-001…Q-010); none block v1-min start. Q-010 (audit
findings triage) rolls into implementation per-phase.
