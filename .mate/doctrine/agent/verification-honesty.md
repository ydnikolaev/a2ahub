<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Verification-honesty doctrine — verify before you conclude, claim only what you checked

> **Thesis.** An agent builds on claims, and a claim you build on must rest on
> checked ground **in proportion to how far you build on it**. For a claim about a
> system you don't own — a library, a provider, a version, an API — the ground is
> that system's own current source of truth, not your memory of it; and you report
> the claim only at the strength you actually verified. This is the **per-action
> half of validation** (the machine half — gates — is [validation](../code/validation.md)):
> validation offloads invariants to machines forever; this offloads *nothing to
> confidence*. Memory ages, summaries distort, and "I'm fairly sure" is how a wrong
> fact ships fleet-wide wearing a true fact's clothes.

## 0. When to apply (and when not)

Not paranoia over every line. The trigger is a **claim you build on**: an assertion
about an external system (a library's API, a provider's behavior, a version's
semantics, a tool's flags), or a far-reaching claim that code, a design, or a
release now rests on. Those earn a check against the source *before* you conclude.
Internal code you can read yourself is the trivial tier — read it, don't guess, but
you needn't cite an external source for your own function. Scale the rigor to the
blast radius: a throwaway script's assumption is cheap to get wrong; a claim that
becomes a shipped artifact's behavior is not. A small project applies §1
(ground-in-primary-source) and §4 (claim-at-verified-strength) and can skip the
rest; a harness that ships claims to many consumers applies all five.

## 1. Ground a claim about an external system in its primary source, not memory

**A statement about a system you don't own stands on that system's own current
documentation or an empirical check — never on recall alone.** Training-time
knowledge is a snapshot that silently ages; the API you remember may have moved, and
the version in front of you may not be the version you learned. A secondary summary
(a search result, a blog, a forum answer) is a *report about* the source, one
paraphrase removed and free to be stale or wrong. Go to the source itself: the
current official docs for the version in play, or an experiment that makes the system
answer for itself. The check is cheap; a wrong external fact baked into an artifact
is not.

## 2. When sources disagree, read the ground truth, not the convenient proxy

**Where evidence conflicts, weight the most authoritative artifact — the one the
system actually runs — over the most convenient mention.** A parser, a schema, a type
definition, or the spec that governs behavior beats a template, a sample, or prose
that merely describes it. The convenient reference is the one you find first and the
one that most flatters the conclusion you already want; that is exactly why it must
yield to the artifact that decides behavior at runtime. If the deciding artifact is
out of reach, say the state is *contested* — do not promote the agreeable proxy to
"confirmed".

## 3. Verify before you build, not after you ship

**The check belongs before the conclusion it licenses, not after the artifact is
out.** Verifying a claim only once it has failed downstream inverts the cost: the
error is now embedded, propagated, and expensive to unwind, when it was cheap to
catch at the point of assertion. Treat "I'll confirm this later" as a decision to
ship unverified. *(Sibling: the agent-doctrine `advisor-before-substantive` applies
this same before-not-after posture to consulting a stronger reviewer; this doctrine
governs the claim, that one governs the counsel.)*

## 4. Report a claim at the strength you actually verified

**The words you attach to a claim — "confirmed", "proven", "tested", "verified" —
must match what you actually ran, not what feels plausible.** A happy-path run is not
"proven"; a provisional call is not settled; "the docs say" is not "I remember". Name
degradation and uncertainty honestly rather than rounding them up to success. A gate
can prove that bytes moved; it cannot see whether a *sentence claims more than the
evidence* — that overclaim is invisible to every machine check and survives exactly
because it reads as confident. Downgrading a claim to match its evidence is not
weakness; it is the only honest signal.

## 5. A correction can overshoot — land on the source, not the opposite claim

**When you revise a claim you got wrong, land on what the primary source shows — not
on the convenient opposite.** A refuted "X is false" has a strong pull toward "X is
true", and swinging to the mirror-image overclaim is the same error with the sign
flipped. Re-anchor on the deciding artifact (§2), and when a capability is still
unconfirmed, **prefer the claim that under-delivers safely** — do not assert a
capability works — over the one that asserts it and might be wrong. Shipping "this is
unsupported, degraded gracefully" when unsure costs a missed feature; shipping "this
works" when it doesn't costs a false promise that propagates.

## 6. Instantiation seam

What each stack/project swaps into these neutral principles:

- **The primary-source tool** (§1) — how *this* environment reaches current
  authoritative docs: a docs-retrieval MCP (e.g. Context7), the vendor's official
  documentation URL, `man`/`--help`, or the source repository. The principle is
  "current official source"; *which* one is a project value.
- **The empirical check** (§1/§2) — how *this* stack makes a system answer for
  itself: a REPL, a scratch test, a probe command, a type-checker run. The mechanism
  is stack-specific; the stance ("make it demonstrate, don't recall") is not.
- **The deciding artifact** (§2) — what counts as ground truth here: a parser/loader,
  a JSON/DB schema, a `.d.ts`, an OpenAPI spec, a config validator.
- **The claim-strength vocabulary** (§4) — the project's words for verified vs
  provisional (a "confirmed/contested/assumed" tag, a review label, a doc status).

## 7. Anti-patterns

| ❌ | ✅ |
|---|---|
| Asserting an external API/flag/version from memory | Check the system's current official docs or make it demonstrate the behavior |
| Trusting a secondary summary (search result, blog) as the fact | Treat it as a pointer; confirm against the primary source before building on it |
| Taking the convenient mention when sources conflict | Weight the artifact that governs runtime behavior; call it *contested* if that artifact is out of reach |
| "I'll verify it later" after the code already depends on it | Verify at the point of assertion, before the conclusion is load-bearing |
| "proven / verified" for a happy-path or hand-wave | Claim exactly the strength you ran; name degradation and uncertainty plainly |
| Correcting a wrong claim by swinging to its opposite overclaim | Re-anchor on the source; when unconfirmed, prefer the safely-under-delivering claim |

## 8. Porting checklist

- [ ] The verify-before-conclude reflex (§0/§3) is in the project's always-on rule set.
- [ ] An external-system claim (API, version, provider, flag) is grounded in the system's **current** source or an empirical check, not recall.
- [ ] When evidence conflicts, the **deciding artifact** (parser/schema/spec) outweighs the convenient mention; unresolved state is reported as *contested*.
- [ ] Claim-strength words match what was actually run; provisional and degraded states are named, not rounded up.
- [ ] A correction re-anchors on the source and, when unconfirmed, prefers the safely-under-delivering claim.
- [ ] The instantiation seam (§6) names this project's primary-source tool, empirical-check mechanism, deciding artifacts, and claim-strength vocabulary.

## 9. Cross-links

Sibling machine-half: [validation doctrine](../code/validation.md) — this is the
per-action half it names ("verify by hand once ⇒ a gate you haven't written yet").
Sibling in spirit to [framework-first](../code/framework-first.md): "search the canon
before you invent" is this doctrine applied to a framework's own mechanism — the canon
is a primary source (a cross-tree kinship, not a profile-style refinement). Future
agent-doctrine siblings (§13d of the architecture spec):
`advisor-before-substantive` (§3's posture applied to counsel) and `context-economy`.
Instantiated by a project's always-on rule set + its `.mate/config.yaml` seam values
(§6).
