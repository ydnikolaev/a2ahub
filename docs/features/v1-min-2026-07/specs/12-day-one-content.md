# P12 — Day-one content & L2 migration — Specification

**Slug**: `v1-min-2026-07`  ·  **Track**: space  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Footprint**: `docs/runbooks/l2-migration.md` (this repo, new file — the ONLY
path this phase creates/touches here). No `internal/*` Go package is touched
(ADR-001 layout unaffected) — **may import: n/a**, this is a
content/migration phase, not a code phase. The substantive work (contracts
published, legacy files triaged, deprecation announcement) happens in the
**live getvisa space repo** and the **axon repo** — both external to this
repo; this spec defines the contract those external changes must satisfy and
this repo records the outcome. Blocked by P8 (contract lifecycle verbs must
exist to publish/verify-export) and P11 (space must be online). Plan ref:
epic README `docs/features/v1-min-2026-07/README.md` §"Epic mapping" (E10 →
P11, P12); tracker.yaml P12 `blocked_by: [P8, P11]`.

---

## 0. User stories

Sourced verbatim (paraphrase-free) from plan §14 `14-us-ac.md`:

| ID | User story |
|----|------------|
| US-1001 | (IA-axon) As the axon agent, the `ingest` and `todo-feed` contracts live in the space, code-backed. |
| US-1002 | (HL) As both leads, the producer-outbox backlog is migrated or consciously closed. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| L2 scope & the fold-in NOTE | [15-rollout.md](../../../the-plan/plan/15-rollout.md) Phase L2 | scope, exit (S-1, S-4, S-5), the mirror-repo fold-in NOTE quoted below — do not re-derive it |
| Contract generation guard | [05-schemas.md](../../../the-plan/plan/05-schemas.md) §5.3 | `generated_from` field, `a2a contract verify-export`, hand-written-vs-code-backed distinction |
| `migrated_from` field | [05-schemas.md](../../../the-plan/plan/05-schemas.md) §5.2 | envelope field for one-time migrations |
| Stack sovereignty / day-one content decisions | [17-decisions.md](../../../the-plan/plan/17-decisions.md) D-006, D-007 | a2ahub specifies formats/guards only; generation is the owning project's concern |
| AC rows | [14-us-ac.md](../../../the-plan/plan/14-us-ac.md) US-1001/US-1002 | quoted verbatim in §8 below — do not modify |
| Success criteria | [01-vision.md](../../../the-plan/plan/01-vision.md) §1.6 | S-1, S-4, S-5 — the L2 exit gate |
| Single-intent decomposition (for triaging legacy "briefings") | [03-domain.md](../../../the-plan/plan/03-domain.md) §3.2, §3.1 coverage notes | legacy bundles MUST decompose into single-intent artifacts sharing a `thread`; data/dictionary asks → `work_request` category `data`; defect reports → `question` category `defect` |
| `producer-outbox` glossary | [00-meta.md](../../../the-plan/plan/00-meta.md) | "the pre-a2ahub folder of relayed exchange files in the axon repo; migration seed for v1 (R-022)" |
| Contract-owner loop | [08-agent-protocol.md](../../../the-plan/plan/08-agent-protocol.md) §8.4 | the regenerate → `verify-export` → version → publish sequence this phase executes for `ingest`/`todo-feed` |
| Announcement category enum + deprecation field rule | [03-domain.md](../../../the-plan/plan/03-domain.md) §3.4.7; [05-schemas.md](../../../the-plan/plan/05-schemas.md) §5.2.1 | category `deprecation` REQUIRES `deprecates: <XC-id>@<version>` — flagged ambiguous for the chat-relay announcement, see Open questions |

### The §15 L2 fold-in NOTE (quoted verbatim, section "Phase L2")

> NOTE: axon's existing CI contract-mirror (the `getvisa-ingest-contract`
> repo publishing per-handle JSON Schemas + golden fixtures) is the
> proto-version of `axon/provides/ingest/` — at L2 its content and export
> CI mechanics fold INTO the space (one home), and the standalone mirror
> repo is deprecated. The SSOT direction is unchanged throughout: axon's
> code → export → space; the runtime `/api/v1/ingest/` never depends on
> a2ahub (D-006, §5.3).

---

### T3. Space & external-repo contract this phase requires (track: space)

> All rows below are EXTERNAL to this repo (getvisa space repo / axon repo).
> Listed for traceability of what the runbook (in-repo deliverable, next
> section) must point to — this repo's CI/tests cannot verify them directly.

| Path | Repo | Purpose | Generated or static |
|------|------|---------|----------------------|
| `axon/provides/ingest/` | getvisa space | `ingest` contract: `contract.md` (+ `generated_from`), `schema/`, `fixtures/{valid,invalid}/` per §5.3 | generated (axon CI export) |
| `axon/provides/todo-feed/` | getvisa space | `todo-feed` contract, same structure | generated (axon CI export) |
| axon export CI job (`a2a contract verify-export`) | axon repo | digest-compares the regenerated export against the space copy per §5.3/§5.7; belongs to axon per D-006 — this spec defines the guard, not the pipeline | n/a (CI, not a schema artifact) |
| `getvisa-ingest-contract` (standalone mirror) | axon repo (or its own repo) | deprecated per the §15 L2 NOTE above — content/CI mechanics fold into `axon/provides/ingest/`; the mirror itself is retired, not deleted-without-record | n/a |
| 15 producer-outbox legacy files | axon repo | triaged 1:1 into typed artifacts (space) or recorded drops (migration note) — see runbook | static (pre-existing, read-only source) |
| chat-relay deprecation `announcement` | getvisa space | AC-1002.2 — declares the chat channel deprecated at migration cutoff | one new space artifact |
| seomatrix counterpart contracts | getvisa space | "as available" per §15 L2 scope — NOT a blocking exit condition (S-4 names `ingest`/`todo-feed` from axon only) | generated, optional this phase |

### Runbook deliverable — `docs/runbooks/l2-migration.md` (this repo, the sole in-repo footprint)

Required contents (implementor writes this after the external work lands —
it is the record, not the mechanism):

| Section | Content |
|---|---|
| Summary | L2 scope recap (one paragraph, cites §15 L2, no restatement of full text) |
| Contract fold-in | axon export CI status, `provides/ingest/` + `provides/todo-feed/` `generated_from` values, confirmation the `getvisa-ingest-contract` mirror is deprecated per the L2 NOTE |
| Triage table | **exactly 15 rows**, columns: legacy filename \| disposition (`migrated`\|`dropped`) \| landed-as (artifact ID, or `n/a — dropped`) \| `migrated_from` value \| note (decomposition/thread if a legacy item split into >1 artifact per §3.2) |
| Chat-relay deprecation | the `announcement` artifact ID, category chosen (see Open questions), cutoff date |
| Seomatrix status | shipped-this-phase or explicitly deferred (non-blocking) |

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `a2a contract new/publish` + `a2a contract verify-export --local <path>` (OP-212/OP-213, [07-client.md](../../../the-plan/plan/07-client.md)) — do not invent a parallel publish mechanism; P8 ships these verbs, P12 only USES them against real content.
- [ ] The `migrated_from` field is already normative on the envelope (§5.2) — do not add a new field for legacy-file provenance.
- [ ] The §8.4 contract-owner loop (regenerate → verify-export → version → publish) is the exact sequence for `ingest`/`todo-feed` — do not shortcut it.
- [ ] §3.2 single-intent decomposition for any legacy "briefing" bundle — split into N artifacts on a shared `thread`, never one multi-intent artifact.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| Runbook completeness | triage table has exactly 15 rows, each with a non-empty disposition | a legacy file with no obvious typed-artifact mapping — MUST still get a row (`dropped` + note), never omitted |
| Contract digest guard | axon CI `verify-export` run is green at the commit the space copy was published from | regenerated export digest mismatch → axon CI red, space copy MUST NOT be updated until fixed (CC-084) |
| `generated_from` presence | both `axon/provides/ingest/contract.md` and `axon/provides/todo-feed/contract.md` carry `generated_from: {tool, source_digest}` | a hand-written fallback (no `generated_from`) would fail AC-1001.1's "space copies carry `generated_from`" clause — not acceptable for these two contracts |
| Deprecation announcement | one `announcement` artifact declares chat-relay deprecated, addressed appropriately (broadcast `to: all` or targeted per space) | announcement published (not left `draft`) before/at cutoff — the §98/99 coexistence-policy cutoff line in 15-rollout.md ("From L2 cutoff... chat-relayed artifacts are protocol violations") depends on this artifact existing |
| Migration note completeness | zero of the 15 legacy files unaccounted (S-5: "none remain untracked") | a file split across multiple typed artifacts (single-intent decomposition) — triage table note column MUST capture the 1:N mapping so the count still reconciles |

## 7. Schema / contract delta

None. This phase produces artifact and contract INSTANCES using fields
already normative from P2 (`internal/schema` embeds envelope/v1, which
already carries `migrated_from?` and, on `contract` type, `generated_from?`
per §5.2/§5.2.1/§5.3). No new field, enum value, or schema version is
introduced by P12.

## 8. Acceptance criteria

> AC rows copied verbatim from `14-us-ac.md` (Given/When/Then text
> unchanged, line-wrap only) — do not modify. Phase-local rows carry US "—".

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1001 | AC-1001.1 Given the axon export, when `a2a contract verify-export` runs in axon CI, then digest match is enforced; the space copies carry `generated_from`. [CC-084] | Inspect axon CI's `verify-export` job log (external repo — not runnable from this repo) + `axon/provides/{ingest,todo-feed}/contract.md` frontmatter for a populated `generated_from: {tool, source_digest}`; runbook's "Contract fold-in" section records both. |
| 2 | US-1002 | AC-1002.1 Given the 15 legacy files, when migration completes, then each is either a typed artifact (with `migrated_from` carrying its legacy filename, §5.2) or recorded as intentionally-dropped in a migration note; none silently lost. [S-5] | `docs/runbooks/l2-migration.md` triage table has exactly 15 rows, each `migrated` row's landed-as artifact has `migrated_from` set to the legacy filename (spot-check against the artifact's frontmatter in the space), each `dropped` row has a note explaining the drop. |
| 3 | US-1002 | AC-1002.2 Given migration cutoff, then the chat-relay channel is declared deprecated in an `announcement`. [S-1] | The space holds a published `announcement` artifact whose body declares chat-relay deprecated; runbook records its ID and cutoff date; `a2a show <id>` (once P7/P8 land) resolves it in state `published`. |
| 4 | — | Exactly two code-backed contracts (`ingest`, `todo-feed`) exist in `axon/provides/` at L2 exit, both `published`. | `a2a contracts --provider axon` (OP-221, once available) or direct listing of `axon/provides/*/contract.md` in the space. |
| 5 | — | The `getvisa-ingest-contract` standalone mirror is deprecated (not silently abandoned) per the §15 L2 NOTE — its content/export-CI mechanics are folded into `axon/provides/ingest/`. | Runbook's "Contract fold-in" section states the mirror's disposition explicitly; the mirror repo (if it stays reachable) carries a pointer/deprecation notice to the space contract. |
| 6 | — | Seomatrix counterpart contracts, if shipped this phase, follow the same `generated_from` guard; if deferred, the deferral is recorded as non-blocking (§15 L2: "as available"). | Runbook's "Seomatrix status" section states shipped-or-deferred; absence alone does NOT fail this phase's exit (only `ingest`/`todo-feed` from axon are named in S-4). |

## Open questions (phase-local — not silently resolved)

1. **Announcement category for the chat-relay deprecation (AC-1002.2).**
   §5.2.1 and §3.4.7 define category `deprecation` as requiring
   `deprecates: <XC-id>@<version>` — a field that names a **contract**
   being deprecated. The chat-relay channel is not a contract; no `XC-id`
   exists to populate `deprecates`. The plan does not name which
   `announcement` category (`release|deprecation|migration|notice|status`,
   §5.2.1) covers a process/channel deprecation. Candidates read from the
   enum: `migration` (fits "the md-relay era ends", §15 L2 Value line) or
   `notice`. Not resolved here — the implementor MUST NOT invent a
   `deprecates` value pointing at a non-contract entity to force category
   `deprecation`; pick `migration` or `notice` and record the choice + why
   in the runbook. Not tracked under an existing Q-### in §17.
2. **Seomatrix counterpart contract scope.** §15 L2 says "seomatrix
   counterpart contracts as available" without naming which contracts or a
   hard deadline. Treated here as explicitly non-blocking for L2 exit
   (S-4 only names axon's `ingest`/`todo-feed`) — flagged, not resolved,
   since a future phase may need to close this gap.

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | Adding a third code-backed contract (e.g. a future seomatrix export) reuses the same §5.3 guard and §8.4 loop unchanged — no core edit. |
| Coupling | Soft only: the space consumes axon's export via digest, never axon's code directly (D-006); this repo's runbook is a read-only record, not a dependency of the binary or the space CI. |
| Migration path | low — this phase IS the migration; nothing here needs a second migration by design (D-030: v1 → v2 is zero-migration). |
| Roadmap conflicts | None identified against in-flight P8 (must land first — `blocked_by`) or P11 (space must be online first). |

## 10. Implementor entry point

Execute after P8 and P11 are done (tracker `blocked_by: [P8, P11]`). Two
distinct work streams: (a) external — using the shipped `a2a` binary,
execute the §8.4 contract-owner loop in axon for `ingest`/`todo-feed`, fold
in the `getvisa-ingest-contract` mirror per the L2 NOTE, triage the 15
producer-outbox files into typed artifacts or recorded drops, publish the
chat-relay deprecation `announcement`; (b) in-repo — write
`docs/runbooks/l2-migration.md` recording the outcome per the table above.
Framework-first: use the CLI verbs (OP-211/212/213/220), never hand-edit
space files outside the funnel. Full loop:
[docs/features/README.md](../../features/README.md).

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it
> here AND amend any downstream spec.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
