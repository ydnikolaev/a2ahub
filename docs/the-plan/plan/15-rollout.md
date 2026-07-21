# §15 Rollout Plan

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Each phase ships standalone value and is an exit-gated increment. Epic
> mapping (E# from §14) is the seed for the implementation epic breakdown.

## V1-min re-cut (D-030, 2026-07-21)

The operator needs a WORKING TOOL now; everything cosmetic defers to v2 with
its foundation preserved in the spec (nothing below is removed from the
plan — only re-phased).

**v1-min = L0 + L1 + L2** with these L1/L4 items moved OUT to v2:
hub service (§6 entirely — statusline runs on the git-fallback path, 7.5,
which already satisfies S-3 at the 5-min TTL), dashboard (11.1), local HTML
(7.6 / OP-214), `a2a update` self-update (OP-217 — releases stay signed,
install is manual/brew), `doctor --space` host-drift audit, chat
notifications via hub (the optional space-CI Actions→Telegram fallback MAY
ship instead), and the full §13 matrix beyond the core tiers.

**v1-min keeps:** space template + PR funnel + V3 CI; the binary's core
(schemas, validator, fold, all lifecycle verbs, submit/batch, sync, inbox/
outbox/show/thread/search, contract publish/verify-export/diff, templates,
basic doctor, statusline); the **MCP surface (OP-216 / 7.7) as a TAIL item**
— built after the core is green, before v1 is declared done (thin façade
over the same core, ~1 day incl. the parity suite; never on the critical
path); the expert skill (minimal) + agent rules; L2 content/migration in
full; core tests (T1 goldens, T2 fold, slim T3, E2E-1 + E2E-4).

v2 then adds the deferred set on top with zero migrations (the hub was
optional-by-design, D-001/D-012; viz is a pure read layer). S-8's hub
clauses and AC rows touching deferred surfaces are re-verified when v2
ships them; the §14 epics E5/E8 and hub parts of E6 are v2 epics; E7 (MCP)
stays in v1 as the tail item above.

## Phase L0 — Space online (E1, part of E2)

Scope: product repo bootstrapped (schemas v1, space template); the getvisa
space created (`axon` + `seomatrix`); manifest, sections, CODEOWNERS, branch
protection; V3 CI in the space (validation via a pinned prebuilt validator
binary — the full CLI need not be complete).
Value shipped: a valid, protected, reviewable neutral ground exists.
Exit: AC-101.*, AC-102.1 green (hub clauses excluded — re-verified at L3);
branch protection verified to reject a direct push and to auto-merge an
ungated hello-world PR without human touch; a gated-path PR verified to
demand owner review; both teams committed a hello-world status
`announcement` via the PR funnel following a draft template.

## Phase L1 — Toolchain (E2, E3, E4)

Scope: `a2a` binary core — new/validate/submit/sync/inbox/show/thread +
lifecycle verbs + templates + doctor; distribution + `min_binary_version`.
Value: agents author and track exchanges without any service.
Exit: T1–T3 green; US-201/301/302/401 ACs pass (hub clauses excluded —
re-verified at L3); both teams' agents complete one real
question→response→verify→close loop entirely via the binary.

## Phase L2 — Day-one content & migration (E10)

Scope: `ingest` + `todo-feed` contracts published code-backed from axon
(D-007); seomatrix counterpart contracts as available; producer-outbox
triage: each legacy item → typed artifact or recorded drop (AC-1002.1);
chat-relay deprecation announcement.
NOTE: axon's existing CI contract-mirror (the `getvisa-ingest-contract`
repo publishing per-handle JSON Schemas + golden fixtures) is the
proto-version of `axon/provides/ingest/` — at L2 its content and export
CI mechanics fold INTO the space (one home), and the standalone mirror
repo is deprecated. The SSOT direction is unchanged throughout: axon's
code → export → space; the runtime `/api/v1/ingest/` never depends on
a2ahub (D-006, §5.3).
Value: the real pain closed — the md-relay era ends for the getvisa chain.
Exit: S-1, S-4, S-5 satisfied.
NOTE: runs partly in parallel with L1 (content work is human+agent, not
blocked on every CLI verb).

## Phase L3 — Hub, signals, adapters (E5, E6, E7)

Scope: hub deployed on the VPS (ingest pipeline, indexes, OP-1xx, chat
webhook); statusline provider + Claude Code wiring via mate; `a2ahub` expert
skill; MCP surface; Codex AGENTS snippet.
Value: proactivity — agents notice inbound work without humans (R-001).
Exit: T4 + E2E-1/2/7 green; US-501/502/601/602/701 ACs pass; S-3 measured.

## Phase L4 — Visibility & hardening (E8, E9, E11 + full matrix)

Scope: dashboard (graph + boards + contracts + health), local HTML; full
runbook set + succession note; handoff directive live (first real handoff);
full §13 matrix including E2E-3…10 and cc-coverage enforcement.
Value: the exchange is observable, operable, transferable — foundation
complete (R-013 "фундамент").
Exit: S-6, S-7, S-8; every CC mapped to a green test; §14 fully green.

## Coexistence & migration policy

- From L0, new cross-team items MUST start in the space (draft template even
  before the CLI); chat relay allowed only as a courtesy pointer to a space
  artifact.
- From L2 cutoff (the deprecation announcement), chat-relayed artifacts are
  protocol violations — politely bounced with the artifact's space ID.
- Legacy docs (producer-service.md and followups) stay in axon as internal
  docs; their externally-relevant content graduates into space contracts on
  the normal contract-owner loop (8.4) — no big-bang rewrite.

## Kill / adjust criteria

Reviewed at each phase exit: if agents bypass the space (items keep landing
in chat), diagnose whether friction is tooling (fix binary UX) or protocol
(revisit §3 with amendments) before building the next phase — the phases
deliberately front-load the pain-closing value (L2 before hub/dashboard) so
sunk cost stays minimal if course-correction is needed.

## Post-v1 horizon (explicitly out of v1)

Hub-write path + public mode (10.6), per-artifact encryption (Q), GitLab
profile (Q), SoT onboarding (runbook-driven when they're ready), open-source
release checklist (naming Q included).
