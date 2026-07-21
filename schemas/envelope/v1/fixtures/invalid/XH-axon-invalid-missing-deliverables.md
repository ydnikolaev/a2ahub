---
schema: envelope/v1
id: XH-axon-20260810-f3s6
type: handoff
title: Ingest playground — self-serve contract testing for the factory
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: claude-code, model: claude-fable-5}
created: 2026-08-10T13:00:00Z
priority: p3
blocking: false
fulfills: [XW-seomatrix-20260722-a1a1]
verification: "Suite: axon e2e ingest-playground (42 cases, green 2026-08-10)."
acceptance_criteria: ["Factory can validate any payload against live schemas without axon involvement."]
limitations: ["Rate-limited to 10 rps.", "No auth token self-service yet."]
classification: internal
---
Invalid fixture: `handoff` requires `deliverables[]` per §16.2 — omitted here.
