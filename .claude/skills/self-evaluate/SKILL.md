---
name: self-evaluate
description: >-
  Structured decision gate — when a proposal, plan, or diff needs autonomous
  validation before you act, when the user cannot answer a technical question and
  cedes the call to you, or when a coordinator must gate a decision before dispatch.
  A tiered checklist with hard blockers, accumulate-to-STOP math, a ⚠️/🔴 verdict,
  and a single ADJUST re-run. Evaluation is judgment, so it is a skill, not a gate.
user-invocable: true
requires_knowledge: [decision-context]      # resolves to the project's context-surface map (Step 2); soft-declared like mate-doctrine/mate-validator until config-validate lands
requires_capabilities: [run-shell, read-files]
allowed-tools: Bash Read
model_tier: reasoning
model: opus
cites: [agent/verification-honesty.md]      # the doctrine this skill's Ground-Truth blocker executes — citation-lint checks it resolves
mate_synced: v0.98.0
---

# /self-evaluate

> **Thesis.** Some decisions cannot wait for a human — the user lacks the
> expertise, or the agent is mid-flight and about to commit. For those, structured
> self-evaluation replaces the missing judgement: a fixed checklist whose blockers
> are non-negotiable and whose warnings accumulate, so an agent cannot flatter its
> own proposal past the gate. This is **not** `self-review` (adversarial critique of
> finished work — "what's wrong with this?"); it is a *decision gate* — "should I
> proceed with THIS, and at what verdict?"

## When to use

- The agent must answer a question requiring expertise the user does not have.
- The agent proposed a plan or approach and the user wants autonomous validation.
- The user responds with `/self-evaluate` instead of answering.
- A coordinator skill needs to gate a plan, a sub-agent brief, or a diff before
  dispatch or commit.
- A sub-agent needs a pre-report sanity check on its own work.

## Modes

Pick one explicitly at the top of the output.

| Mode | Trigger | Output |
|---|---|---|
| **standalone** | user typed `/self-evaluate`, or the agent invoked the skill directly | full procedure, full verdict table, verdict block, action |
| **inline** | a coordinator walks the checklist without ceding control (never invoke this skill from inside another skill — walk the table inline) | compact verdict table in the coordinator's flow, no procedure narration |
| **pre-report** | a sub-agent self-checks before returning to its coordinator | one line — `self-evaluate: ✅ PROCEED` / `⚠️ ADJUST (n: <criteria>)` / `🔴 STOP (<blocker>)`; skip the table, the coordinator does the deep check |

## Steps

### Step 1 — Identify the pending decision

State it in one sentence:

> **Decision**: <what you proposed, planned, briefed, or are about to commit>

### Step 2 — Gather context (fetch on-demand)

Read the **minimum** each criterion needs — do not pre-read everything. Fetch a
criterion's evidence *when you evaluate that criterion*, not upfront.

Where the evidence lives is a **project value**, not this skill's to hardcode. Your
project declares its context surfaces through the `decision-context` knowledge it
provides (spec/feature docs, an API contract, a decision log, a domain map, live
framework docs, symbol definitions + callers). Read from *those*.

If your project declares **no** context surfaces, do not invent paths and do not
evaluate from memory — name what evidence each criterion would need and ask (see
Stop conditions). A decision gate run against no evidence is theatre.

> [!CAUTION]
> **Never claim something is absent without verifying it.** Grep the code, query
> the datastore, read the spec **first** — a "no such thing exists" is a factual
> claim and carries the same burden as any other (this is the Ground-Truth blocker
> below, and the `verification-honesty` doctrine it executes).

### Step 3 — Run the tiered checklist

#### Tier-1 — blockers (any fail = 🔴 STOP; no override, no N/A)

| Criterion | Question to self |
|---|---|
| **Security** | Any injection, leak, privilege escalation, authz bypass, secret exposure? |
| **Reversibility** | If wrong, can this be rolled back without data loss or an outage? |
| **Ground Truth** | Have I verified every factual claim against actual code / data / spec — not memory, not training data? |

#### Tier-2 — quality (each ⚠️ accumulates; ≥ 3 total ⚠️ = 🔴 STOP — death by a thousand cuts)

| Criterion | Question to self |
|---|---|
| **Industry Standard** | Ignoring this repo — what is the standard solution to this problem class today? |
| **Repo Consistency** | How does this repo solve the *same kind* of problem now? Does my approach match, or justifiably diverge? |
| **Intent Compliance** | Does it match the spec / issue / stated intent it traces to? Are amendments needed? |
| **SSOT & DRY** | Does it duplicate logic or data? Violate a single source of truth? |
| **Scalability** | Survives its real deployment topology (concurrency, multiple instances, load)? No hidden shared-state assumption? |
| **Project Knowledge** | Which specific decisions / docs did I actually read? Name 1+ with a one-line takeaway, or state the exact search that found nothing. |
| **Future-Proof** | Adaptable without a rewrite (migration path), not tightly coupled, extensible (new variants without editing old code), no hidden tech debt, and no conflict with the known roadmap? A miss on any one = ⚠️ with the failing aspect named. |

**Project-declared criteria.** A project may extend Tier-2 with its own criteria
(e.g. a monorepo's placement discipline, a regulated domain's audit rule) declared
through its `decision-context` knowledge. They accumulate ⚠️ into the same math.
This skill ships the portable core; it does not assume any one project's shape.

#### N/A handling

- **Tier-2 only**: `N/A` is allowed with a one-line reason ("docs-only change, no
  runtime impact"). `N/A` ≠ ✅ and ≠ ⚠️ — it drops the criterion from the count.
- **Tier-1**: `N/A` is forbidden. If you think Security / Reversibility / Ground
  Truth do not apply, you have mis-scoped the decision — restate it until they do.

### Step 4 — Render the verdict table

Rationale is **≤ 1 sentence, ≤ 20 words**, and names the **evidence**, not the
criterion. "✅ — security is fine" is not a rationale; "✅ — input validated at the
handler, authz enforced in middleware" is.

**standalone / inline format:**

```
## Self-Evaluate (mode: standalone | inline): <one-line decision summary>

| Tier | Criterion | Result | Rationale |
|---|---|---|---|
| 1 | Security | ✅ | <evidence> |
| 1 | Reversibility | ✅ | <evidence> |
| 1 | Ground Truth | ✅ | <evidence — the exact file/row/line verified> |
| 2 | Industry Standard | ✅ | <evidence> |
| 2 | Repo Consistency | ⚠️ | <the mismatch> |
| 2 | … | … | … |
```

**pre-report format:**

```
self-evaluate: ✅ PROCEED
```
or
```
self-evaluate: ⚠️ ADJUST (Repo Consistency, Scalability)
```

### Step 5 — Verdict, action, connect-back

| Verdict | Condition | Action |
|---|---|---|
| ✅ **PROCEED** | all Tier-1 ✅, Tier-2 total ⚠️ ≤ 2 | resume the paused action (commit / dispatch / answer). |
| ⚠️ **ADJUST** | all Tier-1 ✅, Tier-2 has 1–2 ⚠️ | produce the concrete adjustments and **hand them back** to the pending work (this gate evaluates and reads to verify — it does not edit); apply them there, then **re-run the gate ONCE**. After one re-run: PROCEED if clean, else treat as STOP. |
| 🔴 **STOP** | any Tier-1 fail, OR Tier-2 ⚠️ ≥ 3, OR an ADJUST re-run still ⚠️ | hand off with the full table + the named blocker. Do not loop. |

**Borderline → consider `advisor()`:** exactly 2 ⚠️ (right at the edge); low
confidence on Ground Truth in an unfamiliar area; an irreversible change scoring a
suspiciously clean sweep (that is itself a smell).

**Verdict block:**

```
**Verdict: ✅ PROCEED** (or ⚠️ ADJUST → re-run, or 🔴 STOP → handoff)

- [If ADJUST] Change 1: <concrete edit>
- [If STOP] Blocker: <which criterion, what evidence>
- [If borderline] Considered advisor(): <yes — called / no — reason>

→ <next concrete action: resume <thing> | re-run checklist | wait for user>
```

## Worked example (a Tier-2 ⚠️ that becomes ADJUST)

> **Decision**: add a nullable timestamp column to flag-and-sort records for a
> "featured" view, querying `WHERE flagged_at IS NOT NULL ORDER BY flagged_at DESC`.

```
## Self-Evaluate (mode: standalone): featured view via a nullable flag-timestamp

| Tier | Criterion | Result | Rationale |
|---|---|---|---|
| 1 | Security | ✅ | read-only public view, no user input in the predicate |
| 1 | Reversibility | ✅ | dropping a nullable column is safe, no backfill |
| 1 | Ground Truth | ✅ | table + absence of the column verified against the live schema |
| 2 | Industry Standard | ✅ | a single nullable timestamp is the canonical soft-flag + sort key |
| 2 | Repo Consistency | ⚠️ | other flag columns here are boolean + a separate order column |
| 2 | Intent Compliance | ✅ | matches the stated feature intent |
| 2 | SSOT & DRY | ✅ | no existing featured table or tag to reuse |
| 2 | Scalability | ⚠️ | the predicate needs a partial index to stay cheap at scale |
| 2 | Project Knowledge | ✅ | the decision log prefers nullable timestamps over boolean+timestamp pairs |
| 2 | Future-Proof | ✅ | the column carries both flag and ordering; open to new sort keys |

**Verdict: ⚠️ ADJUST → re-run**
- Change 1: add the partial index on the flag predicate to the migration.
- Change 2: the decision-log entry cited above actually resolves the Repo-Consistency
  mismatch — the boolean pattern elsewhere is legacy; note it in the change description.

→ hand these changes back; apply them to the migration, then re-run the gate once.
```

After the changes are applied and the gate re-runs, both ⚠️ flip to ✅; the verdict becomes PROCEED.

## Smells (fake-pass signals)

- **A clean sweep on a non-trivial decision** — almost always self-flattery. Re-run
  with sharper rationales.
- **A rationale that restates the criterion** ("✅ — fits the stack") — Step 2 was
  skipped.
- **Jumping to the table without Step 2** — guessing, not evaluating.
- **Cherry-picked context** (read one file, claim "no conflicts anywhere") — the
  classic Project-Knowledge failure.
- **A Project-Knowledge ✅ with no citation** — replace it with the exact search
  that found nothing, or go read the actual source.
- **An ADJUST loop past one re-run** — you are negotiating with yourself; escalate to
  the user or `advisor()`.
- **A Tier-1 marked `N/A`** — forbidden; restate the decision until it applies.

## Composition

- **`self-review`** and this skill are complements, not duplicates: run
  `self-review` to find what is *wrong* with finished work (adversarial critique);
  run `self-evaluate` to decide whether to *proceed* with a pending decision
  (structured gate with a verdict).
- A coordinator gates a decision by walking this checklist in **inline** mode — it
  does not invoke this skill (that would cede control); it reproduces the compact
  table in its own flow.

## Stop conditions

- **No declared context surfaces** (`decision-context` resolves to nothing) → do not
  invent paths and do not evaluate from memory. State, for each criterion, what
  evidence it would need, and ask the user to point you at the surfaces (or to
  confirm there are none and accept a degraded, clearly-flagged evaluation). This is
  the guard against a hollow gate that goes through the motions with no evidence.
- **A Tier-1 blocker cannot be evaluated** because the evidence is unreachable →
  that is a 🔴 STOP, not a ✅. An unverifiable blocker fails closed.

## Output

A single mode-tagged evaluation plus the verdict block, then the immediate action the
verdict dictates — no waiting for permission unless the verdict is STOP.
