# §5 Schemas & Validation

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).

## 5.1 Schema stack (D-009)

| Layer | Format | Applies to |
|---|---|---|
| envelope schemas | JSON Schema 2020-12, one per object type extending a common base | frontmatter of every artifact (`.md` = YAML frontmatter + Markdown body) |
| event schema | JSON Schema 2020-12 | lifecycle event files (pure YAML) |
| manifest schema | JSON Schema 2020-12 | `space.yaml`, local project config |
| contract payload schemas | project's choice, declared per contract (5.3) | `provides/<slug>/schema/` |

Envelope/event/manifest schemas are **product schemas**: authored in the
a2ahub repo, versioned with the binary, embedded into it, and published under
stable URIs. Every artifact declares which envelope schema version it was
written against (`schema: envelope/v1`). RATIONALE: schemas and validator
travel as one artifact — the no-drift requirement R-004 made executable.

## 5.2 Envelope: normative field set

Common base (all types). `?` = optional. Enums closed unless stated.

| Field | Type/enum | Notes |
|---|---|---|
| `schema` | `envelope/v<N>` | envelope version the artifact targets |
| `id` | per §3.3 | validated against filename and section |
| `type` | the 8 types (§3.1) | MUST match ID prefix |
| `title` | string ≤120 | human/agent scannable |
| `space` | space ID | MUST match hosting repo |
| `from` | system ID | MUST match owning section (validator + authz). Exception `type: decision`: lives in `decisions/`, `from` = drafting system, write authz via the decision flow (§4.2) |
| `to` | [system ID] \| `all` (broadcasts only) | exchanges (X class except decision): EXACTLY one entry (3.4.3); broadcasts: list or `all` |
| `actor` | {kind: human\|agent, name, model?, session?} | attribution per §2.1 |
| `created` | RFC 3339 UTC | |
| `category` | per-type enum (5.2.1) | |
| `priority` | p1\|p2\|p3\|p4 | p1 = drop-everything; default p3 |
| `blocking` | bool | is the sender's work blocked on this? |
| `needed_by?` | RFC 3339 date | staleness reference, never auto-close |
| `effort_estimate?` | xs\|s\|m\|l\|xl | sender's guess of target's effort |
| `expected_response?` | {shape: free text, by?: date} | what a good answer looks like |
| `acceptance_criteria?` | [string] | REQUIRED on work_request, requirement, handoff; verify (§3.4) runs against these |
| `thread?` | thread ID | §3.8 |
| `supersedes?` | artifact ID | linear chains only |
| `refs?` | [{ref: `id@version` \| `id#digest` \| `space:id...`, note?}] | pinning rules §3.8 |
| `origin?` | [string] | opaque LOCAL tracker IDs (epic/spec) only; never a2ahub artifact IDs, never paths (lineage: `refs`, `parent`, `fulfills`) |
| `migrated_from?` | string | opaque legacy identifier for one-time migrations (e.g. producer-outbox filename) |
| `interim_behavior?` | string | REQUIRED when `blocking: false` on requirement/work_request |
| `classification` | per §10.4 | default `internal` |
| `valid_until?` | RFC 3339 | announcements |

### 5.2.1 Per-type extensions and category enums

| Type | Extra required fields | `category` enum |
|---|---|---|
| contract | `version` (semver), `compat_policy` (5.4 ref), `generated_from?` (5.3), `schema_format` — no `stability` field: draft/published is folded lifecycle state, never frontmatter (D-023) | api\|data-feed\|vocabulary\|event-feed\|other |
| requirement | `target_contract?` (`XC-...` or absent for new capability), `acceptance_criteria` | new-capability\|field-change\|vocabulary\|quality\|other |
| question | — | clarification\|defect\|choice |
| work_request | `acceptance_criteria`; categories contract-change/process-change additionally REQUIRE `proposed_change` (structured summary) + a pinned target in `refs` | data\|feature\|fix\|investigation\|contract-change\|process-change\|other |
| decision | `required_approvers` [system ID], `context`, `options_considered` | — |
| response | `parent` (ID of any answerable artifact: exchange or requirement, 3.4.6), `result` (answered\|delivered\|partial\|cannot) | — |
| handoff | §16.2 fields exactly: `deliverables[]`, `verification`, `acceptance_criteria`, `limitations[]`, `env_requirements?`, `fulfills[]` (originating exchange/requirement IDs) | — |
| announcement | `ack_requested?` (bool); category `deprecation` additionally REQUIRES `deprecates: <XC-id>@<version>` (3.4.7); category `status` MAY carry `period?` | release\|deprecation\|migration\|incident\|notice\|status |

Markdown body conventions per type (recommended section headings) ship as
templates (5.6); bodies are free-form, envelope carries all machine truth.

### 5.2.2 Lifecycle event schema (`event/v1`)

| Field | Type | Notes |
|---|---|---|
| `schema` | `event/v1` | |
| `event` | ULID | event ID; intra-commit tiebreak only (3.5 rule 1) |
| `space` | space ID | MUST match hosting repo |
| `subject` | artifact ID | the artifact this event acts on; for `verify` and `dispute` the subject is the `XS` response (3.4.6) |
| `transition` | enum: union of all §3.4 transition names + `note` (`expired` is fold-computed, never an event) | |
| `state` | claimed resulting state | informational; fold wins on mismatch (3.5 rule 5); omitted for transition-free kinds (`note`, broadcast ack) |
| `actor` | {kind, name, system, model?, session?} | `system` MUST match the owning section of the event file |
| `at` | RFC 3339 UTC | display metadata, never ordering authority |
| `note?` | string | |
| `refs?` | as envelope `refs` | response ID, blocker, successor, contract ref |
| `version?` | semver | contract version scope (3.4.1); publish events also record `commit` (SHA) + `digest` of the published content |
| `reason_code?` | enum: split-required \| security-concern \| out-of-scope \| duplicate \| other | machine-readable decline/dispute reason |

### 5.2.3 Consumer registry (`consumes/v1` — `<system>/consumes.yaml`)

Space-visible SSOT of contract dependencies (the second half of the §5.4
registered-consumer definition; the first is satisfied requirements). One
list: `{contract: <XC-id>, major: <int>, since: <date>, note?}` per entry.
Written by `a2a` when a dependency is declared (8.2 step 7); the project's
local config never carries authoritative registration — it mirrors this
file at most (cache).

## 5.3 Contract payload schemas & generation from SSOT (D-006)

- A contract directory MUST contain: descriptor (`contract.md`), at least one
  machine-validatable schema under `schema/`, and fixtures (`fixtures/valid/`,
  `fixtures/invalid/` — at least one each).
- `schema_format` declares the format: `json-schema-2020-12` (first-class:
  the binary validates fixtures against schemas itself) or `openapi-3.x`,
  `proto3`, `other` (the binary then only checks fixtures exist and digests
  match; deep validation is the owner's CI duty).
- **Generation from project code is the owner's concern** (stack-agnostic,
  R-018). a2ahub standardizes only the *guard*: descriptor field
  `generated_from: {tool: free text, source_digest: <hex>}`. The owning
  project's CI regenerates the export and fails if the regenerated content's
  digest differs from what is committed in the space (`a2a contract
  verify-export` performs the compare given a local export path).
- Hand-written contracts (no `generated_from`) are legal; the dashboard marks
  them as such — consumers can see which contracts are code-backed.

## 5.4 Versioning & compatibility policy (D-010)

- Contracts use **semver**. Patch: docs/fixtures only. Minor: strictly
  additive, backward-compatible (new optional field, new enum value where
  consumers were told to expect unknowns, new endpoint/kind). Major:
  anything that can break a conforming consumer (remove/rename/retype a
  field, tighten validation, semantic change, enum removal).
- A major (breaking) publish REQUIRES: G2 human gate (§3.7) + a
  `deprecation` announcement with `ack_requested: true` and `deprecates:
  <XC-id>@<version>` addressed to all **registered consumers**, + prior
  version deprecated with explicit sunset date. Registered consumer = any
  system whose satisfied requirement OR `consumes.yaml` entry (5.2.3 —
  space-visible, never project-local config) references the contract.
- **Version resolution (5.4a):** each publish event records the commit SHA
  and digest of the published content (5.2.2). `id@version` resolves via
  publish-event lookup → git object at that SHA; old versions remain
  readable forever without directory snapshots. `a2a contract diff <id>
  <v1> <v2>` renders the delta for consumers (OP-221).
- **Compat check (5.4b), part of V3 policy class:** for
  `json-schema-2020-12` contracts, a minor/patch bump REQUIRES that all
  prior-version valid fixtures still validate against the new schema; any
  failure ⇒ major required. For `openapi-3.x`/`proto3`/`other`, V3 checks
  only bump declaration + fixture self-consistency; deep compat is the
  owner's CI duty (stated per contract via `compat_policy`). The G2 gate
  trigger is this check, not the author's self-declared bump: V3 computes
  the version delta and, when major or first publish, requires an approving
  owner review on the PR (queried via the host API). CC-080 remains the
  semantic backstop.
- Old versions remain published until `retire` (§3.4.1). Retire of a
  deprecated version is blocked until every registered consumer acked —
  AND the sunset date has passed (the sunset is a promise of availability to
  consumers who acked and plan migration around it — early full-ack does not
  shorten it). Two bounded, human-only exceptions: (a) systems with manifest
  status `left` do not count (their exposure is surfaced per CC-062;
  offboarding a dead consumer via G4 is the normal resolution); (b) gated
  override: retire
  MAY proceed over un-acked consumers only when the sunset date has passed
  AND at least one `note` reminder event is on the deprecation thread AND
  the retire event arrives via a human-reviewed G2-class PR that lists the
  overridden systems. V3 enforces all three; overridden consumers are
  flagged `retired-unacked` and notified. Retire is never time-triggered
  (S-7: a human decides, silence stays visible).
- Envelope schema evolution follows the same rules; the binary understands
  envelope version N and N−1 (one-cycle overlap), and `space.yaml` records
  `min_binary_version` so mixed fleets fail loudly, not subtly.

## 5.5 Validation matrix (D-011)

One validation engine, compiled into the binary, invoked at every point —
identical results everywhere (R-003, R-004).

| Point | Trigger | Scope | On failure |
|---|---|---|---|
| V1 authoring | `a2a new`/`a2a validate` | schema of the drafted artifact | agent fixes before submit |
| V2 pre-write | `a2a submit` (wraps commit+push) | schema + referential (IDs/refs resolve) + authz (from == own section) + lifecycle legality of accompanying events | write refused locally |
| V3 CI on space repo | PR (required status check `a2a-validate`) + post-merge run on `main` | full-repo: everything in V2 for changed files + **diff-authz** (changed paths ⊆ author's section, author mapped github-login→system via `space.yaml`) + fold integrity + supersession linearity + policy checks (classification, gates, compat 5.4b) | PR: merge blocked; post-merge run: flag-only (V4-equivalent, catches cross-PR races CC-095) |
| V4 hub ingest | after fetch | same engine, flag-only | violation surfaces on dashboard + notification; never blocks git |
| V5 consumer read | `a2a show`/MCP read | digest check of pinned refs; staleness | warning surfaced to the reading agent |

Validation classes: **schema** (structure), **referential** (resolvable IDs,
digest match), **lifecycle** (legal transition by authorized actor class),
**policy** (gates, classification, single-intent structural rules), each
reported with machine-readable codes (catalogued for §13 fixtures).

## 5.6 Templates (R-015)

- One canonical template per object type, shipped inside the binary at the
  binary's version, rendered by `a2a new <type>`: pre-filled envelope
  (generated IDs, actor, dates) + body skeleton with per-type headings and
  inline authoring guidance as comments.
- Templates are projections of the schemas and live next to them in the
  product repo; CI in the product repo regenerates/validates templates
  against schemas so they cannot drift.
- Projects MUST NOT fork templates (drift). Per-space additions are limited
  to a manifest-configured extra-fields allowlist (`x_` prefixed keys),
  which the validator tolerates and tooling displays.

## 5.7 Digests

- Artifact digest = SHA-256 over the artifact file's **raw bytes as
  committed** — no canonicalization layer (files are immutable once
  submitted, so byte-hashing is sufficient and unambiguous). String form is
  always `sha256:<full-hex>`; display MAY truncate, storage MUST NOT.
- Multi-file digest (contract exports, `a2a contract verify-export`): a hash
  tree — SHA-256 over the sorted list of `(repo-relative-path,
  sha256(file-bytes))` pairs covering `schema/**` and `fixtures/**`
  (contract.md prose excluded). This is the value in
  `generated_from.source_digest` and in publish events.
- Digests are computed, never stored inside the artifact itself (a stored
  self-digest would be stale by construction); tooling caches them.
