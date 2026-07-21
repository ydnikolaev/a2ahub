# Go conventions — a2ahub (pointer)

**SSOT: [AGENTS.md](../../AGENTS.md) § "a2ahub engineering rails"** — the
project-owned block below the mate-managed reflexes. It carries the stack
table, architecture/ISP/DI rules, idempotency-by-design, error flow,
concurrency, config/secrets, security, the pre-flight checklist, the
anti-pattern table (20 rows), the testing rails (testscript e2e, golden
fixtures, `t.Parallel()`, async tiers, coverage floor 70%), and the
schemas/space-template discipline.

It lives in `AGENTS.md` so BOTH provider surfaces (Claude Code and codex)
load it; this file exists only so Claude-side references
(`.claude/agents/go-auditor.md`, skill briefs) resolve to a stable path.

Rule changes go through the surfaced-diff flow (teamlead S11 / explicit user
ask) against `AGENTS.md`, never against this pointer.
