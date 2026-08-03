---
schema: envelope/v2
id: XA-axon-20260803-d9e0
type: announcement
title: Invalid work dependency kind
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
thread: thread:axon-20260803-d9e0
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 5
  mode: waiting
  subject_ref: XW-axon-20260728-c3d4
  summary: This checkpoint uses a provider-specific dependency kind
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
  waiting_on: [{kind: agent-provider, id: codex}]
---

Invalid fixture: waiting kind is closed.
