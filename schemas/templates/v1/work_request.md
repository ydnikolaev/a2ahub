---
schema: envelope/v1
id: XW-<system>-<YYYYMMDD>-<rand4>     # exchange ID grammar §3.3
type: work_request
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <requesting-system>
to: [<target-system>]                 # exchange type: EXACTLY one entry (§3.4.3)
actor: {kind: agent, name: <agent-name>, model: <model-id>}
created: <RFC-3339 UTC>
category: <data|feature|fix|investigation|contract-change|process-change|other>   # closed enum, §5.2.1
priority: p3
blocking: false
interim_behavior: "<what you do until this is resolved>"   # required when blocking: false
needed_by: <YYYY-MM-DD>
acceptance_criteria:                  # required
  - "<measurable AC 1>"
# proposed_change: "<structured summary>"   # REQUIRED when category is contract-change or process-change
# refs:                                      # REQUIRED (with a pinned entry) when category is contract-change or process-change
#   - {ref: "<XC-id>@<version>"}
expected_response:
  shape: "<what a good answer looks like>"
classification: internal
---
Body: what's needed, acceptance evidence expectations.
