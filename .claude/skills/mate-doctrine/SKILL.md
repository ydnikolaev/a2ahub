---
name: mate-doctrine
description: >-
  The single authoring path for a doctrine — a stack-agnostic principle document.
  Use when you capture a portable principle that has no home yet (`new`), when a
  principle sharpens or a scar teaches an anti-pattern (`update`), or when the
  corpus index needs regenerating after either (`index`). Authoring prose to a
  fixed shape is judgment, so it is a skill; the shape *check* is the
  `doctrine-lint` gate, not a verb here.
requires_knowledge: [authoring-shape]       # resolves to _authoring.md — the 8-section meta-doctrine
requires_capabilities: [read-files, edit-files]
allowed-tools: Read Edit Write
cites: [_authoring.md]   # doctrine this skill restates — citation-lint checks it resolves
mate_synced: v0.98.0
---

# mate-doctrine

> **Thesis.** A principle is only reusable if it lives in exactly one place, in a
> shape a reader learns once and a machine can lint. This skill is the sole way a
> doctrine is born or revised, so the corpus never drifts in shape. It does not
> restate the authoring meta-doctrine; it *executes* it. The canonical shape and
> its rationale live in `_authoring.md` (the eight sections); read it before you
> write.

## When to run

- **A portable principle has no home.** You (or a review) just articulated a
  stack-agnostic rule — how to gate, how to layer env, how to name a registry —
  and it is being restated in a skill body or a commit message instead of cited.
  That is a missing doctrine → `new`.
- **A principle sharpened, or a scar taught an anti-pattern.** An existing
  doctrine is now wrong, incomplete, or missing the ❌/✅ row a live failure just
  earned → `update`.
- **After either** → `index`, so the corpus README reflects the new file or
  status.
- **Not** for project *values* (paths, commands, thresholds) — those go to
  `.mate/config.yaml` or a registry (the cardinal rule: principle in the doctrine,
  value in the config). Not for a two-consumer local rule — that is a `.claude`
  rule, not a doctrine.

## `new "<topic>"` — scaffold one doctrine to the shape

1. **Confirm it's a principle, not a value.** If the content is a list, it is a
   registry (`registries.md`); if it is one project's setting, it is
   config. A doctrine carries a *transferable claim*.
2. **Scaffold the eight sections** from `_authoring.md` — in order: (1) the
   `<!-- SSOT SOURCE… -->` provenance banner on line 1; (2) `# <Topic> doctrine` +
   a one-line `> **Thesis.**`; (3) `## 0. When to apply`; (4) the portable
   principles, each section **leading with a bold, stack-agnostic principle**;
   (5) the instantiation seam (what each stack swaps); (6) the ❌/✅ anti-patterns
   table; (7) the porting checklist; (8) cross-links to siblings + the
   instantiating project-doctrine.
3. **Keep every body line neutral.** No paths, commands, model ids, or provider
   names in the principle — those are the seam (§5) or config. When a body
   genuinely needs a provider fact, read it from the generated provider matrix
   (`reference/providers.md` in the handbook), never from memory; the
   `portability-lint` gate reddens the mechanical classes (inlined model ids).
   Author to all eight sections even though the gate floors at four.
4. **Run the gate** — `make doctrine-lint` (or `scripts/doctrine-lint.sh`) must be
   green; it is the completion step, the way `/mate-validator new` ends in `make check`.
5. **`index`** to register the file in the corpus README.

## `update "<topic>"` — revise without breaking shape

Edit the principle or add the anti-pattern row, but never touch the frame:
re-read `_authoring.md` if unsure which section a change belongs in, keep the
thesis to one claim (a second claim is a *second* doctrine → `new`), and re-run
the gate. If the update changes the doctrine's status (e.g. a legacy file reaches
full 8-section conformance), reflect it in `index`.

## `index` — regenerate the corpus README

Rewrite `doctrine/README.md`: one row per doctrine (link · what it governs ·
status · what instantiates it), sorted with `_authoring.md` first. The index is a
render of the corpus, not a second SSOT — it lists and links, it never restates a
doctrine's content. Status is *stable* (authored to shape, lint-green) or a
tracked note pointing at the SSOT backlog (`docs/backlog.md`).

## The shape check is a gate, not a verb

`doctrine-lint` (in `make check`) enforces the shape floor — provenance banner,
H1, thesis, porting checklist — and reddens CI when a doctrine drops one. It is a
gate so the corpus is guarded continuously, not only when someone remembers to
run a `lint` verb (the same authoring=skill / checking=gate split as
`/mate-validator`). The gate tightens toward the full eight as the legacy corpus is
normalized; this skill always authors to all eight.
