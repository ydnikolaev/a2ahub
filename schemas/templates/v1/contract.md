---
schema: envelope/v1
id: XC-<system>-<slug>              # e.g. XC-axon-ingest — standing ID grammar §3.3
type: contract
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <owning-system>
to: [<consumer-system>]              # standing type: any length, no cardinality rule
actor: {kind: agent, name: <agent-name>, model: <model-id>}   # kind: human|agent
created: <RFC-3339 UTC, e.g. 2026-07-28T10:00:00Z>
category: <api|data-feed|vocabulary|event-feed|other>   # closed enum, §5.2.1
priority: p3                         # p1|p2|p3|p4, default p3
blocking: false
classification: internal             # public|internal|restricted, default internal
version: 1.0.0                       # semver — required
schema_format: json-schema-2020-12   # json-schema-2020-12|openapi-3.x|proto3|other — required
compat_policy: default               # §5.4 — required
# generated_from:                    # optional — REQUIRED only if this contract is code-generated (§5.3)
#   tool: "<free text>"
#   source_digest: "sha256:<hex>"
# refs:                              # optional — pin dependencies as id@version
#   - {ref: "<XC-id>@<version>", note: "<why>"}
---
# <Contract name>

<One paragraph: what this contract is for, in the consumer's terms. Not how it
is implemented.>

## What it covers

<The operations, feeds or vocabulary in scope — and, just as usefully, what is
deliberately OUT of scope so a consumer does not build on something you never
promised. `provides/<slug>/schema/` holds the machine schemas;
`fixtures/valid|invalid/` the golden examples.>

## Error shape

<How failures are reported: the shape, the codes or statuses, and which of them
a consumer is expected to handle rather than treat as fatal. The JSON Schema
cannot say this, and it is the first thing a consumer needs.>

## Compatibility intent

<What you consider a breaking change here, beyond what `compat_policy` and the
computed check enforce. Which fields may gain values, which are frozen, how much
notice you intend to give. The machine check computes SCHEMA SHAPE; intent about
meaning lives here or nowhere.>

## Owner and support

<Who to ask, and where. A contract outlives the conversation that produced it,
and a consumer reading it in six months has only this file.>

<!-- These four headings are the CONVENTION, not a schema rule: nothing rejects
     a contract that renames or drops them. They are here because what a
     consumer's code depends on — the JSON Schema — IS machine-checked,
     including breaking-change computation, while everything the schema cannot
     express is prose that had no agreed shape at all. Keep the headings and a
     reader knows where to look; drop them and each contract has to be read
     from the top to find out whether it says anything about errors.

     On a json-schema-* contract the schema and fixtures are REQUIRED, not
     optional: `publish` refuses without a schema and at least one valid
     fixture, because §5.4b's compatibility check has nothing to compute
     against otherwise. `a2a contract new` scaffolds a starter pair; keep the
     fixtures honest and they become the baseline the next version is checked
     against. Other schema_format values are exempt — deep compatibility for
     those is your own CI's job. -->
