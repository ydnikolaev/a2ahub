<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). This index is maintained by `/mate-doctrine index` — regenerate it when a doctrine is added or its status changes. -->

# Doctrine corpus — index

The stack-agnostic doctrine SSOT. Every principle has a home here; skills and
gates *cite* these files, never re-describe them. Each doctrine follows the
canonical eight-section shape ([_authoring.md](_authoring.md)); the `doctrine-lint`
gate keeps the shape honest (authoring flows through `/mate-doctrine`). The corpus splits **`code/`**
(output quality) from **`agent/`** (how the agent works) per architecture spec §13d.

| Doctrine | What it governs | Status | Instantiated by |
|---|---|---|---|
| [_authoring.md](_authoring.md) | the canonical shape every doctrine follows (meta) | stable | the whole corpus |
| [code/validation.md](code/validation.md) | the machine-validation maniac — gates, teeth, prevention hierarchy | stable ⭐ | `/mate-validator`, `make check`, `mate registry lint` |
| [agent/verification-honesty.md](agent/verification-honesty.md) | verify before you conclude; claim only what you checked (the per-action half of validation) | stable | a project's always-on rule set, `.mate/config.yaml` seam |
| [agent/commit-hygiene.md](agent/commit-hygiene.md) | one intent, this session's, in the project's convention (session-isolation · conventional format · atomicity) | stable | a project's always-on rule set + commit-convention rule, the `commit` skill |
| [agent/harness-layers.md](agent/harness-layers.md) | whose surface you are touching — managed vs owned, classify-before-placing, byte-edit-upstream | stable | the `harness-discipline` always-on rule, `/mate-promote`, `/mate-adopt`, the drift gate |
| [code/framework-first.md](code/framework-first.md) | use the framework's canonical mechanism — custom code is debt | stable | `profiles/<stack>/` refinements, project ADR deviations |
| [code/interface.md](code/interface.md) | the uniform agent-facing interface — make-ABI + CLI scheme | stable | `/mate-adopt`, `make check`, project Makefiles |
| [code/env.md](code/env.md) | layered, modular, secret-safe configuration | stable | env schema dirs, `env-check` gates |
| [code/cli.md](code/cli.md) | portable operator-CLI principles (the how-to behind interface §2) | stable (neutralization pending) | the `mate` CLI, project operator CLIs |
| [code/cli-ux.md](code/cli-ux.md) | interactive CLI-UX — resolve-don't-ask, select-don't-type, human TUI + agent headless | stable | the `mate` wizard/prompts, `profiles/<stack>/` forms library |
| [code/registries.md](code/registries.md) | everything enumerable is a registry — probe>declare, render, lint | stable | `registry/*.yaml`, `mate registry lint` |
| [code/documentation.md](code/documentation.md) | render the machine, template the human — handbook + doc gates | stable | `handbook/`, `mate docs`, coverage/dead-link/drift gates |
| [code/structure.md](code/structure.md) | one project shape — standard homes + defaults, config-seam overrides, template-seeded never enforced (the fourth interface face) | stable | `paths:` config seam, `mate new` skeleton seeding, `/mate-adopt` docs-skeleton delta |

**Status legend:** *stable* = authored to the eight-section shape and lint-green ·
⭐ = foundational · *(note)* = a tracked follow-up (see the SSOT backlog, `docs/backlog.md`).

Topics written **by need, not all at once** (roadmap Phase 3): `code/` topics
`deploy`, `testing`, `harness-meta` and further `agent/` topics
(`advisor-before-substantive`, `context-economy`, … — spec §13d) land when the
first consumer needs them. Stack refinements live under
`profiles/<stack>/<topic>.md` and follow the same shape.
