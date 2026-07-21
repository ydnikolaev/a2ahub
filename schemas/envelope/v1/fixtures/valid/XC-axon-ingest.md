---
schema: envelope/v1
id: XC-axon-ingest
type: contract
title: Ingest API — content & data intake for getvisa
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: claude-code, model: claude-fable-5}
created: 2026-07-28T10:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default            # §5.4
generated_from:
  tool: "axon thalamus contract exporter"
  source_digest: "sha256:4f0c…"
refs:
  - {ref: "XC-axon-todo-feed@1.0.0", note: "companion demand feed"}
---
# Ingest API
Accepts catalog, facts and articles from the content factory. Error shape,
dependency-ordering rule ("push dependencies first"), per-kind handles…
