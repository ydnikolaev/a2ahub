<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Authoring doctrine — the canonical shape every doctrine follows

> **Thesis.** A doctrine corpus is only trustworthy if its documents are
> *uniform in shape* — same eight sections, same order, same provenance — so a
> reader (human or agent) learns the form once and finds any fact by position, and
> a machine can **lint the shape**. This is the meta-doctrine: it governs how every
> other doctrine is written, and it is itself an instance of the shape it defines.
> Uniformity of shape is what makes the `doctrine-lint` gate possible and the corpus
> navigable; drift in shape is the first drift that lets content drift.

## 0. When to apply (and when not)

Apply to every file under `doctrine/` — the code-doctrine corpus
(`doctrine/code/*.md`) and any future layer. A doctrine **tiers its own body**
(§0 of each doctrine says when a small project skips the heavy sections), but it
never skips the *frame*: the provenance header, the H1+thesis, the anti-patterns
and porting sections, and the cross-links are mandatory even for a one-principle
doctrine. A stub that will grow still opens with the full frame — the empty
sections are a to-do list, not an excuse. Not for: skills (they follow the
skill shape — frontmatter + procedure, see `self-review`), rules, or registries
(schema'd YAML, not prose).

## The eight sections — the principle

**A doctrine is these eight parts, in this order.** The author fills each with
portable principle. **All eight are gated** (`doctrine-lint` — shape is a gate's job,
not a skill verb's): the machine now checks every section, not the four-marker floor it
enforced from v0.6.0 to v0.77.0 while the legacy files ([code/cli.md](code/cli.md),
interface, env) still lacked a seam, an anti-patterns table, or cross-links. They carry
them now, so the floor is gone — the reviewer's eye is no longer the only thing standing
between the corpus and a doctrine with nowhere to put its values.

One thing the gate checks as an **organ, not a convention**: the principles body. Every
written doctrine numbers its principles (`## 1.`, `## 2.`), but numbering is a habit —
the gate asks only that a body *exists* (at least one section that is not a frame
section). A gate that demanded the numbering would red this very file, which carries its
body under prose headings.

1. **Provenance header** — the hand-written `<!-- SSOT SOURCE (mate repo)… -->`
   banner on line 1. It marks the file as SSOT-authored (distinct from the
   pull-time `mate_synced:` stamp a consumer's copy carries) and is the first
   thing the `doctrine-lint` gate checks. No banner ⇒ not a doctrine.
2. **`# <Topic> doctrine` + one-line bold thesis.** The H1 names the topic; a
   `> **Thesis.**` blockquote states the single load-bearing claim in one breath.
   If you can't compress it to one thesis, the doctrine is two doctrines.
3. **§0 When to apply (and when not)** — the tiering gate, so a three-command
   project doesn't over-build the twelve-section version. Every doctrine earns its
   weight per project; §0 is where that judgment lives.
4. **Portable principles** — the body. Each section **leads with a bold,
   stack-agnostic principle**, then its rationale. The principle is the transferable
   claim; the rationale is why. Values, paths, and commands do **not** appear here —
   they are the instantiation seam (§5) or config, never the principle (the cardinal
   authoring rule: principle in the doctrine, value in the config/registry).
5. **Instantiation seam** — an explicit list of what each stack/project swaps
   (paths, commands, tools) to instantiate the portable principles. This is the
   contract between the neutral doctrine and its concrete instances; naming it
   keeps the principles above it honest-neutral.
6. **Anti-patterns table** (❌ / ✅) — the failure modes paired with their fix.
   The two-column table is the fastest way a reader self-diagnoses; it is where
   scars become teachable.
7. **Porting checklist** — the actionable `- [ ]` list for bringing a project
   into conformance with this doctrine. The doctrine's own acceptance, in
   checkbox form.
8. **Cross-links** — sibling doctrines this one leans on, plus the
   project-doctrine that instantiates it. A doctrine that links nothing is either
   the root or forgot its dependencies. **Link portability:** a Markdown link
   `](…)` in a doctrine/rule body must resolve in the *consumer* `.mate/` layout,
   not just the SSOT — so link only **sibling doctrines** (`../code/validation.md`,
   which moves with the tree). Reference a **skill, rule, profile, or handbook page
   by bare name / backtick path, never a Markdown link**: they land in a different
   (provider or faceted) tree with no portable relative path, and a faceted profile
   may not even be pulled into a given consumer. The `link-check` gate enforces this
   against the pulled tree.

## Instantiation seam

What a concrete doctrine swaps into this frame: the **topic** (§2 H1), the
**principles** (§4 body — the doctrine's actual content), the **stack table**
(§5 — e.g. `profiles/go` refines a CORE default), the **scars** (§6 — this
project's lived failures). The frame itself never varies; only the fill does. A
stack profile (`profiles/<stack>/<topic>.md`) is a *refinement* document that
follows the same eight sections and cross-links back to the CORE doctrine it
specializes.

## Anti-patterns

| ❌ | ✅ |
|---|---|
| Doctrine with no provenance banner (a synced copy edited in place, or an un-adopted file) | Line-1 `<!-- SSOT SOURCE… -->` banner; edits happen in the SSOT |
| A wall of prose with no bold principle per section | Each section leads with the transferable bold claim, rationale follows |
| Baking a project's paths/commands into the principle | Principle is neutral; the value lives in the instantiation seam / config |
| Two theses fighting in one H1 | One doctrine, one thesis; split the second out |
| Skipping anti-patterns / porting "because it's obvious" | The frame is mandatory even for a stub — empty sections are a to-do list |
| A doctrine that cross-links nothing | Name the siblings it leans on and the project-doctrine that instantiates it |

## Porting checklist

- [ ] Line 1 is the `<!-- SSOT SOURCE… -->` provenance banner.
- [ ] H1 is `# <Topic> doctrine` with a one-line `> **Thesis.**` blockquote.
- [ ] `## 0. When to apply` tiers the doctrine for small vs. large projects.
- [ ] Every body section **leads with a bold, stack-agnostic principle**.
- [ ] An **instantiation seam** names what each stack/project swaps.
- [ ] An **Anti-patterns** (❌/✅) table is present.
- [ ] A **Porting checklist** (this list's shape) is present.
- [ ] **Cross-links** name sibling doctrines + the instantiating project-doctrine.
- [ ] The `doctrine-lint` gate is green on the file (`make check` runs it).

## Cross-links

Instantiated by every file in [doctrine/code/](code/) (validation, interface,
env, cli, registries, documentation). Authored through the `/mate-doctrine` skill
(`new` scaffolds this shape, `update` revises to it); the `doctrine-lint` gate
checks it. Sibling
meta-concern: the validation doctrine ([code/validation.md](code/validation.md)) —
`doctrine-lint` is itself a gate authored to that standard (shape drift is a
validatable dimension).
