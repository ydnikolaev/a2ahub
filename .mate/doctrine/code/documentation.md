<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Documentation doctrine — render the machine, template the human

> **Thesis.** Documentation is never a second source of truth. Every page is one
> of two kinds — a **reference** page *generated* from a machine source
> (manifest, registries, `--help`, frontmatter) and drift-gated, or a **guide**
> hand-written to a fixed human template that *links* to the doctrine for the
> precise statement. Re-describing what a machine already knows is the disease this
> doctrine cures; a doc that drifts from its source is worse than no doc, because it
> radiates false authority.

## 0. When to apply (and when not)

Apply the moment a project has a public surface a human must navigate — CLI verbs,
registries, skills, gates, doctrines. A single-script tool needs a README, not a
handbook; the full mdBook + coverage gates earn their place when the surface is
large enough that a human gets lost or a new verb ships without its page. Tier: a
small project keeps reference-as-README (still generated + drift-gated); a large
one graduates to the handbook (§17). The **never-a-second-SSOT** rule and the
**coverage + dead-link gates** are non-negotiable at every tier — even a README's
command table is a render, not a hand-list.

## 1. Two page classes, two mechanics

**A page is either generated-reference or hand-written-guide — never a blend.**
*Reference* (CLI verbs, registry tables, skill inventory, doctrine index, gate
list, capability matrix) is **generated** from its machine source, banner-stamped
`GENERATED — do not hand-edit`, and drift-gated. *Guides* ("onboard a project",
"push an improvement everywhere", "fix a red drift gate") are **hand-written to a
fixed template** — *What it is / When to use / When NOT to / How / Typical mistakes
/ Links*. The split matters because the two have opposite maintenance: reference is
never touched by hand (regenerate), guides are never generated (they carry
judgment). Blending them produces a page that is both stale and un-editable.

## 2. Never a second SSOT — link, don't re-describe

**A guide explains in human language and links to the doctrine for the precise
statement; a reference page is a render of its machine source.** The precise claim
lives in exactly one place — the doctrine, the registry, the command spec. The
handbook points at it. The instant a page *restates* a rule instead of linking it,
you have two copies with no gate tying them, and the copy will lag. If a human
explanation is worth keeping, it goes in a guide that links down to the
authoritative statement — never a paraphrase that silently forks.

## 3. Coverage and dead links are gated — docs move with the change

**Every public surface must be referenced by the docs, and every link must
resolve — both enforced by gates keyed off the SSOT sets, not hand-lists.** A
coverage gate walks the live surface (manifest skills, the command list, the
registry dir, the doctrine dir, the gate registry) and reddens when a member has
no page — so a new verb *cannot* land without its documentation. A dead-link gate
resolves every internal link. A generated-drift gate re-renders each reference page
and fails when the checked-in copy disagrees. All three are teeth-tested in the ADD
direction (a new surface with no page → coverage red; a hand-edited render → drift
red). Docs move *with* the change, not "later" — later is never.

## 4. Knowledge that must travel is a third genre, and it may not name its origin

**Documentation explains a system to the people who have it; a RECIPE teaches a capability
to someone who does not — and the two cannot be the same document.** A guide is free to name
this project's files, because its reader is standing in this project. A recipe is read by an
agent in a repository that has never heard of this one, and the moment it names a path, a
filename or a product, it silently stops being followable there while still reading perfectly
well to the author. That asymmetry is exactly the reporting doctrine's asymmetry — the reader
who cannot check the document is not in the room — so it gets the same answer: a **gate**,
not a review note. The body names roles; the instances live in an appendix that is marked as
illustration and that the rest of the document stands without.

**Its reader is a machine, and the one section that is not for the machine goes last.** A
recipe is consumed by an agent that will implement — under other constraints, on a stack the
author never used — so the document leads with the payload it implements *from*: the
principles, the ground they need underneath, the sequence, the scars. A human overview placed
first would push all of that down the page to serve a reader who is not the one doing the
work. So the human section **closes** the document, and it earns its place by carrying what
the agent-facing body must not: what this is in plain language, and **the stack it was
originally built on, why that is the recommendation, and what the choice cost** — split into
the *essential* (the design depends on it) and the *incidental* (it came from the author's
house; swap it freely). Which is the line neutrality actually draws: **technologies are free,
coordinates never are.** A technology named as a choice with a rationale is portable — the
reader can weigh it. A path, a filename, a product executes in one repository and transfers to
none. Banning the first would make the recipe unwritable; permitting the second makes it a
manual again.

**And a contract is not a coordinate.** The line above is the one everybody gets wrong on the
first pass, and the correction below is the one they get wrong on the second: a **schema**, a
normalized shape, a declared surface of something external is *portable*. A coordinate
**executes** in one repository; a contract is a shape the reader **copies and adapts**, and it
travels exactly as far as the principle it serves. A transferable document that describes its
interfaces in prose instead of showing them has not protected its neutrality — it has moved
the design work onto every reader, who will each re-derive a different, incompatible shape,
destroying the interoperation the contract existed for. **Show the shapes; strip the
coordinates out of them.**

**And a precondition is a ladder, not a fact.** A transferable document is by definition read
by someone whose ground is not the author's, so a precondition stated flatly ("you need a
machine-readable source of truth") gives the reader who lacks it exactly one honest option:
stop. They will not stop. They will point the capability at the nearest weaker source, and it
will produce confident output over rot. Write each precondition as *probe → build the smallest
version → degrade, with the degradation labelled in the capability's own output*, and say that
there is no fourth rung.

Six consequences worth stating, because each one is a way the genre dies:

- **It is distilled from the record, not from memory.** The principles are in the doctrine,
  the sequence is in the release notes, and the scars are in the fix commits. An author
  writing from recall produces the last week of the build, confidently, and loses the failure
  from three weeks ago that the reader most needs. The machine gathers; the author abstracts.
- **The scars are the payload.** A build with nothing to warn about did not need a recipe.
  And neutrality is about *coordinates*, not about concreteness — stripping "25.3 hours
  inside a 24-hour day" down to "be careful with parallelism" removes the only thing the
  reader could not have derived alone.
- **It is a snapshot, and it says so.** A recipe carries a stamp — whose build, which
  release, when. Doctrine is maintained; a recipe is *earned*, and presenting one as the
  other invites a reader to trust it long after the ground moved.
- **The gate ships where the recipe is written, not where the corpus is kept.** A recipe is
  authored about the repository its author is standing in, which is almost never the one that
  owns the genre. A check only the corpus's home can run is therefore absent at exactly the
  moment it matters — the author names his own paths on every second line, and the document
  reads perfectly *to him*. Put the check in the tool every project already has, and give the
  project a home of its own to write into: a corpus a consumer can only read is a genre it can
  only consume.
- **A payload with no consumption path is not transferable, it is merely portable.** The
  document says what ground the capability needs; it never says what the reader should *do*
  when their ground is missing half of it — and that reader is every reader. The adoption
  protocol (probe, map the roles onto this repository's homes, create or degrade-and-label,
  never substitute silently) is the same text for every document in the genre, so it is
  authored **once** for the corpus and **appended by the delivery step** — not restated by each
  author, which is how a corpus acquires N drifting copies of one paragraph.
- **The return channel is the only way it improves.** A snapshot of one build has exactly one
  source of new truth: someone rebuilding it somewhere else. Ask them, in the document, for the
  stack, what they had to degrade, what they had to invent because a shape was described rather
  than shown, and — the payload — the **new scars**. What comes back is not feedback to file;
  it is the next release. A genre with no return channel has one author forever, and its scars
  stop at the ones he was unlucky enough to hit himself.

## Instantiation seam

What each project swaps into the documentation frame: the **surface sets** the
coverage gate walks (which registries, which command source, where skills live),
the **generator** per reference page (manifest → skill inventory, cobra/`commands.yaml`
→ CLI reference, registries → tables), the **toolchain** (mdBook for the large
tier; a generated README for the small), and the **guide topics** the project
needs first. The two classes, the never-a-second-SSOT rule, and the three gates are
invariant; the sources and toolchain are the fill. The **canonical docs skeleton**
(standard homes — backlog, audits, features, doctrine — Δ8) ships as
`mode:template` scaffolding so a new consumer inherits the layout without it being
enforced-identical.

## Anti-patterns

| ❌ | ✅ |
|---|---|
| A reference page hand-maintained from a list | Generated from the machine source, banner-stamped, drift-gated |
| A guide that restates a doctrine's rule in its own words | The guide links down to the doctrine; the statement lives once |
| A new CLI verb shipped without a page | Coverage gate keyed off the command set reddens until the page exists |
| Coverage gate checking a frozen hand-list of surfaces | Gate walks the live SSOT set; teeth-test the ADD direction |
| Internal links rot silently | Dead-link gate resolves every link in CI |
| "We'll document it after the release" | Docs move with the change — the gate makes "after" impossible |

## Porting checklist

- [ ] Every page is classified: generated-reference **or** hand-written-guide.
- [ ] Reference pages are generated, banner-stamped, and drift-gated.
- [ ] Guides follow the fixed template and **link** to the doctrine — never restate it.
- [ ] A **coverage** gate keyed off the SSOT surface sets reddens on an undocumented member.
- [ ] A **dead-link** gate resolves every internal link.
- [ ] A **generated-drift** gate fails when a render disagrees with its source.
- [ ] All three gates are teeth-tested in the **ADD direction**.
- [ ] The canonical docs skeleton (Δ8) ships as `mode:template`, not enforced-identical.

## Cross-links

Instantiates [validation.md](validation.md) (coverage/dead-link/drift are gates to
its standard) and [registries.md](registries.md) (reference pages render from
registries — render-don't-repeat). Sibling: [interface.md](interface.md) (the CLI
`--help` surface is both an interface and a doc source). Architecture spec §17 is
the handbook design this doctrine generalizes; the project-doctrine that
instantiates it is the project's `handbook/` (or README) + its `mate docs` wiring.
