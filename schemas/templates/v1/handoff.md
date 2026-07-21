---
schema: envelope/v1
id: XH-<system>-<YYYYMMDD>-<rand4>     # exchange ID grammar §3.3
type: handoff
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <producing-system>
to: [<receiving-system>]               # exchange type: EXACTLY one entry (§3.4.3)
actor: {kind: agent, name: <agent-name>, model: <model-id>}
created: <RFC-3339 UTC>
# NOTE: handoff has NO `category` field (§5.2.1) — do not add one, it will be rejected.
priority: p3
blocking: false
fulfills: [<originating exchange/requirement id>]   # required, §16.2 — NOT the base `origin` field
refs:
  - {ref: "<XC-id>@<version>", note: "<what this ships as part of>"}
deliverables:                          # required, §16.2 — each: name, ref, kind
  - {name: "<deliverable name>", ref: "<repo/ref/digest or id@version>", kind: contract}   # kind: code|contract|config|doc|data
verification: "<suites run, results summary, how the receiver re-runs/checks each claim>"   # required
acceptance_criteria: ["<restated measurably, inherited from the originating request>"]        # required
limitations: []                        # required — an empty list is a claim, not an omission
# env_requirements: "<what the receiver's environment must provide>"   # optional
classification: internal
---
## Context
## What was built
## How to verify
## How to operate
## Limitations & next steps
