---
schema: envelope/v2
id: XA-axon-20260803-e6f7
type: announcement
title: Waiting for ingest dependencies
space: checkout-core
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex, model: gpt-5, session: session:01K20ABCDEFHJKMNPQRSTVWXYZ}
created: 2026-08-03T11:00:00Z
category: status
priority: p2
blocking: false
ack_requested: false
classification: internal
thread: thread:axon-20260803-a2b3
refs: [{ref: XW-axon-20260728-c3d4}]
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 5
  mode: waiting
  subject_ref: XW-axon-20260728-c3d4
  summary: Waiting for the remaining ingest dependencies
  reported_at: 2026-08-03T11:00:00Z
  valid_until: 2026-08-04T11:00:00Z
  waiting_on:
    - {kind: system, id: seomatrix, summary: Revised invalid fixtures}
    - {kind: human, id: product-owner}
    - {kind: tool, id: live-e2e}
    - {kind: timer, id: retry-window}
    - {kind: external, id: upstream-registry}
---

Waiting for the remaining ingest dependencies.
