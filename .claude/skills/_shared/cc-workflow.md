# Claude Code — Workflow-based dispatch (dynamic workflows)

SSOT for how a2ahub skills fan work out across sub-agents using the native
**`Workflow`** tool. Skills (`/teamlead`, `/implement`, `/discover`) link here
instead of duplicating the dispatch machinery.

> Ported from the axon harness (project-local there, project-local here).
> Adapted for a single-stack Go repo with sonnet-pinned `scout` / `coder` /
> `go-auditor` agents.

## Opt-in

The `Workflow` tool requires explicit user opt-in. **A skill whose instructions
tell you to use `Workflow` satisfies that opt-in** — invoking the skill *is* the
user's authorization. Inside these skills, author and run `Workflow` scripts
directly; do not re-ask.

## Load-bearing facts

(Verified 2026-05-28 in the axon repo on the same Claude Code harness. On the
FIRST fan-out in this repo, re-verify fact 1 with a cheap sentinel probe — one
no-isolation agent writing a repo-relative scratch file — before trusting it
for a code-wave.)

1. **No-isolation `Workflow` agents edit the main checkout filesystem
   directly.** A repo-relative path in their `Edit`/`Write` resolves against
   the repo root, *not* the agent's cwd. The lead sees every change via
   `git status` / `git diff` in main. → For file-disjoint code-waves there is
   **nothing to merge back**; the lead commits thematically.
2. **`agent()` returns structured data (schema) or final text — NOT the
   worktree path or branch.** An `isolation:"worktree"` agent's changes are
   addressable only if the agent commits to a branch and returns the ref.
3. **Concurrency cap = `min(16, cores − 2)`;** excess queues. Never promise
   hundreds of parallel agents.
4. **Schema validates shape, not truth.** An agent can return a perfectly
   formed schema that lies. Always diff the artifact / re-run the scoped tests
   yourself.

## Dispatch taxonomy — pick by what the agents *do*

| Mode | When | Mechanism | merge-back | Default? |
|---|---|---|---|---|
| **Read-only fan-out** | orient, audit, research, verify — agents READ and REPORT | `parallel()` / `pipeline()` + `schema` | none | ✅ for any read-only multi-probe |
| **Disjoint code-wave** | agents WRITE code to file-disjoint allowlists; no git; no repo-wide build | no isolation; briefs say "DO NOT commit / DO NOT run git"; return `{files, tests, deviations}`; **lead** does `git diff` + thematic commit | none — all in main | ✅ for parallel code work |
| **Isolated code-wave** *(escape-hatch)* | overlapping files, risky change needing atomic rollback, or an agent that needs a repo-wide build to self-verify | `isolation:"worktree"` + agent commits to a branch + returns the ref → lead cherry-picks | yes | only when disjoint+no-build is impossible |

**Decision rule:** read-only → fan-out · writes code + disjoint + no repo-wide
build → no-isolation, lead commits · otherwise → escape-hatch isolation.

## Verification discipline (NON-NEGOTIABLE)

- **Schema = shape, not truth. The summary is sales copy.** After every
  code-wave: `git diff <allowlist>` and re-run the scoped tests yourself
  (`go test ./internal/<pkg>/... -race -count=1 -v` — `t.Skip` is invisible
  without `-v`). For findings, adversarially verify before acting.

## Budget

Honor a declared `budget_cap` with the `budget` global:

```js
while (budget.total && budget.remaining() > 60_000) { /* one more round */ }
const FLEET = budget.total ? Math.floor(budget.total / 100_000) : 5;
```

`budget.total` is `null` when no `+Nk` target was set → guard loops on
`budget.total`.

## Delegate-or-inline gate (decide BEFORE you spawn anything)

A fresh sub-agent pays a cold-start of ~15–30k input tokens — but it is
**one-time, sonnet-priced, and lands OUT of the lead's context** (only a ~1k
structured result returns). **Inline work is the opposite of free**: file
reads + edits + tool output land permanently in the lead's context and are
re-processed every subsequent turn. True cost of inline ≈
`marginal_tokens × remaining_turns × lead_rate`; of delegating ≈
`cold_start × sonnet_rate`, once.

| Task shape | Do this |
|---|---|
| **One** genuinely trivial edit (~1–3 lines, file already loaded) | Lead INLINE |
| **Many** small edits — even trivial, even disjoint | **Batch into ONE delegated wave**, do NOT drip inline |
| Self-contained chunk ≳ a file of real work, or parallel disjoint chunks | Delegate to a pinned worker, tiered per task |
| **Hard / architecture-dense** work | Delegate with a SHARPER brief + auditor recheck — NOT inline, NOT automatically a bigger model |

The lead's job is **decide placement → write the brief → review the diff**,
almost never to implement.

## Model tiering — per-task tier, enforced with pinned agents (CRITICAL)

`agent()` with no `agentType` falls through to the built-in
`workflow-subagent`; `Explore` and `general-purpose` have no `model:`
frontmatter. All three **inherit the lead's session model** — on an expensive
lead every such sub-agent runs at the lead's rate. A call-site
`model:'sonnet'` fixes this only if the lead never forgets it. **The enforced
mechanism is the agent definition's `model:` frontmatter** — route every
fan-out through a pinned agent via `agentType`.

| Fan-out kind | `agentType` | Pinned model | Call-site `model`? |
|---|---|---|---|
| Read-only orient / research / sweep / coherence | `scout` | sonnet | omit |
| Disjoint code-wave | `coder` | sonnet (floor) | only to escalate (`opus`, rare) or downshift (`haiku` for codemods) |
| Audit | `go-auditor` | sonnet | omit |

The only thing that should run on the lead's model is the lead.

## Concurrent-build caveat — the rule is SOLE WRITER, not cost

A fan-out agent may not run a repo-wide gate because **a sibling is writing
the same checkout** — its lint/test would read another agent's half-written
file and red for a reason that is not its own.

| Actor | May run `make check` / `make check-validators`? |
|---|---|
| Fan-out agent (a sibling is live) | **NO** — scoped self-verify only (`go test ./internal/<pkg>/...`) |
| Solo agent (`/implement`, `/quick-fix`, single `Agent()` with no siblings) | YES — it IS the sole writer |
| The lead, between waves | YES — this is the point |

Lane choice at a wave boundary: docs/config/scripts-only wave →
`make check-validators`; any `.go` touched → **the ceiling, `make check`**.
The epic/spec closeout always runs the ceiling.

## Concurrent-session caveat

Another session may be editing the main checkout. Commit isolation survives
(explicit-path staging), but the lead's `git diff` will show their changes and
a wave-end gate can fail on *their* half-finished work. Before a code-wave:
glance at `git status`; a foreign mid-edit in your wave's area → wait or take
the isolated escape-hatch.

## Resumability

- Durable resume points are the **lead's per-wave commits** — commit after
  each wave.
- `Workflow({scriptPath, resumeFromRunId})` re-runs the longest unchanged
  prefix from cache; use it to iterate on a script without re-spending.

## Anti-patterns

- Spinning a Workflow for work that isn't a fan-out (a linear 3-step feature
  is cheaper via `/implement`).
- Committing from inside a workflow agent — the lead commits.
- Trusting a schema return as proof of work.
- `isolation:"worktree"` "to be safe" on disjoint waves.
- Repo-wide build/test inside a fan-out agent.
- Dispatching `agent()` without `agentType`.
- Forking (`Agent` without subagent_type) for delegated work — a fork copies
  the lead's entire context AND inherits its model.

## Script templates

### A — read-only fan-out (orient / audit / research)

```js
export const meta = { name: 'orient', description: '...', phases: [{ title: 'Probe' }] };
const SCHEMA = {
  type: 'object',
  properties: { findings: { type: 'array', items: { type: 'object' } }, summary: { type: 'string' } },
  required: ['findings', 'summary'], additionalProperties: false,
};
const PROBES = [
  { key: 'spec',       prompt: 'Read the plan/spec corpus for <areas>: stable IDs (R/US/AC/CC/D), owning decisions, open questions. Report findings + summary.' },
  { key: 'state',      prompt: 'Current code in <areas>; what is partially implemented; nearby tests + fixtures. Report findings + summary.' },
  { key: 'collisions', prompt: 'Other open specs in docs/features/, recent commits on the same files, deferred items. Report findings + summary.' },
];
const results = (await parallel(PROBES.map(p =>
  () => agent(p.prompt, { label: `probe:${p.key}`, agentType: 'scout', schema: SCHEMA })))).filter(Boolean);
return results;
```

### B — pipeline find → fix → verify (campaign, loop-until-dry)

```js
const seen = new Set(), done = [];
let dry = 0;
const key = f => `${f.file}:${f.rule}`;
while (dry < 2 && (!budget.total || budget.remaining() > 60_000)) {
  const found = (await parallel(FINDERS.map(f =>
    () => agent(f.prompt, { phase: 'Find', agentType: 'scout', schema: FINDINGS }))))
    .filter(Boolean).flatMap(r => r.findings);
  const fresh = found.filter(f => !seen.has(key(f)));
  if (!fresh.length) { dry++; continue; }
  dry = 0; fresh.forEach(f => seen.add(key(f)));
  const fixed = await pipeline(fresh,
    f       => agent(fixPrompt(f),    { phase: 'Fix',    agentType: 'coder', schema: FIX }),
    (fx, f) => agent(verifyPrompt(f), { phase: 'Verify', agentType: 'scout', schema: VERDICT })
                 .then(v => ({ ...f, ...fx, verdict: v })));
  done.push(...fixed.filter(Boolean));
}
return done;   // lead: git diff, commit thematically, run make check
```

### C — disjoint code-wave (no isolation, lead commits)

```js
const WAVE = [ { files: ['internal/validate/codes.go'], brief: '...' }, /* disjoint allowlists */ ];
const RESULT = {
  type: 'object',
  properties: {
    files_modified: { type: 'array', items: { type: 'string' } },
    tests_added:    { type: 'array', items: { type: 'string' } },
    scoped_test_output: { type: 'string' },
    deviations: { type: 'string' },
    skipped: { type: 'string' },
  },
  required: ['files_modified', 'tests_added', 'scoped_test_output', 'deviations'],
  additionalProperties: false,
};
const out = (await parallel(WAVE.map(w => () => agent(
  `${w.brief}

Allowed files (REPO-RELATIVE — resolve against repo root): ${w.files.join(', ')}
Scope any test command to your own package only.`,
  { label: `wave:${w.files[0]}`, agentType: 'coder', model: w.model, schema: RESULT })))).filter(Boolean);
return out;   // lead: git diff each allowlist, commit thematically, then make check
```

### D — escape-hatch isolated code-wave (commit-to-branch)

```js
const BRANCH_RESULT = {
  type: 'object',
  properties: { branch: { type: 'string' }, head: { type: 'string' }, files: { type: 'array', items: { type: 'string' } } },
  required: ['branch', 'head', 'files'], additionalProperties: false,
};
const out = (await parallel(TASKS.map(t => () => agent(
  `${t.brief}

When done: \`git checkout -b ${t.branch}\`, stage ONLY your files, commit with message "${t.msg}",
then run \`git branch --show-current\` and \`git rev-parse HEAD\` and report branch + head + files.`,
  { isolation: 'worktree', agentType: 'coder', label: t.branch, schema: BRANCH_RESULT })))).filter(Boolean);
return out;   // lead: git cherry-pick <head> per result, then re-group / re-commit thematically
```

## Skill checklist (before authoring a Workflow inside a skill)

1. Classify each fan-out: **read-only / disjoint-code / isolated-code**.
2. **Schema** every agent return (shape) — but **verify the artifact** (truth).
3. Briefs: repo-relative paths; "DO NOT commit / run git / repo-wide build"; scoped self-verify.
4. **Lead commits thematically from main** after the workflow returns.
5. Honor `budget_cap` via the `budget` global.
6. Run `make check` **lead-side** after the wave — never inside the workflow.
