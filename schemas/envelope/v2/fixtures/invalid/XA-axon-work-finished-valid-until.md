---
schema: envelope/v2
id: XA-axon-20260803-t0v1
type: announcement
title: Invalid finished work with freshness
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
thread: thread:axon-20260803-t0v1
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 8
  mode: finished
  subject_ref: XW-axon-20260728-c3d4
  summary: This terminal checkpoint improperly claims freshness
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
---

Invalid fixture: finished work has no valid_until.
