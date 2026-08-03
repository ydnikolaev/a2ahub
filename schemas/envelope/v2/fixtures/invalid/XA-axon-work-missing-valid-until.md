---
schema: envelope/v2
id: XA-axon-20260803-s9t0
type: announcement
title: Invalid unfinished work without freshness
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
thread: thread:axon-20260803-s9t0
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 1
  mode: implementing
  subject_ref: XW-axon-20260728-c3d4
  summary: This unfinished checkpoint omits its freshness bound
  reported_at: 2026-08-03T10:00:00Z
---

Invalid fixture: unfinished work requires valid_until.
