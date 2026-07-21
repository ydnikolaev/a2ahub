# Epic-coherence audit — v1-min-2026-07 — 2026-07-21

**Verdict:** PASS after fixes — 4 HIGH / 6 MED / ~12 LOW found on the freshly
cut 14-spec corpus (5 read-only probes: interfaces, plan, parity, boundaries,
northstar); every HIGH and actionable MED fixed the same day by a
lead-adjudicated fix wave (7 agents, file-disjoint), each fix logged in the
touched spec's `## Amendments`. Remaining LOWs are disclosed open questions,
not contradictions.

## Findings & resolutions

| Sev | Where | What | Resolution |
|---|---|---|---|
| HIGH | P3 §7 ↔ P4 §T1 | P4 exported no pre-write legality-check primitive P3's V2 path requires (§5.5 "write refused locally") | P4: added "Legality check (pre-write)" export (3-valued verdict); flag enums reconciled (`state-claim-mismatch` fold-only); P3 acks superset |
| HIGH | P2 ↔ P3 ↔ ADR-001 | P3 claimed Go constants as error-code SSOT, reversing ADR-001 (data in `schemas/` is SSOT) | P3 rewritten: `schemas/errors/v1/registry.yaml` is SSOT, P3 adds non-schema-class rows + embeds; footprint exception added; P2 Q2 closed |
| HIGH | P5 ↔ P8 | P8 assumed a review-required/auto-merge parameter on `host.OpenPR` that P5 never grants | Decision: OpenPR is uniform (auto-merge always); gating = CODEOWNERS + V3 required check (5.4b review query) holding auto-merge; G3 identity binding = V3 + fold, not a host primitive. Both specs rewritten |
| HIGH | P1 OQ1 | Stale wave-1 blocker: "stdlib only" vs YAML — already resolved by ADR-002 | P1 updated to "stdlib + ADR-002 deps"; OQ1 closed; ULID minting assigned to `internal/artifact` |
| HIGH | tracker strategy ↔ DAG | Strategy grouped P9 after P6; DAG has P9 ← [P3,P5], parallel with P6 | Strategy rewritten: {P6,P9} parallel; cmd/a2a wiring lines declared lead-reserved |
| HIGH | P14 ↔ tracker | Spec misquoted tracker `blocked_by` as [P1,P5,P6,P7,P8]; tracker says [P10] | Spec fixed to cite [P10] + transitive chain |
| MED | P8 footprint | "cmd_verbs.go or per-verb files" uncommitted | Pinned: `cmd_lifecycle.go` + `cmd_contract.go` + `internal/validate/policy_retire.go` |
| MED | P8 ↔ ADR-001 | "space wraps host per ADR-001" mis-citation (ADR allows cli→host) | Restated as phase-local choice, ADR attribution dropped |
| MED | P5 OQ1/OQ2 | Credential precedence undefined; idempotent PR lookup unassigned | Closed: env > keychain ref > actionable error; 5th host primitive "Find PR by head branch" granted |
| MED | P1/ADR-002 | ULID ownership unassigned in specs | Assigned to `internal/artifact`; fold stays stdlib-pure consumer |
| MED | cmd/a2a shared wiring | 5 phases edit cmd/a2a wiring with no serialization note | Lead-reserved per tracker strategy |
| LOW | P8 testing row | "(stubbed) hub-equivalent fold" exceeded the v1-min cut | Binary-fold-only; hub half re-verifies at v2 (§15 L1 exclusion) |
| LOW | P7 cache wording | Read as cache importing validate (forbidden) | Clarified: registry lookup in `cmd_show.go` (cli); cache validate-free |
| LOW | P9/P11 naming | "validator binary" vs "a2a CLI" read as two artifacts | Clarified: one binary (D-005/R-004), pinned release vs dev install |
| LOW | README mapping | E9 and P10 missing from epic-mapping table | Rows added (E9 partial via P11; P10 cross-cutting §13) |

## Disclosed open questions retained (not defects)

P2 Q1 (space.yaml has no normative §5 field table — permissive typing, owner:
P9/plan amendment) · P3 (AC-201.1 CC scope vs §5.5 V1 class — P10
cc-coverage reconciliation) · P6 (cache-before-submit seam — explicit stub
hook) · P8 OQ1–OQ3 (`note` scope on announcements, OP-211 `publish` vs
OP-212, publish+deprecate atomicity — plan-amendment candidates) · P12
(deprecation-category for non-contract channel; seomatrix contracts "as
available") · P14 Q-P14-A (AC-301.2 "every capability" narrowed to §7.7
enumeration — disclosed). These surface to the operator at the next handoff;
several are §17 plan-amendment candidates.

## Probe stats

5 probes (scout/sonnet), 14 specs + tracker + README + ADRs + plan cross-read;
fix wave: 7 agents, 9 files, every change amendment-logged. AC rows quoted
from §14 verified byte-identical throughout.
