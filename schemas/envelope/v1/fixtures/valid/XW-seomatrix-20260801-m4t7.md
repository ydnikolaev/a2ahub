---
schema: envelope/v1
id: XW-seomatrix-20260801-m4t7
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
proposed_change: "New optional field translation_key (stable across locales), minor bump."
acceptance_criteria:
  - "Axon confirms integration constraints or declines with reason."
expected_response: {shape: "accept/decline with integration constraints."}
classification: internal
---
