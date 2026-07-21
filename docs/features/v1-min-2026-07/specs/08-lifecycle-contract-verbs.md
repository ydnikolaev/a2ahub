# P8 — Lifecycle & Contract Verbs — Specification

> Track: **cli** (§T1 only — other track sections deleted per template).
> Realizes plan §7.2 OP-211 (generic lifecycle verbs), OP-212 (contract
> lifecycle), OP-213 (`contract verify-export`), and the `contract diff`
> slice of OP-221. Every verb authors an `event/v1` (§5.2.2, P2-shipped) and
> ships via the P5 write funnel (D-002); this spec never restates §3.4's
> transition tables — it cites them.

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Plan**: [plans/08-lifecycle-contract-verbs.plan.md](../plans/08-lifecycle-contract-verbs.plan.md)
**Footprint**: `internal/cli/` (new: `cmd_lifecycle.go` for all OP-211
generic lifecycle verbs; `cmd_contract.go` for OP-212/213 + the
`contract diff` slice of OP-221), `internal/validate/` (named addition
only: `policy_retire.go` — a retire-precondition **policy**-class check
per §5.4, the "engine hook" the brief calls for; CI wiring of this hook
into V3 is P9's, not touched here), `cmd/a2a/` (wiring lines registering
the new verbs only, no other changes). Imports allowed per ADR-001
(`internal/cli` → "core packages above"): `internal/artifact` (digests,
§5.7), `internal/schema` (contract/event field access), `internal/fold`
(transition legality — reused, never re-implemented here),
`internal/validate` (V1/V2 pipeline + the new policy hook), `internal/space`
(write funnel + mirror read for `contract diff`/`verify-export`). No
`internal/cache`, no `internal/mcp`. No direct `internal/host` import: this
is a phase-local choice, not an ADR-001 rule — ADR-001's table permits
`cli` → `host` directly, but this phase's verbs never need it because the
`internal/space` write funnel (§5, D-002) already covers every write this
phase performs.

---

## 0. User stories

> Global plan IDs reused verbatim per the epic SSOT rule (README: "cite
> stable IDs... never restate"); no new local IDs invented.

| ID | User story (from 14-us-ac.md, phase-angled) |
|----|------------|
| US-302 | As an agent, I perform every legal §3.4 transition through one verb-per-transition CLI surface and get the same folded state as the hub. |
| US-302 | As an agent triaging a backlog, I pass N artifact IDs to one lifecycle verb and get one commit, one PR (OP-211 batch triage). |
| US-202 | As a system owner, `a2a contract retire` refuses locally when registered consumers haven't acked, and only proceeds over them via a reviewed, precondition-checked override PR. |
| US-202 | As a system owner, `a2a contract publish` opens a review-requiring PR exactly when G1 (first publish) or G2 (breaking bump) applies — never silently. |
| US-1001 | As the axon agent, `a2a contract verify-export --local` gives me the exact digest-compare CLI axon's CI wires in to enforce the §5.3 generation guard. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Transition tables | [03-domain.md §3.4](../../../the-plan/plan/03-domain.md) | §3.4.1–3.4.7 per type; §3.4.6 closure model (verify/dispute target the response, close targets the parent); §3.5 fold rules (never re-implement — call `internal/fold`) |
| Event shape | [03-domain.md §3.5](../../../the-plan/plan/03-domain.md), [05-schemas.md §5.2.2](../../../the-plan/plan/05-schemas.md) | `event/v1` fields; `note`/broadcast-ack are transition-free (D-025) |
| Contract versioning | [05-schemas.md §5.3, §5.4](../../../the-plan/plan/05-schemas.md), D-010, D-023, D-029 | semver rules, G1/G2, retire-block + override, publish-event version resolution, multi-file digest |
| Command catalog | [07-client.md §7.2 OP-211/212/213/221](../../../the-plan/plan/07-client.md) | flags are implementation detail; semantics are not |
| Human gates | [03-domain.md §3.7](../../../the-plan/plan/03-domain.md) | G1–G5; only G1/G2/G3 are reachable from this phase's verbs |
| Write funnel | [17-decisions.md D-002](../../../the-plan/plan/17-decisions.md) | validate → ephemeral branch → PR → auto-merge (ungated) or CODEOWNERS review (gated) — this phase decides gated/ungated per verb, P5 executes it |

---

### T1. CLI surface (track: cli)

**Generic lifecycle verbs (OP-211).** One verb per §3.4 transition name
except `create`/`submit`/`propose` (collapsed into OP-205/OP-220 per D-026)
and `deprecate`/`retire` (contract-only, exclusively under `contract`, see
below — absent from OP-211's own list). Every verb: accepts multiple IDs
(batch = one commit, one PR); runs V2 (schema/referential/authz/lifecycle
legality via `internal/fold`+`internal/validate`, reused not reimplemented)
before ships; refuses locally on illegal transition or wrong actor
(AC-302.1); every verb ships through the SAME uniform write funnel
(auto-merge always on, §5 below) — no verb, including `approve`/`reject`,
passes the funnel a gate/review parameter. G3 (§3.7) still applies to
`approve`/`reject`: CODEOWNERS on the decision's gated path plus V3's
required check hold auto-merge until an approving owner review lands.
The approve/reject identity binding itself is NOT a host primitive: V3
enforces it by checking the approve event's actor against the decision's
`required_approvers` and the diff-authz github-login→system mapping of the
PR author, with fold-level authorization as the second net (P9).

| Command | Flags | Applies to (§3.4 table) | Notes |
|---|---|---|---|
| `a2a {ack,accept,decline,start,block,unblock,cancel} <id...>` | `--reason <text>` (required: decline), `--reason-code <enum>` (decline), `--refs <blocker-id>` (required: block) | 3.4.3 question/work_request, 3.4.5 handoff (`ack` only), 3.4.2 requirement (`ack`/`decline` only) | `unblock` recovers the pre-block fold state deterministically (3.4.3); no `--refs` needed |
| `a2a respond <parent-id...>` | `--field k=v`, `--body-file <path>`, `--result <answered\|delivered\|partial\|cannot>` (required) | 3.4.6 | scaffolds + submits an `XS` per parent in one flow (draft→submit collapsed, D-026); batch = N parents, one PR |
| `a2a verify <response-id\|parent-id>...` | `--refs` (optional, disambiguates multi-response parents) | 3.4.6 | subject = response; on a single-response exchange also emits `close` on the parent in the same PR (D-024 convenience) |
| `a2a dispute <response-id>` | `--reason <text>` (required), `--reason-code <enum>` | 3.4.6 | folds response→disputed; fold side-effect reopens parent responded→in_progress (one event, not two) |
| `a2a close <parent-id...>` | — | 3.4.6 | sender's explicit close; legal only from `responded`; REQUIRED (not automatic) when a parent has multiple responses |
| `a2a supersede <id>` | `--refs <successor-id>` (required) | 3.4.1–3.4.5, 3.4.7 | linear chains only — validator (existing, P3/P4) rejects forks per §3.8 |
| `a2a withdraw <requirement-id...>` | — | 3.4.2 | any pre-satisfied state |
| `a2a satisfy <requirement-id>` | `--refs <XC-id@version>,<XS-id>` (required) | 3.4.2 | event is the requester's (target already published; requester verifies + authors satisfy) |
| `a2a approve <decision-id>` / `a2a reject <decision-id>` | `--reason <text>` (required: reject) | 3.4.4 | ALWAYS G3-gated PR (see above); fold detects quorum on the last required approve |
| `a2a verify-pass <handoff-id>` / `a2a verify-fail <handoff-id>` | `--findings <text>` (required: fail) | 3.4.5 | receiver runs the handoff's stated verification (§16, cited not restated) before calling |
| `a2a note <id...>` | `--note <text>` (required) | 3.5, D-025 | transition-free annotation, either party, no fold-legality check — see Open Q1 on scope |

**Contract verbs (OP-212/213 + `contract diff` of OP-221).**

| Command | Flags | Gate posture (CODEOWNERS + V3 required check — not a funnel parameter) | Notes |
|---|---|---|---|
| `a2a contract new <slug>` | as `a2a new contract` | — | thin alias into the P6-built `a2a new`/OP-203 template path (§5.6); no new draft logic in this phase — see §5 reuse |
| `a2a contract publish <id>` | `--version <semver>` or `--bump major\|minor\|patch`, `--generated-from-digest <hex>` (optional, §5.3) | Advisory-gated on first publish (G1) or self-declared major bump (G2): the verb adds an advisory gate marker to the PR title/body; CODEOWNERS on the contract's path + V3's §5.4b compat check (the authoritative backstop, P9) hold auto-merge until an approving owner review lands. Self-declared minor/patch: no marker, auto-merge proceeds once V3 passes — same funnel call either way, no parameter toggled | Draft→published (first) or published→published (new version); publish event records commit SHA + the §5.7/D-029 multi-file digest tree over the published `schema/**`+`fixtures/**` (§5.4a version resolution) |
| `a2a contract deprecate <id>` | `--version <semver>` (omit = whole-contract), `--successor <XC-id@version>` (required), `--sunset <date>` (required) | No advisory gate marker (deprecating is reversible awareness, not the breaking act itself); auto-merge proceeds once V3 passes, same as any other write | authors the deprecate event AND a linked `announcement` (category `deprecation`, `ack_requested: true`, `deprecates: <XC-id>@<version>`, §5.2.1/3.4.7) in the same PR |
| `a2a contract retire <id>` | `--version <semver>`, `--override` | No gate marker when the retire-precondition policy check passes cleanly (all registered consumers acked, §5.4) — refused locally, never reaching the funnel, when it fails and `--override` absent (AC-202.2); with `--override`, still refused locally unless sunset passed AND ≥1 `note` reminder recorded on the deprecation thread AND actor is human — then the verb adds an advisory gate marker (mirrors G2) listing overridden consumers, and CODEOWNERS + V3 hold auto-merge until an approving owner review lands (AC-202.3) | calls the new `internal/validate` retire-precondition hook (registered consumer = satisfied requirement ∪ `consumes.yaml` entry, §5.2.3/D-022; `left` systems excluded, §5.4) |
| `a2a contract diff <id> <v1> <v2>` | `--json` | read-only, no funnel | resolves both versions via publish-event lookup → commit SHA (§5.4a/D-023), reads the space mirror at those SHAs, renders added/removed/changed paths under `schema/**`+`fixtures/**` |
| `a2a contract verify-export --local <path> <id>[@version]` | — | read-only, no funnel | computes the §5.7/D-029 multi-file digest tree over `<path>/schema/**`+`<path>/fixtures/**`; compares to the space copy's `generated_from.source_digest` (or the resolved version's recorded digest); exit 0 on match, non-zero + path-level diff on mismatch (AC-1001.1 — CLI command only; wiring into axon's own CI pipeline is P12) |

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `internal/fold`'s transition tables are THE legality check (ADR-001:
      `fold` is pure, reused by V2/V3/V4/hub) — no verb re-derives §3.4
      locally; every verb calls the existing V1/V2 pipeline (P3/P4 output).
- [ ] The P5 write funnel (validate → ephemeral branch → PR, D-002) is the
      ONLY commit/push path; every verb goes through the SAME uniform
      funnel call (`space` → `host.OpenPR`, auto-merge always on) — no
      verb opens a PR by hand, and no verb passes the funnel a
      gate/review-required parameter. Gating is enforced downstream by
      CODEOWNERS on gated paths plus V3's required check (§5.4b for
      `contract publish`'s G1/G2, §3.7 for G3 decisions); auto-merge
      cannot fire until an approving owner review lands. A verb MAY add an
      advisory gate marker to the PR title/body (see the contract verbs
      table below) but never toggles auto-merge itself — this mirrors P5's
      "Gating needs no OpenPR parameter" fix; see also P9's V3
      required-check enforcement.
- [ ] `a2a contract new` reuses the P6-built `a2a new <type>`/OP-203
      staging+template mechanics verbatim (drafts under `.a2a/staging/`,
      D-026) — it is registration/discoverability sugar, not new logic.
- [ ] Digest computation (single-file §5.7 and multi-file §5.7/D-029) comes
      from `internal/artifact` — `contract publish`, `contract diff`, and
      `contract verify-export` all call the same digest-tree helper, never
      three separate implementations.
- [ ] OP-220's batch pattern (all-or-nothing validate, one commit/one PR) is
      the model for every multi-ID verb here — do not invent a second batch
      mechanism.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| Every OP-211 verb | legal transition → correct event + correct folded state on a fresh binary fold (AC-302.1's binary half only; the hub half is excluded from v1-min per [15-rollout.md](../../../the-plan/plan/15-rollout.md) L1 exit ("hub clauses excluded — re-verified at L3") and D-030 — re-verified when v2 ships the hub) | illegal transition for current state; unauthorized actor (wrong `system`); batch of 3 IDs → exactly one commit/one PR |
| `respond`/`verify`/`dispute`/`close` | single-response verify emits close (D-024); multi-response verify does NOT auto-close; dispute reopens parent | disputing an already-disputed response; `close` attempted from a non-`responded` state |
| `contract publish` | first publish → G1 gated; declared-major → G2 gated; minor/patch → ungated; digest+SHA recorded on the event | missing `--version`/`--bump`; publish on a `draft` still `draft` (no prior published version edge) |
| `contract retire` | clean ack set → succeeds ungated (AC-302.1 general path); un-acked, no override → blocked (AC-202.2) | un-acked + sunset passed + agent actor → blocked (AC-202.3 first clause); un-acked + sunset passed + no reminder → blocked (AC-202.3 first clause); un-acked + sunset passed + reminder + human + `--override` → succeeds, overridden consumers flagged `retired-unacked` (AC-202.3 second clause); `left` consumers excluded from the ack set (§5.4) |
| `contract diff` | two published versions → correct path-level delta | one version unresolved (no publish event); v1 == v2 |
| `contract verify-export --local` | matching local export → exit 0 (AC-1001.1) | mismatched digest → non-zero + diagnostic; missing `schema/`/`fixtures/` dirs locally |
| `note` | annotation lands without touching fold state | noting a closed/superseded artifact (should still be legal per D-025 — flag if plan disagrees, Open Q1) |

## 7. Schema / contract delta

None. This phase authors instances of `event/v1` (§5.2.2) and reads
`contract`/`requirement`/`decision`/`handoff`/`announcement` envelope fields
(§5.2.1) already shipped by P2 — no new fields, no schema version bump.

## 8. Acceptance criteria

> Rows 1–4 copied verbatim from `14-us-ac.md`; do not edit. Rows 5+ are
> phase-local (US "—"), added per this brief's scope.

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| AC-302.1 | US-302 | Given every legal transition of §3.4, when performed via CLI, then the folded state matches the transition tables on binary and hub. [T2, T4] | run every §3.4 transition via its verb against a fixture space; assert fold output |
| AC-202.2 | US-202 | Given un-acked registered consumers (per the space-visible registry: satisfied requirements + `consumes.yaml`, §5.4), when retire is attempted, then the validator blocks it. [CC-081, CC-082] | `a2a contract retire` on a fixture contract with an un-acked `consumes.yaml` entry; assert local refusal + machine code |
| AC-202.3 | US-202 | Given un-acked consumers and sunset passed, when retire is attempted by an agent actor or without a recorded reminder, then the validator blocks it; when submitted as a human-reviewed override PR meeting all §5.4 preconditions, then retire succeeds and each overridden consumer is flagged `retired-unacked` and notified. [CC-082, CC-086] | two fixture runs: (a) agent actor / no reminder → blocked; (b) human actor + reminder + `--override` → succeeds, overridden list flagged |
| AC-1001.1 | US-1001 | Given the axon export, when `a2a contract verify-export` runs in axon CI, then digest match is enforced; the space copies carry `generated_from`. [CC-084] | run `a2a contract verify-export --local` against a matching and a deliberately-drifted fixture export; assert exit codes |
| P8-1 | — | Batch triage: N artifact IDs on one lifecycle verb produce exactly one commit and one PR carrying N events | `a2a ack XQ-1 XQ-2 XQ-3` against a fixture space; assert one PR, three events |
| P8-2 | — | `contract publish` opens a gated (review-required) PR exactly on G1 or self-declared-major G2, ungated otherwise | three fixture publishes: first-ever, declared-major, declared-minor; assert gate flag per case |
| P8-3 | — | `approve`/`reject` always open a G3-gated PR regardless of prior V3 state | fixture decision approve; assert gate flag always set |
| P8-4 | — | `contract diff` renders the correct added/removed/changed path set between two resolved versions | two-version fixture contract with a schema field added; assert diff output |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | new §3.4 transition names slot in as new verb-table rows without touching the funnel or `internal/fold`; the retire-precondition hook is the only policy-class addition and is itself reused unchanged by V3 in P9 |
| Coupling | soft — verbs depend on `internal/fold`'s tables and `internal/validate`'s pipeline by interface, never duplicate them; the funnel's gate signal is the only new coupling point with P5 |
| Migration path | low — v2 hub reuses `internal/fold`/`internal/validate` unchanged (D-011/D-012); no verb here is hub-specific |
| Roadmap conflicts | P9 (V3 CI wiring of the retire hook), P12 (axon CI wiring of `verify-export`) both consume this phase's output without modifying it — sequencing matters, not shape |

## 10. Implementor entry point

Execute as one wave of the `v1-min-2026-07` epic, blocked_by P6 (tracker.yaml).
TDD default: red on each transition's illegal/unauthorized-actor case before
green; framework-first (stdlib `flag`/`net/http`-free CLI per repo
precedent — check P1/P6 output before inventing parsing); log-or-return per
`.claude/rules/go-conventions.md`. Full loop:
[docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. When shipped reality deviates from this spec, record it here
> AND amend any downstream spec (notably P9, P10, P12).

### 2026-07-21 — from wave 4: shipped-reality deltas

- **Multi-file digest tree is CONTRACT-ROOT-relative, not repo-relative.**
  §5.7's prose says "repo-relative-path", but `verify-export --local <path>`
  operates on an arbitrary local export dir with no repo-relative path — so
  the digest tree roots at the contract's own directory (paths like
  `schema/main.schema.json`). **P12 (axon CI wiring of verify-export) MUST
  use the same contract-root-relative basis.** Plan §5.7 wording flagged for
  an operator amendment (backlog).
- **Publish events do not record the real `commit` SHA.** The SHA is only
  known after `space.WriteFunnel.Submit` returns (post-commit), and
  backfilling it would violate "one commit". `digest` IS recorded
  (pre-commit computable). `contract diff` therefore resolves `id@version`
  by walking the contract descriptor's git history (`git log`/`show`/
  `ls-tree`, read-only), not by publish-event→SHA lookup. Pre-existing P6
  gap (cmd_submit never set CommitSHA either). Plan D-023 "records commit
  SHA" clause flagged for operator amendment (backlog).
- **retire code = POL-006** (registry): retire refused when a registered
  consumer hasn't acked; override needs sunset-passed + reminder + human.
  The lead added its production path to internal/validate's closure test
  (registry_test.go was off P8's allowlist).
- **fold verify/dispute legality** closed in internal/fold/legality.go (the
  wave-3 backlog gap) — a pure 26-line addition, existing verdicts
  untouched. NOTE: fold's `Apply` still has no dispatch for a response's own
  draft→submit mini-lifecycle (§3.4.6) — out of this phase's granted fold
  footprint (only legality.go); a response is introduced by `a2a respond`'s
  submit event, folded via the primary path. Flag for a future fold pass if
  a response sub-state beyond verified/disputed is ever needed.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — from coherence audit (pre-implementation)
- Rewrote the T1 generic-verbs intro's gating sentence and the contract
  verbs table's "Gate behavior" column (now "Gate posture") to remove the
  implication that verbs pass a gate/review parameter into the write
  funnel/host API; corrected mechanism: every write uses the same uniform
  funnel call with auto-merge always on, and gating is enforced by
  CODEOWNERS on gated paths plus V3's required check (§5.4b for
  publish G1/G2, §3.7 for G3) — verbs may only add an advisory PR
  title/body marker. Mirrors the P5 fix ("Gating needs no OpenPR
  parameter"); cross-references P5 and P9.
- Rewrote the T1 intro's `approve`/`reject` G3 sentence to state that the
  approve/reject identity binding is not a host primitive: V3 enforces it
  by checking the approve event's actor against the decision's
  `required_approvers` and the diff-authz github-login→system mapping of
  the PR author, with fold-level authorization as the second net (P9).
- Footprint: replaced the underspecified "`cmd_verbs.go` or per-verb
  files" with the committed filename `internal/cli/cmd_lifecycle.go` for
  all OP-211 verbs, and named the `internal/validate` addition exactly
  `internal/validate/policy_retire.go`.
- Footprint: dropped the "no `internal/host` direct import (space wraps
  host per ADR-001)" mis-citation — ADR-001's table permits `cli` → `host`
  directly; restated the exclusion as a phase-local choice, since the
  `internal/space` write funnel already covers every write this phase
  performs.
- §6 testing requirements: rewrote the "Every OP-211 verb" row to verify
  only the binary fold, since the hub half of AC-302.1 is excluded from
  v1-min per 15-rollout.md's L1 exit ("hub clauses excluded — re-verified
  at L3") and D-030; the hub half re-verifies when v2 ships the hub. The
  AC-302.1 row in §8 is unchanged (quoted verbatim from the plan).

---

## Open questions (spec-local — not existing plan Q-### items)

1. **`note` scope: "exchange" vs. broadcast/standing threads.** §3.5/D-025
   scopes `note` to "any open exchange" (X-class), but §5.4's retire
   override precondition requires "at least one `note` reminder event... on
   the deprecation thread" — and a deprecation is an `announcement`
   (B-class), not an exchange. This phase implements `note` as legal on any
   open artifact with a thread (X, S, or B) to satisfy the override
   precondition; a plan amendment to §3.5/D-025 should confirm or narrow
   this. [§3.5, §5.4, D-025]
2. **OP-211's generic `publish` verb vs. OP-212's `contract publish`.**
   OP-211 lists `publish` among its per-transition verbs, but cross-checking
   §3.4: contract's first publish and requirement's/announcement's publish
   are all first-transitions (collapsed into OP-205/D-026); the only
   non-first `publish` (contract's version bump, §3.4.1) is contract-only
   and OP-212 already owns it with gate-aware behavior this phase needs
   anyway. This spec does NOT add a separate bare `a2a publish`; `publish`
   is realized exclusively through `a2a contract publish`. Flag for plan
   clarification — OP-211's catalog entry may be redundant with OP-212 or
   may intend a verb this spec is missing. [§7.2 OP-211, OP-212]
3. **Atomicity of major-bump publish + prior-version deprecate.** D-010
   requires a breaking publish to come with "prior version deprecated with
   explicit sunset date" but §3.4.1 models `publish` and `deprecate` as
   separate transitions/events, and the plan does not state whether they
   must land in one PR or may be sequential. This spec treats them as two
   independently-invocable verbs (`contract publish` then `contract
   deprecate`) and does not enforce ordering/atomicity in code; V3 (P9) is
   left to enforce the precondition at merge time. Flag for §5.4
   clarification. [§5.4, §3.4.1]
