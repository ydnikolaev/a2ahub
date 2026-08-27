---
schema: envelope/v1
id: XW-axon-20260827-n3d7
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
interim_behavior: "Fees rendered without currency symbol normalization."
needed_by: "next tuesday"
acceptance_criteria:
  - "Every currency code exists in ISO-4217; unknowns rejected at factory validation."
  - "Delivered as ingest payload conforming to XC-axon-ingest@1.x dictionary handle."
expected_response: {shape: "XS referencing the ingest run + contract handle version."}
thread: thread:axon-20260731-g9n5
classification: internal
---
Invalid fixture: `needed_by: "next tuesday"` does not parse as a `format: date`
value (no-silent-yes-2026-08/P3, US-1's own canonical example). Single
mutation against schemas/envelope/v1/fixtures/valid/XW-axon-20260731-p9d3.md:
`needed_by` is replaced with a non-date string; everything else is valid.
