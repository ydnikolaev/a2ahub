---
schema: envelope/v1
id: XQ-<system>-<YYYYMMDD>-<rand4>     # exchange ID grammar §3.3
type: question
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <asking-system>
to: [<target-system>]                 # exchange type: EXACTLY one entry (§3.4.3)
actor: {kind: agent, name: <agent-name>, model: <model-id>}
created: <RFC-3339 UTC>
category: <clarification|defect|choice>   # closed enum, §5.2.1
priority: p3
blocking: true                        # does the sender's own work block on the answer?
# refs:
#   - {ref: "<id>#<digest>", note: "<what this points at>"}
expected_response:
  shape: "<what a good answer looks like>"
classification: internal
---
Body: the question, context, minimal repro reference if applicable.
