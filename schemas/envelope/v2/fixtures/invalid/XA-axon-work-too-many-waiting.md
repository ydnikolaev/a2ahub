---
schema: envelope/v2
id: XA-axon-20260803-c8d9
type: announcement
title: Invalid waiting work with too many dependencies
space: checkout-core
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex, session: session:01K20ABCDEFHJKMNPQRSTVWXYZ}
created: 2026-08-03T10:00:00Z
category: status
priority: p2
blocking: false
ack_requested: false
classification: internal
thread: thread:axon-20260803-c8d9
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 5
  mode: waiting
  subject_ref: XW-axon-20260728-c3d4
  summary: This waiting checkpoint exceeds the dependency bound
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
  waiting_on:
    - {kind: system, id: one}
    - {kind: human, id: two}
    - {kind: tool, id: three}
    - {kind: timer, id: four}
    - {kind: external, id: five}
    - {kind: system, id: six}
    - {kind: human, id: seven}
    - {kind: tool, id: eight}
    - {kind: timer, id: nine}
---

Invalid fixture: waiting_on is bounded to eight entries.
