# §3 Domain Model: Object Types & Lifecycles

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> This section is the semantic core. Schemas that encode it are in §5;
> storage layout in §4; the agent algorithm that drives it in §8.

## 3.1 Object taxonomy (D-004)

Three lifecycle classes; eight types (D-004 revised per Q-009).

| Class | Type | Prefix | One-line purpose | Responds? |
|---|---|---|---|---|
| **S — standing** | contract | `XC` | Versioned interface a system provides; others implement against it | — |
| **S — standing** | requirement | `XR` | Published demand on another system's contract/capability: "this is what I need" | fulfilled via contract version + response |
| **X — exchange** | question | `XQ` | A question needing an answer (genuine ambiguity, defect report, choice request) | yes |
| **X — exchange** | work_request | `XW` | Request that the target perform work (data, feature, fix, investigation) | yes |
| **X — exchange** | decision | `XD` | Multi-party decision (ADR); binding once all required parties approve | approvals |
| **X — exchange** | handoff | `XH` | Transfer of implemented + tested work to another system's agents (§16) | verification |
| **X — exchange** | response | `XS` | The answer/result attached to a parent exchange; closes the loop | verified by requester |
| **B — broadcast** | announcement | `XA` | One-way notice (release, deprecation, migration, incident); no response expected | no |

Two former type-candidates are categories, not types (identical lifecycles
made separate types pure surface cost): a proposed change to another
party's artifact = `work_request` with `category:
contract-change|process-change` (+ required `proposed_change` summary and a
pinned target in `refs`); a periodic/state snapshot = `announcement` with
`category: status` (+ `period?`).

Coverage notes (from the producer-outbox evidence, designed anew — not copied):

- A defect report against a counterparty's contract ("your two sections
  disagree") is a `question` with `category: defect`.
- A data/dictionary request (the dominant real case) is a `work_request` with
  `category: data` (D-004).
- The legacy "briefing" (bundle of mixed items handed as one unit) is
  intentionally NOT a type: bundling existed because manual relay was
  expensive. With automated delivery, authors MUST decompose into
  single-intent artifacts linked by a shared `thread` (see 3.2, 3.6).
- "You are not blocked on us" is a first-class field (`blocking`,
  `interim_behavior`), not prose (see 3.6).

## 3.2 The single-intent rule

An artifact MUST carry exactly one intent of exactly one type. Multi-intent
documents ("we shipped X, also here's a question") violate the protocol.
Enforcement is honest about its limits: the validator checks what is
structurally provable (per-type field sets, forbidden field combinations);
prose-level smuggling is countered by template guidance and by the
recipient's right to decline with `reason_code: split-required` (CC-009),
after which tooling guides re-submission as a superseding thread batch.
Composite needs are the NORMAL case: decompose into parts (`a2a new
--thread`), submit together as one batch (OP-220) — one commit, one PR,
N single-intent artifacts.

- Related artifacts created together share a `thread` ID (3.6) and MAY
  reference each other; tooling renders threads as one conversation.
- RATIONALE: single-intent gives each item its own lifecycle state, its own
  closure, and unambiguous tracking — the core failure of the md-relay era
  was items with no individual state. Cost of authoring N small artifacts is
  near-zero for agents.

## 3.3 Identity scheme

| Class | ID format | Example | Uniqueness |
|---|---|---|---|
| standing | `<PREFIX>-<system>-<slug>` | `XC-axon-ingest`, `XR-axon-country-vocabulary` | slug unique within system; stable across versions |
| exchange, broadcast | `<PREFIX>-<system>-<YYYYMMDD>-<rand4>` | `XQ-axon-20260721-k3f9` | date + 4-char base32 random suffix; no central counter, no coordination (federation-safe) |

Rules: IDs are immutable, never reused, survive archival. `<system>` is the
authoring system. The full reference form for cross-space or immutable
citation is `id@version` (standing) or `id#digest` (any), per §5.

## 3.4 Lifecycle state machines

State is NEVER edited in place in the artifact file, and no envelope field
stores status. Every transition is an append-only **lifecycle event** (3.5);
current state is a deterministic fold of events, computed identically by the
binary and the hub. This removes the two-truths problem and lets each party
write only its own section (D-002).

**Drafts are local-only.** A `draft` lives under the project's `.a2a/`
staging area and never enters the space; the space contains only submitted/
published artifacts. The `draft → submit` (or `→ publish`) transition
collapses into the artifact's first commit: the submit/publish event travels
in the same PR as the artifact file. An artifact present in the space with
zero events folds to its class's post-submission state (`submitted` /
`published`); inbox and notification queries are defined over committed
artifacts only.

**Version-scoped transitions.** For contracts, `publish`, `deprecate` and
`retire` events carry a `version` field. A publish event records the commit
SHA and digest of the published content — this is how `id@version` resolves
forever (§5.4a); "deprecate (whole)" = an event without `version`.

### 3.4.1 contract (standing)

| From | Event | To | Actor |
|---|---|---|---|
| — | create | draft | owner system |
| draft | publish | published | owner system (human gate if first publish, §3.7) |
| published | publish (new version) | published | owner; semver rules of §5.4; breaking bump = human gate |
| published | deprecate (version or whole) | deprecated | owner; MUST name successor + sunset date; the deprecation announcement carries a machine-readable `deprecates: <XC-id>@<version>` link |
| deprecated | retire | retired | owner; autonomous only after sunset AND all registered consumers acked (`left` systems excluded); otherwise human-gated override per §5.4 (sunset passed + reminder recorded + G2-class PR, overridden consumers listed) — never time-triggered |

Every published version is immutable (git + digest). Consumers reference
`id@version`. Drafts MUST NOT be relied on by consumers (validator warns).

### 3.4.2 requirement (standing)

| From | Event | To | Actor |
|---|---|---|---|
| — | create | draft | requesting system |
| draft | publish | published | requesting system |
| published | acknowledge | acknowledged | target system |
| acknowledged | satisfy (refs contract `id@version` + response) | satisfied | target publishes, requester verifies — satisfy event is requester's |
| published/acknowledged | decline | declined | target; reason mandatory |
| any pre-satisfied | withdraw | withdrawn | requesting system |
| any | supersede (refs successor) | superseded | requesting system |

### 3.4.3 question / work_request (exchange)

| From | Event | To | Actor |
|---|---|---|---|
| — | create | draft | sender |
| draft | submit | submitted | sender |
| submitted | acknowledge | acknowledged | target ("seen") |
| acknowledged | accept | accepted | target ("will do"; MAY carry ETA). Optional for `question` — a question MAY go straight to responded |
| accepted | start | in_progress | target (optional granularity) |
| submitted…in_progress | decline | declined | target; reason mandatory |
| acknowledged…in_progress | block (refs blocker) | blocked | target |
| blocked | unblock | *pre-block state* | target; the fold recovers the state that held immediately before the block event (deterministic from the event sequence) |
| accepted/in_progress/acknowledged | respond (attaches `XS`) | responded | target |
| responded | close | closed | sender, after verifying (see 3.4.6: `verify` events target responses; `close` targets the parent). `a2a verify` on a single-response exchange emits verify + close together as a convenience |
| responded | dispute (reason) | in_progress | sender; the dispute event's `subject` = the disputed `XS` (folds it to `disputed`) AND the fold reopens the parent responded→in_progress (3.4.6); bounded — see CC-catalog for dispute loops |
| draft…in_progress | cancel | cancelled | sender |
| any open | supersede | superseded | sender |
| any open past `needed_by` | *(no auto-transition)* | — | staleness is flagged by tooling/dashboard, never auto-closed (silence must stay visible, S-7) |

Exchange types (X class except decision) MUST address exactly one system:
`to` has exactly one entry (schema-enforced). Multi-party needs = one
artifact per target on a shared thread. RATIONALE: per-target sub-state
machines are complexity with no v1 consumer.

### 3.4.4 decision (exchange, multi-party)

| From | Event | To | Actor |
|---|---|---|---|
| — | create | draft | any participant |
| draft | propose (lists required approvers) | proposed | author |
| proposed | approve | proposed (n/m recorded) | each required party, human gate |
| proposed | approve (last required) | approved | fold detects quorum = all required |
| proposed | reject | rejected | any required party; reason mandatory |
| rejected | supersede (refs successor decision) | superseded | author of the successor; the revised decision is a NEW `XD` on the same thread |
| approved | supersede | superseded | new approved decision only |

### 3.4.5 handoff (exchange)

| From | Event | To | Actor |
|---|---|---|---|
| — | create | draft | producing system |
| draft | submit | submitted | producing system; §16 completeness checks MUST pass |
| submitted | acknowledge | acknowledged | receiving system |
| acknowledged | verify-pass | accepted | receiver, after running the handoff's stated verification |
| acknowledged | verify-fail (findings) | rejected | receiver; producer resubmits as a new `XH` on the same thread |
| rejected | supersede (refs successor `XH`) | superseded | producer; the resubmission links its predecessor |

### 3.4.6 response (attached exchange)

`draft → submit → submitted`. A response MUST reference its parent exchange
ID. **One closure model, normative:** `verify` and `dispute` events target a
RESPONSE (`subject` = the `XS` ID) and fold that response to
`verified`/`disputed`. Parent movement: the parent CLOSES only via the
sender's explicit `close` event (`subject` = parent ID, legal from
`responded`); a `dispute` additionally reopens the parent
responded→in_progress as a fold side-effect (so the target can respond
again). One parent MAY receive multiple responses (partial answers), each
individually verifiable. A response's `parent` may be any answerable
artifact — an exchange OR a requirement (requirements complete via
`satisfy`, §3.4.2, not verify/close). Convenience: `a2a verify` on a
single-response exchange emits the response-verify and the parent-close
together (one PR); with multiple responses, `close` is a separate
deliberate act.

### 3.4.7 announcement (broadcast)

`draft → publish → published`; announcement additionally `published →
superseded`; `expired` is a fold-COMPUTED overlay from `valid_until` (no
event, no actor — the only time-derived display state, never a transition;
it is excluded from the event transition enum). Broadcasts are immutable
once published;
corrections are new broadcasts superseding the old. No acknowledgement is
required; targeted announcements (`to:` a list) MAY request ack-only
(`ack_requested: true`). **Broadcast acks are per-recipient annotations, not
transitions:** an acknowledge event on a broadcast does not change the
broadcast's state; the fold collects such events into a per-recipient ack
set (exempt from rule 3.5-2), which is exactly what the §5.4 retire
precondition reads. Deprecation announcements MUST carry the
machine-readable `deprecates: <XC-id>@<version>` field so the validator can
link acks to the contract version.

## 3.5 Lifecycle events

An event is a small, schema-validated, append-only file committed to the
**acting** system's own section (never the artifact author's section — this
is what makes cross-party transitions possible under section ownership).

Required fields: event ID (ULID), space, subject artifact ID, transition
name, resulting-state claim, actor block (as §2.1, including `system`),
timestamp, optional note, optional refs, optional `version` (contract
scope), optional `reason_code` (closed enum incl. `split-required`,
`security-concern`). Normative field table in §5.2.2.

Two special event kinds carry no state transition and are exempt from fold
rule 2: **`note`** (annotation/reminder on any open exchange, authorized for
either party — this is the 8.5 reminder mechanism) and **broadcast
acknowledge** (folds into the per-recipient ack set, 3.4.7).

Fold rules (normative, implemented once in the binary, reused by hub):

1. **Order = first-parent commit order on `main`** (the order merges landed
   — tamper-evident, clock-skew-immune); within one commit, events sort by
   ULID. Client timestamps are display metadata, never ordering authority: a
   backdated event committed later cannot reorder settled history (CC-099).
2. An event encoding an illegal transition for the current folded state is
   **ignored and flagged** as a protocol violation (surfaces in validation
   and on the dashboard; never crashes the fold).
3. An event by an actor not authorized for that transition (per the tables
   above + §10.3) is ignored and flagged likewise. Authorization is
   evaluated against the space manifest **as of the event's commit**
   (manifest history is part of the fold input) — so membership changes
   never rewrite historical folds.
4. The fold is deterministic: same commit history ⇒ same state, everywhere.
5. The event's resulting-state claim is informational: the fold's computed
   state wins; a mismatch is flagged, not honored.

## 3.6 Envelope metadata (summary; normative schema in §5)

Every artifact carries frontmatter with at minimum:

| Group | Fields |
|---|---|
| identity | `id`, `type`, `schema` (envelope schema version), `title` |
| addressing | `space`, `from` (system), `to` (systems; broadcasts MAY use `all`) |
| attribution | `actor` {kind, name, model?, session?}, `created`, plus git commit trail |
| intent | `category` (per-type enum), `priority` (`p1..p4`), `blocking` (bool: does the sender's work block on this?), `needed_by?`, `effort_estimate?` |
| expectations | `expected_response` {shape, by?}, `acceptance_criteria[]` (verify runs against these) |
| relations | `thread?`, `supersedes?`, `refs[]` (artifact refs as `id@version` / `id#digest` / `id@version#digest`), `origin?` (opaque IDs of the author's LOCAL tracker items, e.g. mate epic/spec — never a2ahub artifact IDs, never paths; artifact lineage uses `refs` and per-type fields: `parent` on response, `fulfills[]` on handoff, `migrated_from?` for legacy migration) |
| interim | `interim_behavior?` (what the sender does until resolved — required when `blocking: false` on requirement/work_request) |
| safety | `classification` (§10.4), `valid_until?` |

Per-type extensions (contract: `version`, `compat_policy`,
`generated_from?`, `schema_format`; handoff: §16 evidence block; etc.) are
defined in §5.

## 3.7 Human approval gates (D-008)

Agents are autonomous by default. A human (system owner) is REQUIRED only at:

| Gate | Where enforced |
|---|---|
| G1: first `publish` of a contract | PR review (CODEOWNERS) |
| G2: contract version with a breaking change (per §5.4) | PR review + CI compat check |
| G3: `approve`/`reject` on a decision | event merged from a PR authored/approved by the human owner's own GitHub account; V3 checks the PR identity against `space.yaml` owners (the self-declared `actor.kind` alone proves nothing — §10.3) |
| G4: onboarding/offboarding a participant (space manifest change) | space admin PR review |
| G5: anything crossing classification limits (§10.4) | validator block, human override only |

Everything else — drafting, submitting, acknowledging, accepting, responding,
verifying, closing, broadcasting — agents do without humans (R-001, §1.3).
RATIONALE: gates sit exactly where an error is expensive and hard to reverse;
all reversible steps stay autonomous. Gate list is per-space configurable in
the manifest (stricter allowed, looser not below G1–G3 in v1).

## 3.8 Relations, threads, supersession

- `thread`: free-form correlation ID minted by the first artifact
  (`thread:<system>-<YYYYMMDD>-<rand4>`); everything sharing it renders as
  one conversation. Threads have no lifecycle of their own.
- `supersedes`: the successor points backward; fold marks the predecessor
  superseded when the successor's supersede event lands. Supersession chains
  MUST be linear (validator rejects forks; see CC catalog).
- `refs`: standing references SHOULD pin (`id@version`); exchanges citing
  point-in-time content SHOULD pin digest (`id#digest`); the combined form
  `id@version#digest` pins both. Unpinned refs are valid but flagged by the
  validator as drift-prone.
- Archival: artifacts never move or get deleted; git history + closed states
  are the archive. Index-level filtering (hub, local HTML) keeps working sets
  small. Space-level growth policy is an ops concern (§9.4), not a lifecycle
  concern.
