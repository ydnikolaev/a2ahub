---
schema: envelope/v2
id: XC-<system>-<slug>              # e.g. XC-axon-ingest — standing ID grammar §3.3
type: contract
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <owning-system>
to: [<consumer-system>]              # standing type: any length, no cardinality rule
actor: {kind: agent, name: <filled by a2a>, model: <filled by a2a>}   # kind: human|agent
created: <RFC-3339 UTC, e.g. 2026-07-28T10:00:00Z>
category: <api|data-feed|vocabulary|event-feed|other>
priority: p3
blocking: false
classification: internal
version: 0.0.0                       # set by `a2a contract publish`, never by hand — see the note at the bottom
schema_format: json-schema-2020-12
compat_policy: default
# generated_from:                    # include only for a code-generated contract
#   tool: "<generator name and version>"
#   source_digest: "sha256:<export-source-v1 digest>"
# x_binding:                          # OPTIONAL — what this contract IS and whether it may be adopted or pinned
#   artifact_class: <author's own vocabulary, e.g. non_binding_review>
#   compatibility_status: <the claim being made, or "none" to make no claim at all>
#   adoptable: <true|false — may `a2a contract adopt` target this>
#   runtime_pinnable: <true|false — may a consumer pin a runtime dependency against this>
#   Or the literal `x_binding: none` to declare non-binding in one line. Commented,
#   like generated_from above, and deliberately: ABSENCE IS `undeclared` AND IS
#   DISTINCT FROM A DECLARED `none` (P-1, and the schema's own words). A live
#   block here would make every fresh contract author a declaration nobody
#   made, and a live block with placeholder booleans would additionally render
#   a draft that cannot validate (spec 05, the same reversal work_request's
#   `binding` measured).
thread: <thread:system-YYYYMMDD-rand4 — a2a new mints this>
# refs:
#   - {ref: "<XC-id>@<version>", note: "<why>"}
artifacts:
  - path: schema/<slug>.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/<slug>.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/<slug>.schema.json
  - path: fixtures/invalid/<slug>.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/<slug>.schema.json
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

<!-- THE VERSION FIELD IS NOT YOURS TO SET. Leave it at 0.0.0.
     `a2a contract publish` finalizes this descriptor with the version you are
     publishing, and its commit — the one where the version FLIPS — is what
     makes that version resolvable ever after. `a2a contract materialize`
     depends on it, and so does every later version, which resolves the
     earlier one as its compatibility baseline.

     Set this to the version you intend to publish and there is nothing left
     to flip: publish writes byte-identical bytes, its commit carries only the
     publish event, and no commit establishes the version. `publish` refuses
     that up front — "publication-would-not-establish" — while it is still a
     one-line fix here. Bump by publishing (`--version 1.1.0`, or
     `--bump minor`), never by editing this field. -->
