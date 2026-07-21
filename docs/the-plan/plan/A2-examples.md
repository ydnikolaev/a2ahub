# Appendix B — Filled Examples (golden fixtures seed)

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> One example per object type + events + manifest, on real getvisa-chain
> material. These become the T1 valid-fixture set verbatim (13.5). Bodies
> are abbreviated here with `…` where prose continues; fixtures ship full.

## B.1 contract — `axon/provides/ingest/contract.md`

```yaml
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
```
Plus `schema/` (per-handle JSON Schemas) and `fixtures/valid|invalid/`.

## B.2 requirement — `axon/requires/XR-axon-country-vocabulary.md`

```yaml
---
schema: envelope/v1
id: XR-axon-country-vocabulary
type: requirement
title: Canonical country vocabulary with ISO codes and provenance
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: claude-code, model: claude-fable-5, session: s-0a1b}
created: 2026-07-29T09:12:00Z
category: vocabulary
priority: p2
blocking: false
interim_behavior: "Axon renders English names from ISO-3166 fallback table until delivered."
needed_by: 2026-08-15
target_contract: XC-seomatrix-content-payloads
acceptance_criteria:
  - "Every destination row carries iso2 from the real ISO-3166 registry (no invented codes)."
  - "Locale name maps cover en, ru at minimum; missing locales explicitly null, not absent."
  - "Each row carries source_url + verified_at provenance."
expected_response:
  shape: "Contract version reference + fixture set demonstrating both datasets."
thread: thread:axon-20260729-c7q2
classification: internal
---
## What we need … ## The rule for judging a value … ## Why …
```

## B.3 question — `seomatrix/exchanges/XQ-seomatrix-20260730-h2k8.md`

```yaml
---
schema: envelope/v1
id: XQ-seomatrix-20260730-h2k8
type: question
title: Ingest 422 error shape contradicts §4.3 example
space: getvisa
from: seomatrix
to: [axon]
actor: {kind: agent, name: claude, model: claude-fable-5}
created: 2026-07-30T14:02:00Z
category: defect
priority: p2
blocking: true
refs:
  - {ref: "XC-axon-ingest@1.0.0", note: "descriptor §4.3 vs schema/errors.json disagree"}
expected_response: {shape: "Which is authoritative; corrected version if schema wrong."}
classification: internal
---
Body: the two locations, observed payload, minimal repro reference…
```

## B.4 work_request — `axon/exchanges/XW-axon-20260731-p9d3.md` (category `data`)

```yaml
---
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: Currency dictionary keyed by real ISO-4217 codes
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: 2026-07-31T08:40:00Z
category: data
priority: p3
blocking: false
interim_behavior: "Fees rendered without currency symbol normalization."
needed_by: 2026-08-20
acceptance_criteria:
  - "Every currency code exists in ISO-4217; unknowns rejected at factory validation."
  - "Delivered as ingest payload conforming to XC-axon-ingest@1.x dictionary handle."
expected_response: {shape: "XS referencing the ingest run + contract handle version."}
classification: internal
---
```

## B.5 work_request (contract-change) — `seomatrix/exchanges/XW-seomatrix-20260801-m4t7.md`

```yaml
---
schema: envelope/v1
id: XW-seomatrix-20260801-m4t7
type: work_request
title: Add translation_key minted by factory to content payloads
space: getvisa
from: seomatrix
to: [axon]
actor: {kind: agent, name: claude}
created: 2026-08-01T11:00:00Z
category: contract-change
priority: p3
blocking: false
interim_behavior: "Factory keeps sending locale-pinned payloads meanwhile."
refs: [{ref: "XC-seomatrix-content-payloads@2.1.0"}]
proposed_change: "New optional field translation_key (stable across locales), minor bump."
acceptance_criteria:
  - "Axon confirms integration constraints or declines with reason."
expected_response: {shape: "accept/decline with integration constraints."}
classification: internal
---
```

## B.6 decision — `decisions/XD-axon-20260802-r1w9.md`

```yaml
---
schema: envelope/v1
id: XD-axon-20260802-r1w9        # <system> = drafting system per §3.3
type: decision
title: Provenance fields are mandatory for fact-gate bypass
space: getvisa
from: axon                       # decisions live in decisions/ — the §5.2
                                 # from-matches-section rule has an explicit
                                 # exception for type: decision
to: [axon, seomatrix]
actor: {kind: human, name: yura}
created: 2026-08-02T16:00:00Z
priority: p2
blocking: false
required_approvers: [axon, seomatrix]
context: "YMYL: unverified facts must not skip the factory fact-gate."
options_considered: ["mandatory provenance", "triage-only feed", "per-row trust flags"]
classification: internal
---
```

## B.7 response — `seomatrix/exchanges/XS-seomatrix-20260805-b6n2.md`

```yaml
---
schema: envelope/v1
id: XS-seomatrix-20260805-b6n2
type: response
title: Country vocabulary delivered
space: getvisa
from: seomatrix
to: [axon]
actor: {kind: agent, name: claude}
created: 2026-08-05T10:30:00Z
parent: XR-axon-country-vocabulary
result: delivered
refs:
  - {ref: "XC-seomatrix-content-payloads@2.2.0", note: "vocabulary handle added"}
  - {ref: "XC-seomatrix-content-payloads@2.2.0#sha256:9e1a…", note: "fixture set"}
thread: thread:axon-20260729-c7q2
classification: internal
---
Per-AC evidence: AC1 → …, AC2 → …, AC3 → …
```

## B.8 handoff — `axon/exchanges/XH-axon-20260810-f3s5.md`

```yaml
---
schema: envelope/v1
id: XH-axon-20260810-f3s5
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
refs:
  - {ref: "XC-axon-ingest@1.1.0", note: "playground ships as part of ingest 1.1.0"}
deliverables:
  - {name: "playground endpoint", ref: "XC-axon-ingest@1.1.0", kind: contract}
verification: "Suite: axon e2e ingest-playground (42 cases, green 2026-08-10); receiver re-check: steps in §How-to-verify against staging."
acceptance_criteria: ["Factory can validate any payload against live schemas without axon involvement."]
limitations: ["Rate-limited to 10 rps.", "No auth token self-service yet."]
env_requirements: "Staging credentials issued per §9.3."
classification: internal
---
## Context … ## What was built … ## How to verify … ## How to operate … ## Limitations & next steps …
```

## B.9 announcement — `axon/exchanges/XA-axon-20260901-d8k1.md`

```yaml
---
schema: envelope/v1
id: XA-axon-20260901-d8k1
type: announcement
title: "Deprecation: ingest v1 destination handle sunset 2026-10-01"
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: human, name: yura}
created: 2026-09-01T09:00:00Z
category: deprecation
priority: p2
blocking: false
ack_requested: true
deprecates: XC-axon-ingest@1.0.0
refs: [{ref: "XC-axon-ingest@2.0.0", note: "successor"}]
valid_until: 2026-10-01
classification: internal
---
```

## B.10 announcement (status) — `seomatrix/exchanges/XA-seomatrix-20260901-z2v4.md`

```yaml
---
schema: envelope/v1
id: XA-seomatrix-20260901-z2v4
type: announcement
title: Factory weekly: coverage & pipeline state
space: getvisa
from: seomatrix
to: [axon]
actor: {kind: agent, name: claude}
created: 2026-09-01T18:00:00Z
category: status
priority: p4
blocking: false
period: 2026-W35
classification: internal
---
```

## B.11 lifecycle events

`seomatrix/events/2026/01J3ZK8Q2R4X6T8V0B2D4F6H8K.yaml` — acknowledge:

```yaml
schema: event/v1
event: 01J3ZK8Q2R4X6T8V0B2D4F6H8K
space: getvisa
subject: XR-axon-country-vocabulary
transition: acknowledge
state: acknowledged
actor: {kind: agent, name: claude, system: seomatrix}
at: 2026-07-29T15:20:00Z
note: "Linked to factory epic; ETA in accept to follow."
```

`axon/events/2026/01J40A7M9P1S3U5W7Y9A1C3E5G.yaml` — satisfy (requester's
event folding the requirement to `satisfied`, referencing response B.7 and
the fulfilling contract version — requirements complete via `satisfy`,
§3.4.2, not verify/close):

```yaml
schema: event/v1
event: 01J40A7M9P1S3U5W7Y9A1C3E5G
space: getvisa
subject: XR-axon-country-vocabulary
transition: satisfy
state: satisfied
actor: {kind: agent, name: claude-code, system: axon}
at: 2026-08-05T17:45:00Z
refs:
  - {ref: "XS-seomatrix-20260805-b6n2"}
  - {ref: "XC-seomatrix-content-payloads@2.2.0"}
note: "All three ACs verified against fixtures."
```

## B.12 consumer registry — `axon/consumes.yaml` (§5.2.3)

```yaml
schema: consumes/v1
system: axon
dependencies:
  - {contract: XC-seomatrix-content-payloads, major: 2, since: 2026-08-05}
```

## B.13 space manifest — `space.yaml` (excerpt)

```yaml
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
gates: default                    # G1–G5 per §3.7
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
  - system: seomatrix
    org: seomatrix
    section: seomatrix/
    owners: [misha-gh]
    status: active
    joined: 2026-07-28
vendored: []
```
