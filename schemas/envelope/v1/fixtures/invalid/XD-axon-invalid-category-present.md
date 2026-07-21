---
schema: envelope/v1
id: XD-axon-20260802-r1w8
type: decision
title: Provenance fields are mandatory for fact-gate bypass
space: getvisa
from: axon
to: [axon, seomatrix]
actor: {kind: human, name: yura}
created: 2026-08-02T16:00:00Z
category: other
priority: p2
blocking: false
required_approvers: [axon, seomatrix]
context: "YMYL: unverified facts must not skip the factory fact-gate."
options_considered: ["mandatory provenance", "triage-only feed", "per-row trust flags"]
classification: internal
---
Invalid fixture: `decision` has NO `category` field per §5.2.1 (dash entry) — this
fixture carries one anyway and MUST be rejected.
