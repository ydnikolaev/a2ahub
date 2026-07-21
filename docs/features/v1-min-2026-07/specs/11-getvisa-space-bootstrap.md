# P11 — getvisa space bootstrap (axon + seomatrix) — L0 exit — Specification

> Ops/runbook phase (human + agent, real GitHub org). Consumes the P9
> space-template + P6 `a2a` binary as prebuilt artifacts; writes zero
> product-repo code. The plan is normative — this spec cites, it does not
> restate or re-decide.

**Slug**: `v1-min-2026-07`  ·  **Track**: space  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Footprint**: `docs/runbooks/space-bootstrap.md` — the only artifact this
phase creates in THIS repo (a filled runbook capturing every step actually
executed, seeding §9 for v2). Everything else this phase produces lives
OUTSIDE this repo: the live `getvisa` space repo on GitHub (`space.yaml`,
`axon/` + `seomatrix/` sections, CODEOWNERS, branch protection, V3 required
check) and the GitHub org (machine accounts, fine-grained PATs, team
membership). **May import**: none — no Go code, no `internal/*` package
(ADR-001). This phase consumes P9's `space-template/` scaffold and pinned V3
validator binary, and P6's `a2a` CLI, as prebuilt artifacts; it does not
modify any of them. (These two "artifacts" are the SAME single `a2a` binary
— one binary, one CLI, D-005/R-004, §7.1 — the V3 validator is not a
separate build: the pinned copy is a CI-fetched release download, the P6
copy is a developer-installed copy.)

---

## 0. User stories

> Cited verbatim from [14-us-ac.md](../../../the-plan/plan/14-us-ac.md) §E1 — not restated.

| ID | User story |
|----|------------|
| US-101 | (OP) "As the operator, I create a new space from a template so a circle can start exchanging in minutes." |
| US-102 | (HL) "As a space admin, I add a participant via one manifest PR so onboarding is explicit and reviewable." |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Rollout scope/exit | [15-rollout.md](../../../the-plan/plan/15-rollout.md) §Phase L0 | Scope + exit list — quoted below verbatim |
| Onboarding runbooks | [09-human-ops.md](../../../the-plan/plan/09-human-ops.md) §9.1–§9.3 | Source of every runbook step this phase must execute and record |
| Hosting prerequisite | [04-topology.md](../../../the-plan/plan/04-topology.md) §4.5 | Paid-plan (Team+) requirement for branch protection on private repos — MUST be verified before proceeding |
| Actor/identity model | [02-actors.md](../../../the-plan/plan/02-actors.md) §2.1–§2.3 | System IDs (`axon`, `seomatrix`), participant registry, machine accounts |
| Credentials | [10-security.md](../../../the-plan/plan/10-security.md) §10.5 | Machine-account PAT scope, CI token scope |
| Write funnel | [04-topology.md](../../../the-plan/plan/04-topology.md) §4.2 (D-002) | Branch protection shape this phase must configure and verify |
| Day-one content decision | [17-decisions.md](../../../the-plan/plan/17-decisions.md) D-007 | Participants = axon + seomatrix, no others in L0 |

---

## T3. Space instantiation layout (track: space, repurposed)

> §T3 in the template describes *changes to the space-template*. P11 changes
> nothing in the template (that is P9's footprint) — it **instantiates** one
> concrete space from it. This table documents the resulting `getvisa` space
> layout per §4.2, populated with the actual participants (D-007). This
> repurposing is a deliberate deviation from the template's literal section
> intent — see `deviations` in the final report.

| Path (in the `getvisa` space repo) | Purpose | Generated or static |
|------|---------|----------------------|
| `space.yaml` | manifest: space id, schema version, `axon` + `seomatrix` participants (system id, org, section path, human owners, machine actors, join date, status), gates config | generated from P9 template, filled per §9.2 step 2 |
| `axon/provides/`, `axon/requires/`, `axon/consumes.yaml`, `axon/exchanges/`, `axon/events/`, `axon/docs/` | axon's section scaffold (§4.2) | generated (section scaffold) |
| `seomatrix/provides/`, `seomatrix/requires/`, `seomatrix/consumes.yaml`, `seomatrix/exchanges/`, `seomatrix/events/`, `seomatrix/docs/` | seomatrix's section scaffold (§4.2) | generated (section scaffold) |
| `decisions/` | multi-party decisions; CODEOWNERS = all required parties | generated (empty at L0) |
| `.github/CODEOWNERS` | gated paths only: `/axon/provides/**`, `/seomatrix/provides/**` → each system's human owners (G1/G2); `/space.yaml` → space admins (G4); `/decisions/**` → all participants' humans | generated from P9 template, filled with real owner handles |
| `.github/workflows/<V3-check>.yml` | required status check `a2a-validate` — fetches the pinned prebuilt validator binary from the (private) product repo using a repo secret (§9.1, §10.5) | static from P9 template; this phase wires the repo secret and confirms the check is required |

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] The P9 `space-template/` scaffold — do not hand-author `space.yaml`,
  CODEOWNERS, or the V3 workflow from scratch; instantiate from the template.
- [ ] The P9 pinned V3 validator binary release-fetch mechanism — do not
  build or vendor a second validation path for the space CI.
- [ ] The P6 `a2a init`/`a2a connect`/`a2a doctor`/`a2a new`/`a2a submit`
  verbs for every agent-side onboarding and hello-world step — no shell
  scripting around the binary's write funnel (D-002: the binary always
  writes via ephemeral branch → PR → auto-merge).
- [ ] §9.1/§9.2 runbook step sequence — do not invent a different onboarding
  order; the filled runbook records deviations, it does not redesign them.

## 6. Testing requirements

> This phase has no unit-testable surface (no code). "Testing" here means
> live, observable proof against the real GitHub org, per the §15 L0 exit
> list — not `go test`, not a harness run (E2E-9 is P10's automated
> cross-check of the same protection shape, cited as corroboration, not the
> verification method here).

| Area | What to verify | Edge cases |
|------|--------------|------------|
| Branch protection | direct push to `main` is rejected for every actor kind (§4.2, §10.3) | admin bypass path exists only for incident recovery (F-7 alarmed), not exercised here |
| Diff-authz / auto-merge | an ungated own-section PR (hello-world) with green V3 auto-merges with zero human touch | a cross-section PR (member PRs into foreign section) must fail V3 and be unmergeable — do not exercise a real cross-section PR in the live org; cite AC-101.2's own clause as the negative-path spec |
| CODEOWNERS gate | a `provides/**` PR from either system demands owner review before merge | space-admin PR to `space.yaml` (G4) also demands review |
| Hosting prerequisite | org confirmed on a plan supporting branch protection on private repos (§4.5) BEFORE space creation | if not on Team+, this phase is blocked — record the check outcome in the runbook regardless of result |
| Hello-world funnel | both `axon` and `seomatrix` publish an `announcement` (category `status`) via the PR funnel following a draft template (§9.2 step 4) | announcement authored by the system's machine account, not a human's own account (§10.5: machine account ≠ human owner, no self-approval) |

## 7. Schema / contract delta

None. This phase introduces no schema, template, or code change — it
instantiates artifacts (manifest, CODEOWNERS, section scaffolding) whose
shape is fixed by §4.2/§5 and produced mechanically by the P9 template.

## 8. Acceptance criteria

> AC-101.1, AC-101.2, AC-102.1 quoted verbatim from
> [14-us-ac.md](../../../the-plan/plan/14-us-ac.md) — unchanged, including
> bracketed test-tier tags. Rows 4–7 are phase-local, added per this
> spec's brief (US = `—`); their text is the §15 L0 exit list quoted
> verbatim, split one clause per row.

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-101 | AC-101.1: "Given the space template, when the repo is created, then layout 4.2, CI workflow (V3), CODEOWNERS skeleton and a schema-valid `space.yaml` exist and CI is green on the empty space. [T6, E2E-9]" | Live: inspect the created `getvisa` repo against §4.2 layout; observe the V3 check green on the empty (pre-participant) repo. |
| 2 | US-101 | AC-101.2: "Given branch protection configured per §4.2/§10.3, when any actor pushes directly to `main`, then the push is rejected; when a member PRs into a foreign section, then V3 diff-authz fails and the PR is unmergeable; when an ungated own-section PR is green, then it auto-merges with zero human involvement. [CC-060, CC-095]" | Live: attempt a direct push (rejected); observe an ungated own-section hello-world PR auto-merge unassisted. Cross-section-PR clause is spec-verified (see §6), not exercised live. |
| 3 | US-102 | AC-102.1: "Given a join PR (manifest + section scaffold), when merged (G4), then the section is writable by that system only, and the hub serves it with no hub-side config change. [E2E-9]" | Live: merge each participant's join PR (§9.2 step 2); confirm the section is writable only by that system (V3 diff-authz). **Hub clause excluded at L0** — see note below. |
| 4 | — | Direct-push rejection proof (§15 L0 exit: "branch protection verified to reject a direct push … without human touch"). | Runbook records the rejected-push attempt and its GitHub response. |
| 5 | — | Ungated hello-world auto-merge proof (§15 L0 exit: "…and to auto-merge an ungated hello-world PR without human touch"). | Runbook records the hello-world PR merging with zero reviewer action. |
| 6 | — | Gated-path review proof (§15 L0 exit: "a gated-path PR verified to demand owner review"). | Runbook records a `provides/**` (or `space.yaml`) PR blocked on missing CODEOWNERS approval. |
| 7 | — | Both-teams hello-world announcement (§15 L0 exit: "both teams committed a hello-world status `announcement` via the PR funnel following a draft template"). | Runbook links both merged `announcement` PRs (axon's and seomatrix's), each authored via `a2a new`/`a2a submit`. |

> §15 L0 exit (quoted verbatim): "Exit: AC-101.*, AC-102.1 green (hub clauses
> excluded — re-verified at L3); branch protection verified to reject a
> direct push and to auto-merge an ungated hello-world PR without human
> touch; a gated-path PR verified to demand owner review; both teams
> committed a hello-world status `announcement` via the PR funnel following
> a draft template."
>
> The excluded hub clause of AC-102.1 is exactly: "…and the hub serves it
> with no hub-side config change." — not verified at L0 (no hub exists yet;
> hub ships in L3 per §15 Phase L3). Row 3's verification above covers only
> the non-hub clause.

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | Onboarding a third system later reuses the exact §9.2 "new system into an existing space" runbook this phase exercises and records — no redesign per participant (D-003: adding circles/participants never restructures existing ones). |
| Coupling | Soft — this phase's only coupling to product-repo code is consuming P9's template and P6's CLI as black boxes; the space repo itself has no dependency back on this repo beyond the pinned validator binary fetch. |
| Migration path | low — nothing here is re-migrated at v2; the hub (L3) mounts read-only against the same space with zero space-side changes (§15 v2 "zero migrations"). |
| Roadmap conflicts | P12 (day-one content) is `blocked_by: [P11]` and runs its content work against this live space; P9 must ship first (template) — both are the tracker's stated `blocked_by`. |

## 10. Implementor entry point

Execute as the runbook itself: fill `docs/runbooks/space-bootstrap.md` with
every step below AS ACTUALLY EXECUTED (not as planned) — deviations get
recorded, not silently absorbed. Steps are drawn from §9.1 row 2
(org/space admin profile) and §9.2 ("New system into an existing space",
run once per participant: axon, then seomatrix). Actor tags: **[OP]** =
operator/space-admin action (defaults to the operator per §2.2), **[agent]**
= project agent action via the `a2a` binary (D-002: the binary always
writes).

| Step | Plan ref | Actor |
|---|---|---|
| Verify org plan supports private-repo branch protection (Team+) | §4.5 | [OP] |
| Create space repo from the P9 space-template (layout §4.2, V3 CI incl. `a2a-validate` required check + release-fetch token secret, CODEOWNERS skeleton, `space.yaml`) | §9.1 | [OP] |
| Configure branch protection (PR-only `main`, auto-merge, no force-push) | §4.2, §9.1 | [OP] |
| Verify a direct push is rejected and an ungated PR auto-merges | §9.1 | [OP] |
| Per participant (axon, seomatrix): create the system's machine account, issue its fine-grained PAT (§10.5 scope) | §9.2 step 1 | [OP] |
| Space admin: PR adding the participant to `space.yaml` (incl. github-login→system-id mapping) + section scaffold + CODEOWNERS entry for gated paths (G4 merge) | §9.2 step 2 | [OP] |
| New team: project-dev install profile — `a2a init` + `a2a connect` + credentials (§10.5) + `a2a doctor` green | §9.2 step 3 | [agent] |
| New team: publish an `announcement` (category `status`) as the hello-world via `a2a new`/`a2a submit` | §9.2 step 4 | [agent] |
| Confirm gated-path PR (a `provides/**` change) demands owner review before merge | §4.2 CODEOWNERS rule | [OP] |
| Record every executed step, actual timings, and any deviation from the §9.1/§9.2 runbook text in `docs/runbooks/space-bootstrap.md` | this phase | [OP] |

Note: §9.2 step 5 ("Hub picks the section up automatically") is explicitly
OUT of L0 scope (hub ships in L3, §15 Phase L3) — the runbook records this
as deferred, not skipped-silently. `sot` onboarding is out of scope per
Q-005 (L2+); only axon + seomatrix per D-007.

Framework-first: no scripting around the write funnel; every agent-side
write goes through `a2a`'s existing verbs (P6), never a raw `git push` or
`gh pr create` bypassing the binary.

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it
> here AND amend any downstream spec.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — from coherence audit (pre-implementation): clarified the footprint/consumes paragraph, which listed "the pinned V3 validator binary, and P6's `a2a` CLI" as two separate consumed artifacts; they are the SAME single `a2a` binary (D-005/R-004, §7.1) — a CI-pinned release download vs. a developer-installed copy, not a separate validator build.
