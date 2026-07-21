<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- cites: [agent/commit-hygiene.md] -->

# Commit hygiene — one intent, this session's, in the project's convention

Always-on (you commit outside an explicit `/commit` too, so the reflex can't wait to be summoned). On every commit:

- **Stage only what this session touched.** Never `git add -A` / `.` / `*` — a blanket add sweeps in another terminal's, worktree's, or manual edit's files, mis-attributing work you can't vouch for. List the paths you changed, reconcile against `git status`, leave the rest.
- **Write the message in the project's convention.** `type(scope): subject` — imperative, bounded, machine-readable so changelog / semver / blame keep working; the body explains *why*, not what. The concrete scope, type, and trailer vocabulary is a project value — it lives in the project's own commit-convention rule, not here.
- **Keep it atomic.** One logical change per commit, so it reverts, bisects, and reviews as one decision; split a multi-intent tree at the intent boundary.

Full treatment: the commit-hygiene doctrine (`.mate/doctrine/`); the concrete scope, type, and trailer vocabulary lives in the project's own commit-convention rule.
