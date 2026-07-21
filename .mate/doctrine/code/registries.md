<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Registries doctrine — everything enumerable is a registry

> **Thesis.** If a fact can be maintained as a list, it lives in exactly one
> schema'd, machine-readable YAML registry in its owning layer — prose *references*
> it, human surfaces are *generated* from it, and nothing re-enumerates it by hand.
> This is the validation doctrine's prevention hierarchy (generate + drift-gate)
> pointed at lists: the naming chaos, the stale inventory, and the "ports collide at
> 11pm" failure class all die at the registry.

## 0. When to apply (and when not)

The moment a fact exists in **two places** — or would be hand-copied into a doc,
a render, or a second file — it is a registry candidate. A three-project operator
needs `projects` and `ports`; a solo greenfield repo may need none on day one.
Apply when: the list has more than a couple of entries, it is referenced from more
than one place, or a machine could *detect* its members (then §1 mandates a
registry). Do **not** invent a registry for a two-item constant that only one file
reads — that is a `const`, not a registry. The test is enumeration-with-consumers,
not mere listing.

## 1. Probe > declare — the prevention hierarchy for facts

**Any fact a machine can detect is probed, never remembered.** OS, arch,
runtime versions, a tool's install/auth state, a server's reachability — these are
written by a probe (`mate profile refresh`, `mate doctor`), never typed by an
agent. Declaration is reserved for what a machine *cannot* detect: constraints,
intents, scars. Split the two explicitly in the schema (`probed:` vs `declared:`),
so a refresh overwrites the former and never touches the latter. A remembered fact
is a fact that silently goes stale; a probed fact is correct by construction — the
same rung-1/rung-2 offload the validation doctrine preaches, applied to the
environment.

## 2. Render, don't repeat — human surfaces are generated

**Every human-facing surface derived from a registry is generated from it,
banner-stamped, and drift-gated — never hand-maintained.** The operator fragment
in `CLAUDE.md`/`AGENTS.md` renders from `machines.yaml`; the handbook reference
tables render from the manifest + registries; `--help` and shell completions
render from the command definitions. Anything an agent or human used to copy out
of a list by hand is a rung-2 candidate: generate it, stamp it `GENERATED — do not
hand-edit`, and let a `*-drift` gate fail CI when the render disagrees with its
source. A hand-maintained render is a second SSOT waiting to diverge.

## 3. Registry coherence is gated — `registry lint`

**Every registry parses its schema and every cross-registry reference resolves,
checked by a gate, not by hope.** A project's `servers:` entry must exist in the
machines registry; a ports block must name a registered project; a `key:` pointer
must be a reference scheme (`keychain:`/`age:`/…) and never a secret value.
Registry coherence is a row in the validatable-dimension taxonomy
([validation.md](validation.md)); `mate registry lint` is its gate, teeth-tested
in the ADD direction (a new dangling reference reddens). A registry with no lint
is prose with a `.yaml` extension.

## Instantiation seam

What each project/operator swaps into the registry frame: the **set of
registries** it owns (SSOT layer: `projects`; operator layer: `tools`, `ports`,
`machines`, `servers`; SSOT knowledge layer: `knowledge-kinds`), each registry's
**schema** (the fields that layer needs), the **probes** that fill `probed:`
blocks, and the **renders** each registry feeds. The three rules (probe>declare,
render-don't-repeat, lint) are invariant; the registry set and schemas are the
per-layer fill. A new enumerable domain is a new registry, authored to this same
frame — not an exception.

## Anti-patterns

| ❌ | ✅ |
|---|---|
| A hand-typed inventory an agent updates from memory | `probed:` block written by a probe; `declared:` only for the undetectable |
| A doc table listing the same facts the registry holds | The table is a `GENERATED` render of the registry, drift-gated |
| A registry with no schema lint | `mate registry lint` parses the schema + resolves cross-refs, teeth-tested |
| A cross-registry reference (ports→project) checked by eye | The lint fails on a dangling reference in the ADD direction |
| A secret value in a `key:` field | A pointer (`keychain:`/`age:`/`env:`) — never the value |
| Re-enumerating a list in a second file "for convenience" | One SSOT registry; the second place references or renders it |

## Porting checklist

- [ ] Every enumerable fact with consumers lives in one schema'd YAML registry.
- [ ] Detectable facts are `probed:`; only the undetectable is `declared:`.
- [ ] Every human surface derived from a registry is a **generated**, banner-stamped, drift-gated render.
- [ ] `registry lint` parses each schema **and** resolves every cross-registry reference.
- [ ] The lint is teeth-tested in the **ADD direction** (a new dangling ref reddens).
- [ ] No `key:` field holds a secret value — only a pointer.

## Cross-links

Instantiates [validation.md](validation.md) (registries are prevention-hierarchy
rungs 2+3 applied to lists; coherence is a taxonomy dimension). Feeds
[documentation.md](documentation.md) (handbook reference pages render from
registries) and [interface.md](interface.md) (`--help`/completions render from the
command registry). Architecture spec §15 is the registry catalogue this doctrine
generalizes; the project-doctrine that instantiates it is each layer's registry
set under `registry/`.
