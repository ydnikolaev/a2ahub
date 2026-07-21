---
schema: envelope/v1
id: XR-axon-invalid-blocking-no-interim
type: requirement
title: Canonical country vocabulary with ISO codes and provenance
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: claude-code, model: claude-fable-5, session: s-0a1b}
created: 2026-07-29T09:12:00Z
category: vocabulary
priority: p2
blocking: false
needed_by: 2026-08-15
target_contract: XC-seomatrix-content-payloads
acceptance_criteria:
  - "Every destination row carries iso2 from the real ISO-3166 registry (no invented codes)."
expected_response:
  shape: "Contract version reference + fixture set demonstrating both datasets."
classification: internal
---
Invalid fixture: `blocking: false` without `interim_behavior` — §5.2 marks
`interim_behavior` REQUIRED when `blocking: false` on requirement (CC-011).
Single mutation: the `interim_behavior` line is omitted; everything else is valid.
