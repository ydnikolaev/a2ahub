---
schema: envelope/v2
id: XC-<system>-<slug>              # e.g. XC-axon-ingest — standing ID grammar §3.3
type: contract
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <owning-system>
to: [<consumer-system>]              # standing type: any length, no cardinality rule
actor: {kind: agent, name: <agent-name>, model: <model-id>}   # kind: human|agent
created: <RFC-3339 UTC, e.g. 2026-07-28T10:00:00Z>
category: <api|data-feed|vocabulary|event-feed|other>
priority: p3
blocking: false
classification: internal
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
# generated_from:                    # include only for a code-generated contract
#   tool: "<generator name and version>"
#   source_digest: "sha256:<export-source-v1 digest>"
thread: <thread:system-YYYYMMDD-rand4 — a2a new mints this>
# refs:
#   - {ref: "<XC-id>@<version>", note: "<why>"}
artifacts:
  - path: schema/contract.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/example.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/contract.schema.json
  - path: fixtures/invalid/example.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/contract.schema.json
# Declare every other regular carried file exactly once. Companion roles live
# under artifacts/: errors, vocabulary, limits, changelog, example, or other.
# `conforms_to` is required only on valid-fixture and invalid-fixture entries.
#  - path: artifacts/errors.yaml
#    role: errors
#    normative: true
#    media_type: application/yaml
---
# <Contract name>

<One paragraph describing the contract in the consumer's terms.>

## What it covers

<The operations, feeds, or vocabulary in scope, plus deliberate exclusions.>

## Error shape

<How failures are represented and which codes or statuses consumers handle.>

## Compatibility intent

<What is breaking beyond the computed schema-shape check, and what may grow.>

## Owner and support

<Who owns this contract and where consumers can ask for help.>
