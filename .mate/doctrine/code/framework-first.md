<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Framework-first doctrine — use the canon, custom code is debt

> **Thesis.** A mature framework already ships a documented, canonical mechanism
> for nearly every concern it owns; reach for that mechanism before writing custom
> code, because every hand-rolled plugin, wrapper, or workaround is a standing
> liability the framework's next version will fight — so removing custom code beats
> adding it, and the default answer to "how do I make it do X" is "the framework
> already does X; find the knob."

## 0. When to apply (and when not)

Apply wherever a framework or library **owns a concern**: a web framework
(routing, rendering, lifecycle, config), an ORM (querying, migrations), a build
tool (bundling, transforms), a test runner (mocking, fixtures). The moment you
reach for a custom plugin, a lifecycle hack, or a low-level escape hatch to force
behavior the framework has an opinion about, this doctrine governs.

Do **not** apply to genuinely novel domain logic the framework has no opinion on —
that is the code you are actually paid to write, and "the framework doesn't do
this" is the correct finding, not a failure to search. A small script with no
framework has nothing to be first about; it skips this doctrine entirely (§0 of the
tiering rule). The doctrine is about *deferring to canon where canon exists*, not
about never writing code.

## 1. Search the canon before you invent

**A documented mechanism exists for most concerns a framework owns — find it
before you write your own.** The transferable principle is the *search order*: the
framework's own docs first, then the library's docs, then existing precedent in
the repo, and only then custom code. Inverting that order — reaching for a custom
solution first and back-filling docs later — is how a codebase accretes wrappers
that each re-solve a solved problem, slightly wrong.

The search is cheap and the framework's answer is almost always more correct than
the first custom idea, because it survived the framework authors' own edge cases.
"I didn't know it could do that" is the normal outcome of searching, not an
embarrassment.

## 2. Prefer the highest-level knob that owns the concern

**When configuration layers stack, configure at the highest layer that owns the
concern — never drop to a lower layer to force behavior the higher one governs.**
Frameworks compose downward (framework > build tool > runtime); the higher layer
exists precisely to give the concern a stable, upgrade-safe surface. Reaching past
it to poke a low-level flag couples your code to an internal the framework is free
to change, and the coupling breaks silently on upgrade.

The tell is a fix that names a low-level internal (a raw runtime define, a bundler
option) to achieve something the framework has a documented top-level option for.
The fix works today and is debt tomorrow.

## 3. Custom code is debt, not precedent

**Each custom plugin, wrapper, or hack is a future liability — an upgrade hazard
and a race-condition surface — and the existence of one never justifies the
next.** Removing custom code is worth more than adding it: the code you delete can
never break on the framework's next release, and the primitive you defer to is
maintained by someone else. Treat "we already have a custom X, so another is
consistent" as the anti-pattern it is — consistency with debt compounds the debt.

This is the load-bearing cultural claim: existing custom code is **debt to retire,
not precedent to extend**. A review that cites a prior workaround as license for a
new one has the arrow backwards.

## 4. Escalate from guessing to diagnosis — the Round-N stop rule

**After a small fixed number of failed speculative fixes, STOP guessing and switch
to diagnosis.** Speculative fixes — defer it, wrap it, patch the symptom — are
cheap to try and expensive in aggregate: each one that "might work" without a
mechanism-level understanding burns time and, in a deploy loop, real money. Set a
threshold (a Round-N count); once you cross it, the next move is not another guess
but a minimal reproduction, the canonical doc read end-to-end, or an upstream
issue. The principle is the *phase change* from patching to understanding, made
explicit so it happens by protocol rather than after the third wasted deploy.

## 5. Instantiation seam

What a stack or project swaps into this frame:

- **The concrete search order** — the actual doc sources for the stack (a web
  framework's docs site, the library reference, the repo's precedent location, the
  agent's doc-fetch tool). The *order* is the principle; the *sources* are the fill.
- **The configuration layering** — the specific "highest-level first" chain for the
  stack (e.g. a meta-framework's top-level config above its build tool above the
  runtime). Named per stack in a `profiles/<stack>/` refinement.
- **The low-level escape hatches to watch** — the concrete raw-flag / timing-hack
  patterns worth grepping for and gating, which differ per stack.
- **The Round-N threshold** — the number and the stop-protocol a project adopts.

Stack refinements (`profiles/<stack>/`) carry the concrete mechanism tables; the
project layer records any documented, ADR-backed deviation. The values, framework
version numbers, incident post-mortems, and tool names live in those layers or in
config — never in this principle.

## 6. Anti-patterns

| ❌ | ✅ |
|---|---|
| Hand-rolled timing hack (defer/microtask/sleep-0) to mask a lifecycle bug | Use the framework's documented lifecycle hook or boundary |
| Dropping to a low-level internal flag to force behavior | Configure at the framework's highest-level option for the concern |
| "We already have a custom wrapper — add another for consistency" | Remove the wrapper; defer to the framework primitive |
| Nth speculative fix with no minimal reproduction | Round-N stop: minimal repro + canonical doc, then decide |
| Citing existing custom code as precedent for more | Treat custom code as debt to retire, not a pattern to extend |
| Writing custom code before searching the framework's docs | Search order: framework docs → library docs → repo → custom |
| Hand-rolling a solved terminal concern (interactive prompt loop, arg parsing) | Use the stack's canonical library for it (its `profiles/<stack>/` names which) |

## 7. Porting checklist

- [ ] The search order (§1) is written into the project's always-on rule set for its stack.
- [ ] For each existing custom plugin/wrapper/hack, the framework mechanism it replaces is named — or its absence is justified (§3).
- [ ] The highest-level configuration layer for each concern is documented, and low-level escape hatches (§2) are grepped for and gated.
- [ ] A Round-N threshold (§4) and a stop-and-diagnose protocol are agreed and written down.
- [ ] Any deliberate deviation from a framework's canonical mechanism is an ADR with a reason, not silent custom code.
- [ ] A `profiles/<stack>/` refinement carries the stack's concrete mechanism table when a second project on that stack exists (rule-of-three).

---

## Cross-links

[validation doctrine](validation.md) (gate the low-level escape
hatches §2 grep — fail-closed on the patterns this doctrine bans) · instantiated
by `profiles/<stack>/` refinements (`profiles/nuxt`, the Nuxt-4/Vue mechanism table;
`profiles/go`, the Go interactive-CLI canon — a stack without a profile falls back to
this doctrine's stack-agnostic body) and by each project's own ADR-backed deviations.
