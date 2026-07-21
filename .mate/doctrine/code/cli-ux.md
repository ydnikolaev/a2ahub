<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Interactive CLI-UX doctrine — the human's side of the command

> **Thesis.** A command that a person runs interactively is a *conversation*, and a
> good conversation asks the fewest questions, never asks what it can find out, offers
> a choice instead of demanding a spelling, defaults the near-certain, and shows the
> user what it is about to do before it does it — while the SAME command, run by a
> machine, takes every one of those answers as a flag and never blocks on a prompt.
> Interactive convenience and headless automation are not a trade-off; they are two
> front doors onto one set of values, and a CLI that is excellent has both.

> **Scope.** This doctrine is the *human-input* face of a CLI — the wizard/prompt/
> confirm layer. It is the companion to the **cli** doctrine (which governs command
> *architecture* — dispatch, flags, safety, audit) and is instantiated per stack by a
> `profiles/<stack>/` refinement that names the concrete forms library. The principle
> is stack-agnostic; the library is a value.

## 0. When to apply (and when not)

Apply the moment a command has an **interactive surface** — it asks a person for a
value, a choice, or a confirmation. The five principles below govern that surface.

Do **not** read this as "add a wizard to everything": most commands are pure
flags/args/stdin and should stay that way (the **cli** doctrine's §0 tiering — a
3-command tool needs no interactive layer). And do **not** apply the interactive
principles to the *machine* path: a value an agent or CI supplies belongs on a flag or
env var, never behind a prompt. This doctrine is about making the *human* path
excellent **without** compromising the headless one — §4 is the hinge.

## 1. Resolve, don't ask

**A value the tool can discover, it resolves — and remembers — rather than
prompting for.** The working directory, the repository name, a path the environment
already declares, a default set the tool ships: none of these is the human's to retype.
Every prompt for a knowable value is a small insult to the operator ("you already know
this — why are you asking me?"). Resolve from the obvious sources in a fixed order,
persist the resolution where the tool will find it next time, and prompt **only** for
what is genuinely unknowable. The tell that this principle is being violated: a form
field whose answer the tool is standing inside of.

The corollary is **don't mint a second source of truth to "remember" a fact that
already has one.** If an environment variable or an existing config already carries the
value, read *that*; a new dotfile pointing at the same fact is drift, not memory.

## 2. Select, don't type

**A field whose values are known or enumerable is a selection, never free text.**
Typing invites typos, demands the user recall the exact spelling, and turns a
one-keystroke choice into a copy-paste hunt. A closed or curated vocabulary — a set of
providers, a set of environments, a set of stack facets — is a single-select, a
multi-select, or a confirm. Free text is reserved for the genuinely open: a name, a
message, a path with no prior. The vocabulary the selection reads is itself an
**enumerable registry** (the harness's "everything enumerable is a registry" rule), so
a new valid value appears in the picker the moment it is registered — no code edit, no
stale hardcoded list.

The rare value outside the vocabulary is served by the headless path (a flag accepts
any string) — it does not justify degrading the interactive path back to free text for
the 99% case.

## 3. Sane defaults, minimal prompts

**Every prompt must earn its place; a question whose answer is "almost always X"
becomes a default with an opt-out, not a question.** The cost of a prompt is not the
keystroke — it is the decision it forces onto the user, most of whom will pick the same
answer every time. Pre-fill each field with the overwhelmingly likely value (the repo
basename, the standard provider set, "yes, claim the resource"), let the user override
when they care, and remove outright any prompt whose "no" branch is a rare edge the
operator can reach with a flag. A wizard that asks eight questions to which the answer
is seven defaults and one real choice has seven prompts too many.

## 4. Two first-class front doors — human TUI, agent headless

**The interactive form and the headless flag path are the same command with two
faces, and neither is a second-class citizen.** A human runs the bare command and gets
a guided TUI; an agent or CI passes `--headless` (or the equivalent) and gets a
non-interactive run that reads flags and env only. Two contracts bind them:

- **The form only ever pre-fills flags — it is never a second source of truth.** Any
  value a flag can set, the form seeds from that flag and writes back the same way, so
  the two paths can never diverge on what a command *means*.
- **The headless path never blocks on input that will not arrive.** Detect a
  non-interactive stdin *before* constructing any prompt and take the flag/env path or
  error cleanly — a form that hangs waiting for a TTY that isn't there is the single
  worst failure of an "automatable" CLI, because it wedges the very agent it was meant
  to serve. (An accessibility mode for screen readers is still interactive — it is not
  the headless path.)

## 5. Confirm before mutation

**A command that writes shows the user what it is about to do and waits for a yes —
once, at the end, as a reviewable summary.** The confirmation is not a nag on every
field; it is a single gate before the irreversible step, rendering the collected
decisions (what will be created, changed, deleted, where) so the operator approves the
*outcome*, not each keystroke. The headless path replaces this human gate with an
explicit opt-in flag (`--yes`/`--apply`) and a dry-run default for the dangerous case —
same safety posture (the **cli** doctrine's §6), reached the machine way.

## 6. Instantiation seam

What a stack or project swaps into this frame — each is a *value*:

- **The forms library** — the concrete TUI/prompt toolkit the stack builds on
  (named in that stack's `profiles/<stack>/` refinement, never here). "Use the fleet's
  chosen library, don't hand-roll a prompt loop" is the principle; its name is the fill.
- **The resolution sources and their order** (§1) — which env var, which config, which
  probe the tool reads to avoid asking, and where it persists a resolution. The *order*
  and "resolve-then-remember" are the principle; the concrete sources are the project's.
- **The facet/choice vocabularies** (§2) — the enumerable registries the selects read
  (providers, environments, stack facets). The registry mechanism is the principle; its
  entries are the project's.
- **The headless opt-out's spelling** (§4) — `--headless`, a `CI` probe, `--yes`. The
  principle is "a machine path exists, is first-class, and never blocks"; its flag name
  is the project's.
- **What counts as a mutation worth a confirm** (§5) — the project's risk model decides
  which writes gate; "confirm the outcome before the irreversible step" is the principle.

## 7. Anti-patterns

| ❌ | ✅ |
|---|---|
| Prompt for the SSOT path / repo name while standing inside it | Resolve from cwd/env/config and remember; prompt only the unknowable (§1) |
| A new dotfile to "remember" a value an env var already holds | Read the source that exists; don't mint a second SSOT (§1) |
| Free-text field for a value from a known set ("type: go, ts, python…") | Select/multiselect from the enumerable registry (§2) |
| Eight prompts where seven answers are the default | Default the near-certain, opt-out with a flag, delete the rest (§3) |
| A `serves? [y/N]` whose "yes" is almost always right | Do it by default; `--no-…` for the rare opt-out (§3) |
| The interactive form is the only way; scripts can't drive it | `--headless` reads flags/env — a first-class second front door (§4) |
| The form hangs on a piped/CI stdin | Detect non-TTY before prompting; take the flag path or error cleanly (§4) |
| Writes fire the moment the last field is entered | One summary confirm before the mutation; `--yes`/dry-run for headless (§5) |

## 8. Porting checklist

- [ ] Every prompt is for a genuinely unknowable value; each knowable one is resolved and remembered (§1).
- [ ] No resolution invents a second SSOT for a fact an env var / existing config already carries (§1).
- [ ] Every field with a known vocabulary is a select/multiselect/confirm reading an enumerable registry — no free text where a set exists (§2).
- [ ] Every field is pre-seeded with its near-certain default; near-always-yes prompts are defaults-with-opt-out, not questions (§3).
- [ ] A `--headless` (or equivalent) path reads flags/env, pre-fills the same values, and **never blocks** on a non-interactive stdin (§4).
- [ ] A single summary confirm precedes any mutation; the headless path uses an explicit `--yes`/`--apply` + dry-run default (§5).
- [ ] The concrete forms library, resolution sources, choice vocabularies, and headless flag are named in the project/profile layers, not hardcoded into a shared body (§6).

---

## Cross-links

[cli doctrine](cli.md) (the command-*architecture* companion — dispatch, flag parser,
tiered safety, audit; this doctrine is that one's interactive-input face, and §5 here is
§6 there reached the human way) · [validation doctrine](validation.md) (the "never block
on a TTY that isn't there" guarantee §4 is a fail-closed gate like any other — guard it
with a test) · instantiated per stack by a `profiles/<stack>/` refinement that names the
concrete forms library (today the Go profile's framework-first refinement, where the huh
mechanics live).
