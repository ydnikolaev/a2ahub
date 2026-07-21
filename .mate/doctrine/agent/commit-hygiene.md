<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Commit-hygiene doctrine — one intent, this session's, in the project's convention

> **Thesis.** A commit is the unit the history is read, reverted, and bisected in, so
> each one must record **one logical change, made in this session, in a form a machine
> can parse** — and nothing else. Three disciplines make it safe everywhere: stage
> only what *this* session touched (never sweep in another's work), write the message
> in the project's convention (`type(scope): subject`), and keep the change atomic (one
> intent per commit). This is an **agent-process discipline** — a sibling of
> [verification-honesty](verification-honesty.md): both govern how the agent conducts
> its work, not the quality of a shipped artifact. The commit is process metadata with
> a permanent repository effect; a sloppy one corrupts attribution and revertability
> for everyone downstream, and no gate can un-mix it after the fact.

## 0. When to apply (and when not)

The trigger is **making a commit** — which happens far more often than an explicit
"make a commit" instruction, so the reflex must be ambient, not summoned. It applies
to any version-controlled repository. A directory under no VCS has nothing to be
hygienic about — skip it entirely. A throwaway solo scratch repo can relax the atomic
and convention disciplines (there is no history to be read by anyone else), but
**session-isolation never relaxes** the moment more than your own uncommitted work is
present in the tree.

## 1. Stage only what this session touched — never `-A`

**Stage the explicit paths you modified in this session; never `git add -A` / `git add
.` / `git add *`.** A blanket add sweeps in files from another terminal, another
worktree, an unrelated manual edit, or a half-finished change that happens to share
the working tree — attributing someone else's work to your commit and making it
unrevertable as a unit. The tree is a shared surface; your session is the only slice
you can vouch for. Build the file list from what you actually changed, reconcile it
against `git status`, and leave everything you did not touch for whoever owns it. The
cost of listing paths is trivial; the cost of a commit that entangles two authors'
work is paid at every future revert and blame.

## 2. Write the message in the project's convention, machine-readable

**A commit message is read by machines and humans long after the change — write it in
the structured convention (`type(scope): subject`, imperative, bounded length), not as
free prose.** The structure is what lets changelog generation, semantic-version
inference, history-scanning, and blame archaeology work without a human re-reading
every diff. An unstructured "fixed stuff" message discards that leverage permanently —
the history cannot be re-parsed retroactively. The *type* and *shape* are the universal
convention; the concrete scope vocabulary and any trailer/attribution rules are project
values (§6). The body, when present, explains **why**, not what — the diff already shows
what.

## 3. Keep the commit atomic — one intent per commit

**One commit records one logical change; do not fold a refactor, a feature, and a
formatting sweep into a single commit.** Atomicity is what makes a commit revertable in
isolation, bisectable to a single cause, and reviewable as one decision. A commit that
mixes intents cannot be reverted without losing unrelated work, defeats `git bisect`
(the culprit hunk hides among innocents), and forces a reviewer to untangle several
decisions at once. When the working tree holds more than one intent, split it into
separate commits — the seam between intents is the commit boundary.

## 4. Instantiation seam

What each stack/project swaps into these neutral principles:

- **The scope vocabulary** (§2) — the project's concrete set of scopes (feature slugs,
  stack/area labels, domain names) and how narrow to go. The principle is "scope by the
  narrowest meaningful label"; *which* labels exist is a project value.
- **The type vocabulary** (§2) — the allowed `type` set (`feat`/`fix`/`docs`/`chore`/…)
  and any project-specific additions or subject-length ceiling.
- **The trailer / attribution convention** (§2) — co-author lines, session/attribution
  trailers, sign-off requirements. Whether and how they are attached is a project value.
- **The escalation on a rejected commit** (§0/§1) — what to do when a pre-commit hook
  rejects: fix-and-recommit vs the project's own remediation path.

## 5. Anti-patterns

| ❌ | ✅ |
|---|---|
| `git add -A` / `git add .` to stage "everything" | Stage the explicit paths this session touched; reconcile against `git status` |
| Committing files another session/worktree left in the tree | Leave untouched files for their owner; note the skipped count |
| "fixed stuff" / free-prose message | `type(scope): subject`, imperative, bounded; body explains *why* |
| Baking a house scope/trailer style into the universal reflex | Keep the reflex principle-only; scope vocabulary + trailers are project values (§6) |
| One commit mixing refactor + feature + formatting | One intent per commit; split the tree at the intent boundary |
| `--no-verify` / `--amend` past a hook rejection to force it through | Fix the underlying issue, re-stage, make a new commit |

## 6. Porting checklist

- [ ] The commit-hygiene reflex (§0) is in the project's always-on rule set (staging isolation + conventional format + atomicity).
- [ ] Staging is by explicit session-touched paths; `git add -A`/`.`/`*` is never used unless the user explicitly instructs it.
- [ ] Messages follow `type(scope): subject` (imperative, bounded); the body explains *why*.
- [ ] Each commit is one logical change; multi-intent trees are split at the intent boundary.
- [ ] The instantiation seam (§4) names this project's scope vocabulary, type set, and trailer/attribution convention — none of which leak into the universal rule.

## 7. Cross-links

Sibling agent-process discipline: [verification-honesty](verification-honesty.md) —
both govern how the agent conducts its work (this one the commit, that one the claim),
not a shipped artifact's quality. Instantiated on-demand by the `commit` skill (the
invoked procedure) and ambiently by the project's always-on commit-conventions rule
(the reflex that fires whenever the
agent commits, inside or outside an explicit `/commit`). The scope-vocabulary and
trailer values (§4) live in the project's own commit-convention rule +
`.mate/config.yaml`, never in the neutral doctrine or the always-on rule.
