# Fold Engine — Specification

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Plan**: [plans/04-fold-engine.plan.md](../plans/04-fold-engine.plan.md)
**Footprint**: `internal/fold/` only — may import `internal/artifact` (ADR-001);
**MUST NOT** import `internal/schema`, `internal/space`, `internal/host`,
`internal/validate`, `internal/cache`, `internal/cli`, or any package that
touches git/network/filesystem. Pure package: given the same inputs it
returns the same outputs, always (ADR-001 "fold and validate are pure — no
git/network").

---

## 0. User stories

> Plan IDs reused verbatim (epic rule: cite, never renumber) — see
> [14-us-ac.md](../../../the-plan/plan/14-us-ac.md).

| ID | User story |
|----|------------|
| US-302 | (IA) As an agent, lifecycle verbs write correct events that fold identically everywhere. [14-us-ac.md US-302] |
| L1 (phase-local) | As the binary and the (v2) hub, I need one fold implementation reused unchanged by both, so state can never diverge between surfaces (ADR-001 consequences: "the hub (v2) mounts fold/validate/schema unchanged"). |
| L2 (phase-local) | As `internal/cache` (P7) building computed inbox/outbox, I need incremental fold (apply-one-event) to agree with full replay, so cache rebuilds and hub reconcile (AC-501.1 shape) never diverge. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Lifecycle state machines, all 8 types | [03-domain.md §3.4](../../../the-plan/plan/03-domain.md#34-lifecycle-state-machines) | §3.4.1–§3.4.7, every row of every table — this IS the test-case list (T2). Read in full; not restated here. |
| Fold rules 1–5 | [03-domain.md §3.5](../../../the-plan/plan/03-domain.md#35-lifecycle-events) | Ordering, illegal/unauthorized handling, determinism, informational `state` claim. |
| Event schema | [05-schemas.md §5.2.2](../../../the-plan/plan/05-schemas.md) | `event/v1` field table — the Go input type's field set. |
| AuthZ matrix | [10-security.md §10.3](../../../the-plan/plan/10-security.md) | Role/membership enforcement points; fold's own check is defense-in-depth against illegitimate history, not the primary enforcement point (that is git-host merge + V3/V4). |
| Test tier | [13-testing.md §13.1 T2](../../../the-plan/plan/13-testing.md) | Fold-determinism tier definition this phase must satisfy. |
| Corner cases | [12-corner-cases.md CC-020…022](../../../the-plan/plan/12-corner-cases.md) | Fold-level illegal/unauthorized/conflicting-event scenarios. |
| Decisions | [17-decisions.md D-017/D-024/D-025/D-027](../../../the-plan/plan/17-decisions.md) | Fold order + closure model + note/ack exemption + single-target exchanges — top precedence, quoted below. |

---

### T1. Package contract (track: cli — no CLI verbs ship in this phase)

> This phase adds no `a2a` command. It is a pure library consumed by `internal/validate` (P3, illegal-transition checks feed V3/V4), `internal/cache` (P7, computed inbox/outbox), and the lifecycle/contract verbs (P6/P8) that will expose it through the CLI. The table below describes the exported contract as data shapes, not Go code (implementor's job per template header).

| Surface | Input | Output | Notes |
|---|---|---|---|
| Full fold | ordered event stream (see below) for one artifact + its own create/envelope facts (id, type, `from`, `to`/required-approvers where the type carries them) + a manifest-membership view | folded state (§3.4.x state name) + accumulated flags (illegal-transition, unauthorized-actor, state-claim-mismatch) + closure sub-state for each attached response + broadcast per-recipient ack set | Deterministic pure function of its inputs (D-017, rule 4). |
| Incremental fold | prior folded state (as returned above) + exactly one next event in canonical order | updated state + flags | MUST agree with full-fold-from-scratch over the same prefix (T2 property, L2). |
| Legality check (pre-write) | current folded state (as returned by fold) + candidate transition (event-shaped, not yet committed) + actor block + actor's system + manifest as of the target commit | verdict in {legal, illegal-transition, unauthorized-actor} | Pure function over the same transition-table data as the fold (§T1.1). Used ONCE per candidate event by `internal/validate`'s V2 path (P3 §7; 05-schemas.md §5.5 V2 "write refused locally") to refuse a write **before** it exists as a committed event — the only rejecting surface this phase exports; see the flag-set reconciliation note below the table. |
| Ordering key | caller-supplied per event: first-parent commit sequence number (or any caller ordering that reflects `main` merge order) + the event's own ULID | a total order over the input event set | Fold does **not** read git; the caller (space/host, which DO touch git) computes and supplies this key (D-017: "first-parent commit order on `main`... ULID = intra-commit tiebreak only"). Within one commit, ULID breaks ties (3.5 rule 1). |
| Manifest-membership view | caller-supplied per (system, commit-order-key) | member \| left \| unknown-as-of-that-commit | Fold never parses `space.yaml` (that's `internal/space`, off the import allowlist). The caller resolves manifest history and hands fold only the membership fact needed for 3.5 rule 3; role checks (is this system the exchange's `to`, the decision's required approver, the contract's owner) are derived from the artifact's own envelope facts already in fold's input — no extra manifest data needed for role, only for membership validity. |
| `expired` overlay (announcement) | folded state + `valid_until` (from envelope) + a caller-supplied reference instant | `expired: bool` display flag, never a state | Never an event, never in the transition enum (§3.4.7); fold has no clock, so "now" is caller-supplied — this keeps fold pure (no `time.Now()` inside the package). |

**Flag-set reconciliation (P3 §7 ↔ this spec):** the legality check's verdict set {legal, illegal-transition, unauthorized-actor} is a strict subset of the fold's flag set {illegal-transition, unauthorized-actor, state-claim-mismatch} — `state-claim-mismatch` is fold-only, since it compares a *committed* event's claimed `state` against the fold result, and a candidate (not-yet-committed) event has no committed claim to check.

---

## T1.1 Transition tables to encode as data (§3.4, cite-not-restate)

Every row of every table below is a distinct test case (T2 exit criterion,
13.2: "every transition-table row exercised ≥1"). Encode as data (e.g. a
table keyed by `(type, fromState, transition) → (toState, requiredActorRole)`)
so the table doubles as the fixture list — do not hand-write per-transition
`switch` branches that can drift from the plan.

| §3.4.N | Type | Rows |
|---|---|---|
| 3.4.1 | contract | create, publish (first + new version), deprecate (version/whole), retire |
| 3.4.2 | requirement | create, publish, acknowledge, satisfy, decline, withdraw, supersede |
| 3.4.3 | question / work_request | create, submit, acknowledge, accept, start, decline, block, unblock (recovers pre-block state — deterministic from event sequence, not stored separately), respond, close, dispute (reopens parent), cancel, supersede; `needed_by` passed = **no auto-transition** |
| 3.4.4 | decision | create, propose, approve (n/m, then quorum=all-required), reject, supersede (only from rejected/approved, per distinct rows) |
| 3.4.5 | handoff | create, submit, acknowledge, verify-pass, verify-fail, supersede |
| 3.4.6 | response (attached) | create, submit — plus the closure model below (D-024) |
| 3.4.7 | announcement | create, publish, supersede — plus the `expired` overlay and per-recipient ack set below (D-025) |

### 3.4.6 Closure model (D-024, normative — quoted)

> "`verify` and `dispute` events target a RESPONSE (`subject` = the `XS` ID)
> and fold that response to `verified`/`disputed`. Parent movement: the
> parent CLOSES only via the sender's explicit `close` event (`subject` =
> parent ID, legal from `responded`); a `dispute` additionally reopens the
> parent responded→in_progress as a fold side-effect... One parent MAY
> receive multiple responses (partial answers), each individually
> verifiable." — [03-domain.md §3.4.6](../../../the-plan/plan/03-domain.md)

Fold consequence: the engine tracks response sub-state *per response ID*,
independently of the parent's own state; `close` and `dispute` are the only
events whose `subject` differs in kind (response vs. parent) from every
other transition in §3.4 — this is the one place the fold's subject
resolution branches on transition name, not just on current state.

### 3.4.7 / D-025 Transition-free kinds (normative — quoted)

> "Two special event kinds carry no state transition and are exempt from
> fold rule 2: **`note`**... and **broadcast acknowledge** (folds into the
> per-recipient ack set, 3.4.7)." — [03-domain.md §3.5](../../../the-plan/plan/03-domain.md)

Fold consequence: `note` and broadcast-`acknowledge` events never appear in
the illegal-transition flag stream regardless of current state (they are
not evaluated against the transition table at all) and never change
`state`; broadcast-acknowledge instead appends to a per-recipient set keyed
by acting system, which is the exact structure the §5.4 retire precondition
(consumer-acked check, owned by P8) reads.

### D-017 fold order (normative — quoted)

> "Fold order = first-parent commit order on `main`... (ULID = intra-commit
> tiebreak only); authorization evaluated against the manifest as of the
> event's commit" — [17-decisions.md D-017](../../../the-plan/plan/17-decisions.md)

### D-027 single-target exchanges (normative — quoted)

> "Exchange types address exactly one system (`to` single-entry;
> broadcasts excepted)" — [17-decisions.md D-027](../../../the-plan/plan/17-decisions.md)

Fold consequence: role-authorization for `acknowledge`/`accept`/`decline`/
`respond`/etc. on an exchange is a single-system comparison (event actor's
`system` == artifact's `to[0]`), never a set membership check — D-027
removes the need for per-target sub-state the fold would otherwise have to
carry.

## T1.2 Illegal / unauthorized handling (3.5 rules 2–3, CC-020…022)

| CC | Scenario | Expected behavior (verbatim) |
|---|---|---|
| CC-020 | event encodes illegal transition (respond on closed exchange) | "fold ignores + flags (3.5 rule 2); event stays in history" — [12-corner-cases.md CC-020](../../../the-plan/plan/12-corner-cases.md) |
| CC-021 | event by unauthorized actor class (agent approves a decision) | "fold ignores + flags (3.5 rule 3, G3)" — [12-corner-cases.md CC-021](../../../the-plan/plan/12-corner-cases.md) |
| CC-022 | two contradictory events near-simultaneously (accept + decline) | "merge order on `main` wins (3.5 rule 1); loser becomes illegal-transition flag; parties see the conflict on the thread view" — [12-corner-cases.md CC-022](../../../the-plan/plan/12-corner-cases.md) |

Both flag classes (illegal-transition, unauthorized-actor) are non-fatal:
the fold function MUST NOT return an error/panic for these inputs — it
returns the folded state plus the flag list (3.5 rule 2/3: "ignored and
flagged... never crashes the fold"). The event itself is retained in the
returned history/flag record (CC-020: "event stays in history") — fold
never mutates or drops input events, it only excludes flagged ones from
state computation. The pre-write legality check (§T1) is the only surface
this package exports that rejects anything, and it rejects only *before* a
candidate event exists as a committed event; once an event is committed,
fold itself never rejects it — it only flags.

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Reuse `internal/artifact`'s ID type/parser (§3.3 ID scheme) for
  subject/parent/refs resolution — do not re-implement ID parsing here.
- [ ] One flag enum shared by illegal-transition and unauthorized-actor
  cases (both feed the same "protocol violation" surface per 3.5 rules
  2–3) — do not create two parallel flag types.
- [ ] The transition table itself is the single source for both the fold
  switch and the T2 fixture generator — derive fixtures from the table,
  never hand-duplicate rows into test code (drift risk called out by the
  brief: "the table IS the test-case list").

## 6. Testing requirements (T2)

| Area | What to test | Edge cases |
|------|--------------|------------|
| Every §3.4.1–3.4.7 legal row | folded `to` state matches the table exactly | first vs. non-first `publish` (contract), `unblock` recovering the correct pre-block state, decision quorum arithmetic (n/m vs. all-required) |
| CC-020 illegal transition | event ignored, flagged, retained, no crash | respond on `closed`; any transition from a terminal state (`retired`, `superseded`, `withdrawn`, `cancelled`) |
| CC-021 unauthorized actor | event ignored, flagged, retained, no crash | wrong system entirely; right system wrong role (e.g. non-required-approver "approves" a decision); `left` system per manifest-as-of-commit |
| CC-022 conflicting events | first-in-commit-order wins; loser flagged | same-commit ULID tiebreak; cross-commit ordering |
| 3.4.6 closure model (D-024) | multi-response independence; dispute reopens parent; close only from `responded`; close is a no-op-illegal from any other state | 2 responses, verify one, dispute the other, then close |
| D-025 note/broadcast-ack | never flagged illegal regardless of state; never changes `state`; ack accumulates per-recipient, not global | note on a `closed` exchange (still legal, exempt from rule 2); duplicate ack from same recipient |
| 3.4.7 `expired` overlay | computed only, never an event; absent from the transition enum; changes only the overlay flag, never `state` | `valid_until` in the past vs. future vs. absent |
| **T2 property: order-independence** | same event set, any legal arrival grouping (all-at-once vs. incremental one-at-a-time vs. chunked) ⇒ identical final state + flags | property-based generator over shuffled *valid* interleavings (illegal reorderings that violate commit order are out of scope — order is caller-guaranteed, not fold-inferred) |
| **T2 property: idempotent replay** | folding the same ordered event set twice, or replaying a duplicate event (same ULID) once more, does not change or double-apply state | duplicate `close` event; full re-fold from scratch vs. incremental continuation must match |
| Purity | `internal/fold` imports nothing beyond stdlib + `internal/artifact` | static import-list check (`go list -deps`) as a phase-scoped guard, not a repo-wide gate |

## 7. Schema / contract delta

No JSON Schema changes — this phase consumes the `event/v1` field set fixed
in [05-schemas.md §5.2.2](../../../the-plan/plan/05-schemas.md) (owned by
P2) as-is. Fold defines its own minimal Go input/output types (per T1
above) rather than depending on `internal/schema`'s parsed types, since
ADR-001 restricts this package's imports to `internal/artifact` only —
callers (validate/space/cache) are responsible for translating a validated
`event/v1` document into fold's input shape.

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-302 | AC-302.1 Given every legal transition of §3.4, when performed via CLI, then the folded state matches the transition tables on binary and hub. [T2, T4] | `go test ./internal/fold/... -run TestTransitionTable -race -count=1` — table-driven, one subtest per §3.4.x row; CLI/hub identity is proven once P6/P8/hub call this same package (out of this phase's scope to run the CLI itself) |
| 2 | US-302 | AC-302.2 Given illegal or unauthorized events injected into a section, when folded, then they are ignored and flagged, never crash. [CC-020…022] | `go test ./internal/fold/... -run TestIllegalAndUnauthorized -race -count=1` — includes CC-020/021/022 fixtures; asserts no panic/error return, flags present, event retained |
| 3 | — | Every row of §3.4.1–§3.4.7 has ≥1 executed test case (13.2 exit criterion, phase-scoped down payment; full `cc-coverage.yaml` wiring is P10) | count of table rows == count of distinct subtests, asserted by a meta-test |
| 4 | — | T2 order-independence property holds over shuffled valid interleavings | `go test ./internal/fold/... -run TestFoldOrderIndependence -race -count=1` |
| 5 | — | T2 idempotent-replay property holds | `go test ./internal/fold/... -run TestFoldIdempotentReplay -race -count=1` |
| 6 | — | D-024 closure model: multi-response independence, dispute reopens parent, close only from `responded` | `go test ./internal/fold/... -run TestClosureModel -race -count=1` |
| 7 | — | D-025: `note` and broadcast-ack never flagged illegal, never change `state` | `go test ./internal/fold/... -run TestTransitionFreeKinds -race -count=1` |
| 8 | — | 3.4.7 `expired` is overlay-only: absent from transition enum, computed from caller-supplied reference instant, never an event | `go test ./internal/fold/... -run TestExpiredOverlay -race -count=1` |
| 9 | — | Purity: `internal/fold` imports only stdlib + `internal/artifact` | `go list -deps ./internal/fold/... \| grep a2ahub` shows only `internal/artifact` (plus fold's own subpackages) |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | New object types/transitions are new data-table rows, not new switch branches — adding a v2 type costs one table entry + fixtures, per ADR-001's "hub (v2) mounts fold... unchanged." |
| Coupling | Soft only: fold consumes plain event/state value shapes, never a git object or schema-package type; callers own translation. No shared mutable state — every fold call is a pure function of its arguments. |
| Migration path | low — the package has no I/O and no schema version awareness beyond the `event/v1` field set fixed by P2; a future `event/v2` is a caller-side translation concern. |
| Roadmap conflicts | P7 (cache/computed inbox) and P8 (lifecycle verbs) both depend on this package's exact function/type shapes; changing them after those phases start requires an amendment here plus downstream updates in both specs. |

## 10. Implementor entry point

Execute as one wave of the `v1-min-2026-07` epic, after P1 (artifact IDs)
and P2 (event schema) land (`blocked_by: [P1, P2]` in
[tracker.yaml](../tracker.yaml)). TDD default: write the table-driven §3.4
fixtures first (red), then the fold switch/table lookup that satisfies them
(green); property tests (order-independence, idempotent replay) follow the
same red-green discipline. Framework-first: stdlib only, no third-party
state-machine library — the plan's tables already ARE the state machine.
Log-or-return per
[.claude/rules/go-conventions.md](../../../../.claude/rules/go-conventions.md);
this package returns flags, it never logs (pure, no I/O side effects
including stderr).
Full loop (README/tracker/specs shapes, lint gate):
[docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it here AND amend any downstream spec.

### 2026-07-21 — from coherence audit (pre-implementation)

- Added a third §T1 export, "Legality check (pre-write)", to close the P3↔P4 seam: P3 §7 and 05-schemas.md §5.5 (V2 "write refused locally") require a distinct pre-write legality primitive, not just fold's own post-hoc flags.
- Added an explicit flag-set reconciliation sentence under the §T1 table: the legality check's verdict set is a strict subset of fold's flag set, with `state-claim-mismatch` called out as fold-only (no committed claim exists to check for a not-yet-committed candidate event).
- Clarified in §T1.2 that the legality check is the only rejecting surface this package exports and rejects only pre-write; fold itself keeps its existing non-fatal semantics (never errors/panics on flagged, already-committed events) unchanged.

### 2026-07-21 — from wave 2: shipped-reality deltas

- **respond self-loop added** (responded→respond→responded) beyond the
  literal §3.4.3 rows — without it a second response is structurally
  illegal, contradicting D-024's explicit multi-response guarantee.
- **Zero-events fallback**: `Fold` over an empty stream returns each kind's
  post-submission state (per the domain doc's literal line); the internal
  per-event starting carrier is `draft`. The asymmetry is documented and
  unreachable in practice (entry event travels in the artifact's first PR);
  decision's empty-stream case stays `draft` (its first event is propose).
- **Decision supersede is membership-only**: the table's stated actor
  ("author of the successor decision") is a fact about a different,
  not-yet-existing artifact — structurally unverifiable from the
  predecessor's envelope. Real enforcement gap flagged to P3/P8 (backlog).
- **`note` additionally gets rule-3 authorization** (either party;
  unauthorized-actor flag for a third party, never illegal-transition,
  never a state change) — interpretive extension of D-025 for fidelity to
  the surrounding prose; broadcast-ack stays membership-only.
- **Unknown membership is fail-closed** everywhere (spec left the policy
  open; defense-in-depth default chosen).
- **Contract carries one top-level state** (publish-new-version =
  published self-loop); no per-version sub-state machine this phase.
- **ExpiredOverlay(validUntil, reference)** drops the folded-state input
  the §T1 table listed — the overlay never depends on it.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
