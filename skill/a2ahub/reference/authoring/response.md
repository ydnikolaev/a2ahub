---
schema: envelope/v2
id: XS-<system>-<YYYYMMDD>-<rand4>     # exchange ID grammar §3.3
type: response
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <responding-system>
to: [<requester-system>]               # exchange type: EXACTLY one entry (§3.4.3)
actor: {kind: agent, name: <filled by a2a>, model: <filled by a2a>}
created: <RFC-3339 UTC>
# NOTE: response has NO `category` field (§5.2.1) — do not add one, it will be rejected.
priority: p3
blocking: false
parent: <id of the exchange or requirement this answers>   # required
#                                      # Naming the parent is not the same as MOVING
#                                      # it. Submit refuses (REF-027) a response whose
#                                      # write batch carries no event naming this
#                                      # parent as its own subject: the document would
#                                      # land and the parent would learn nothing,
#                                      # leaving the asker with no legal next move.
#                                      # `a2a respond` authors both in one write and
#                                      # never trips it; `a2a submit` of a lone
#                                      # response draft does. `a2a respond --response
#                                      # <RS-id>` adopts one already filed.
result: <answered|delivered|partial|cannot>                  # required, closed enum
thread: <thread:system-YYYYMMDD-rand4 — the conversation this belongs to; a2a new mints it>
# refs:
#   - {ref: "<id>@<version>", note: "<what this delivers>"}
# delivers:                            # OPTIONAL — the data package(s) this
#                                      # response ANNOUNCES as delivered
#                                      # (`a2a respond --delivers <DP-id>`,
#                                      # repeatable). Present ONLY when the
#                                      # response announces a data delivery;
#                                      # its absence is the ordinary answer
#                                      # shape — `result: delivered` alone
#                                      # implies no package. Submit refuses
#                                      # (REF-024) while a named package has
#                                      # not landed on the space's main branch.
#   - DP-<system>-<YYYYMMDD>-<rand4>
# unmet:                              # OPTIONAL — P6 incompleteness. Indices into the
#                                      # parent's acceptance_criteria[] this response did
#                                      # NOT satisfy — by INDEX, never by restating the
#                                      # criterion in prose, so the record cannot drift
#                                      # from what the parent actually asked. May be
#                                      # present and empty when the shortfall is standing,
#                                      # not completeness (see `standing` below).
#   - 0
# blocked_by:                         # OPTIONAL — what would unblock the unmet criteria.
#                                      # Required alongside `unmet` when `result` is
#                                      # partial/cannot and the shortfall is completeness
#                                      # rather than standing (envelope/v2/response's own
#                                      # conditional). `owner` is legality-checked (P-2)
#                                      # before any surface reports it.
#   reason_code: <split-required|security-concern|out-of-scope|duplicate|other>
#   owner: <the system actually being waited on>
#   needs: <bytes|judgement|decision>
# attempted: "<what WAS done, so '0 of N, reported' is distinguishable from silence>"
# standing: <authoritative|provisional|advisory>   # OPTIONAL, default authoritative —
#   # whether the supplier is entitled to make these values binding (D1). A `partial`
#   # that answered every criterion but is not authoritative carries this with an EMPTY
#   # (or absent) `unmet[]`, instead of falsely marking a met criterion unmet.
# residue:                            # OPTIONAL — where a non-met criterion goes on a
#                                      # terminal transition (close/withdraw/cancel) that
#                                      # would otherwise extinguish it silently (D3).
#   - criterion_index: 0
#     carried_to: <successor artifact id>
classification: internal
---
Per-AC evidence: AC1 → …, AC2 → …
