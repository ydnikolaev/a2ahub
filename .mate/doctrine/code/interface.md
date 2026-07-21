<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Interface doctrine — the uniform agent-facing interface

> **Thesis.** An agnostic skill must operate any project **without per-project
> knowledge**. That is only possible if the project's agent-facing interface — its
> make targets, its operator CLI, its env surface — is *uniform across projects*.
> Uniformity is not cosmetic: it is what lets a synced skill hardcode `make check`
> and be right everywhere. The human stops re-learning each repo; the skill stops
> needing a config lookup. **One vocabulary, learned once, true everywhere.**

This is the keystone that makes every other agnostic skill *simple*. A skill that
must ask "what's the check command here?" is a skill carrying a per-project
branch. Remove the branch by making the interface a contract, not a per-repo
accident.

---

## 0. When to apply (and when not)

Apply the moment a project will be touched by **anything but its own authors** — an
agnostic skill, a shared agent, a teammate onboarding, a CI job written elsewhere. The
ABI's whole value is that a caller who has never seen the repo can act correctly in it;
a project nobody else will ever operate has nothing to gain from a contract with nobody.

The tiering is in the **surface count, not the vocabulary**. A library with no
deployment has no env face and no operator CLI, and it does not need to invent them —
it needs `check`, `fmt`, `gen`, `clean` to mean what they mean everywhere else. Add the
CLI face when a project grows operator verbs (§2); add the env face when it grows
environments (`env.md` §0). What is NOT tiered is the *naming*: a project that has a
gate suite calls it `check`, at any size. The contract is cheap exactly because it is
not negotiable.

Do **not** apply by inventing empty targets to look conformant. A `gen` that regenerates
nothing is worse than no `gen` — it teaches a caller to trust a no-op, which is the exact
lie this doctrine's own SSOT shipped until v0.66.0 (`gen` printed "no generated artifacts
yet" while seven generated pages drifted).

## 1. The make-ABI — the canonical target vocabulary

Every project exposes these verbs from its Makefile, **same name, same meaning**.
This is a mandatory ABI, not a suggestion: a synced skill calls the bare verb and
trusts the contract. *(Sub-scoping for monorepos: the bare verb is the umbrella;
`<verb>-<scope>` narrows — axon's `cd thalamus && make check` is `check-backend`
in spirit.)*

| Target | Contract — exactly what it means | Required when |
|---|---|---|
| **`check`** | the **complete** quality gate — the ceiling a skill trusts before declaring "done"; what CI runs. **Not** a middle tier with a fuller gate above it. | always |
| **`check-<scope>`** | the same gate narrowed to one stack/package for the inner loop | monorepo |
| **`test`** | tests only (no lint/compliance) | always |
| **`lint`** | static analysis only | always |
| **`fmt`** | format in place (idempotent) | always |
| **`gen`** | regenerate **all** derived artifacts (the validation-doctrine "generate" rung) | any generated artifact |
| **`dev`** | run the dev server(s) in place | runnable service |
| **`build`** | production build | buildable |
| **`migrate`** / **`seed`** | apply migrations / seed dev data (idempotent) | has a DB |
| **`clean`** | sweep controllable ephemera | always |
| **`mate-check`** | the **synced-harness lane** of `check` — the drift gate (a managed file edited in place is red) + whatever suite-health gates the harness ships (see below) | always (synced) |
| **`report-epics`** | print this project's work items as the harness's normalized epic contract (JSON on stdout) — the seam a progress report reads progress through | **optional**: a project with machine-readable work items |

**The cardinal rule about `check`: it is the CEILING, not a middle rung.** If a
project wants a faster inner loop, that is `check-fast` or `check-<scope>` *below*
`check` — never a fuller `all-check` *above* it. The moment the real ceiling has a
different name, a skill calling `make check` runs a partial gate and ships a hole.

**An ABI row promises what the harness ships, not what it plans.** `mate-check` is the
one row whose body a *harness* fills, so it is the one row that can lie: until v0.78.0 it
promised four organs — drift, gate-of-gates, a suite wall-clock budget, a retirement scan —
of which mate ships the first two, and the fleet was held to a contract that could not be
implemented. The two economics organs (validation.md's suite economics) are the target's
**growth path**, not its contract: they consume gate-firing telemetry, and a scan run
against a partial history is worse than no scan at all — it retires a gate that never
reported as if it never fired (ADR-005). A consumer whose `mate-check` runs drift alone is
**conformant**. Name a growth path as a growth path; the moment it sits in the contract
column, every consumer's green `check` asserts a suite-health claim nobody checked.

**An OPTIONAL row is how a seam stays uniform without becoming a tax.** `report-epics` is
the first: a project that tracks work items machine-readably implements it and its progress
report gets bars; a project that does not implement it gets a report that **says** it
publishes no epic state. The alternative designs both fail — a *mandatory* row taxes every
consumer with an obligation most cannot meet (a library has no epics), while a *config
pointer* (`epics_cmd: ./scripts/...`) reintroduces the indirection map the next section
forbids. A fixed target name that a project may simply not define keeps the ABI's promise
("the name resolves, always") and the harness's ("no project's schema enters the shared
body"): the target's OUTPUT is the contract; whatever it wraps stays home. The rule
generalizes — an optional row must degrade to a *stated absence*, never to a silent default.

### Convention, not configuration — the Makefile *is* the adapter

There is **no `commands:` indirection map** in `.mate/config.yaml`. A skill
hardcodes `make check`; the ABI guarantees it resolves. A project whose build
system isn't make writes a three-line delegating Makefile:

```make
check: ; pnpm run check      # the Makefile is the universal adapter
gen:   ; pnpm run gen
```

Hardcoding is correct **iff** the ABI is mandatory; the mandatory Makefile makes
it so, with zero runtime indirection. (Indirection would be dead code: a skill
that hardcodes `make check` never reads the override; a skill that reads the
override never hardcodes. You can't have both — so pick the simpler, and let the
Makefile absorb the one non-make outlier if it ever appears. YAGNI until then.)

### The toolchain a target runs on is DECLARED, never ambient

**A target that shells out to an interpreter (`python3`, `node`, `ruby`) must run on a
toolchain the repository declares — never on whatever `PATH` happens to resolve first.** The
ABI's promise is that `make check` means the same thing everywhere; it is void if the *thing
it runs* is whichever interpreter the caller's shell found.

The failure this prevents is not theoretical, and it is not a missing package. On a live
machine, `make` resolved `python3` to one interpreter and an operator's shell resolved it to
another — and only the second had the YAML library the project's scripts import. So a script
**worked when a human ran it and died under `make`**, and the caller that wrapped it read the
traceback as *"this data file is invalid"* — blaming the data for a broken toolchain, in a gate
whose entire job was to tell the truth about the data. (The PATH interpreter turned out to be
broken outright: it could not even build a virtualenv. "Just install the package globally" was
never going to hold, on any machine, for any length of time.)

Three obligations follow, and they are cheap:

- **Declare it.** A repo-local, version-pinned toolchain (a `.venv/`, a lockfile, a pinned
  runtime), created by a bootstrap target — so CI, a fresh clone, and a second machine agree.
- **Resolve it, in order.** Scripts take the declared toolchain first, and only then fall back
  to an ambient one that can demonstrably do the job (not one that merely exists).
- **Fail loudly, with the fix, and distinguish the failure from a data failure.** "No
  interpreter here can read YAML — run `make tools`" is actionable. A traceback that a caller
  can mistake for bad input is worse than a crash: it produces a confident, wrong verdict.

Which toolchain, and how it is pinned, is a **stack value** — it belongs in the project (and,
once a second project needs the same one, in that stack's profile). The *principle* — declared,
resolved in order, loud on failure — is the harness's, because the bug it prevents is the same
in every language.

### axon conformance delta (proof the ABI bites)

Consumer #1 is **not yet conformant** — recorded here, not silently mandated:

| Canonical | axon today | Conformance action |
|---|---|---|
| `check` = ceiling | `all-check` is the ceiling; `check` is a sub-gate (both stacks, no arch invariants) | rename `all-check`→`check`; today's `check`→`check-stacks` |
| `fmt` | `fmt` (backend) / `lint-fix` (web) | alias web `fmt`→`lint-fix` |
| `clean` | `clean-artifacts` | alias `clean`→`clean-artifacts` |
| `gen` | `gen` (web only) | add a root `gen` umbrella |

A non-vacuous doctrine produces a conformance list for its own reference instance.
The `fmt`/`clean`/`gen` rows are cheap aliases; the **`all-check`→`check` row is a
true rename, not an alias** — it rewrites every doc/rule/muscle-memory reference to
`make all-check` and changes what `make check` does for the human typing it daily
(now the heavy cross-module ceiling, not the fast both-stacks gate). Defensible —
the ceiling *should* own the canonical name — but it carries a real retraining
cost, so it's a deliberate migration, tracked as a harness-backlog row, not a
drive-by alias.

---

## 2. The CLI scheme — noun-verb, uniform homes

Every project's operator CLI follows one grammar, **extracted from axon's existing
`axon <command> [subcommand] [flags]`**, not invented:

```
<cli> <noun> <verb> [args] [--flags]
```

- **Nouns** are resources/domains: `dev`, `project`, `db`, `<entity>`. They are
  the *homes* — `<cli> dev *`, `<cli> project *`.
- **Verbs** are a small reused vocabulary, same meaning under every noun:
  `new` · `list` · `start` · `stop` · `restart` · `reload` · `status` · `logs` ·
  `reset` · `validate`. *(axon: `axon dev start|stop|reload|status|logs`,
  `axon project new|scaffold|up|stop|seed|validate`.)*
- **Binary name is per-project** (`axon`); the **shape is uniform**. A skill that
  needs to invoke it reads one key — `.mate/config.yaml: cli_name` — then speaks
  the universal grammar (`$(cli) dev status`). This is the *only* per-project name
  a skill ever learns, and it's a name, not a command structure.
- The CLI is built to **[cli.md](cli.md)**: fail-closed parser (unknown verb →
  abort, never a destructive default — validation-doctrine §2), `--help` at every
  level, exit codes that mean something.

**`reload` ≠ `restart` is a canonical-verb safety contract.** `reload` recycles a
server in place (`respawn-pane`) and preserves the human's session; `restart`
tears it down. Agents may only `reload`. Same verb, same safe semantics, every
project — so the agent-doctrine rule needs no per-project caveat.

---

## 3. Why this is load-bearing for the whole harness

Every agnostic skill in the corpus leans on this contract:

- `/ship`, `/commit`, `/implement` call `make check` — the ABI makes that literal safe.
- `/mate-validator` wires new gates into `make check` — uniform target = uniform wiring.
- `/harness` invokes `$(cli) …` via `cli_name` — uniform grammar = one code path.
- The verification-honesty agent-doctrine ("`make check` before done") is
  *meaningless* if `check` means something different per repo.

Uniform interface is the precondition for "agnostic skill, zero per-project
branch." Break it and every skill regrows the branch this doctrine deletes.

---

## Instantiation seam

What a project swaps into this frame — and the one thing it may not:

- **The target BODIES.** `check` runs *this* project's gates, `fmt` runs *this* project's
  formatter, `gen` regenerates *this* project's derived artifacts. The recipe is entirely
  the project's.
- **The build system underneath.** The ABI says `make check`; it does not say make must do
  the work. A non-make stack writes a thin delegating `Makefile` — the contract is the
  vocabulary a caller can rely on, not the tool behind it.
- **The operator CLI's name and its noun-verb vocabulary** (§2) — `cli_name` plus the
  project's own nouns. The *grammar* is the contract; the words are the project's.
- **The env surface's layout** — behind a config key, never a mandated path (`env.md`).
- **Which faces exist at all** — a library has no CLI face; §0 says that is correct, not a
  gap to paper over.

**What a project may NOT swap: the target NAMES.** `check` is not `test`, not `verify`,
not `all-check`. The name is the entire contract — the moment it is a project value, every
synced skill regrows the per-project branch this doctrine exists to delete, and the
`.mate/config.yaml: commands: {check: …}` indirection that "fixes" it is the same branch
wearing a config key.

---

## Anti-patterns

| ❌ | ✅ |
|---|---|
| `check` is a middle tier; the real gate is `all-check` above it | `check` is the ceiling; faster loops are `check-fast`/`check-<scope>` below |
| `.mate/config.yaml: commands: {check: ...}` indirection | mandatory make-ABI; non-make writes a delegating Makefile |
| Skill branches on "which check command?" | skill hardcodes `make check`; the ABI guarantees it |
| Each project invents its own CLI verbs (`spin-up`, `boot`, `launch`) | the canonical verb set; `start`/`stop`/`reload`/`status` everywhere |
| Agent runs `restart` and kills the human's dev session | agents only `reload` (in-place); `restart` is human-only |
| Hardcoding the CLI *binary name* in a skill | read `cli_name` once; speak the universal grammar |

---

## Porting checklist

- [ ] The Makefile exposes the make-ABI (§1); `check` is the **ceiling**.
- [ ] Non-make builds are wrapped by a delegating Makefile (the adapter).
- [ ] The operator CLI follows `<cli> <noun> <verb>` with the canonical verb set.
- [ ] `.mate/config.yaml: cli_name` is set; no skill hardcodes the binary name.
- [ ] `reload` exists and is in-place; agents are constrained to it.
- [ ] Any divergence from the canonical names is a recorded conformance row, not silent drift.

---

## Cross-links

[cli.md](cli.md) (how to *build* the operator CLI to this shape) ·
[validation.md](validation.md) (`check`/`mate-check` are where gates land;
fail-closed parser is the same principle) · [env.md](env.md) (the env surface is
the third face of the uniform interface) · [structure.md](structure.md) (the
standard homes are the fourth face — where to read and write a project) ·
agent-doctrine `reload-not-restart`.
