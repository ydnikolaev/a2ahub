---
schema: envelope/v1
id: XC-axon-invalid-missing-version
type: contract
title: Ingest API — missing required version field
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: claude-code, model: claude-fable-5}
created: 2026-07-28T10:00:00Z
category: api
priority: p2
blocking: false
classification: internal
schema_format: json-schema-2020-12
compat_policy: default
---
# Ingest API
Invalid fixture: `contract` requires `version` (semver) per §5.2.1 — omitted here.
