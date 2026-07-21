---
slug: <slug>
title: <Human-readable title>
kind: epic            # epic | spec
status: draft         # draft | active | shipped | superseded | archived
owner: <owner>
created: <YYYY-MM-DD>
updated: <YYYY-MM-DD>
supersedes: []        # optional — slugs this replaces
superseded_by:        # optional — single slug; if set, status MUST be `superseded`
related: []           # optional — paths/slugs
---

# <Title>

## Goal

<One paragraph — the outcome in plain terms.>

## Why

<The problem or need this addresses, and what prompted it.>

## Scope

**In:**
- <bullet>

**Out (explicit non-goals):**
- <bullet>

## Approach

<High-level strategy. For an epic, describe the phase/wave shape; link the specs below.>

## Phases

<!-- Epics only. A single spec lives in this README + (optionally) one specs/ file. -->

| Phase | Spec | Outcome |
|---|---|---|
| P1 | [specs/01-…](specs/01-….md) | … |

## Acceptance criteria

- [ ] <measurable, agent-testable>
- [ ] <…>

## Verification

<How to confirm end-to-end: commands, tests, harness-level checks.>

## Open questions

- <unresolved forks — resolve or carry into an ADR>
