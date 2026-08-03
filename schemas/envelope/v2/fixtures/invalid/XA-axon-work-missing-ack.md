---
schema: envelope/v2
id: XA-axon-20260803-k3m4
type: announcement
title: Invalid work without explicit ack policy
space: checkout-core
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex, session: session:01K20ABCDEFHJKMNPQRSTVWXYZ}
created: 2026-08-03T10:00:00Z
category: status
priority: p2
blocking: false
classification: internal
thread: thread:axon-20260803-k3m4
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 1
  mode: planning
  subject_ref: XW-axon-20260728-c3d4
  summary: This checkpoint omits the explicit false acknowledgement policy
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
---

Invalid fixture: work requires ack_requested false.
