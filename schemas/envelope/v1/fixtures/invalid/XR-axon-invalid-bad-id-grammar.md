---
schema: envelope/v1
id: XR-axon
type: requirement
title: Canonical country vocabulary — malformed id (missing slug)
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: claude-code, model: claude-fable-5}
created: 2026-07-29T09:12:00Z
category: vocabulary
priority: p2
blocking: false
acceptance_criteria:
  - "Every destination row carries iso2 from the real ISO-3166 registry."
classification: internal
---
Invalid fixture: `id` must be `<PREFIX>-<system>-<slug>` per §3.3 — this id has no slug segment.
