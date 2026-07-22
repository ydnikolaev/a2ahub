# Status

<!-- Seeded once by mate (structure standard: .mate/doctrine/code/structure.md).
     This file is YOURS: mate will never touch it again — edit, restructure, or
     delete it freely. Role: what is in flight / recently shipped — the first
     orientation read of any session. -->

## In flight

- **Epic [v1-min](features/v1-min-2026-07/README.md)** — space + `a2a` binary
  core + day-one content (plan §15 L0–L2 + E7 tail, D-030). Spec corpus cut
  from the plan 2026-07-21; waves 1–4 shipped 2026-07-21: P1–P6 + P9
  (foundation, schemas, validation, fold, space/host, author verbs,
  space-template/CI/doctor) + P7 (read surface: cache + inbox/outbox/show/
  thread/search/statusline) + P8 (lifecycle + contract verbs) + P10
  (integration harness: T3 testscript + E2E-1/E2E-4 + cc-coverage). The
  `a2a` binary is wired for all 27 verbs; authoring + read paths run
  locally, live GitHub write path (submit/lifecycle/contract) is P11. P14
  (MCP façade `a2a mcp` + CLI/MCP parity + per-write-verb equivalence)
  shipped 2026-07-22.
  <!-- epic-state: v1-min-2026-07 phases=11/14 -->

- **Epic [publish-prep](features/publish-prep-2026-07/README.md)** — make
  `ydnikolaev/a2ahub` a public, hardened, cross-platform Go-binary repo
  modeled 1:1 on `ydnikolaev/sporo` (sporo-style publish boundary + goreleaser
  release + cosign/SBOM/SLSA + govulncheck/gitleaks/CodeQL). Built PRIVATE +
  reversible first (P1–P5); the one-way history re-root + public flip is P6,
  user-gated. Spec corpus cut 2026-07-22.
  <!-- epic-state: publish-prep-2026-07 phases=0/6 -->

- **Dev-pipeline harness bootstrap** (2026-07-21) — orchestration inventory ported
  from the axon harness: agents (`scout`, `coder`, `go-auditor`), skills
  (`/teamlead`, `/implement`, `/discover`, shared `cc-workflow.md` dispatch SSOT),
  project rules (`go-conventions`, `check-convention`, `commit-convention`),
  feature-doc templates, tracker gates (`feature-lint`, `epic-drift`,
  `parse_tracker`) and the make-ABI (`check` / `check-validators` / `harness-check`).
  Next: `git init`, then `/discover` to cut the v1-min epic from the plan
  (§14 US/AC + §15 L0–L2 phase set), then `/teamlead <epic-slug>`.

## Shipped

_nothing yet_
