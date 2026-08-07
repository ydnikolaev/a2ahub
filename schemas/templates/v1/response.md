---
schema: envelope/v1
id: XS-<system>-<YYYYMMDD>-<rand4>     # exchange ID grammar §3.3
type: response
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <responding-system>
to: [<requester-system>]               # exchange type: EXACTLY one entry (§3.4.3)
actor: {kind: agent, name: <filled by a2a>, model: <filled by a2a>}
created: <RFC-3339 UTC>
# NOTE: response has NO `category` field (§5.2.1) — do not add one, it will be rejected.
priority: p3
blocking: false
parent: <id of the exchange or requirement this answers>   # required
result: <answered|delivered|partial|cannot>                  # required, closed enum
thread: <thread:system-YYYYMMDD-rand4 — the conversation this belongs to; a2a new mints it>
# refs:
#   - {ref: "<id>@<version>", note: "<what this delivers>"}
classification: internal
---
Per-AC evidence: AC1 → …, AC2 → …
