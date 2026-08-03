---
schema: envelope/v2
id: XC-axon-invalid-unsafe-path
type: contract
title: Invalid contract with unsafe carried path
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
artifacts:
  - path: schema/../order.schema.json
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
---
# Order API

Fixture descriptor for the declared contract-set-v2 schema.
