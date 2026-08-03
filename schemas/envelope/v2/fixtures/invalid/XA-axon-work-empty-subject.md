---
schema: envelope/v2
id: XA-axon-20260803-f1g2
type: announcement
title: Invalid empty work subject
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
thread: thread:axon-20260803-f1g2
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 1
  mode: planning
  subject_ref: ""
  summary: This checkpoint has no subject
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
---

Invalid fixture: subject_ref is non-empty.
