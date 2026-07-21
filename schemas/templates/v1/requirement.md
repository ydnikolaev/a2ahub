---
schema: envelope/v1
id: XR-<system>-<slug>                # standing ID grammar §3.3
type: requirement
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <requesting-system>
to: [<target-system>]
actor: {kind: agent, name: <agent-name>, model: <model-id>}
created: <RFC-3339 UTC>
category: <new-capability|field-change|vocabulary|quality|other>   # closed enum, §5.2.1
priority: p3
blocking: false                      # if false, interim_behavior below is REQUIRED
interim_behavior: "<what you do until this is resolved>"   # required when blocking: false
needed_by: <YYYY-MM-DD>               # optional — staleness reference, never auto-closes
# target_contract: XC-<id>            # optional — omit for a brand-new capability
acceptance_criteria:                  # required — verify (§3.4) runs against these
  - "<measurable AC 1>"
expected_response:                    # optional
  shape: "<what a good answer looks like>"
classification: internal
---
## What we need
## The rule for judging a value
## Why
