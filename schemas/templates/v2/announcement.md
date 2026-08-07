---
schema: envelope/v2
id: XA-<system>-<YYYYMMDD>-<rand4>
type: announcement
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <announcing-system>
to: [<recipient-system>]
actor:
  kind: agent
  name: <filled by a2a>
  model: <filled by a2a>
  session: <opaque-session-id>
created: <RFC-3339 UTC>
category: status
priority: p3
blocking: false
ack_requested: false
classification: internal
thread: <thread:system-YYYYMMDD-rand4>
refs:
  - ref: <artifact-or-contract-subject>
    note: Work checkpoint subject
work:
  id: <work:ULID>
  semantic_sequence: 1
  mode: <planning|implementing|testing|reviewing|waiting|paused|finished>
  subject_ref: <thread/artifact/contract reference>
  summary: <plain-text semantic checkpoint, <=240 Unicode scalars>
  reported_at: <RFC-3339 UTC>
  valid_until: <RFC-3339 UTC; required except for finished>
  # waiting_on:                    # required only for mode: waiting
  #   - kind: <system|human|tool|timer|external>
  #     id: <opaque dependency id>
  #     summary: <optional plain-text reason, <=160 Unicode scalars>
---

<One generated plain-text sentence explaining this structured work checkpoint.>

<!--
Work checkpoints deliberately have no top-level valid_until, deprecates or
period, and no durable operation_key/operation_id/pending artifact or event
IDs. For mode: finished, remove work.valid_until. For all non-waiting modes,
remove work.waiting_on.
-->
