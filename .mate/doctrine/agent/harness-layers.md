<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Harness-layers doctrine — know whose surface you are touching before you touch it

> **Thesis.** A project working under a synced harness holds **two kinds of surface
> in one tree**: artifacts it *owns* (its native skills, rules, gates, config) and
> artifacts it merely *holds* — provenance-stamped copies whose source of truth is
> upstream. Every harness change starts with one classification: **whose is this,
> and where does the fix belong?** Three disciplines follow: never edit a managed
> copy (the byte-edit is always upstream), classify before placing (a portable
> principle promotes up; a project value stays home — and *refusing to promote* is
> the answer that keeps the shared layer alive), and split principle from value
> (the transferable claim goes into the shared artifact; the project's paths,
> commands, and ids go into its config). This is an **agent-process discipline** —
> a sibling of [commit-hygiene](commit-hygiene.md) and
> [verification-honesty](verification-honesty.md): it governs how the agent
> conducts harness work, not the quality of a shipped artifact. Get it wrong and
> the failure is structural: an edited managed copy forks the source of truth and
> is silently clobbered on the next sync; a project value promoted upstream leaks
> into every other consumer as someone else's noise.

## 0. When to apply (and when not)

The trigger is **touching any harness artifact** — a skill, an always-on rule, a
gate, a doctrine, the harness config — in a repo that syncs part of its harness
from a shared source. That includes the ambient case: a task that ends "...and
keep this for next time" is a harness change, whether or not anyone said the word.
It applies in every consumer of a shared harness and in the shared source itself
(where the same classification decides *which layer inside it* owns the change). A
repo whose harness is entirely its own — nothing synced, no lock — has only one
layer and needs no classification; and *reading* a managed artifact is always
free. The discipline gates **writes**.

## 1. Never edit a managed copy — the byte-edit is always upstream

**A managed artifact carries a provenance mark and an entry in the sync lock;
treat it as read-only in the consumer, no matter how small the fix.** An edit to
the local copy is not a shortcut — it is a fork of the source of truth: the drift
gate reddens, the next sync either refuses or overwrites, and every other consumer
still has the bug. The fix travels the long way *because* that is what makes it
land everywhere: author in the upstream source, release, sync back down. If you
must prototype, prototype in a checkout of the upstream itself — there is no
sanctioned "edit the locked copy for now" mode; a red drift gate is the system
catching the fork, not an obstacle to route around. The only honest local edit is
in the artifacts the project *owns* — which is what the classification below is
for.

## 2. Classify before you place — and refuse to promote by default

**Before writing, sort the change into exactly one layer: shared-core (every
consumer benefits), shared-profile (one stack facet benefits), operator (the
human's machines and homes), or project-only — and when unsure, it is
project-only.** A project-only change lives in the project: a native skill, a
project rule, an entry in its own config — under the project's own name, beside
the synced artifacts, never inside them. Only a change whose *second consumer you
can name* earns promotion, and promotion is a procedure (classify → neutralize →
author upstream → release), not a copy. The refusal is load-bearing: a shared
layer that accepts everything becomes a dumping ground that every consumer pays
for on every sync; "this is project-only, it stays here" protects the commons.
The inverse holds too — a fix to a *shared* artifact that you keep local is theft
from the fleet: it belongs upstream even though keeping it local is easier today.

## 3. Principle into the artifact, value into the config

**A shared artifact's body carries only what is true for every consumer; anything
that varies per project — paths, commands, thresholds, vocabularies, hosts, ids —
goes into the project's config seam, where the shared artifact reads it.** A body
that names one project's directory layout or gate command is not yet shareable,
whatever its intent; neutralizing is the act of splitting the transferable claim
from the local instantiation. The same rule read backwards places project values:
they go into the config *seam* the shared artifacts already read — not into a
fork of the shared body, and not into a parallel structure the harness cannot
see. When a shared skill defers to "the project's convention", the config seam is
where that convention lives.

## 4. The layer you do not own — the provider

Above the layers you author sits one you cannot: the **provider** (the agent runtime
your artifacts are loaded by). It has its own release schedule, its own powers, and
its own limits, and it will change all three without telling you. Three rules follow,
and they are not optional:

**Read the current canon before you design — memory is not a source.** A claim about
what the provider can do rests on the provider's own docs *today* plus a search of the
problem, checked at the moment you design, not recalled. A false negative here is the
expensive kind: believing a provider *cannot* do something buys an architecture built
to route around a wall that isn't there. That is not hypothetical — it is how this
harness came to carry a rule-routing design premised on a provider having no hook
channel, months after that provider had shipped one.

**Write down what you verified, in data, with a stamp.** A provider fact in a prose
comment rots silently on someone else's release schedule: nothing compares it to
reality, so nothing can ever go red. The same fact in a registry, carrying *which
build it was checked against*, can be gated — and the gate's verdict is not "this is
wrong" but "this is **unverified**", which is the exact state that produces the
failure. Never bump the stamp to silence the gate; the stamp *is* the claim that
someone looked.

**Branch on the declared surface, never on the provider's name.** *Provider-conditional
⇒ surface-conditional.* A name literal in the engine freezes a provider fact where no
adapter can correct it, and it lets two code paths decide one question by two different
means — the divergence is invisible, because both compile and both pass. Ask the
surface: does it have a rules home, does its entry file follow imports, will it truncate.
Where a literal genuinely must remain — a **default** (which provider an unset flag
means) or one provider's own **content shape** (a settings file is not a neutral body
wearing that provider's hat; it *is* that provider) — it carries a written reason on the
line, not a silence. And a fact nobody branches on has no business in the surface: a
field lands with its reader.

## 5. Instantiation seam

The doctrine names the layers; the sync tool names the mechanics. Under mate:
the provenance mark is the `SYNCED FROM mate@…` banner (or `mate_synced:`
frontmatter) and the lock is `.mate/lock.json`; the config seam is
`.mate/config.yaml`; the drift gate is `mate status` (a project typically wires
it into its check ceiling as `make mate-check` — recommended wiring, not shipped). The
up-route is `mate promote "<finding>"` (files it) and the `/mate-promote` skill
(classifies core / profile / operator / project-only — and refuses honestly);
authoring upstream goes through `/mate-doctrine` for doctrine and
`/mate-validator` for gates. The down-route is a release + `mate pull`. A
same-name collision between a synced and a native artifact fails closed
(report-not-clobber) — resolution is a decision, not an overwrite.

## 6. Anti-patterns

- **The convenient fork.** "I'll just fix the synced copy here and promote it
  later" — later never comes; the next pull eats the fix or the drift gate blocks
  the team. The long way is the only way that lands.
- **The eager promotion.** Lifting a project artifact upstream verbatim because
  it *might* help someone — its paths and vocabulary ride along, and every
  consumer now carries one project's noise. No named second consumer, no
  promotion.
- **The shadow harness.** Keeping "our real rules" in a parallel structure the
  sync tool doesn't manage, because touching the managed one feels risky. Now two
  sources of truth compete and the agent reads the wrong one.
- **The baked value.** A shared body that names one project's gate command or
  coverage floor — works in the first consumer, breaks or misleads in the second.
  The second consumer's failure was decided at authoring time.

## 7. Porting checklist

Adopting this doctrine under a different sync tool needs: (1) a **provenance
mark** distinguishing managed from owned files, and a **lock/manifest** the drift
check reads; (2) a **config seam** — one project-owned file the shared artifacts
read values from; (3) an **up-route** with an explicit classify step whose default
answer is *no*; (4) a **fail-closed collision policy** for same-name artifacts.
Without (1) the classification has nothing to stand on — build that first.

## 8. Cross-links

- [commit-hygiene](commit-hygiene.md) — the sibling process discipline; a harness
  change still commits session-isolated and atomic.
- [verification-honesty](verification-honesty.md) — classify against the real
  artifact (read the stamp, check the lock), not memory of who owns what.
- `validation.md` (code/) — the drift gate is a validation gate; this doctrine is
  why its red is a feature.
- The `mate-promote` and `mate-adopt` skills instantiate the up-route and the
  conformance path; the architecture spec (§5d, §6) carries the full sync
  contract.
