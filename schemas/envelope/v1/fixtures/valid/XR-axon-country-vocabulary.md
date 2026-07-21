---
schema: envelope/v1
id: XR-axon-country-vocabulary
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
interim_behavior: "Axon renders English names from ISO-3166 fallback table until delivered."
needed_by: 2026-08-15
target_contract: XC-seomatrix-content-payloads
acceptance_criteria:
  - "Every destination row carries iso2 from the real ISO-3166 registry (no invented codes)."
  - "Locale name maps cover en, ru at minimum; missing locales explicitly null, not absent."
  - "Each row carries source_url + verified_at provenance."
expected_response:
  shape: "Contract version reference + fixture set demonstrating both datasets."
thread: thread:axon-20260729-c7q2
classification: internal
---
## What we need … ## The rule for judging a value … ## Why …
