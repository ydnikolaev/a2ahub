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

<What this contract covers, error shape, key rules. `provides/<slug>/schema/`
holds the machine schemas; `fixtures/valid|invalid/` the golden examples.>
