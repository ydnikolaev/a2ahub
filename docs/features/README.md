# docs/features/ — the doc pipeline

One directory per feature or epic: `docs/features/<slug>/`.

## Layout

- **`README.md`** — the spec or epic overview. Its YAML frontmatter carries
  `kind: spec` (one self-contained unit of work) or `kind: epic` (a multi-phase
  unit of work broken into a tracker + phase specs). Scaffold from
  [`docs/_templates/feature/README.template.md`](../_templates/feature/README.template.md).
- **`tracker.yaml`** — required for `kind: epic` only. The machine-readable phase
  DAG: ids, statuses, `blocked_by` dependencies, `spec:` paths, `commits:`
  receipts. Scaffold from
  [`docs/_templates/feature/tracker.template.yaml`](../_templates/feature/tracker.template.yaml).
- **`specs/`** — one file per phase (epics) or the single detailed spec (a
  `kind: spec` feature that outgrows its README). Scaffold from
  [`docs/_templates/feature/spec.template.md`](../_templates/feature/spec.template.md).

## Conventions

- **slug** matches the directory name and the frontmatter `slug:` field.
- An epic slug carries a date suffix (`<name>-YYYY-MM`); the commit scope drops
  it (`feat(<name>): …`) unless the tracker declares `commit_scope:` explicitly.
- `status:` (feature) — `draft | active | shipped | superseded | archived`.
- `status:` (phase) — `pending | in-progress | done | deferred`.
- A phase `status: done` requires either a non-empty `commits:` list or
  `audit: n/a` (research/design phases that shipped no code).

## Lint gate

Every feature under this directory is linted by
[`scripts/check-feature-lint.sh`](../../scripts/check-feature-lint.sh) (`make
feature-lint`), and — once an epic has commits — cross-checked for doc/reality
drift by [`.agents/scripts/epic_docs_drift.sh`](../../.agents/scripts/epic_docs_drift.sh)
(`make epic-drift`).

Enforcement is a ratchet: a `README.md` whose frontmatter carries `kind:` is
enforced (lint errors fail `make check`); anything without it is grandfathered
and only reported as info. Every feature scaffolded from the templates above
carries `kind:` from creation, so the standard ratchets forward without a
retroactive rewrite of existing docs.
