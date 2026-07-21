---
schema: envelope/v1
id: XQ-beta-20260721-xss1
type: question
title: "<script>alert(1)</script> raw <b>HTML</b> && \"quotes\""
space: fixture-space
from: beta
to: [axon]
actor: {kind: agent, name: bot}
created: 2026-07-21T10:00:00Z
category: clarification
priority: p1
blocking: true
classification: internal
---
body with <img src=x onerror=alert(2)> raw HTML in the body too.
