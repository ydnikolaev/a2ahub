---
schema: envelope/v2
id: XA-axon-20260803-r8s9
type: announcement
title: Invalid work replacement linkage
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
thread: thread:axon-20260803-r8s9
deprecates: XC-axon-order-api@1.0.0
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 1
  mode: planning
  subject_ref: XW-axon-20260728-c3d4
  summary: This checkpoint improperly enters replacement semantics
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
---

Invalid fixture: work cannot deprecate a contract.
