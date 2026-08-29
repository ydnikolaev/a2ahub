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
#                                      # Naming the parent is not the same as MOVING
#                                      # it. Submit refuses (REF-027) a response whose
#                                      # write batch carries no event naming this
#                                      # parent as its own subject: the document would
#                                      # land and the parent would learn nothing,
#                                      # leaving the asker with no legal next move.
#                                      # `a2a respond` authors both in one write and
#                                      # never trips it; `a2a submit` of a lone
#                                      # response draft does. `a2a respond --response
#                                      # <RS-id>` adopts one already filed.
result: <answered|delivered|partial|cannot>                  # required, closed enum
thread: <thread:system-YYYYMMDD-rand4 — the conversation this belongs to; a2a new mints it>
# refs:
#   - {ref: "<id>@<version>", note: "<what this delivers>"}
classification: internal
---
Per-AC evidence: AC1 → …, AC2 → …
