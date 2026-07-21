# Commit convention — a2ahub

Project value file behind the mate-synced commit-hygiene reflex: the concrete
type/scope vocabulary. Format: `type(scope): subject` — imperative, ≤72 chars,
no period; body says *why*.

## Types

`feat | fix | refactor | docs | chore | test | perf | ci | build | audit`

## Scopes (priority order — pick the most specific that applies)

1. **Epic slug minus date suffix** — e.g. `feat(v1-min): …` for work filed
   under `docs/features/v1-min-YYYY-MM/`. This is load-bearing: the
   `epic-drift` gate matches commits to trackers by this derived scope.
2. **Area**: `cli` (the `a2a` binary), `schemas` (envelope/frontmatter/contract
   schemas + templates + fixtures), `space` (space template, CI workflow,
   CODEOWNERS), `hub` (v2), `docs`, `harness` (`.claude/**`, `.agents/**`,
   gate scripts, Makefile).
3. Omit the scope only for repo-wide chores.

## Session isolation (CRITICAL)

Build the staged file list from THIS session's own Write/Edit/Bash history and
reconcile against `git status`. Never `git add -A` / `.` / `*`. One logical
change per commit; split multi-intent trees at the intent boundary.
