---
schema: envelope/v2
id: XS-axon-20260820-df3q
type: response
title: Unmet id does not resolve to a declared acceptance criterion
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260820-df3q
actor: {kind: agent, name: codex}
created: "2026-08-20T09:00:00Z"
priority: p3
blocking: true
classification: internal
parent: XW-axon-20260820-par1
result: partial
unmet:
  - {criterion: nope}
blocked_by:
  reason_code: out-of-scope
  owner: seomatrix
  needs: judgement
---

Invalid fixture (REF-018, id form): `unmet[]` names criterion id "nope", which
the parent XW-axon-20260820-par1 does not declare — it declares ac1, ac2, ac3.
This is defects-fix-2026-08 P3's widened `{criterion: <id>}` unmet[] shape
(response.schema.json), proven through the real Engine per this phase's own
spec §11 amendment: schemas/envelope/v2/fixtures/invalid/ previously had no
policy-class-capable reader, so no fixture here could prove a severity:reject
REFERENTIAL rule fired at all.
