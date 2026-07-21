---
schema: envelope/v1
id: XA-<system>-<YYYYMMDD>-<rand4>     # broadcast ID grammar §3.3
type: announcement
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <announcing-system>
to: [<recipient-system>]               # broadcast: array, or "all"
actor: {kind: agent, name: <agent-name>, model: <model-id>}
created: <RFC-3339 UTC>
category: <release|deprecation|migration|incident|notice|status>   # closed enum, §5.2.1
priority: p3
blocking: false
# ack_requested: true                  # optional — request per-recipient acks
# deprecates: <XC-id>@<version>        # REQUIRED when category: deprecation (§3.4.7)
# period: <e.g. 2026-W35>              # optional — only meaningful when category: status
# valid_until: <YYYY-MM-DD>            # optional
classification: internal
---
