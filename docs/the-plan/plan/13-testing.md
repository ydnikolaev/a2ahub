# §13 Testing Strategy

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Fulfills R-016. Test tiers are named so §14 ACs and §12 CCs can cite them.

## 13.1 Tiers

| Tier | Name | Scope | Runs |
|---|---|---|---|
| T1 | schema goldens | every product schema × valid + invalid fixtures with expected machine-readable error codes (5.5) | product repo CI, every commit |
| T2 | fold determinism | lifecycle engine: every transition table row (legal), every illegal/unauthorized case, ordering, idempotent replay — property-based where possible (same events, any arrival grouping ⇒ same state) | product repo CI |
| T3 | binary integration | each OP-2xx against a local throwaway git space fixture; CLI/MCP parity suite (7.1) | product repo CI |
| T4 | hub integration | ingest pipeline against fixture repos: F-1…F-9 injected (missed/dup webhooks, force-push, corrupt DB, rebuild equivalence: rebuilt index ≡ incremental index) | product repo CI |
| T5 | multi-party e2e | simulated 3-system space (three actors, three credentials) in CI: full scenario scripts below | product repo CI, pre-release gate |
| T6 | space conformance | the CI workflow shipped into every space repo (V3): validates real content; its own correctness is covered by T1/T3 | every space, every push |

## 13.2 Coverage matrix (the grid)

The e2e matrix is generated, not hand-enumerated: dimensions × injections,
pruned to legal combinations, each cell an executable scenario.

- **Dimensions:** object type (8) × lifecycle path (happy + every declared
  side path from §3.4) × actor role (member agent, system owner, machine-RO)
  × surface (CLI, MCP).
- **Injections (orthogonal):** hub down / hub up; webhook lost / duplicated;
  stale cache; concurrent second actor; credential revoked mid-flow.
- **Exit criterion:** every transition-table row exercised ≥1; every CC-###
  in §12 mapped to ≥1 named test (traceability file `cc-coverage.yaml` in
  the product repo, CI-enforced: a CC without a test fails the build —
  this makes §12's append-only growth self-enforcing).

## 13.3 Named e2e scenarios (minimum set, T5)

| E2E | Scenario |
|---|---|
| E2E-1 | the north-star cascade (§1.3) end to end: requirement → ack → downstream requirement → contract version → response → response-verify → satisfy (requirements complete via `satisfy`, §3.4.2); assert every intermediate folded state and statusline signal |
| E2E-2 | question with decline + re-route to second system |
| E2E-3 | work_request(data) with partial responses, one dispute, human escalation stop (CC-027, CC-029) |
| E2E-4 | contract breaking change: G2 PR gate, deprecation announcement, consumer acks, blocked retire until acks (CC-081/082) |
| E2E-5 | decision with 3 required approvers incl. one reject → revision → approve |
| E2E-6 | handoff submit → verify-fail → resubmit → accepted (§16 evidence checked) |
| E2E-7 | hub killed mid-scenario and rebuilt; assert zero durable divergence (S-8) |
| E2E-8 | offboarding with open exchanges (CC-062) |
| E2E-9 | onboarding a new system by runbook only (S-6, scripted): manifest PR → hello-world status announcement → appears in inbox/graph |
| E2E-10 | federation: one system in two spaces; aggregated inbox; cross-space ref with access asymmetry (CC-070/073) |

## 13.4 Non-functional checks

- statusline render <100 ms from cache (7.5) — perf test in T3.
- V3 full-repo validation on a space at 10-system/2-year synthetic volume
  completes within CI norms (minutes, not tens of) — synthetic-volume
  fixture in T4.
- Secret-scan corpus (T1): known-pattern fixtures MUST block; benign
  lookalikes MUST pass (false-positive budget documented).
- Compat-check goldens (T1/T6): for json-schema contracts, fixture pairs
  proving the 5.4b rule — a mislabeled-minor breaking change MUST fail V3;
  a genuinely additive minor MUST pass (covers CC-080's real scenario).
- Sanitization fixtures (T1/T3): malicious titles/bodies/notes (script
  tags, raw HTML, control chars) MUST render inert on dashboard, local
  HTML, and statusline (10.8).

## 13.5 Fixtures policy

Appendix B examples are literally T1 fixtures (golden valid set) — the plan's
examples, the docs' examples, and the test fixtures are one set of files in
the product repo (no drift between docs and tests, R-004 for examples).
