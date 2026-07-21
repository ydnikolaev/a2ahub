# P9 — Space template + V3 CI + basic doctor — Specification

> Projection of `docs/the-plan/plan/` §4.2, §4.5, §5.4/§5.5, §7.2 (OP-218),
> §9.1/§9.3, §10.3, §14 (US-101/102/201/202), §15 L0. This spec cites; it
> never restates or re-decides plan content. `docs/the-plan/plan/` has
> normative precedence; a conflict here is a defect fixed via §11 Amendments.

**Slug**: `v1-min-2026-07`  ·  **Track**: space  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Plan**: [plans/09-space-template-ci.plan.md](../plans/09-space-template-ci.plan.md)
**Footprint**: `space-template/**`, `internal/cli/cmd_doctor.go`, one wiring
line in `cmd/a2a` — the space repo scaffold + V3 CI workflow + the basic
`a2a doctor` command. Per ADR-001: `space-template/` is data (no imports);
`internal/cli/cmd_doctor.go` is an `internal/cli` file and MAY import any
core package below `internal/cli` (`artifact`, `schema`, `space`, `host`
observed as the ones basic doctor actually needs — credentials, space
access, `min_binary_version`, CI-presence, statusline-wiring checks); it
MUST NOT import `mcp`. This phase authors no new Go package.

---

## 0. User stories (cited verbatim from 14-us-ac.md, §E1/§E2)

| ID | User story (plan text) | AC rows this phase covers |
|----|------------------------|----------------------------|
| US-101 | "As the operator, I create a new space from a template so a circle can start exchanging in minutes." | AC-101.1, AC-101.2 |
| US-102 | "As a space admin, I add a participant via one manifest PR so onboarding is explicit and reviewable." | (context only — P9 ships the CODEOWNERS/manifest *skeleton* the P11-executed onboarding PR of US-102 targets; AC-102.* are proven end-to-end in P11, not here) |
| US-201 | "As an agent, every artifact I draft is validated before it can leave my machine, so I can never publish garbage." | AC-201.2 (V2≡V3 slice only — see Scope) |
| US-202 | "As a system owner, no breaking contract change can reach consumers without my gate and their awareness." | AC-202.1, AC-202.4 |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Space layout, write funnel, CODEOWNERS, diff-authz | [04-topology.md §4.2](../../../the-plan/plan/04-topology.md), §4.5 | Layout tree, branch-protection rules, and the github-login→system mapping are normative; do not re-derive them |
| Validation matrix | [05-schemas.md §5.5](../../../the-plan/plan/05-schemas.md), §5.4/§5.4b | V3 row is the CI contract; 5.4b is the compat-gate rule this phase must wire, not reimplement (owned by `internal/validate`, P3) |
| Command catalog | [07-client.md §7.2 OP-218](../../../the-plan/plan/07-client.md) | Basic doctor only; `--space` is v2 (D-030) |
| Rollout scope cut | [15-rollout.md L0](../../../the-plan/plan/15-rollout.md), the D-030 re-cut header | L0 exit criteria this phase must make provable; `doctor --space` explicitly out |
| AuthZ / credentials | [10-security.md §10.3](../../../the-plan/plan/10-security.md), §10.5 | CI credential row: default `GITHUB_TOKEN` + read-only token for the pinned binary fetch |
| Runbook this phase feeds | [09-human-ops.md §9.1](../../../the-plan/plan/09-human-ops.md) org/space admin profile, §9.3 | P11 executes the branch-protection checklist this spec documents |
| Package boundaries | [ADR-001](../../../decisions.md) | Footprint and import rule above are derived from it |

## Scope

Ships: the `space-template/` scaffold (§4.2 layout minus per-system sections,
which only exist after the P11/US-102 onboarding PR), the V3 CI workflow
wired for the checks §5.5's V3 row lists, the pinned-binary fetch mechanism
for CI while the product repo is private, the branch-protection checklist
P11's runbook (§9.1) executes, and the basic (non-`--space`) `a2a doctor`.

Explicitly NOT this phase: the validation engine's rule logic (owned by
`internal/validate`, P3 — this phase only invokes it from CI), the GitHub
host adapter (P5), the actual getvisa space creation (P11), `doctor --space`
(v2 per D-030). AC-201.2's V4/hub clause is not provable here — hub is
deferred to L3 (README SSOT rule, 15-rollout.md L0/L1 exit notes "hub
clauses excluded — re-verified at L3"); this phase proves only the V2≡V3
slice.

---

### T3. Space template layout (track: space)

| Path | Purpose | Generated or static |
|------|---------|----------------------|
| `space.yaml` | manifest instance seeded from the manifest schema (§5.1/§5.2, owned by P2's `internal/schema`): space id placeholder, schema version, empty `participants: []`, default gates config, notification routes | generated — schema-valid immediately, so AC-101.1's "CI is green on the empty space" holds with zero participants |
| `CODEOWNERS` | gated-paths-only per §4.2: `/space.yaml` → space-admins placeholder team, `/decisions/**` → advisory all-participants placeholder. `/<system>/provides/**` entries do NOT exist at template time — they are added by each system's onboarding PR (§9.2 step 2, US-102), never pre-seeded | static skeleton |
| `.github/workflows/a2a-validate.yml` | V3 CI — see T5 below | static |
| `decisions/`, `vendored/` | empty placeholders (`.gitkeep`), per §4.2 | static |
| `README.md` | points at the §9.2 onboarding runbook; not itself normative content | static |

No `<system-id>/` directories ship in the template — §4.2's per-system tree
is created by the onboarding PR, matching US-102, not by this phase.

### T5-equivalent. V3 CI workflow (`space-template/.github/workflows/a2a-validate.yml`)

| Trigger | Scope | Steps (behavior contract — flags/verb owned by `internal/cli`/`internal/validate`, P3/P6, outside this footprint) | Gate |
|---|---|---|---|
| `pull_request` targeting `main` | changed files in the PR | (1) fetch the **pinned-version** validator binary from the product repo's releases via the read-only token stored as a repo secret (§7.3, §10.5 CI row) — pinned version, not "latest"; self-update signature verification (T-8) is `a2a update`'s mechanism and is v2-deferred (D-030), out of scope here (2) invoke the binary's V3 full-repo entrypoint: V2 (schema+referential+authz+lifecycle) for changed files + diff-authz (changed paths ⊆ author's section, author = PR's GitHub login mapped via `space.yaml`) + fold integrity + supersession linearity + policy (classification, gates, 5.4b compat) (3) emit machine-readable pass/fail | **blocking**; the emitted GitHub status check name MUST be literally `a2a-validate` (§4.2's exact string) — this is the seam branch protection binds to; a name mismatch silently defeats AC-101.2 and the P11 checklist |
| `push` to `main` (post-merge) | full repo | same engine, same binary fetch | **flag-only** (§5.5 V3 row): never fails the run as a merge gate (already merged); surfaces cross-PR races (CC-095) as an annotation/flag, not a block |

G1/G2 wiring (AC-202.1): the V3 major/first-publish branch of the policy
check queries the PR's approving reviews via the host API (§5.4, "queried
via the host API", not the author's self-declared bump) — this query is
`internal/validate`+`internal/host` logic (P3/P5); the workflow step's only
CI-layer duty is to run *after* `GITHUB_TOKEN` is available so that query
can execute, and CODEOWNERS-required review on `/<system>/provides/**` is
what makes the review exist to be queried (§4.2 CODEOWNERS rule).

### T1. CLI surface — basic doctor (track: cli, footprint-granted)

| Command | Flags | Input | Output | Notes |
|---------|-------|-------|--------|-------|
| `a2a doctor` | none in v1-min (`--space` is v2, D-030 — OP-218's admin host-drift diff is out of scope; flag parsing MUST reject `--space` with an explicit "not available in v1-min" error, not silently ignore it) | `.a2a/config.yaml`, credential store (10.5), each connected space's mirror clone + `space.yaml` | one line per check, pass/fail, exit 0 iff all pass, non-zero + actionable message per failing check otherwise | Checks, per OP-218's basic-doctor field list verbatim: **credentials** (present, readable, not expired per §10.5/§9.3), **space access** (each connected space's mirror is fetchable), **versions** (local binary vs each connected space's `min_binary_version`, §7.3), **CI presence** (connected space's default branch carries `.github/workflows/a2a-validate.yml` and a required check named `a2a-validate` — a lightweight existence check, not the full §9.3 host-drift diff that `--space` owns), **statusline wiring** (git-fallback statusline, §7.5/P7, is present/reachable — presence check only, not statusline's own rendering logic) |

`internal/cli/cmd_doctor.go` registers via the same command-registration
convention as whichever CLI verb file already exists in `internal/cli` at
integration time (P6/P7/P8 land verbs in the same package); it does not
invent its own flag-parsing or logging convention (framework-first rule).

---

## Branch-protection checklist (for the P11-executed §9.1 org/space admin runbook)

This phase documents these settings; P11 applies them when the real getvisa
repo is created. Cite: §4.2 write funnel, §10.3 AuthZ matrix.

| Setting | Value | Cite |
|---|---|---|
| Direct pushes to `main` | forbidden for all actors, including admin (bypass reserved for incident recovery, alarmed via F-7) | §4.2 |
| Required status check | `a2a-validate` (exact name — see T5 above) | §4.2 |
| Require branches up to date before merge | OFF (concurrent event PRs must not serialize) | §4.2 |
| Force-push | forbidden | §4.2 |
| Require review from Code Owners | ON, applies only to CODEOWNERS-listed paths (`/space.yaml`, `/decisions/**`, and each system's `/provides/**` once onboarded) | §4.2 |
| Auto-merge (repo setting) | ON — `a2a submit`'s PRs (OP-205) open with auto-merge enabled and merge unattended on green `a2a-validate` for ungated paths | §4.2 |
| Private-repo protections require a paid plan | verified before space creation; `a2a doctor --space` (v2) re-checks it later | §4.5 |

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] The manifest schema and its loader (P2 `internal/schema`) — `space.yaml`
      generation in the template MUST be a literal instance of that schema,
      never a hand-rolled shape.
- [ ] `internal/cli`'s existing flag-parsing/logging convention (whichever
      verb lands first in this wave) — `cmd_doctor.go` follows it, does not
      re-invent.
- [ ] §5.5's V3 row and §5.4b compat rule are logic owned by `internal/validate`
      (P3) — this phase's CI workflow is a thin invocation, never a
      reimplementation of schema/referential/lifecycle/policy checks.
- [ ] §10.5's CI credential row (`GITHUB_TOKEN` + read-only binary-fetch
      token) — no new credential shape invented for this phase.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| Empty-space CI green | AC-101.1: template scaffolded → CI run on the empty space is green | zero participants, `space.yaml` alone must still validate (T6) |
| Branch protection mechanics | AC-101.2: direct push to `main` rejected; foreign-section PR unmergeable (diff-authz fail); ungated own-section PR auto-merges with zero human touch | CC-060 (cross-section write), CC-095 (concurrent-PR race caught post-merge, flag-only) |
| Required-check name binding | `a2a-validate` literal string match between workflow job name and branch-protection required-check config | a renamed job silently defeats the gate — assert byte-equality |
| V2≡V3 parity (hub slice excluded) | AC-201.2: same invalid golden fixture (T1) produces the identical machine code locally (V2, `a2a validate`) and in the template's CI (V3) | do not assert V4/hub parity — deferred, README SSOT |
| G1/G2 wiring | AC-202.1: major-version contract PR fixture merges only after an approving CODEOWNERS review is queried via host API, alongside a linked `deprecation` announcement with `ack_requested` | missing review → blocked; review present but no linked announcement → blocked (both preconditions of AC-202.1) |
| 5.4b compat gate | AC-202.4: mislabeled-minor fixture pair (prior-version valid fixture, new schema) fails V3 with the major-required machine code | §13.4 compat-goldens pair: genuinely additive minor MUST still pass |
| Pinned binary fetch | fetch uses the exact pinned version + read-only token secret, not "latest" | token missing/expired → CI fails loudly, not silently skips validation |
| Basic doctor | each of the five OP-218 basic checks independently pass/fail with an actionable message; `--space` flag rejected explicitly | expired credential, unreachable mirror, `min_binary_version` mismatch, missing `a2a-validate` workflow file, missing statusline wiring |

## 7. Schema / contract delta

None. This phase introduces no new JSON Schema. `space.yaml` instances are
generated against the manifest schema already owned by P2/`internal/schema`;
this phase only seeds and wires, per the Footprint statement above.

## 8. Acceptance criteria

> AC rows quoted verbatim from `14-us-ac.md` (Given/When/Then unchanged);
> tags (`[T#/CC-#/E2E-#]`) moved into the Verify column per the plan's own
> bracket convention. Phase-local rows (US = `—`) are additive, not a
> modification of the cited rows.

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-101 | AC-101.1: "Given the space template, when the repo is created, then layout 4.2, CI workflow (V3), CODEOWNERS skeleton and a schema-valid `space.yaml` exist and CI is green on the empty space." | `[T6, E2E-9]` — scaffold a repo from `space-template/`, push, assert the `a2a-validate` check is green with zero content |
| 2 | US-101 | AC-101.2: "Given branch protection configured per §4.2/§10.3, when any actor pushes directly to `main`, then the push is rejected; when a member PRs into a foreign section, then V3 diff-authz fails and the PR is unmergeable; when an ungated own-section PR is green, then it auto-merges with zero human involvement." | `[CC-060, CC-095]` — three sub-scenarios against a scaffolded repo with the branch-protection checklist applied |
| 3 | US-201 | AC-201.2: "Given the same content, when validated locally (V2), in space CI (V3), and by the hub (V4), then results are identical." | `[T1/T4/T6]` — V2≡V3 slice only in this phase (hub/V4 excluded per README SSOT + 15-rollout L0/L1 "hub clauses excluded") |
| 4 | US-202 | AC-202.1: "Given a major-version contract PR, when CI runs, then merge requires human review (G2) and a linked deprecation announcement with `ack_requested`." | `[E2E-4]` — fixture major-bump PR without owner review is blocked; with review + linked announcement, unblocked |
| 5 | US-202 | AC-202.4: "Given a mislabeled-minor breaking change (prior-version valid fixture fails against the new schema), when V3 runs, then the merge is blocked with a major-required error." | `[CC-080]` — §13.4 compat-golden pair against the CI workflow |
| 6 | — | The `a2a-validate` job's emitted GitHub check name is byte-identical to the branch-protection required-check config and to §4.2's cited string | grep/assert on the workflow YAML `name:` field vs the branch-protection checklist entry |
| 7 | — | Pinned-binary fetch step uses an explicit version pin and the read-only token repo secret; no "latest" resolution | inspect workflow YAML for a hardcoded/parameterized version, not a floating tag |
| 8 | — | `CODEOWNERS` in the shipped template lists ONLY `/space.yaml` and `/decisions/**` — no `/<system>/provides/**` entries pre-seeded | diff the template's `CODEOWNERS` against §4.2's rule |
| 9 | — | `a2a doctor` (no flags) runs all five OP-218 basic checks and exits non-zero with an actionable per-check message on any failure; `--space` is rejected with an explicit "v1-min: not available" error, never silently ignored | `go test ./internal/cli/... -run Doctor` |
| 10 | — | The `push`-to-`main` CI job never appears as a branch-protection required check (flag-only, per §5.5 V3 row) | branch-protection config lists only the PR-triggered job as required |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | A later GitLab/Gitea profile (§4.5, Q-004) swaps the workflow file and the host-API query inside `internal/host`; the template's layout and branch-protection *intent* carry over unchanged (§4.5's stated design goal) |
| Coupling | Soft — the workflow invokes the pinned binary by version + behavior contract, never by internal Go symbol; CI and the binary can version independently as long as the V3 entrypoint's I/O contract holds |
| Migration path | low — `doctor --space` (v2) and self-update-verified CI fetch (post-D-030) both extend this phase's scaffold additively, no rework of what ships here |
| Roadmap conflicts | P6/P8 land the actual `a2a validate`/`a2a contract` CLI verbs this workflow invokes; DAG allows P9 to run in parallel with P6 (both blocked only on P3+P5) — the workflow step is specified as a behavior contract, not a flag string, precisely so the two can proceed independently and reconcile at P10 integration |

## 10. Implementor entry point

Execute as one wave of the `v1-min-2026-07` epic (tracker phase P9, `blocked_by:
[P3, P5]`). TDD default: red (empty-space CI fixture asserting `a2a-validate`
absent/red) → green (workflow + doctor land) → refactor. Framework-first: the
workflow is plain GitHub Actions YAML calling one pinned binary — no custom
CI framework, no shell reimplementation of validation logic. Log-or-return
per `.claude/rules/go-conventions.md` for `cmd_doctor.go`. Full loop
(README/tracker/specs shapes, lint gate): [docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. When shipped reality deviates from this spec, record it here
> AND amend any downstream spec (notably P10's integration harness and P11's
> runbook, which consume this phase's workflow contract and checklist).

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
