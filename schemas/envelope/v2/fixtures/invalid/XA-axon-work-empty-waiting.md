---
schema: envelope/v2
id: XA-axon-20260803-b7c8
type: announcement
title: Invalid waiting work with empty dependencies
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
thread: thread:axon-20260803-b7c8
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 5
  mode: waiting
  subject_ref: XW-axon-20260728-c3d4
  summary: This waiting checkpoint has no actual dependency
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
  waiting_on: []
---

Invalid fixture: waiting_on is non-empty.
