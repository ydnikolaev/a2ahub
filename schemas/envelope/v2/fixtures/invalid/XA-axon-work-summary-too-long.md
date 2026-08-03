---
schema: envelope/v2
id: XA-axon-20260803-h3j4
type: announcement
title: Invalid overlong work summary
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
thread: thread:axon-20260803-h3j4
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 1
  mode: planning
  subject_ref: XW-axon-20260728-c3d4
  summary: >-
    This invalid semantic checkpoint deliberately repeats a bounded plain text sentence so that the
    authored summary crosses the strict two hundred and forty Unicode scalar limit without changing
    any other field in the otherwise valid announcement and therefore remains a single fault fixture.
  reported_at: 2026-08-03T10:00:00Z
  valid_until: 2026-08-04T10:00:00Z
---

Invalid fixture: summary exceeds 240 Unicode scalars.
