---
schema: envelope/v1
id: XW-seomatrix-20260801-m4t8
type: work_request
title: Add translation_key minted by factory to content payloads
space: getvisa
from: seomatrix
to: [axon]
actor: {kind: agent, name: claude}
created: 2026-08-01T11:00:00Z
category: contract-change
priority: p3
blocking: false
interim_behavior: "Factory keeps sending locale-pinned payloads meanwhile."
refs: [{ref: "XC-seomatrix-content-payloads@2.1.0"}]
acceptance_criteria:
  - "Axon confirms integration constraints or declines with reason."
classification: internal
---
Invalid fixture: category `contract-change` additionally REQUIRES `proposed_change`
per §5.2.1 — omitted here (refs is present and pinned, so only proposed_change is missing).
