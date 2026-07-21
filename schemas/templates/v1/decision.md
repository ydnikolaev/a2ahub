---
schema: envelope/v1
id: XD-<system>-<YYYYMMDD>-<rand4>     # exchange ID grammar §3.3; <system> = drafting system
type: decision
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <drafting-system>                # decisions live in decisions/ — from-matches-section exception, §5.2
to: [<participant-system>, ...]        # decision is exempt from the exactly-one-entry rule (§3.4.3)
actor: {kind: human, name: <human-name>}   # decisions typically carry a human actor (G3 gate)
created: <RFC-3339 UTC>
# NOTE: decision has NO `category` field (§5.2.1) — do not add one, it will be rejected.
priority: p3
blocking: false
required_approvers: [<system-1>, <system-2>]   # required
context: "<why this decision is needed>"        # required
options_considered: ["<option A>", "<option B>"]   # required
classification: internal
---
