---
schema: envelope/v1
id: XW-axon-20260731-p9d5
type: work_request
title: Currency dictionary keyed by real ISO-4217 codes
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: 2026-07-31T08:40:00Z
category: data
priority: p3
blocking: false
needed_by: 2026-08-20
acceptance_criteria:
  - "Every currency code exists in ISO-4217; unknowns rejected at factory validation."
expected_response: {shape: "XS referencing the ingest run + contract handle version."}
classification: internal
---
Invalid fixture: `blocking: false` without `interim_behavior` — §5.2 marks
`interim_behavior` REQUIRED when `blocking: false` on work_request (CC-011).
Single mutation: the `interim_behavior` line is omitted; everything else is valid.
