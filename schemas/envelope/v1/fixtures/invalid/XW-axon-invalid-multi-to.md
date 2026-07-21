---
schema: envelope/v1
id: XW-axon-20260731-p9d4
type: work_request
title: Currency dictionary keyed by real ISO-4217 codes
space: getvisa
from: axon
to: [seomatrix, thalamus]
actor: {kind: agent, name: codex}
created: 2026-07-31T08:40:00Z
category: data
priority: p3
blocking: false
interim_behavior: "Fees rendered without currency symbol normalization."
needed_by: 2026-08-20
acceptance_criteria:
  - "Every currency code exists in ISO-4217; unknowns rejected at factory validation."
classification: internal
---
Invalid fixture: exchange types (X class except decision) MUST address exactly
one system (§3.4.3); `to` here carries two entries.
