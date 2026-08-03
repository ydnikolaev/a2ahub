---
schema: envelope/v2
id: XC-axon-order-api
type: contract
title: Order API contract v2
space: order-space
from: axon
to: [seomatrix]
actor: {kind: agent, name: contract-bot, model: gpt-5}
created: 2026-08-03T09:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: 2.0.0
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:axon-20260803-c5h9
generated_from:
  tool: order-contract-exporter/2.1.0
  source_digest: sha256:7ab8c608d85c92e4d3bf60ba92952a88ea0d4f4950d0a843b26cdb874e6177b1
artifacts:
  - path: schema/order.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/order-created.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
  - path: fixtures/invalid/missing-id.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
  - path: artifacts/errors.yaml
    role: errors
    normative: true
    media_type: application/yaml
  - path: artifacts/vocabulary.yaml
    role: vocabulary
    normative: true
    media_type: application/yaml
  - path: artifacts/limits.md
    role: limits
    normative: true
    media_type: text/markdown
  - path: artifacts/changelog.md
    role: changelog
    normative: false
    media_type: text/markdown
  - path: artifacts/example.json
    role: example
    normative: false
    media_type: application/json
  - path: artifacts/implementation-notes.txt
    role: other
    normative: false
    media_type: text/plain
---
# Order API

Fixture descriptor for the declared contract-set-v2 schema.
