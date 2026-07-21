# P5 — Space Model, Mirrors & GitHub Host Adapter — Specification

> Deviation from template mechanics (not from plan content): **Track** is `cli`
> per the epic's E3 mapping (README §Epic mapping: P5 ∈ E3 client binary
> core), but this phase ships no new `a2a` verb — it is the library layer
> `internal/host`/`internal/space` that P6–P8's CLI verbs call. §T1 below
> therefore documents the Go API surface as tables (per the template's
> "describe contracts as tables, not code" rule) instead of a command table.

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Footprint**: `internal/host/`, `internal/space/` — host adapter interface +
GitHub implementation (ephemeral branch push, PR open with auto-merge,
check/review status queries); space layout model, `space.yaml` manifest
load/validate, mirror clones, the write funnel, project + machine config,
credential resolution (ADR-001). Imports: `internal/host` → `internal/artifact`
only; `internal/space` → `internal/artifact`, `internal/schema`,
`internal/validate`, `internal/host`. Neither package imports `cli`/`mcp`
(ADR-001 rule).

---

## 0. User stories

> Plan-level US-IDs this phase's plumbing underpins (verbs themselves ship in
> P6–P8; quoted verbatim from `14-us-ac.md`).

| ID | User story |
|----|------------|
| US-101 | As the operator, I create a new space from a template so a circle can start exchanging in minutes. |
| US-102 | As a space admin, I add a participant via one manifest PR so onboarding is explicit and reviewable. |
| US-301 | As an agent, I operate the whole exchange from one binary with idempotent commands. |

This phase provides the mechanics AC-101.2 (branch protection / auto-merge /
diff-authz, §4.2), AC-102.1 (section becomes writable on manifest merge)
and AC-301.3 (below) depend on. It does not implement the CLI verbs that
surface those ACs to a user — those are P6 (`init/connect/new/submit/sync`)
and P8 (lifecycle/contract verbs).

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Space layout & write funnel | [04-topology.md §4.1–4.5](../../../the-plan/plan/04-topology.md) | §4.2 tree is the normative directory shape; D-002 (revised) is the funnel contract |
| Distribution / project config | [07-client.md §7.3/7.4](../../../the-plan/plan/07-client.md) | §7.3 `min_binary_version` write-refusal guard (self-update itself, OP-217, is v2 per README scope cut); §7.4 is the config-file SSOT |
| Credentials | [10-security.md §10.3–10.5](../../../the-plan/plan/10-security.md) | §10.5 credential table; §10.3 AuthZ matrix for enforcement-point mapping |
| Hub-down behavior | [12-corner-cases.md CC-042](../../../the-plan/plan/12-corner-cases.md) | v1-min ships no hub at all (README scope cut) — CC-042's "keeps working" is the *default* mode here, not a fallback |
| Decisions | [17-decisions.md D-001/D-002/D-003/D-019/D-026](../../../the-plan/plan/17-decisions.md) | D-002 funnel, D-019 host isolation, D-026 draft/submit boundary |
| Package boundaries | [decisions.md ADR-001](../../../decisions.md) | normative import table |

---

### T1. Go API surface (track: cli, adapted — see deviation note above)

**`internal/host`** — GitHub-specific primitives only; never orchestrates, never sees `space.yaml`.

| Capability | Behavior | Plan cite |
|---|---|---|
| Push ephemeral branch | Push a local commit ref to the space repo's remote as `a2a/<system>/<id>`, authenticated with the caller-supplied write credential | §4.2 D-002 |
| Open PR (auto-merge) | Open a PR from the pushed branch into `main` with auto-merge enabled; returns PR number/URL | §4.2 D-002: *"commit to an ephemeral branch `a2a/<system>/<id>`, push, open a PR with auto-merge enabled"* |
| Check status query | Read the `a2a-validate` required status check (V3) result for a PR | §4.2, §10.3 enforcement-layering note |
| Review status query | Read CODEOWNERS-required review approval state for a PR touching a gated path | §4.2 CODEOWNERS rule, §10.3 |
| Find PR by head branch | Look up an existing open/merged PR by its deterministic head branch name `a2a/<system>/<id>`; the idempotent-retry read path — no dependency on `internal/cache` (P7) | §4.2 D-002 (deterministic branch name); resolves Open question 2 below |

GitHub implementation notes: uses the fine-grained PAT / GitHub App
installation credential resolved by the caller (§10.5 — "contents RW on the
specific space repos"); never accepts a machine-RO token for these calls
(scope rejection is GitHub's own enforcement, CC-063, not duplicated here).
The interface is host-agnostic on purpose — a GitLab/Gitea profile is a new
adapter behind the same interface, not a redesign (D-019; Q-004 tracked,
§17, "on first demand").

#### Gating needs no OpenPR parameter

`host.OpenPR` is UNIFORM — it always opens the PR with auto-merge enabled
(per D-002); there is no gated/ungated variant and no parameter that turns
gating on or off. Gate enforcement is path- and CI-driven, not an API
parameter: CODEOWNERS on gated paths (`provides/**`, `space.yaml`,
`decisions/**`) plus the V3 required check — which, for major-bump or
first-publish contract changes, requires an approving owner review queried
via the host API (plan [05-schemas.md
§5.4b](../../../the-plan/plan/05-schemas.md)) — simply prevent auto-merge
from firing until the required approvals land. Verbs (P8/P9) MAY annotate
the PR title/body with an advisory gate marker for human readability; they
never toggle auto-merge themselves. The "Review status query" primitive
above is the READ surface for that pending-approval state. See P8
([08-lifecycle-contract-verbs.md](08-lifecycle-contract-verbs.md)) for the
gate-aware verb that consumes this, and P9
([09-space-template-ci.md](09-space-template-ci.md)) for the CODEOWNERS/V3
CI side that produces it.

**`internal/space`** — orchestration; the only caller of `internal/host`.

| Capability | Behavior | Plan cite |
|---|---|---|
| Layout builder | Path constructors for the §4.2 tree: `<system>/provides/<slug>/{contract.md,schema/,fixtures/{valid,invalid}/}`, `<system>/requires/`, `<system>/consumes.yaml`, `<system>/exchanges/`, `<system>/events/<year>/`, `<system>/docs/`, `decisions/`, `vendored/<vendor>/` | §4.2 |
| Manifest load/validate | Parse `space.yaml`; schema-validate via `internal/schema`'s manifest schema (P2 seam); referential/policy checks (github-login→system map, `min_binary_version` pin) via `internal/validate` (P3 seam) | §4.2, ADR-001 |
| Mirror clone | Plain clone/fetch of the space repo under the resolved mirror location (project config's per-space mirror-location key: machine-level root, or `.a2a/cache/mirrors/`) | §7.4 |
| Write funnel | See §7 below | §4.2 D-002, D-026 |
| Config load | Project-level `.a2a/config.yaml` (own system ID, connected spaces: repo URL + mirror-location key); machine-level `~/.config/a2a/config.yaml` (credential *references* only, mirror root, personal defaults) | §7.4 |
| Credential resolution | Resolve a machine-config credential reference to an actual secret at call time (env var or OS keychore lookup); the resolved secret is passed to `internal/host` calls and never persisted or logged | §7.4, §10.5 |

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Artifact IDs, digest computation (D-029, `sha256:<hex>`), md+YAML
      frontmatter parse/serialize — all live in `internal/artifact` (P1).
      `internal/space` imports and reuses them; it does not re-implement ID
      minting or digesting.
- [ ] Manifest schema validation and the V2 policy/referential checks are
      `internal/schema` (P2) and `internal/validate` (P3) respectively — the
      write funnel's "validate" step calls into P3's engine; it is not a
      second validator (D-011: one engine).
- [ ] No new Go module dependency for git plumbing or GitHub API calls —
      shell out to the system `git` binary (`os/exec`) for local plumbing
      (clone/fetch/checkout/commit), consistent with "core speaks plain git"
      (D-019) and the repo's stdlib-first convention; the GitHub-specific PR
      calls in `internal/host` are the only place that talks to a host API.
- [ ] Machine error codes for any funnel-local validation failure come from
      the single registry in `internal/validate` (ADR-001), not a new
      `space`-local code space.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| Layout builder | Every §4.2 path class resolves correctly for a given system ID | unicode/invalid system-id rejected before path construction |
| Manifest load/validate | Valid `space.yaml` loads; invalid YAML / schema-invalid manifest rejected | missing participant map entry (CC-097 precondition), stale `min_binary_version` (CC-085) |
| Mirror clone | Fresh clone at configured location; re-run is a fetch, not a re-clone | mirror-location key absent (falls back per §7.4 default), target dir already non-empty non-git |
| Write funnel shape | Exactly ONE commit contains the artifact file **and** its first lifecycle event (D-026); branch name is exactly `a2a/<system>/<id>` | wrong-section artifact refused before any git action (AC-201.3 precondition, not this phase's AC but shared refusal path) |
| Write funnel refusal | Refuses to write when local binary version < `space.yaml` `min_binary_version` | read still succeeds, warning is loud (CC-085, §7.3) |
| Host adapter (GitHub) | `PushBranch`/`OpenPR` against a fake/mock host (interface-level fake, no live GitHub calls in this phase's unit tests) | push rejected (revoked credential, CC-061) surfaces as an explicit error, no partial state |
| Credential resolution | Resolves a configured reference to a secret at call time | missing/unresolvable reference fails loudly, never falls back to a literal in config |
| No-hub direct-git path | Full mirror-clone → write-funnel round trip succeeds with zero hub configuration present anywhere in project or machine config | AC-301.3 / CC-042 |
| Config precedence | Project-level system ID / space list load independent of machine-level credential refs; neither file ever contains a literal secret | round-trip serialize/deserialize asserts no secret-shaped field |

## 7. Schema / contract delta

No new JSON Schema — `space.yaml` is P2's manifest schema (`specs/02-product-schemas.md`); this phase only loads/validates against it. The two config files are this phase's own contract (not JSON Schema, per §7.4):

| File | Level | Fields (per §7.4) | Committed? |
|---|---|---|---|
| `.a2a/config.yaml` | project | own system ID; connected spaces (repo URL + mirror-location key); harness adapter toggles | yes (whole project team inherits it) |
| `~/.config/a2a/config.yaml` | machine | credential references (env/keychain pointers, never secrets); mirror root dir; personal defaults (TTL overrides, statusline options) | never |

Write funnel (orchestration owned by `internal/space`, executed via `internal/host`), quoting the two plan statements it must match exactly:

> §4.2 D-002: "Every write — by the binary, always — is: commit to an
> ephemeral branch `a2a/<system>/<id>`, push, open a PR with auto-merge
> enabled."

> D-026: "Drafts are local-only (`.a2a/staging/`); the space holds only
> submitted/published artifacts; submit event travels in the artifact's
> first PR."

Idempotency: before step 3, a re-run first calls `host.FindPRByHeadBranch`
for `a2a/<system>/<id>` (deterministic per D-002) — an existing open or
merged PR short-circuits the funnel and returns that PR's write-result
directly, without a second push/open cycle; this lookup has no dependency
on `internal/cache` (P7).

Steps: (1) validate via `internal/validate` (V2), including the
`min_binary_version` guard (§7.3, CC-085); (2) assemble ONE commit = artifact
file + its first lifecycle event; (3) `host.PushBranch` → `a2a/<system>/<id>`;
(4) `host.OpenPR` with auto-merge enabled — this call is uniform regardless
of whether the touched paths are CODEOWNERS-gated; see "Gating needs no
OpenPR parameter" (§T1) for how gate enforcement actually blocks the
auto-merge without any funnel-level branching; (5) return a write-result
value (branch, PR number/URL, commit SHA, state = pending-merge) to the
caller.
Persisting that pending-merge marker into `.a2a/cache/` is `internal/cache`'s
job (P7, out of this phase's footprint per ADR-001) — this phase's contract
ends at returning the data P7 needs, it does not write the cache file.

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-301 | AC-301.3 — Given no hub configured, when I run inbox/submit/sync, then everything works via direct git. [CC-042, E2E-7] | `internal/space` write-funnel + mirror-clone round trip succeeds with zero hub config anywhere in project/machine config; `go test ./internal/space/... -run TestDirectGitNoHub` |
| 2 | — | `internal/host`'s GitHub implementation pushes `a2a/<system>/<id>` and opens a PR with auto-merge enabled, returning PR number/URL | `go test ./internal/host/... -run TestOpenPRAutoMerge` against an interface-level fake |
| 3 | — | The write funnel produces exactly ONE commit containing the artifact file and its first lifecycle event before any push occurs | `go test ./internal/space/... -run TestFunnelSingleCommit` asserts commit tree contents |
| 4 | — | The write funnel refuses to write when the local binary version is older than `space.yaml`'s `min_binary_version`, remains read-only, and warns | `go test ./internal/space/... -run TestMinBinaryVersionGuard` (CC-085) |
| 5 | — | Credentials are resolved from env/keychain at call time and never appear, literally, in `.a2a/config.yaml` or `~/.config/a2a/config.yaml` on disk | `go test ./internal/space/... -run TestCredentialNeverInConfig` |
| 6 | — | The space layout builder's paths match the §4.2 tree exactly for all 8 artifact-type locations plus `consumes.yaml`, `decisions/`, `vendored/` | `go test ./internal/space/... -run TestLayoutPaths` (golden path table) |
| 7 | — | Mirror clone location resolves per the project config's per-space mirror-location key | `go test ./internal/space/... -run TestMirrorLocationResolution` |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | Adding a GitLab/Gitea profile = a new `internal/host` implementation of the same 5-method interface; zero `internal/space` changes (D-019, Q-004 tracked) |
| Coupling | Soft — `internal/space` → `internal/host` crosses only branch names, PR handles and status booleans; no GitHub API types leak into `space` |
| Migration path | low — the v2 hub-write path reuses the identical funnel with enforcement moved server-side ("Hub-mediated write remains the public-mode path — same funnel, enforcement moves server-side," D-002) |
| Roadmap conflicts | P7 (`internal/cache`) consumes this phase's write-result shape for the pending-merge marker — keep that return contract stable across phases; P8 (`contract publish/deprecate`, OP-212) reuses `internal/host`'s PR primitives for gated contract-publish PRs — same uniform `host.OpenPR` (auto-merge always enabled, §T1 "Gating needs no OpenPR parameter"), auto-merge simply doesn't fire until CODEOWNERS/V3 approval lands |

## 10. Implementor entry point

Execute as one wave of the epic; TDD default (fixture/mock-driven for host
GitHub calls — no live network in unit tests), framework-first (stdlib +
`os/exec` git, no new dependency), log-or-return per
[.claude/rules/go-conventions.md](../../../.claude/rules/go-conventions.md).
Full loop: [docs/features/README.md](../../README.md). Live-GitHub
integration (E2E-7, the real getvisa space) is P10/P11's concern, not this
phase's unit-test surface.

## 11. Amendments

> Append-only. When shipped reality deviates from this spec, record it here.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — from coherence audit (pre-implementation)

- Added the "Gating needs no OpenPR parameter" subsection (§T1) and a
  cross-reference in the write-funnel step 4 (§7): `host.OpenPR` is
  uniform/always-auto-merge per D-002, gating is enforced by
  CODEOWNERS+V3 blocking auto-merge, not by an API parameter — closes an
  ambiguity flagged in the coherence audit before P8/P9 built against it.
- Closed Open question 1 (credential resolution order) with an explicit
  precedence — explicit `A2A_*` env vars > machine-config keychain/helper
  reference > actionable missing-credential error — mirroring the §7.4
  actor-identity pattern; this is a lead adjudication at the spec level,
  pending a formal plan amendment to plan §7.4/§10.5 to record the same
  ordered list there.
- Closed Open question 2 (idempotent-retry PR lookup) by granting a 5th
  `internal/host` primitive, "Find PR by head branch," added to the T1
  capability table and to the write-funnel idempotency step (§7); updated
  the "4-method"/"4 explicitly granted" cross-references in §9 and the
  remaining Open question 3 to "5-method" for consistency.
- Tightened the §9 Roadmap-conflicts line's "gate-aware
  (non-auto-merge-until-approved)" phrasing for P8, which was ambiguous on
  the exact point fix 1 disambiguates — reworded to state explicitly that
  P8's gated PRs use the same uniform always-auto-merge `host.OpenPR`, not
  a toggled/non-auto-merge variant.

---

## Open questions

Genuine ambiguities found in the read sections for this phase — flagged per
the epic's consistency rule. Question text is append-only per the epic's
audit convention; #1 and #2 below carry a RESOLVED note appended in place
(2026-07-21, lead adjudication, see §11 Amendments) — #3 remains open.

1. **Credential resolution order** (§7.4, §10.5): §7.4 says credentials are
   "resolved from environment/keychain (§10.5)"; §10.5 says the PAT is
   "held in env/keychain (7.4)" — each section points to the other for the
   actual precedence. Unlike the explicit actor-identity precedence list in
   §7.4 ("explicit flags > `A2A_ACTOR_*` env vars > harness adapter defaults
   > config"), no equivalent ordered list or named env var exists for
   *credentials*. `internal/space`'s credential-resolution function needs
   one; not invented here.
   **RESOLVED (lead adjudication, 2026-07-21; see §11 Amendments):**
   precedence, mirroring the actor-identity pattern above: (a) explicit
   `A2A_*` credential env vars, if set; else (b) the keychain/credential-
   helper reference named in machine-level config
   (`~/.config/a2a/config.yaml`); else (c) an actionable error naming
   exactly which credential is missing and which of (a)/(b) was checked.
   Credentials never appear as plaintext in any config file (§10.5
   unchanged). This is a spec-level adjudication pending a formal plan
   amendment to §7.4/§10.5 to record the same ordered list there.
2. **Idempotent-retry PR lookup** (§7.2 preamble: "every mutating command is
   safe to re-run"; not itself in this phase's cited section list, but the
   only place that states the requirement): whether locating an
   already-open or already-merged PR for a given artifact ID on re-run is a
   `internal/host` adapter method (a 5th primitive, beyond the 4 explicitly
   granted to this phase's footprint: push/open/check-status/review-status)
   or is resolved entirely from `internal/cache`'s pending-merge marker
   (P7) is not specified. Flag for P6/P7 spec alignment.
   **RESOLVED (lead adjudication, 2026-07-21; see §11 Amendments):**
   granted as a 5th `internal/host` primitive, "Find PR by head branch"
   (§T1): the ephemeral branch name `a2a/<system>/<id>` is deterministic,
   so a re-run locates an existing open/merged PR by branch name directly;
   no dependency on P7's cache. See §7 write-funnel idempotency step.
3. Q-004 (§17, tracked, not invented here): non-GitHub host profile timing —
   directly relevant to whether `internal/host`'s 5-method interface is
   sufficient for a second (GitLab/Gitea) implementation; "on first demand,"
   no action required this phase.
