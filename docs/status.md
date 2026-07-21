# Status

<!-- Seeded once by mate (structure standard: .mate/doctrine/code/structure.md).
     This file is YOURS: mate will never touch it again — edit, restructure, or
     delete it freely. Role: what is in flight / recently shipped — the first
     orientation read of any session. -->

## In flight

- **Epic [v1-min](features/v1-min-2026-07/README.md)** — space + `a2a` binary
  core + day-one content (plan §15 L0–L2 + E7 tail, D-030). Spec corpus cut
  from the plan 2026-07-21; waves 1–3 shipped 2026-07-21: P1 foundation,
  P2 schemas, P3 validation, P4 fold, P5 space/host, P6 author verbs +
  templates, P9 space-template + V3 CI + doctor. The `a2a` binary is wired
  and runs the authoring path end-to-end (init→new→validate→template);
  live GitHub submit is P11.
  <!-- epic-state: v1-min-2026-07 phases=7/14 -->

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
