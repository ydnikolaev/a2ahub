<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Structure doctrine — one project shape, so nothing has to be searched for

> **Thesis.** An agent (or human) arriving at any project should already know
> where everything lives: what is in flight, what is queued, where decisions are
> recorded, where a spec goes, where ephemera may be written. That is only
> possible if every project shares **one standard set of homes with standard
> default locations** — overridable through the config seam, but never silently
> absent. The structure is the **fourth face of the uniform interface** (after
> the make-ABI, the operator CLI, and the env layout): the make-ABI answers "how
> do I check this project?", the structure answers "where do I read and write
> it?". Homes are **template-seeded, not enforced-identical** — a project owns
> its content and internal shape; what the standard fixes is each home's *role*
> and *default location*, so a skill written once works everywhere.

## 0. When to apply (and when not)

The trigger is **placing anything that outlives the current session** — a status
note, a backlog item, a decision, a spec, an audit report, a generated artifact —
or **looking for one**. It applies to every project under the shared harness,
including the harness SSOT itself (which must be the first conformant example,
not an exception). A throwaway script or scratch repo carries no docs surface
and needs none (the same §0 tiering as the validation doctrine); and a home with
no content yet is legitimately empty — the standard mandates *where things go*,
not that things exist.

## 1. Standard homes, standard defaults — the config seam carries the exception

**Every durable artifact kind has exactly one standard home with a standard
default path; a project may relocate a home through its config seam, and a
consumer of the standard (a skill, a gate, a human) resolves the seam first and
falls back to the default — never to a guess.** The standard set:

| home | default | role |
|---|---|---|
| status | `docs/status.md` | what is in flight / recently shipped — the first orientation read |
| backlog | `docs/backlog.md` | the open-items queue (paired archive file when it grows) |
| validator_backlog | `docs/validator-backlog.md` | the proposed-gate queue the validation doctrine (§7) mandates |
| decisions | `docs/decisions.md` | the decision/ADR log — why things are the way they are |
| architecture | `docs/architecture/` | system documentation: specs, contracts, diagrams |
| features | `docs/features/` | one dir per feature (`<slug>/`): the spec and its satellites |
| audits | `docs/audits/` | audit reports, review ledgers, deferred-items lists |
| operations | `docs/operations/` | runbooks: deploy, setup, wiring |
| inbox | `docs/inbox/` | intake — items filed by tools or people, awaiting triage |
| reports | `docs/reports/` | one dir per day (`<dd.mm.yyyy>/`): the derived progress report and the facts it was derived from |
| presentations | `docs/presentations/` | standalone doc-presentations (`<name>.html`): self-contained pages written for a human reader |
| artifacts | `.artifacts/` | controllable ephemera (logs, coverage, exports) — one subdir per producer (`<producer>/`), swept by a single cleanup target |
| harness | the sync tool's managed dirs (`.mate/` + provider dirs under mate) | the synced surface — owned upstream, drift-gated, never a docs home |

A skill that reads or writes one of these names the home by **role**, resolves
the project's `paths:` override, and defaults to the table — so the same skill
body works in every project without carrying any project's tree.

**The vocabulary is closed, and the seam fails closed on anything outside it.** A
`paths:` key that is not in the table is refused, loudly, naming the table. This
is not pedantry about spelling: the failure it prevents is the **inert seam** — a
key that *looks* authoritative, is parsed by nobody, and answers to nobody. An
unbuilt seam merely makes a skill fall back to the default and be right; an inert
one makes it **confidently wrong**. (Learned the hard way: a consumer carried a
`doctrine:` key pointing at a placement the harness had long since moved, so every skill that
dutifully "resolved the seam first" — exactly as this section instructs — was
sent to a directory that no longer existed. Nothing in the table above is
optional to validate.) Note what that example also shows: the **harness home is
not a project home** (§1, last row). Where the synced corpus lands is the sync
tool's business, not a path a project may relocate — it never belongs in `paths:`.

**Declaring a home is an assertion, and the seam is checked against reality, not
just against the vocabulary.** A closed vocabulary only shuts half the door: a
*known* key pointing at a path that isn't there still resolves, and the skill that
trusts it still goes nowhere. So a declared home must **exist** — but only where
absence actually means a lie, or the check becomes a gate that cries wolf:

| the home is… | may it be absent? | why |
|---|---|---|
| **read-resolved** — a skill or doctrine tells an agent to *look* there | **no** | the declaration is the project saying "it lives HERE"; if it doesn't, every agent that resolves the seam is sent to a dead path while every other gate stays green |
| **seeded** — the harness creates it at whatever path the project declares | yes | the declaration cannot be *confidently wrong*: the next sync lays the file down at exactly the declared path |
| **ephemeral** — the cleanup target deletes it by design | yes | absence **is** the clean state; a gate demanding it exist reds on a freshly-cleaned tree |

An **undeclared** home is never held to this: the default is a *fallback*, not a
claim, and a project with no inbox has an empty inbox, not a lie. Hence the rule
that carries: **do not declare a home you do not have** — the declaration is the
thing an agent trusts, and the fallback is the one answer that is never wrong.

## 2. Homes are template-seeded, never enforced-identical

**The standard lays a home once (create-if-absent) and then the project owns
it.** A home is scaffolding, not a synced artifact: no drift gate on its
content, no overwrite on update, no prune on removal — the inverse of the
harness surface. What IS checkable mechanically: that a declared-or-default home
*exists* where the seam says it does (a conformance *report*, the adoption
path's job — not a red gate on every commit; an empty project should not fail CI
for having no audits yet). The project's internal shape inside a home — status
sections, spec templates, epic trackers, domain maps — is project value: it may
be far richer than the standard, and the standard never flattens it.

## 3. Ephemera never mix with durable artifacts

**Anything regenerable — logs, coverage output, build exports, scratch files —
goes under the artifacts home, one subdir per producer, and a single cleanup
target removes it all without touching anything durable.** The moment ephemera
scatter (a log beside source, coverage in the package dir, an export in docs/),
two failures follow: cleanup becomes a judgment call that nobody makes, and the
durable tree stops being trustworthy (is this file a record or a leftover?). The
inverse holds too: nothing durable may live under the artifacts home — it must
survive `clean` by definition.

## 4. Instantiation seam

What each project swaps: the `paths:` map in its config seam (`.mate/config.yaml`
under mate — keys like `backlog`, `features`, `audits`, `artifacts` override the
defaults); the internal shape of each home (status sections, spec/epic template,
ADR format, audit report format); the cleanup target's wiring (`make
clean-artifacts` or the project's equivalent); which optional homes exist at all
(a library may never grow `operations/`). Under mate, the skeleton is seeded by
`mate new` / the first pull (template mode) and the adoption path reports the
delta between the standard table and the repo's reality.

## 5. Anti-patterns

| ❌ Anti-pattern | ✅ Instead |
|---|---|
| **The searched-for home** — a skill greps the tree to find "where specs live here" | nothing durable is *found*, only *resolved*: seam → default |
| **The enforced home** — drift-gating a project's own status file or spec dir | the standard owns the *location*, the project owns the *content*; gating content turns scaffolding into a cage |
| **The private layout** — "our real docs" in a parallel tree because the standard homes feel foreign | relocate the home through the seam; that is what it is for |
| **The scattered ephemeral** — logs/coverage sprinkled through the source tree, each with ad-hoc cleanup | §3: one artifacts home, one sweep |
| **The status-bloated brief** — release history accumulating in the always-on entry file | orientation content lives in `status`/`decisions`, loaded on demand; the brief stays lean |

## 6. Porting checklist

- [ ] The **homes table** — role → default path — published where every skill can cite it.
- [ ] A **config seam** whose `paths:` keys override the defaults per project (without it the standard becomes a cage — §5).
- [ ] A **seeding mechanism** that lays missing homes once, never overwriting or gating them (without it the standard becomes a suggestion).
- [ ] A **conformance report** in the adoption path — exists-where-declared, never a hard per-commit gate.
- [ ] A **cleanup target** wired to the artifacts home.

## 7. Cross-links

- [validation.md](validation.md) — placement decisions *trigger* gates: a new
  home or entity placed per this doctrine is a checkable surface the validator
  owes a gate for; the drift gate guards the harness surface, never the docs
  homes (§2).
- [documentation.md](documentation.md) — what goes *inside* the docs homes
  (generated reference vs hand-written guides, the three documentation gates);
  this doctrine owns *where*, that one owns *what and how checked*.
- [interface.md](interface.md) — the sibling faces of the uniform interface
  (make-ABI, CLI, env); this doctrine adds the fourth.
- [agent/harness-layers.md](../agent/harness-layers.md) — the harness home
  (`.mate/` + provider dirs) is managed surface, not a docs home; its rules are
  that doctrine's.
