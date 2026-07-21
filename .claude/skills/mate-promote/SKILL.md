---
name: mate-promote
description: >-
  Make a local improvement universal — the up-route. Use when an artifact or fix
  proven in one consumer belongs to the whole fleet. Classifies the layer (core /
  profile / operator / project-only — refuses project-only honestly), neutralizes
  it (principle into the artifact, values into config/registry), authors it in the
  SSOT, cuts a release, and hands back `mate fleet pull`. `--from <project>` lifts
  an artifact out of another consumer. The byte-edit always happens upstream.
requires_capabilities: [run-shell, read-files, edit-files]
allowed-tools: Bash Read Edit Write
model_tier: reasoning   # classify + neutralize is high-judgment; pull compiles this → the adapter's model (§16.5)
model: opus
cites: [_authoring.md, code/interface.md]   # doctrines this skill restates — citation-lint checks they resolve
mate_synced: v0.98.0
---

# mate-promote

> **Thesis.** The harness only stays alive if improvements flow **up** as
> reliably as fixes flow down — otherwise the SSOT rots and every consumer
> re-solves the same problem. But the up-route has one failure mode that kills it:
> becoming a dumping ground. So promotion is two judgments, not a copy. **Classify
> honestly** — most findings are *project-only* and must be refused, not lifted —
> and **neutralize before authoring**: the portable *principle* goes into the
> artifact, the project's *values* go to config/registry (the cardinal authoring
> rule). A consumer **never edits its locked copy**; the byte-edit happens
> upstream, in the SSOT. The pipeline now rests on real verbs — `mate promote`
> files the finding, `mate fleet pull` lands the result — so this skill is the
> *judgment* between them, not glue.

## When to run

- A consumer filed a finding (`mate promote`) and it's time to author it upstream.
- An artifact proven in one repo looks reusable — decide whether it's really core.
- `--from <project>`: lift a concrete artifact out of another consumer (resolve
  its checkout via `registry/projects.yaml` + the operator's machines registry).

Skip when the change is obviously this-project-only — that's not a promotion, and
forcing it up pollutes the SSOT. Refusing is the common, correct outcome.

## The procedure — in order

1. **Classify the layer.** Sort the finding into exactly one:
   - **core** — stack-agnostic machinery every consumer benefits from → promotes.
   - **profile** — shared by a *facet* (a lang/kind/infra class), not all →
     promotes into that profile, not the neutral core.
   - **operator** — about the operator's homes/machines/fleet, not any project →
     promotes into `operator/`.
   - **project-only** — genuinely specific to this repo → **refuse, honestly.**
     Say so and stop. Not everything promotes; the gate that keeps the up-route
     clean is a truthful *no*.
2. **Neutralize (the cardinal rule).** Separate the *principle* from the *value*.
   The transferable claim (the doctrine, the gate's standard, the artifact's
   behavior) goes into the neutral artifact; the project's paths/commands/ids/hosts
   go to `.mate/config.yaml` or a registry — **never baked into the body.** A body
   that names a project's path or a provider's model id is not yet neutralized.
   A provider fact the body needs comes from the generated provider matrix
   (`reference/providers.md` in the handbook), not memory; `portability-lint`
   reddens the mechanical leaks.
3. **Author upstream, through the right path.** Don't hand-write what a skill
   owns: a **doctrine** change goes through `/mate-doctrine` (the 8-section shape); a
   **gate** goes through `/mate-validator` (with its mandatory teeth-test); a skill or
   registry is edited in place to the corpus's conventions. Author in the SSOT
   checkout, never in the consumer's locked copy.
4. **Author the release migration when the change is breaking.** A structure or
   template change that consumers can't absorb by a plain pull owes a migration
   that carries every consumer across (spec §5d, Δ25). **Honest state today:** the
   migration *runner* does not exist yet — write the migration's intent as a
   tracked step and carry consumers by hand until the runner lands (it's on the
   harness backlog). Don't imply tooling that isn't there.
5. **Update the manifest.** A new synced artifact (skill, doctrine tree, hook) is
   not real until it's in `harness/manifest.yaml` (or `operator/manifest.yaml` for
   operator artifacts) — that's what a pull reads. Bump the manifest `version:`
   only when the change is not backward-readable by the prior release's binary.
6. **Cut a release.** Tag + CHANGELOG (consumers pin tags, never HEAD): bump the
   source-of-truth release pins, add the changelog section, run the release
   teeth (`make check` + `make release-check TAG=…`), then tag and push. One
   promotion is one reviewable release.
7. **Hand back `mate fleet pull`.** The improvement reaches the fleet the same way
   every fix does — each consumer pulls (or `mate fleet pull` iterates them,
   per-repo review). The loop is closed: the finding that came up as a `mate
   promote` row lands back down as a stamped, drift-gated artifact.

## What keeps promotion honest

- **Refusal is a first-class outcome.** "This is project-only" is the answer that
  protects the SSOT; a skill that promotes everything is worse than none.
- **Neutralize, don't transcribe.** Lifting a consumer's artifact verbatim carries
  its values up with it — the promotion isn't done until principle and value are
  separated.
- **The byte-edit is always upstream.** If you find yourself editing a consumer's
  locked copy to "prototype," stop — there is no sanctioned local-edit mode: the
  drift gate reddens and the next pull eats the edit. Prototype in a checkout of
  the SSOT itself; the change reaches the consumer as a release + pull.

---

This skill reads `${CLAUDE_PROJECT_DIR}/.mate/config.yaml` for what varies and
speaks semantic model tiers, never provider ids. Authority: the cardinal authoring
rule (`_authoring.md` — principle in the doctrine, value in the config/registry)
and the sync contract (architecture spec §5d — the `promote` up-route, the layer
classification, the release-migration duty). It composes with `/mate-doctrine` and
`/mate-validator` for the authoring step and rests on the `mate promote` / `mate fleet
pull` verbs for the file/land ends.
