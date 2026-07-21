---
name: mate-harness-hardening
description: >-
  Audit and sharpen the agentic harness itself — the loaded rules, skills, agents,
  gates, and the doctrine graph. Finds drift, collisions, dead surfaces, missing
  gates, and over-orchestration; proposes and (approval-gated) applies fixes. Use
  when "harden the harness", "audit the agentic env", the agents keep getting `<X>`
  wrong, or to drain the harness-backlog. Runs in two contexts: in a consumer it
  audits that consumer's env and routes shared-harness findings UP; in the SSOT it
  drains the shared harness-backlog. Delegates the actual fix — a missing gate to
  `/mate-validator`, a shared-harness finding to `/mate-promote`.
requires_capabilities: [run-shell, read-files, edit-files]
model_tier: reasoning   # audit + classify + route is high-judgment; pull compiles this → the adapter's model (§16.5)
cites: [code/validation.md, code/documentation.md, code/interface.md]   # doctrines this skill restates — citation-lint checks they resolve
mate_synced: v0.98.0
---

# mate-harness-hardening

> **Thesis.** The harness is a product, and a product without a maintainer rots —
> a rule drifts from the arch it describes, one fact ends up copied across three
> surfaces, a skill accretes inline work it should delegate, a failure class
> recurs with no gate to catch it. This skill is that maintainer. But the
> maintainer has one failure mode that would make it worse than nothing:
> **becoming a re-implementer** — porting a gate it should have handed to the gate
> path, or authoring upstream what the up-route already owns. So hardening is
> **audit → classify → route**, never audit → rebuild. Each finding goes to the
> surface (and the skill) that already owns its class: a missing gate to
> `/mate-validator`, a shared-harness fix to `/mate-promote`, a local-env fix in
> place. The skill's whole value is the *judgment* — which findings are real,
> which surface loads the fix, and whether it belongs to the fleet or to one
> consumer — not the mechanics of the fix.

## When to run

- Manual: "harden the harness", "audit the agentic env", "the agents keep getting
  `<X>` wrong".
- **Drain the harness-backlog** — the signals a session captured cheaply (a gap,
  a collision, a dead surface it tripped over). This is the usual trigger.
- Periodically (a schedule). NOT on every session — the full audit is an expensive
  fan-out; the cheap per-session *capture* is what runs each session (below).

## Two contexts + the core/local router

One synced skill, two run-contexts — the router is the bridge between them:

- **In a consumer** it audits *that consumer's own* harness (its loaded rules /
  skills / agents / gates). A finding that belongs to the **shared** harness is
  routed **up** — handed to `/mate-promote`, which files it into the SSOT's
  `docs/backlog.md` and (on classify) neutralizes → authors → releases.
  A finding that is **local** to this consumer's env is fixed in place. A consumer
  **never edits a locked SSOT copy** — the byte-edit for a shared fix happens
  upstream.
- **In the SSOT** it drains that same `docs/backlog.md` directly —
  authoring the fix, cutting the release.

The backlog is **fed by consumers, drained in the SSOT**; the router decides which
side of that line a finding sits on. Classify core-vs-local *honestly* — the
default for an ambiguous finding is **local** (never lift a project-only concern
into the shared harness; that is `/mate-promote`'s refuse-honestly rule seen from
the other end).

## The cost model — capture vs process

The full audit (a fan-out over every surface + gate + the doctrine graph) is
expensive, so it is **split**:

- **Capture** (every session, ~free, session-aware): a finalizing step (`/ship` or
  equivalent) appends a one-line signal to the harness-backlog **iff** the session
  revealed a harness gap / collision / dead surface / missing gate. Usually nothing
  → no entry. The session already holds the context, so the signal is caught fresh.
- **Process** (this skill, manual/scheduled, batched): read the accumulated backlog
  + a delta-audit, then propose/route/fix.

**Do not invert this** — never wire the full audit into every session's finalizer.
That over-orchestration is exactly what this skill flags.

## Procedure

1. **Load context.** Read the harness-backlog (the captured signals) + the
   project's harness surface map if it keeps one (**recommended, not required** —
   its `last verified` stamp scopes the delta) + the user's framing.

2. **Audit — scope by trigger.**
   - **Backlog-triggered → DELTA.** Process the captured signals + only what moved
     since the stamp (`git log --since=<stamp> --name-only` over the harness
     surfaces + doctrine). Cheap.
   - **First run / periodic / "harden the harness" → FULL SWEEP.** Pre-existing debt
     predates the stamp, so a pure delta would miss it.
   - Either way, audit through four lenses (fan out read-only reader agents when the
     surface is large — but that is a means, never a mandate):
     - **dead surface** — a rule / skill / agent that never loads (a `paths:` that
       never matches, a file in a tree nothing reads) or is never invoked.
     - **SSOT collision** — one fact claimed by two or more surfaces (a decision
       table copied into N docs; a value restated instead of referenced).
     - **over-orchestration** — a skill doing heavy work inline instead of
       delegating; a workflow where a plain call suffices; a heavy step on the wrong
       tier.
     - **gate gap** — a known failure class with no gate (a recurring mistake
       nothing catches). If a property can be machine-checked, a machine should
       check it (`validation`).

3. **Triage by class + leverage.** Tag each finding — `collision · drift · dead ·
   gap · cost · cc-feature` — as a **triage lens** (a way to see the finding's
   shape), *not* a mandated backlog-row format. Rank by leverage: **always-on >
   path-scoped > skill-invoked > one-off doc**, and **cheap gate > prose**. A quiet
   run is the success mode.

4. **Route — the core of the skill (delegate, do not re-implement).**
   - **gap** → hand to **`/mate-validator`** — the single gate-authoring path (with
     mandatory teeth). Never port gate mechanics inline; that both duplicates the
     gate path and drifts from it.
   - **shared-harness (core)** → hand to **`/mate-promote`** — it classifies the
     layer, neutralizes (principle into the artifact, values into config), authors
     upstream, and releases.
   - **local** → fix in place, in the **right load-home**.
   - **cc-feature** (a new provider capability to wire in, or a workaround it lets
     you retire) → **`/mate-feature-review`**. Do **not** touch the review baseline —
     that skill is its sole owner (it records "triaged through X", advanced at
     triage time).

5. **Propose.** A concise plan: for each accepted finding, the fix + its home (the
   surface that *loads* it) + why. A fix that adds a **second copy** of a fact is
   itself a collision — reject it (`documentation`: one fact, one home).

6. **Approval gate.** Editing the harness is high-touch — **never auto-apply
   structural changes.** Surface the triage + plan and get the go. (Pure backlog
   grooming or a dead-link fix may proceed.)

7. **Implement + archive.** Apply approved fixes; stage **session-isolated**, commit
   thematically. Move each processed backlog row to an archive with the closing
   commit range — don't just delete it (history lives in the archive). A finding
   that belongs to other in-flight work is handed off there, never silently dropped.

8. **Verify + self-check.** Links resolve; a routed gate fires on the bad case AND
   is clean on the good one (positive + negative teeth); the project's gate-of-gates
   is green. Restamp the surface map if the project keeps one. Confirm the change
   created **no new drift or dead surface** — the maintainer must not violate what
   it enforces. Consider `advisor()` for a genuinely structural change.

9. **Forward proposals (skip-is-default).** After the work, look *forward* —
   impact-gated, never a manufactured list: a recurring failure class a cheap gate
   would catch forever; a mis-firing or too-narrow existing gate; a refactor **only**
   when the impact is real. Most runs surface **0–2**. Each is approval-gated and
   captured as a backlog row for an *incremental* future run — never a big-bang
   rewrite.

## Composition

This skill **routes; the composed skills do the work.** It depends on:

- **`/mate-validator`** for every `gap` finding (a machine-checkable invariant → a
  teeth-tested gate). Hardening never authors a gate itself.
- **`/mate-promote`** for every `core` finding (the up-route: classify →
  neutralize → author upstream → release → `mate fleet pull`).
- **`/mate-feature-review`** for every `cc-feature` finding (triage a provider
  capability against its baseline).

If a project carries differently-named equivalents, route to those; the dependency
is on the *role* (gate-authoring path, up-route, provider-review), not the literal
name.

## Principles (it must not violate what it enforces)

- **One fact, one home** — every fix links the SSOT; never paste a second copy
  (`documentation`).
- **Place where it loads** — a fix belongs in the surface the provider actually
  loads (an always-on rule, not a reference-only tree); a rule nothing loads is a
  dead surface, not a fix.
- **Cheap gate > prose**; **non-blocking WARN > hard fail** for a judgment call
  (`validation`).
- **Policy changes ripple** — when you change a cross-cutting policy in its home,
  grep every skill/rule for the OLD policy; a policy fixed in one place silently
  leaves the contradiction in the others.
- **Agnostic over the two contexts** — the same audit runs in a consumer and in the
  SSOT; it reads the project's surfaces from config, never hard-codes one project's
  layout (`interface`).

## Stop conditions

- A finding overlaps in-flight work → coordinate / hand off, never double-fix.
- A fix has no single right home (genuinely cross-cutting) → surface to the user.
- You can't classify core-vs-local honestly → default to **local**; never lift a
  project-only concern into the shared harness.
- You re-derived more than the delta from scratch → you're ignoring the surface
  map; reload it.

## Output

Triage (by class + leverage) → approval-gated plan → routed fixes (gaps to
`/mate-validator`, core to `/mate-promote`, cc-features to `/mate-feature-review`,
local fixed in place) → thematic commits + a groomed, archived backlog → forward
proposals. Terse handoff: what was hardened, what was routed where, what's deferred,
and the forward proposals queued.
