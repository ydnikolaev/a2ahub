<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# Validation doctrine — the machine-validation maniac

> **Thesis.** If a property can be checked by a machine, a machine checks it — on
> every run, forever. Every agent continuously **expands the checkable surface**:
> an unguarded invariant is a bug-in-waiting, and a gate is how we move that
> burden off human and agent attention onto the machine. Validation is not a QA
> phase — it is a **permanent lifecycle organ** of every project. The point is
> cognitive offload: machines hold the invariants so agents spend their attention
> on the irreducible — design and correctness.

This is **foundational**, not one doctrine among many. It produces three
surfaces: an always-on agent mindset (§6), a capture→build loop (§7), and a
validator standard (§5). It is also **self-applying**: the harness's own
drift-gate, env-parity, and schema-shape checks are instances of this very
doctrine (§Self-application).

---

## 0. When to gate — and when a gate is NEGATIVE value

Maximalism with a brake. "Validate everything validatable" is the default, but a
**bad gate adds cognitive load** — flapping CI, false positives, maintenance —
which is the exact opposite of the offload we're after. So a gate earns its place
when the invariant is:

1. **Silently violatable** — nothing else (compiler, type system, formatter,
   review) reliably catches it. *Don't* gate what `tsc`/`go vet`/the formatter
   already guarantee — that's noise.
2. **High-harm if violated** — prod breakage, data loss, security, silent drift.
3. **Cheaply + deterministically checkable** — fast, no flakiness. A
   non-deterministic gate is worse than no gate (it trains people to ignore red).

Miss any of the three and the answer is *not yet a gate* (leave it to review, or
to a faster check first). Everything that clears all three **must** become a
gate. Tier by cost: a 50ms host-side grep-gate has a far lower bar than a 10-min
docker gate.

**Suite economics — "validate everything" is a ratchet that only tightens.** 20
gates become 100, and even fast gates sum. So the suite carries a **wall-clock
budget**; breaching it forces tiering (move gates to a host-side fast lane / a
nightly lane) — not silently accepting an ever-slower `check`. And gates **retire**:
a gate subsumed by a stronger one, or one that has **never fired in N months**, is
consolidated or removed. A never-fired gate is one of two things — perfect
prevention already did the job (rung 1–2 of §1, the gate is redundant), or it's
theater (§3). Either way it leaves. Retirement is the half of gate-economics that
maximalists always omit; without it the suite rots into slow noise that *adds*
the cognitive load the doctrine exists to remove.

Both economics halves need **data, not vibes**: every `check` run appends a
gate-firing record (gate id, verdict, duration) to a telemetry log (JSONL).
The retirement and wall-clock-budget scans read that history — a scan run
against an empty history refuses with an error rather than issuing blind
verdicts. No telemetry ⇒ no retirement authority.

---

## 1. Prevent before you check — the offload hierarchy

A gate catches an error *after the fact*; eliminating the error *class* is
strictly more cognitive offload. So before writing a gate, climb the hierarchy:

1. **Make-impossible** (best) — encode the invariant in types/schema/the build so
   the bad state is *unrepresentable*. No gate needed; the compiler is the gate.
2. **Generate** — derive the artifact from its SSOT with a tool + a `GENERATED —
   do not hand-edit` banner, so the correct output is *produced*, not hand-written
   and hoped-for. This is what "автогенераторы" buys you: the error can't be
   introduced because nobody writes the artifact by hand.
3. **Gate** (last resort) — for what you *can't* prevent, fail CI on violation.

**Only gate what you couldn't make-impossible or generate.** A generator plus its
drift-gate (rungs 2+3 together) is the workhorse: anything **derived** is
regenerated, banner-stamped, and a `*-drift` gate fails CI when the rendered
output disagrees with its source — hand-edits caught, not trusted. *(axon:
`synapse-bundle`→`openapi.yaml` + `synapse-drift`; `generate-root-env.sh`→
`.env.example` + `env-check`; routes/status/rbac-matrix drift.)*

The **single most important rule about a drift gate**: key it off the **SSOT set**,
not a frozen snapshot of current values — a gate that compares against a hardcoded
list of *today's* members is blind to *new* members (the exact thing you want to
catch). Pin the SSOT; test the ADD direction (§3).

---

## 2. Fail-closed everywhere a default could write

Unknown flag, missing required input, ambiguous resolution → **abort with an
actionable reason**, never proceed with a destructive default. A typo'd
`--dry-runn` must stop, not silently run the dangerous path. This extends to
knowledge resolution (a required knowledge-home missing → fail loud, never a
silently-hollow run) and to config (`config validate` rejects an unregistered or
unshaped declaration). Fail-open is how silent corruption ships.

---

## 3. Every invariant gets a gate; every gate gets TEETH

A gate you never proved catches its violation is **theater** — and theater is
negative value, because it radiates false safety. Every new gate ships with a
**teeth-test**: a fixture that *violates* the invariant and asserts the gate goes
red. Test the **ADD direction** specifically — most broken gates pass on current
inputs and silently fail to catch the next addition. *(axon scars: a ratchet
keyed off a frozen enum was blind to new members; `t.Skip` looks identical to
PASS in non-verbose `go test`; `testdb` clones DDL but not triggers, so
trigger-enforced invariants pass green while broken.)* If you can't write a test
that makes the gate fail, you don't understand the invariant well enough to gate
it.

---

## 4. Gates are fast, layered, and discovered-cheap-first

Cheap host-side gates **discover** failures; the expensive full gate **confirms**
once. Never use a 10-minute docker `make check` to find failures one at a time —
run scoped `lint`/`test -cover`/drift-scripts locally first, then the full gate
once, captured to a readable log. *(axon: `make check` runs ~20 fast gates before
the docker `-race` suite.)* A gate that can run in 50ms at the host has no
business waiting for the cold-image build.

---

## 5. The validator standard — the canonical shape of a new gate

Every gate, new or old, conforms — so they compose and stay trustworthy:

- **Deterministic** — same inputs, same verdict; no clock/network/order
  dependence. Non-determinism disqualifies (§0.3).
- **Fast + tiered** — host-side and quick by default; heavy gates justify their
  cost and run last.
- **Fail-closed** — exits non-zero on violation AND on its own internal error
  (a gate that crashes to exit 0 is a fail-open hole).
- **Actionable error** — the failure message prints the *fix*, not just the
  symptom (`RETAIN_FOO is read by config.go but missing from .env.api.example`,
  not `drift detected`). The error message is the gate's primary documentation.
- **Teeth-tested** — ships with the ADD-direction fixture (§3).
- **Line-precise opt-out** — a narrow, documented marker for legitimate
  exceptions, never a blanket disable.
- **Registered** — listed in the gate registry and wired into the project's
  `check` seam (`make check` / `.mate/config.yaml: check`). An unwired gate
  rots.

A new gate is itself reviewed against this standard — meta-validation. Make that
review a **real gate, the gate-of-gates**: a meta-check that walks the registry
and asserts every registered gate (a) has a teeth-test, (b) exits non-zero on its
own internal error (not just on a violation), and (c) prints a non-empty,
actionable message. It is the highest-leverage gate in the project — the one that
keeps all the others honest — and it is §Self-application made executable.

---

## 6. The maniac loop — always-on agent behavior

This is the surface that makes the doctrine *fundamental*: it is **always-on**,
part of the validation pillar loaded every session. On every code-touch the agent
asks, reflexively:

> *"What invariant did I just create or rely on that is now unguarded? What did I
> verify by hand that a machine should verify forever?"*

A hand-verification you did once is a gate you haven't written yet. New entity,
new SSOT, new boundary, new convention, new parity requirement → **propose the
gate**. The agent does not wait to be asked; proposing and building validators is
ambient behavior, like writing a test for new code. The goal is selfish in the
right way: every gate you build is cognition you never spend again.

---

## 7. The capture → build lifecycle (mirrors the harness-backlog)

Continuous proposal needs a near-zero-friction home, or the maniac mindset
evaporates into good intentions. Reuse the proven backlog shape:

- **Capture (cheap, session-aware):** the moment an unguarded invariant is
  spotted, append one row to a `validator-backlog` — finding + proposed gate +
  rough tier + **layer**. Capture points are wired into the lifecycle, not a
  separate ritual: `/ship` self-review, `/implement` close-out, `/teamlead`
  per-wave, the audit-inbox loop, and a design-time trigger ("new entity/SSOT →
  does it need a gate?", like the core/module/satellite placement decision).
- **Route by layer (same discriminator as harness-hardening's core/local router):**
  a proposed gate has a home. A Go-anti-pattern gate is **STACK** (`profiles/go`),
  a domain-invariant gate is **PROJECT**, a harness/drift gate is **CORE**. Tag the
  row so the maniac neither promotes a project gate to core (over-abstraction —
  failure #11/#12) nor re-implements a stack gate per-project. Missing this field
  is how "validate everything" silently duplicates or wrongly-generalizes.
- **Build (batched, gated):** drain the backlog in batches — write the gate to
  the §5 standard (with teeth), wire it into `check`, move the row to the
  archive. Same machinery as `/harness-hardening` draining the harness-backlog.

**Drain guarantee — the load-bearing rule.** Capture is cheap and ambient *by
design*, so fill-rate runs hot; building is expensive (gate + teeth + wire +
maintain forever). Fill ≫ drain turns the backlog into a graveyard, and a
graveyard backlog is theater (§3) wearing a process costume. *(Cautionary tale
from this very repo: 63 open audit reports — an ambient-capture loop with no drain
guarantee.)* So the loop carries one of three brakes, not exhortation:

1. **WIP limit** — can't add the Nth open row without draining one (build it, or
   close it as §0-economics "not worth it"). Backpressure forces the verdict.
2. **Guaranteed cadence** — a validator-sweep wave in every epic drains the queue
   to a floor.
3. **Capture-with-expiry** — a row unbuilt in N weeks auto-closes as "not worth
   it" — which is just the §0 economics verdict applied late, honestly.

Pick one and wire it; an un-braked capture loop is aspirational, not fundamental.

The lifecycle framing is the whole ask: validation is an organ that runs for the
life of the project, not a milestone.

---

## The validatable-dimension taxonomy — the maniac's scan checklist

What can be gated (with axon's reference instance). When scanning a change, walk
this list and ask which dimensions it touched:

| Dimension | Gate the property that… | axon reference |
|---|---|---|
| **Structural / schema** | tables/types/configs have the required shape | `check-schema-shape.sh` |
| **Drift (SSOT↔derived)** | generated artifacts match their source | `synapse-drift`, `env-check`, routes/status/rbac drift |
| **Boundaries** | import/layer/package rules hold | `pkg-boundary-check` (ADR-022), catalog-layers |
| **Conventions / naming** | files/migrations/keys follow the scheme | `migrate-lint` (gap-free numbering), i18n key-parity |
| **Parity** | two sides stay in sync (env, i18n, docs, contract, routes) | env-drift, i18n-check, legal-drift, contract-check |
| **Registry coherence** | registries parse their schema; cross-registry refs resolve; renders match source | `mate registry lint` (architecture spec §15) |
| **Coverage / test-honesty** | coverage floors held; tests actually run | coverage gates, no-silent-skip discipline |
| **Security** | rbac/ratelimit/secrets/vuln invariants | rbac-check, ratelimit-coverage, vulncheck |
| **Migration safety** | blue/green-incompatible DDL blocked | `check-migrations.sh` |
| **Dead surface** | no orphaned/unreferenced artifacts | status-drift broken-link, dead-rule detection |
| **Compliance / design** | anti-patterns, design tokens, dark-mode parity | backend/frontend compliance, design-compliance, dark-mode-audit |
| **Performance budgets** | LCP/CWV/bundle-size thresholds | (perf-reset epic) |

The list is a **floor, not a ceiling** — a new dimension the project invents is a
new family of gates, not an exception.

---

## Instantiation seam

What a stack or project swaps into this frame — everything below is a *value*, and a
value never belongs in the principles above it:

- **The validator tech.** What a gate *is* in this stack: a shell script, a test in the
  project's runner, a lint rule, a type. The doctrine mandates the four properties (§5:
  deterministic · fail-closed · actionable · teeth-tested); it does not mandate the
  language. A stack profile refines the default; the CORE default (a script wired into
  `check`) works on day one for any stack, which is why the doctrine is functional before
  any profile exists.
- **The gate registry** — where the enumerable list of gates lives, and therefore what the
  gate-of-gates reads. Every project has one; its path is the project's.
- **The `check` command.** Its *name* is the one thing that is NOT a project value: the
  make-ABI fixes it (`interface.md` §1), so a shared skill can invoke a gate suite it has
  never seen. What the project fills is the *contents* — which gates run, in what tiers.
- **The teeth convention** — how a gate's teeth-test is named and invoked, so the
  gate-of-gates can find it mechanically rather than by trust.
- **The `validator-backlog` home and its ONE drain brake** (§7) — WIP limit, cadence, or
  expiry. The doctrine mandates *exactly one, wired*; **which** one is the project's call,
  and only a brake a deterministic gate can enforce is a brake at all.
- **The thresholds** — the WIP number, the suite's wall-clock budget, the tier boundaries.
  Numbers are the definition of a value.

## Self-application

The harness that distributes this doctrine is **built on it**: the sync drift-gate
(§5d of the architecture spec), `config validate` (shape-checking knowledge
homes), the `harness-backlog` capture→build loop — all are instances of
validation doctrine. A doctrine you don't apply to your own machinery is one you
don't believe. The recursion is the proof.

---

## Anti-patterns

| ❌ | ✅ |
|---|---|
| Gate that compares against a frozen list of current values | Gate keyed off the SSOT set; teeth-test the ADD direction |
| New gate with no teeth-test | A fixture that violates the invariant and asserts red |
| `drift detected` (symptom only) | Error prints the exact file + fix |
| Flapping/non-deterministic gate left in CI | Fix determinism or remove it — a flaky gate trains red-blindness |
| Gating what the compiler/formatter already guarantees | Spend the gate budget on silently-violatable invariants |
| Using the 10-min full gate to discover failures | Cheap host-side gates discover; full gate confirms once |
| "We should validate this" said and forgotten | One-line `validator-backlog` row, built in the next batch |
| Blanket `// nolint` / disable-all | Line-precise, documented opt-out marker |

---

## Porting checklist

- [ ] The maniac loop (§6) is in the project's always-on rule set.
- [ ] A `validator-backlog` exists with capture wired into ship/implement/audit, a **layer** field, and one **drain brake** (WIP limit / cadence / expiry).
- [ ] A **gate-of-gates** meta-check verifies every registered gate has teeth + fails-closed + an actionable message.
- [ ] Before each new gate, the **prevention hierarchy** (§1: make-impossible > generate > gate) was climbed.
- [ ] Every generated artifact has a banner + a drift gate keyed off its SSOT.
- [ ] Every gate meets the §5 standard (deterministic, actionable, **teeth-tested**, registered).
- [ ] Gate discovery is tiered: host-side cheap gates before the expensive full gate.
- [ ] `check` (the config seam) runs the full registered gate set.
- [ ] The gate-economics brake (§0) is applied — no flapping, no compiler-dup gates.

---

## Cross-links

the [structure doctrine](structure.md) (placement
decisions that *trigger* gates) · [cli doctrine](../code/cli.md) (fail-closed parser, the same
principle in the operator tool) · [agent doctrine `verification-honesty`](../agent/verification-honesty.md)
(the per-action half of validation) · architecture spec §5d (the sync drift-gate as
a worked instance).
