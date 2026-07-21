---
name: mate-validator
description: >-
  The single authoring path for a validation gate. Use when you spot an unguarded
  invariant, when a change adds a new checkable surface, or when draining the
  validator-backlog — `new` scaffolds one complete gate (with mandatory teeth),
  `drain` builds the backlog in braked batches, `scan` walks a diff for gates to
  propose. Authoring is judgment, so it is a skill, never a CLI alias.
requires_knowledge: [validator-tech]        # resolves to a CORE default (below); a stack profile refines it
requires_capabilities: [run-shell, edit-files]
cites: [code/validation.md, code/interface.md]   # doctrines this skill restates — citation-lint checks they resolve
mate_synced: v0.98.0
---

# mate-validator

> **Thesis.** A gate is how an invariant's burden moves off human and agent
> attention onto the machine, forever. That only works if *every* gate is uniform
> — deterministic, fail-closed, teeth-tested, wired, registered — and the only way
> to guarantee uniformity is to make **one authoring path** the sole way a gate is
> born. This skill is that path. It does not restate the validation doctrine; it
> *executes* it. Full rationale lives in the validation doctrine (`validation.md`,
> the doctrine corpus); the procedure below is self-sufficient without it.

## When to run

- **`/mate-validator new "<invariant>"`** — you have one invariant to gate now (the
  maniac loop fired: "what did I just verify by hand that a machine should verify
  forever?").
- **`/mate-validator drain`** — the `validator-backlog` has open rows; build a batch.
- **`/mate-validator scan "<diff>"`** — a change just landed; walk it for unguarded
  dimensions and file backlog rows (propose, don't build).

Not for: gates that already exist (that's a `make check` run), or checking the gate
suite's health — suite health is *itself a gate*, so it lives in `make mate-check` and
runs like any other. Authoring is a skill; checking is a gate. Never a `mate validator
audit` verb — one path per job.

**What that gate covers today: the gate-of-gates only** (every registered gate exists,
is wired, and has teeth). The suite **wall-clock-budget** and **retirement** scans are
*not built* — they read gate-firing telemetry, and a scan on a partial history retires a
gate that never *reported* as if it never *fired* (ADR-005). So: do not go looking for
them, and **do not hand-roll one here** — a "this gate looks unused, drop it" verdict with
no firing data is exactly the blind retirement the doctrine forbids. Retirement is a
proposal to a human until the scan exists.

## `new "<invariant>"` — scaffold one complete gate

Do these in order. **Step 5 is a hard stop: no teeth-test, not done.**

1. **Climb the prevention hierarchy first (offload beats catch).** Before writing
   any gate, ask in order: can this invariant be **made impossible** (encode it in
   a type/schema/the build so the bad state is unrepresentable — the compiler is
   the gate)? Failing that, can the artifact be **generated** from its SSOT with a
   `GENERATED — do not hand-edit` banner, so the error can't be introduced? Only
   what you *can't* make-impossible or generate earns a **gate**. Say which rung
   you landed on and why the higher ones don't apply.
2. **Earn the gate's place (§0 economics).** Gate only an invariant that is
   *silently violatable* (nothing else — compiler, formatter, type system, review —
   reliably catches it), *high-harm if violated*, and *cheaply + deterministically
   checkable*. Miss any one → it's *not yet a gate* (leave it to review). Don't
   gate what `go vet`/`tsc`/the formatter already guarantee — that's noise that
   trains red-blindness.
3. **Pick the validator tech.** Resolve `validator-tech` — **CORE default: a
   `scripts/<name>.sh` gate wired into `make check`**, which works for any stack
   and is why this skill is functional on day one. A stack profile *refines* the
   default when one exists (Go → a `_test.go` or a grep-gate; a JS/TS app → a
   vitest test or an eslint rule); the profile is an optional override, never a
   blocker.
4. **Write the gate to the standard (§5).** Deterministic (same inputs → same
   verdict; no clock/network/order). Fail-closed (non-zero on violation **and** on
   its own internal error — a gate that crashes to exit 0 is a hole). Actionable
   message that prints the **fix**, not the symptom (`RETAIN_FOO is read by
   config.go but missing from .env.example`, not `drift detected`) — the message is
   the gate's primary docs. Line-precise, documented opt-out marker for real
   exceptions — never a blanket disable. **Key a drift gate off the SSOT set**, not
   a frozen snapshot of today's values, or it's blind to new members.
5. **Write the teeth-test — refuses to finish without one (fail-closed
   authoring).** A fixture that *violates* the invariant and asserts the gate goes
   **red**. Test the **ADD direction** specifically: most broken gates pass on
   today's inputs and silently miss the next addition. If you can't write a test
   that makes the gate fail, you don't understand the invariant well enough to
   gate it — stop here.
6. **Wire + register.** Wire into `make check` (the uniform make-ABI — same target
   name everywhere, so wiring is uniform). Register it in the project's gate
   registry. An unwired or unregistered gate rots and the gate-of-gates can't see
   it.

Output: the gate script/test, its teeth-test, the `make check` wiring, and the
registry row — as a reviewable diff.

## `drain` — build the backlog in braked batches

**The home:** resolve `paths.validator_backlog` from `.mate/config.yaml`, falling
back to the structure standard's default (`docs/validator-backlog.md`) — never guess
a path. mate seeds the file on first pull, brake and row template already in it.

Pull open rows from there, build each to the `new` standard (steps 1–6), move the row
to the Done table. **Honor the drain brake** — the file declares exactly one of: a
**WIP limit** (can't add the Nth open row without draining one), a **guaranteed
cadence** (a sweep every epic drains to a floor), or **capture expiry** (a row unbuilt
in N weeks auto-closes as §0 "not worth it"). Fill-rate runs hot by design; without the
brake the backlog rots into a graveyard, and a graveyard backlog is theater. If no
brake is wired, that is the first thing to fix — and prefer the WIP limit, the only one
a deterministic gate can *enforce* rather than merely confirm you declared (a cadence
needs an epic tracker; an expiry needs a clock, and a clock-reading gate is
non-deterministic).

## `scan "<diff>"` — propose gates, don't build

Walk the change against the validatable-dimension taxonomy and, for each dimension
it touched, ask "is this newly-created invariant guarded?" — file a one-line
backlog row (finding + proposed gate + rough tier + **layer**) for each gap.
Dimensions: structural/schema · drift (SSOT↔derived) · boundaries · conventions /
naming · parity (env, i18n, docs, contract) · registry coherence · coverage /
test-honesty · security · migration safety · dead surface · compliance / design ·
performance budgets. The list is a **floor, not a ceiling**. **Route by layer** —
a stack-anti-pattern gate is STACK (a profile), a domain-invariant gate is
PROJECT, a harness/drift gate is CORE — so the maniac neither promotes a project
gate to core nor re-implements a stack gate per project. `scan` only proposes;
building is `drain`'s job under the brake.

---

This skill reads no stack-specifics: the CORE `validator-tech` default is
stack-agnostic, and it hardcodes `make check` (the make-ABI fixes the target
name — config carries the variable, the ABI carries the invariant). It speaks
semantic model tiers, never provider model ids. Authority for every rule above:
the validation doctrine (`validation.md`).
