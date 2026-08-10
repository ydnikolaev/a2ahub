---
schema: envelope/v2
id: XW-<system>-<YYYYMMDD>-<rand4>     # exchange ID grammar §3.3
type: work_request
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <requesting-system>
to: [<target-system>]                 # exchange type: EXACTLY one entry (§3.4.3)
actor: {kind: agent, name: <filled by a2a>, model: <filled by a2a>}
created: <RFC-3339 UTC>
category: <data|feature|fix|investigation|contract-change|process-change|other>   # closed enum, §5.2.1
priority: p3
blocking: false
interim_behavior: "<what you do until this is resolved>"   # required when blocking: false
needed_by: <YYYY-MM-DD>               # response-bearing ask: created +1d if blocking/p1, else +2d; >2d must cite an external non-agent constraint
acceptance_criteria:                  # required
  - "<measurable AC 1>"
# proposed_change: "<structured summary>"   # REQUIRED when category is contract-change or process-change
thread: <thread:system-YYYYMMDD-rand4 — the conversation this belongs to; a2a new mints it>
# refs:                                      # REQUIRED (with a pinned entry) when category is contract-change or process-change
#   - {ref: "<XC-id>@<version>"}
# binding:                                   # OPTIONAL — what this artifact IS and whether it may be adopted or pinned (P5 D1/Q2).
#   artifact_class: <author's own vocabulary, e.g. non_binding_review>
#   compatibility_status: <the claim being made, or "none" to make no claim at all>
#   adoptable: <true|false — may `a2a contract adopt` target this>
#   runtime_pinnable: <true|false — may a consumer pin a runtime dependency against this>
#   Or the literal `binding: none` to declare non-binding in one line. Commented,
#   like proposed_change and refs above, and deliberately: ABSENCE IS `undeclared`
#   AND IS DISTINCT FROM A DECLARED `none` (P-1, and the schema's own words). A
#   live block here would make every fresh work_request author a declaration
#   nobody made — B3's defect one level out — and a live block with placeholder
#   booleans would additionally render a draft that cannot validate.
# attachments:                        # bytes carried WITH this request, addressed by digest — NOT a fulfilled deliver and NOT a contract pin.
#   - ref: <content-addressed blob id>
#     digest: sha256:<...>
#     role: <author's own vocabulary>
#     conforms_to: <XC-id>@<version>   # optional
#     verification: <required|offered|none>
#     retention: <pinned|duration, e.g. 720h>
#     expires_at: <RFC-3339 UTC>       # written by the tool at attach time, absent under retention: pinned
#   ref/digest/expires_at are written by `a2a attach` from real bytes at attach
#   time — do not hand-write an entry here; run `a2a attach` against the file
#   you want carried, which fills role/conforms_to/verification/retention from
#   you and mints the rest.
expected_response:
  shape: "<what a good answer looks like>"
classification: internal
---
Body: what's needed, acceptance evidence expectations.
