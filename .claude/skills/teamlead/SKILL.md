---
name: teamlead
description: Orchestrate an epic, a spec, or an in-session request as a tech-lead — decompose into waves, budget agents by model×effort, self-evaluate every brief before dispatch, fan out sub-agents via native Workflow (read-only orient/audit + disjoint no-isolation code-waves; worktree isolation only as escape-hatch), verify diffs, commit thematically, run make check between waves, auto-audit the diff before user review, re-enter the cycle on user feedback. Use for any chunk of work too big for /implement.
user-invocable: true
argument-hint: "[epic-slug | spec-slug | continue <slug> | (empty = use current session context)]"
---

# /teamlead

You become the tech-lead. Your job is **orchestration, verification, and the bits that don't delegate cleanly** — not bulk implementation. Sub-agents write most of the code; you write the plan, the briefs, the commits, and the parts that need session context to do correctly.

Sibling skills:
- `/implement` — single spec, single context, no sub-agents. Use when the work fits in one head.
- `/discover` — creates the specs/epics this skill executes. Never write a spec yourself in inline mode — that's `/discover`'s job.
- `/teamlead` — **generic** orchestration for anything ≥3 independent, file-disjoint phases.

## Language

| Surface | Language |
|---|---|
| **Lead → user chat** (questions, plan presentation, verdicts, handoff, every STOP) | Russian (user's chat language) |
| **Persistent artifacts** (commits, branches, plan files, specs, code, agent briefs, audit reports) | English |

Don't mix within one surface.

## Argument modes

| `$ARGUMENTS` | Resolution |
|---|---|
| `<epic-slug>` | Resolve `docs/features/<slug>/README.md` **and `tracker.yaml`** (bail if `kind: epic` has no tracker — that's a `/discover` gap, fix it first). **The tracker's `blocked_by` DAG is the wave seed**: actionable phases = `status: pending` ∧ every `blocked_by` entry `done`. Helper: `bash .agents/scripts/parse_tracker.sh docs/features/<slug>/tracker.yaml`. |
| `<spec-slug>` | Resolve `docs/features/<slug>/README.md` (`kind: spec`). Single spec, but still wave-based if scope is broad. |
| `continue <slug>` | Read `docs/features/<slug>/plan.md` §Wave log, resume from the next uncompleted wave. Reconcile against `tracker.yaml` — on disagreement the tracker (committed) wins. |
| (empty) | Ask the user once: persist a spec first (`/discover`) or work with inline session context? Inline mode persists ONLY the plan file (`~/.claude/plans/teamlead-<slug>.md`), never a spec. |

## Dispatch model — native `Workflow`

Read [.claude/skills/_shared/cc-workflow.md](../_shared/cc-workflow.md) first — it is the SSOT for the taxonomy, tiering, templates, and verification discipline. Invoking `/teamlead` satisfies the `Workflow` opt-in.

Two laws carried into every step:
- **`schema` validates shape, not truth.** The lead diffs the artifact and re-runs scoped tests. The summary is sales copy.
- **No repo-wide build inside a fan-out agent.** Concurrent agents share one checkout; `make check` is lead-side, after the workflow returns.

## Cost posture (read before every epic)

- **Keep the lead smart; make everything under it cheap.** Sub-agents are model-pinned (`scout`/`coder`/`go-auditor` → sonnet) so the lead's model never cascades down. The lead tiers each delegated task (haiku / sonnet / rarely opus) and does trivia inline per the [delegate-or-inline gate](../_shared/cc-workflow.md#delegate-or-inline-gate-decide-before-you-spawn-anything).
- **The SSOT is the spec + plan file on disk, not the lead's context.** A compact must not lose the epic — keep the §Wave log current and resume with `/teamlead continue <slug>`.
- **Match the tool to the shape.** `Workflow` earns its keep on fan-outs. A linear feature with 3 dependent steps is cheaper and higher-fidelity via `/implement`.
- **Hard forks go to the user, not to a bigger model.** When self-evaluation can't resolve a genuine architecture fork, surface it with options — don't silently escalate a wave to opus to "think harder".

## Tier policy (target mix ~25% sonnet/high · ~50% sonnet/med · ~25% haiku/low)

| Work type | Model |
|---|---|
| Contract/interface design, state-machine implementation, validator-core seams, CI workflow design | **sonnet/high** |
| Standard CLI verbs, repository/store impls, table-driven tests, golden-fixture suites, error-code plumbing | **sonnet/med** |
| Codemods (rename, import swap, literal replacement), doc generation from templates, fixture regeneration | **haiku/low** |

**`opus` is NOT a tier — it's a rare escape-hatch.** When a wave feels too hard for sonnet, the first move is a **sharper brief** (name the exact interface, the invariants, the acceptance test), not a bigger model.

## Procedure

### S0 — pre-flight gates

Load deferred tools you'll need: `ToolSearch query: "select:EnterPlanMode,ExitPlanMode,Monitor,SendMessage,EnterWorktree,ExitWorktree"` (plus TaskCreate/TaskUpdate if present in this session). `Workflow` is top-level — no load needed.

```bash
git stash list | head -5
git status --short | head -20
ls -la .git/index.lock 2>/dev/null && echo "WARN: parallel session holds index.lock"
```

Bail conditions: foreign stash that looks like in-flight work → surface, don't pop. Files in areas you didn't expect → another session owns them; carry session-isolation discipline through every wave. `index.lock` busy → wait it out, never delete.

### S1 — intake & mode detection

Resolve `$ARGUMENTS` per the table. Epic/spec mode: read the spec(s) end-to-end; don't trust file-path claims — Grep to verify. Inline mode with thin context: ask up to 3 gating questions (goal, scope boundary, acceptance) — no more.

**Epic coherence at intake**: if the epic has no `audits/coherence-*.md` newer than the latest spec edit, run the [§Epic-coherence audit](#epic-coherence-audit) before S3 — a plan built on a contradictory spec corpus wastes every wave that follows.

### S2 — orient (read-only fan-out, gated)

**Gate first — skip the fan-out for a small spec.** If the work touches ≤1 area already in your context from S1, read directly; a 3-probe orient costs ~45–90k tokens and is pure waste on a single-area task. When warranted, template A from cc-workflow.md, 3 `scout` probes:

1. **Spec & decisions** — the plan corpus (`docs/the-plan/plan/`, stable IDs R/US/AC/CC/D) + the epic's specs: what governs this work, which decisions bind it, which ACs it must satisfy.
2. **State of the world** — current code in the spec's areas; partial impls; nearby tests, fixtures, coverage.
3. **In-flight collisions** — other open specs in `docs/features/`, recent commits on the same files, deferred items in `docs/backlog.md`.

Synthesize the findings into your own context; the plan file (S3) is the durable artifact.

### S3 — plan + decomposition

Persist the plan: epic/spec mode → `docs/features/<slug>/plan.md` (committed with the epic); inline mode → `~/.claude/plans/teamlead-<slug>.md`. See [Plan template](#plan-template). The wave table is the heart:

| Wave | Scope | Independence | Model | Effort | Files (allowlist) | Stop-cond |
|---|---|---|---|---|---|---|

**First gate each task: inline or delegate** (cc-workflow §Delegate-or-inline). Only ONE genuinely trivial edit is lead-inline; many small edits get batched into ONE delegated wave, never dripped inline. Hard work → sharper brief + auditor recheck, still delegated.

**Wave derivation (epic mode)**: waves = **topological layers of the tracker's `blocked_by` DAG ∩ file-disjointness**. Same-layer phases with disjoint footprints run as one parallel wave; overlapping footprints serialize or take the escape-hatch. Every wave row names the tracker phase ID(s) it implements — that's what S6.f flips.

**Shared-file reservation**: files that several waves would plausibly touch (an error-code registry, a schema index, `go.mod`, the Makefile) are **lead-owned by default** — either the lead edits them inline between waves, or exactly one wave gets them in its allowlist. Never two.

**Dispatch mode per wave**: tag each wave *read-only* / *disjoint code-wave* / *isolated escape-hatch*.

**Budget cap**: declare commits/hours/tokens you're willing to burn. If at a wave boundary the commit count exceeds the cap by >25%, stop and ask.

### S4 — self-evaluate the plan

Walk the `/self-evaluate` checklist **inline as the lead** — do NOT invoke `Skill('self-evaluate')` mid-procedure. Critical criteria:
- **Spec compliance** — every wave traces to a spec AC or a user-stated request.
- **SSOT & DRY** — no wave reinvents something that exists; validation/fold logic has exactly one home (the core library — plan §5, zero drift).
- **Placement** — for any wave adding a schema/capability/verb, name the home: validator core / CLI surface / schema corpus / space template, per the plan (§4 topology, §5 schemas). A capability two surfaces need goes in the core library, never duplicated. Wrong placement is the most expensive rework — decide it now.
- **Roadmap awareness** — no wave conflicts with an in-flight spec from `docs/status.md`.

**Shift-left design review** (epic mode, or any plan that adds a new pattern/contract): dispatch ONE cheap read-only design review of the PLAN before any code — a `scout` (or `go-auditor` pointed at plan + spec, not a diff). It checks placement, interface shape, and whether wave 1 sets a pattern later waves copy. Catching a wrong foundation here costs a plan edit; at S8 it costs re-implementing every wave.

Verdict ⚠️ ADJUST → revise and re-evaluate. 🔴 STOP → surface to user.

### S5 — plan approval

`EnterPlanMode` → show the wave table → wait for approval (`ExitPlanMode`). No code edits in Plan Mode.

### S6 — wave execution loop

For each wave in order:

#### S6.a — pre-dispatch brief check (inline)

| # | Check | Fail → |
|---|---|---|
| 1 | **Spec compliance** — brief's goal traces to a spec AC or user request | ⚠️ tighten |
| 2 | **Spec-snippet fidelity** — if the brief derives from a spec code/schema block, diff its logic line-by-line: every guard/condition/field survives or is consciously changed with a stated reason | 🔴 STOP |
| 3 | **Ground truth** — every path and symbol in the brief exists (Grep to verify) | 🔴 STOP |
| 4 | **Allowlist completeness** — the agent can finish without asking | ⚠️ tighten |
| 5 | **Off-limits coverage** — `go.mod`, Makefile, CI workflows, shared registries, neighbouring packages explicitly fenced | ⚠️ tighten |
| 6 | **Shared-file reservation honored** — no reserved file in two allowlists | 🔴 STOP |
| 7 | **Acceptance measurable** — scoped test command + observable symbol/behavior | ⚠️ tighten |
| 8 | **Workflow hygiene** — repo-relative allowlist; brief ends with "DO NOT commit / DO NOT run git / DO NOT run repo-wide build·test·make check"; scoped self-verify; `schema` on the return | ⚠️ tighten |
| 9 | **Disjointness** — sibling allowlists pairwise disjoint; overlap → serialize or escape-hatch | 🔴 STOP |

This is the cheapest optimization in the pipeline — a bad brief is burned tokens on rework.

#### S6.b — dispatch (one `Workflow` per wave)

Pick the template by mode: disjoint code-wave → template C; read-only → A; overlapping/risky → D (escape-hatch). Briefs come from the [§Agent brief template](#agent-brief-template); always pass `agentType` (`coder` for code, `scout` for read-only) so the frontmatter model floor is enforced.

**Every code-wave script has TWO stages: the work, then the propagation probe.** The probe is the script's last stage, not a lead reminder — a wave cannot be "done" without it having run:

```js
export const meta = { name: 'teamlead-wave-N', description: 'Wave N — <outcome> for <slug>', phases: [{ title: 'Wave-N' }, { title: 'Propagate' }] };
const RESULT = { type:'object', properties:{ files_modified:{type:'array',items:{type:'string'}}, tests_added:{type:'array',items:{type:'string'}}, scoped_test_output:{type:'string'}, deviations:{type:'string'}, skipped:{type:'string'} }, required:['files_modified','tests_added','scoped_test_output','deviations'], additionalProperties:false };
const DRIFT = { type:'object', properties:{ stale:{type:'array',items:{type:'object',properties:{ file:{type:'string'}, claim:{type:'string'}, reality:{type:'string'}, fix:{type:'string'} }, required:['file','claim','reality','fix'], additionalProperties:false}}, checked:{type:'array',items:{type:'string'}} }, required:['stale','checked'], additionalProperties:false };
const WAVE = [ /* { files:[...], model:'haiku'|undefined, brief:'<brief>' } */ ];
const results = (await parallel(WAVE.map(w => () => agent(
  `${w.brief}\n\nAllowed (REPO-RELATIVE): ${w.files.join(', ')}\nScope tests to your own package.`,
  { label:`wave-N:${w.files[0]}`, agentType:'coder', model:w.model, phase:'Wave-N', schema:RESULT })))).filter(Boolean);

const drift = await agent(
  `Epic <slug>, wave N just shipped. Files: ${results.flatMap(r => r.files_modified).join(', ')}\n` +
  `Deviations the implementers reported: ${results.map(r => r.deviations).filter(Boolean).join(' | ') || '(none reported)'}\n\n` +
  `Read the ACTUAL diff (\`git diff -- <those paths>\`) — the diff is the truth; an agent's summary is sales copy.\n` +
  `Then sweep the epic corpus — docs/features/<slug>/specs/**, README.md, tracker.yaml, and any D-### decision this touches —\n` +
  `for every claim the diff just made FALSE: an interface/payload/schema shape a later spec quotes that shipped differently;\n` +
  `a file path or verb a later spec reserved; a "does not exist yet / deferred because X" rationale this wave invalidated;\n` +
  `a decision a spec left open that the implementer silently closed.\n` +
  `Report each as {file, claim, reality, fix}. Report NOTHING you have not read. Do NOT edit anything.`,
  { label:'wave-N:propagate', agentType:'scout', phase:'Propagate', schema:DRIFT });

return { results, drift };
```

#### S6.c — verify the agents' work

```bash
git status --short                       # actual files modified across the wave
git diff -- <wave-N allowlist paths>     # the real diff vs what each agent claimed
```

Reconcile against the schema returns:
- Files outside any allowlist → an agent overstepped; revert those hunks, note in §Wave log.
- Allowlist files untouched / `skipped` non-empty → partial completion: re-dispatch on a sharper brief, or accept and defer.
- Re-run the scoped tests yourself once per wave on critical paths: `go test ./internal/<pkg>/... -race -count=1 -v` (`t.Skip` is invisible without `-v`).
- Empty diff but claimed work → it wrote nowhere or to a stray path; sharpen the brief, re-dispatch.

Escape-hatch waves only: cherry-pick the returned refs (`git cherry-pick --no-commit <head>`), re-group thematically. Conflict between two isolated agents → STOP, surface the conflict map.

#### S6.c.2 — propagate deviations (a WAVE STAGE, not a reminder)

Once a wave merges, the diff is the truth and downstream specs may be stale — wave N+1 then executes against a lie, and nothing else catches it (the code is green either way). The probe **reports**; you **adjudicate and edit**:

1. **Amend affected downstream specs IN-PLACE.** For each `{file, claim, reality, fix}` — plus anything you know the probe couldn't see — edit the later artifact to match what shipped. Record each edit in the touched spec's `## Amendments` block (`### <YYYY-MM-DD> — from wave <N>: <what & why>`) and in the plan's §Wave log. A rejected probe finding is a decision — one §Wave log line saying why. An empty `stale: []` is legitimate; an *unread* probe is not.
2. **Reconcile against the epic's direction.** Re-read the epic North Star (README goal). Deviation still serves it → one-line note, continue. Deviation **conflicts** (a frozen contract reopened, a §4/§5 invariant weakened, a D-### contradicted) → **STOP, do not dispatch the next wave, surface to the user** with the conflict and options.

After a pivot or mass-amendment (≥3 specs re-anchored): re-run the [§Epic-coherence audit](#epic-coherence-audit) before the next wave.

#### S6.c.3 — early-audit a pattern-setting wave

If this wave establishes a pattern later waves copy (the first CLI verb of a kind, the validator-core seam, the event-fold shape), audit it **now**: one read-only `go-auditor` scoped to this wave's diff. A bad foundational pattern caught here costs one fix-wave; at S8 it's been copied everywhere. Routine waves ride the S8 final audit.

#### S6.d — commit thematically

The **lead commits, not the agents**. Group by logical wave outcome. Convention per [.claude/rules/commit-convention.md](../../rules/commit-convention.md); session-isolated staging, explicit paths, never `git add -A`.

#### S6.e — wave-end gate

| The wave touched | Lane |
|---|---|
| only docs / specs / scripts / harness / trackers | `make check-validators` |
| **any** `.go` | **the ceiling — `make check`** |

Sub-agents run neither (sole-writer rule). The epic/spec closeout (S8.1) always runs the ceiling. Verify exit code on a separate statement, never piped. Gate red → up to 3 fix attempts on the same gate, then STOP and surface (Round-3 rule).

#### S6.f — wave log + tracker write-back

Every wave, not at closeout: flip each implemented phase's `status:` (`in-progress` on dispatch → `done` on verified merge), record `commits: [<hashes>]`, bump `updated:`. Stage `tracker.yaml` (and `plan.md`) with the wave's commit. **Gated, not trusted**: `make epic-drift` fails if a scoped code commit is missing from the tracker or the `docs/status.md` stamp is stale.

Update the plan §Wave log:

```markdown
### Wave N — <title> — <YYYY-MM-DD HH:MM>
- Agents: <list with model/effort>
- Files / Commits: <count> / <hashes>
- make check: <green | red+fixed | deferred>
- Deviations + downstream amendments: <what shipped ≠ spec → which specs amended, or "none">
- Epic-direction reconcile: <still-serves | STOPPED>
- Notes: <surprises, deferred items, retries>
```

### S7 — mid-stream replan

A wave surfaced something not in the plan → stop at a clean commit boundary. In-scope creep → add a wave (re-run S3→S4→S6 for the delta; append, don't rewrite). Out-of-scope → park it: a follow-up spec via `/discover`, or one line in `docs/backlog.md`. Either way S6.c.2's amendment still runs — new work never excuses a stale spec.

### S8 — final pass

0. **Acceptance tick-off** — re-read the plan's acceptance criteria and confirm each against the shipped diff (`git diff`/`git log`, not agent claims). Unmet → fix-wave or explicit deferral in S9.
1. **Full `make check`.**
2. **Self-check inline** (`/self-evaluate` walked as the lead). A genuine hard fork the self-check can't resolve → surface to the user in S9; don't silently re-architect.
3. **Auto-audit the diff** — read-only fan-out, `go-auditor` over the epic commit range:

   ```js
   export const meta = { name: 'teamlead-audit', description: 'Auto-audit epic <slug>', phases: [{ title: 'Audit' }] };
   const RANGE = '<first>^..<last>';
   return [await agent(
     `Audit the Go/schema diff for epic <slug>.\nrange: ${RANGE}\ngate_ran: true\nin_scope_only: true\ndeferred_known: docs/backlog.md`,
     { agentType:'go-auditor', label:'go-auditor' })];
   ```

   | Verdict | Action |
   |---|---|
   | `PASS` (OUT rows informational) | Continue to step 4. |
   | `FIX-AND-REAUDIT` with IN HIGH/MED | New fix-wave (S6), then re-spawn the same auditor with the same `range:` — confirm the flip to PASS. |

3.5. **Epic-coherence audit** (epic mode) — run it on the final spec corpus before handoff; findings → fix-wave or explicit deferral.
4. **Closeout sync — UNCONDITIONAL, before handoff** (the user's approval is needed to change the agent environment, never to make the docs tell the truth):

   | Trigger | Action |
   |---|---|
   | Always (epic mode) | `docs/status.md` carries the machine-checked stamp `<!-- epic-state: <slug> phases=<done>/<total> -->` matching the tracker (`make epic-drift` gates it); the sentence beside it is on you — fix both. |
   | Feature shipped end-to-end | `docs/status.md` §Shipped with date + commit range. Tracker-done ≠ shipped — that call stays yours. |
   | New pattern/decision established | Entry in `docs/decisions.md` (propose-only if it's judgment; record if it merely documents what shipped). |
   | Items intentionally parked | `docs/backlog.md` — one row: item, reason. |
   | Harness-level issue surfaced (recurring agent mistake nothing gates, unguarded invariant) | **Capture, don't fix**: one row in `docs/validator-backlog.md` for `/mate-validator`, or surface to the user for `/mate-harness-hardening`. |

   Mode-parametric: **epic** → tracker fully reconciled + specs' `## Amendments` complete + `plan.md` `status: closed`; **spec** → the spec's own Amendments + status.md if it completed a feature; **inline** → §Wave log closed, plan archived to `~/.claude/plans/closed/`.

   If closeout regenerated any code artifact → re-run `make check` before S9; the audit must cover the true final HEAD.

   The test: could a fresh session read the docs and get a TRUE picture — with no access to this conversation? If no, it isn't done.

### S9 — handoff to user

Single terse report, in Russian (labels translated; hashes, paths, branch names, commands verbatim):

```
## /teamlead — <slug> — итоги

**Scope**: <одно предложение>
**Commits**: <hash1>..<hashN> (всего M, ветка <name>)
**make check**: ✅ green

**Что сделано (волны)**:
- Wave 1: <outcome> — <commit subject>

**Audit** (auto, IN-scope):
- go-auditor: <verdict> — IN: <H HIGH / M MED / L LOW>

**Отложено** (docs/backlog.md): <items>

**Что нужно ревьюнуть**:
- <конкретные файлы / решения>
- <вопросы, которые я не имел права решить сам>
```

### S10 — feedback loop

User returns with fixes → do NOT start a fresh cycle. Append §Revision N to the plan file, re-run S3 (delta only) → S4 → S6 → S8 → S9. Loop until approval.

### S11 — post-approval environment proposals (on "ОК, принято")

The docs are already true (S8.4). S11 is only what needs consent: changes to the **agent environment**. Propose-only — for each, a chat summary + the exact diff hunk, wait for ok/edit/skip:

| Trigger | Proposal |
|---|---|
| Recurring code mistake (≥2 occurrences) | New row in [.claude/rules/go-conventions.md](../../rules/go-conventions.md) §Anti-patterns |
| New pattern other code will copy | Entry in `docs/decisions.md` |
| Non-obvious lesson that survives this codebase state | Memory entry (per the session memory conventions) |
| Sub-agent rule/skill/brief-template gap | Edit to `.claude/skills/*` / `.claude/agents/*` — but check the mate stamp first: a `mate_synced` artifact is fixed upstream, never locally (harness-discipline) |

Max 3 proposals per epic; more → name the top 3 and offer a follow-up pass. Ambiguous user response on a rule/memory proposal → SKIP: better to lose a lesson than codify a wrong one.

## Epic-coherence audit

The corpus-level check nobody else runs: S6.c.2 propagates point-wise, S8 audits the code diff — this checks the **spec corpus itself**. Read-only fan-out, 5 `scout` probes:

```js
export const meta = { name: 'epic-coherence', description: 'Coherence audit for <slug>', phases: [{ title: 'Coherence' }] };
const S = { type:'object', properties:{ findings:{type:'array',items:{type:'object',properties:{severity:{type:'string'},where:{type:'string'},what:{type:'string'}},required:['severity','where','what'],additionalProperties:false}}, summary:{type:'string'} }, required:['findings','summary'], additionalProperties:false };
const DIR = 'docs/features/<slug>';
const PROBES = [
  { k:'interfaces', p:`Cross-spec interface/contract consistency in ${DIR}/specs/: every schema shape, CLI verb, error code, or seam one spec defines and another consumes must match. Report mismatches.` },
  { k:'plan',       p:`${DIR}/specs/ vs the architecture plan (docs/the-plan/plan/): does any spec contradict a D-### decision or an R-###/AC-### it cites? Report violations with IDs.` },
  { k:'parity',     p:`${DIR}: tracker.yaml phases ⇆ specs/ files ⇆ README Phases/goal. Statuses, phase lists, pivot banners must agree. Report drift.` },
  { k:'amendments', p:`Every '## Amendments' entry in ${DIR}/specs/*: is the change reflected in the downstream specs that build on it? Report unpropagated amendments.` },
  { k:'northstar',  p:`Read ${DIR}/README.md goal/North-Star, then each spec's direction. Report specs that contradict the epic's stated direction or each other.` },
];
return (await parallel(PROBES.map(x => () => agent(x.p, { label:`coherence:${x.k}`, agentType:'scout', schema:S })))).filter(Boolean);
```

**Persist the verdict** to `docs/features/<slug>/audits/coherence-<YYYY-MM-DD>.md` (English): one-line verdict + findings table (severity · where · what · resolution). Commit with the epic. HIGH findings block the next wave; LOW/MED may defer with a note.

**Triggers**: S1 intake (no coherence report newer than the last spec edit) · after a pivot/mass-amendment · S8 step 3.5.

## Plan template

`docs/features/<slug>/plan.md` (inline mode: `~/.claude/plans/teamlead-<slug>.md`):

```markdown
---
slug: <slug>
mode: epic | spec | inline
started: <YYYY-MM-DD HH:MM>
status: draft | in-progress | awaiting-review | closed
budget_cap: <N commits | M hours>
---

# Teamlead plan — <title>

## Context
- Source: <spec path, or "in-session context">
- User goal: <one paragraph>
- Placement: <for any new schema/capability/verb — validator core / CLI surface / schema corpus / space template, per plan §4/§5. Decide here so every brief inherits it.>
- Constraints: <compat, protocol invariants, deadlines>
- Acceptance criteria:
  - [ ] ...

## Wave plan
| # | Wave | Independence | Model | Effort | Files | Stop-cond |
|---|---|---|---|---|---|---|

## Parallelism plan
- <which waves run concurrently; which files are lead-reserved>

## Self-evaluate (plan-level)
| # | Criterion | Result | Rationale |
|---|---|---|---|

Verdict: ✅ PROCEED | ⚠️ ADJUST | 🔴 STOP

## Wave log
(updated after each wave)

## Revisions (user feedback loop)

## Closeout
- Final commits: <range>
- Audit findings: <count>
- Deferred: <backlog rows>
- Status: closed <date>
```

## Agent brief template

The brief body becomes the `Workflow` `agent()` prompt. **Repo-relative paths everywhere.**

```
Stack: Go 1.26, stdlib-first (net/http ServeMux, flag-based CLI verbs, log/slog, encoding/json; NO new deps without a lead decision).
All file paths are REPO-RELATIVE — they resolve against the repo root.

## Goal
<one sentence — verb-led, outcome-shaped>

## Spec / context links
- Spec: <docs/features/<slug>/specs/NN-*.md or "inline — see below">
- Plan-corpus IDs to respect: <R-### / AC-### / D-### with one-line summaries>
- Anti-patterns to avoid: <rows from .claude/rules/go-conventions.md>

## Allowed files (allowlist) — REPO-RELATIVE ONLY
- internal/validate/codes.go     # ✅
- /Users/.../foo.go              # 🔴 never absolute machine paths

## Off-limits (NEVER touch)
- go.mod, go.sum, Makefile, .github/workflows/*
- schemas/** outside your grant; testdata/golden/** you weren't told to regenerate
- <any neighbouring file the brief does NOT explicitly grant>

## What to do
1. <step>
2. <step>
3. Sanity: `go test ./internal/<pkg>/... -race -count=1`

## Constraints
- No new dependencies. No new config files.
- No `//nolint` without a granted reason, no `t.Skip`, no suppressions.
- Log-or-return — never both.
- Schema fidelity: carry every guard/field of any spec snippet, or report the deviation.

## DO NOT
- DO NOT commit. DO NOT run git at all — unless this is an explicit commit-to-branch escape-hatch brief.
- DO NOT run `make check` / `make check-validators` / repo-wide `go build|test`. Scope self-verification to your own package.
- DO NOT touch files outside the allowlist.

## Acceptance
- <scoped test(s) that must pass>
- <observable behavior or symbol that must exist>

## Report back
- Files modified, tests added, scoped test output.
- **Deviations from the spec — REQUIRED; "none" is a real answer, but only if you mean it.** A different shape, path, or silently-closed open question — report it even if obviously right; it feeds the propagation probe.
- Anything skipped + why; any off-limits file you wanted.
```

## Stop conditions (lead-level)

- `make check` red after 3 fix attempts on the same gate → STOP, surface the log.
- An agent reports "could not complete due to X" twice → STOP the wave, address X, re-dispatch.
- Conflicting commit from a parallel session on the same files → STOP, surface; do NOT auto-merge.
- Budget cap exceeded >25% → STOP, ask.
- Self-evaluate 🔴 on plan or brief → surface, do not proceed.
- Epic close with phases still `pending`/`in-progress` → STOP; finish or flip to `deferred` with a user-approved reason.

## Don't

- Don't trust a `schema` return as proof of work — read the `git diff`, re-run scoped tests.
- Don't reach for `isolation:"worktree"` on a file-disjoint wave.
- Don't let an agent run a repo-wide build/test inside the workflow.
- Don't commit from inside a workflow agent.
- Don't dispatch a fan-out `agent()` without `agentType` (model-inheritance leak).
- Don't fork (`Agent` without subagent_type) for delegated work.
- Don't spawn a fresh agent for a ≤2–3-file change with no parallelism benefit — lead does it inline; the spawn is the cost, not the model.
- Don't squash wave commits — bisect-hostile.
- Don't write the spec yourself in inline mode — that's `/discover`'s job.
- Don't skip S4 or S6.a — both self-evaluate gates are the budget protection.
- Don't `git add -A`. Don't pop a foreign stash.
- Don't edit `mate_synced` artifacts locally (harness-discipline) — route upstream.
