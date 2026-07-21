# P2 — Product schemas v1 + golden fixtures + error-code registry

**Slug**: `v1-min-2026-07`  ·  **Track**: schemas  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Footprint**: `schemas/**` only — `schemas/envelope/v1/` (common base + 8
per-type extensions, plan §5.2/§5.2.1), `schemas/event/v1/` (§5.2.2),
`schemas/manifest/v1/` (`space.yaml`), `schemas/consumes/v1/` (§5.2.3), the
error-code registry (data, classes per §5.5), golden fixtures (valid +
invalid per schema, T1 §13.1), and the 8 canonical per-type templates
(§5.6). **No Go code** — `internal/schema` embedding (P1/P3) and
`internal/validate`'s runtime use of the registry (P3) are out of footprint.

---

## 0. User stories

Cited verbatim from plan §14; this phase supplies the data these stories
are validated against — the CLI/engine behavior itself ships in P3/P6.

| ID | User story |
|----|------------|
| US-401 | (IA) "As an agent, `a2a new` gives me a template that cannot drift from the schema." |
| US-201 | (IA) "As an agent, every artifact I draft is validated before it can leave my machine, so I can never publish garbage." — this phase supplies the fixture corpus AC-201.1 is checked against; the `a2a validate` behavior is P3. |
| US-202 | (HL) "As a system owner, no breaking contract change can reach consumers without my gate and their awareness." — this phase supplies the 5.4b compat-check goldens (AC-202.4); the CI enforcement is P9/P3. |
| US-203 | (IA) "As an agent, secret-looking content in an outbound artifact is blocked before it crosses the boundary." — this phase supplies the secret-pattern corpus (AC-203.1); the scanner is P3. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Schema stack & envelope fields | [05-schemas.md §5.1, §5.2, §5.2.1–§5.2.3, §5.6](../../../the-plan/plan/05-schemas.md) | normative field tables; do not retype, cite |
| Object taxonomy & identity | [03-domain.md §3.1, §3.3](../../../the-plan/plan/03-domain.md) | 8 types, prefixes, ID grammar drives fixture filenames |
| Test tiers & fixture policy | [13-testing.md §13.1 T1, §13.4, §13.5](../../../the-plan/plan/13-testing.md) | T1 scope, secret/compat/sanitization corpora, no-drift rule |
| Golden examples | [A2-examples.md](../../../the-plan/plan/A2-examples.md) | B.1–B.13 = the literal T1 valid-fixture seed |
| Decisions | [17-decisions.md D-004, D-009, D-023](../../../the-plan/plan/17-decisions.md) | taxonomy, schema format choice, version resolution (no stability field) |
| Space layout (manifest grounding) | [04-topology.md §4.2](../../../the-plan/plan/04-topology.md) | `space.yaml` fields not fully tabulated in §5 — see Open questions |
| Package boundaries | [decisions.md ADR-001](../../../decisions.md) | `schemas/` row; error-registry wording — see Open questions |

---

## T2. Schema fields (track: schemas)

### File layout (this phase's design; not restated plan content)

| Path | Content | Normative source |
|---|---|---|
| `schemas/envelope/v1/base.schema.json` | common base fields (`schema`, `id`, `type`, `title`, `space`, `from`, `to`, `actor`, `created`, `category`, `priority`, `blocking`, `needed_by?`, `effort_estimate?`, `expected_response?`, `acceptance_criteria?`, `thread?`, `supersedes?`, `refs?`, `origin?`, `migrated_from?`, `interim_behavior?`, `classification`, `valid_until?`) | §5.2 table verbatim |
| `schemas/envelope/v1/{contract,requirement,question,work_request,decision,response,handoff,announcement}.schema.json` | one file per §3.1 type; each composes `base.schema.json` and adds only its row's extra required fields + `category` enum | §5.2.1 table verbatim, one row per file |
| `schemas/event/v1/event.schema.json` | `schema`, `event`, `space`, `subject`, `transition`, `state?`, `actor`, `at`, `note?`, `refs?`, `version?`, `reason_code?` | §5.2.2 table verbatim |
| `schemas/manifest/v1/space.schema.json` | `space.yaml` — see manifest note below | §4.2 layout comment + A2.13 (no single §5 table — Open Q1) |
| `schemas/consumes/v1/consumes.schema.json` | `schema: consumes/v1`, `system`, `dependencies: [{contract, major, since, note?}]` | §5.2.3 verbatim |
| `schemas/errors/v1/registry.yaml` | machine-readable code catalog | §5.5 classes; code vocabulary is this phase's design (delegated by §5.5: "catalogued for §13 fixtures") |
| `schemas/templates/v1/<type>.md` | 8 canonical per-type templates, one per §3.1 type | §5.6 |
| `schemas/**/fixtures/{valid,invalid}/*` | golden fixtures, one dir pair per schema | §13.1 T1, §13.5 |

**Envelope composition.** `type` selects the extension schema; every
extension schema `allOf`-includes `base.schema.json` and layers its §5.2.1
row's extra-required fields + closed `category` enum. `decision` has no
`category` per §5.2.1 — its extension schema omits the enum entirely, not an
empty enum. The `decision`-type exception to `from`-matches-section (§5.2,
"lives in `decisions/`") is a **referential**-class rule (needs the file's
directory), out of this phase's schema-only (structural) scope — it is
recorded here as a fixture note, enforced by P3.

**Manifest note.** No single §5 table enumerates `space.yaml` fields; this
phase derives the schema from the §4.2 layout comment ("manifest: space id,
schema version, participants, gates config, notification routes") plus the
A2.13 excerpt (`schema`, `space`, `min_binary_version` [D-013], `gates`,
`participants[]` {`system`, `org`, `section`, `owners[]`, `status`,
`joined`}, `vendored[]`). Fields named in prose but absent from A2.13
("notification routes", full `gates` sub-shape) are typed permissively
(open object) pending a normative table — see Open Q1.

### Error-code registry shape

One entry per code: `{code, class, title, applies_to}` where `class` ∈
{`schema`, `referential`, `lifecycle`, `policy`} per §5.5's four validation
classes, and `applies_to` names the schema/rule the code fires from. This
phase populates **`schema`-class entries only** (T1 is schema-structural
validation per §13.1's own tier description); `referential`/`lifecycle`/
`policy` entries need runtime context this phase's static fixtures don't
have and are P3's to add to the same file. Code format: `<CLASS-ABBR>-<3
digits>` (`SCH-001`, `REF-001`, `LFC-001`, `POL-001`) — zero-padded,
monotonic per class, never reused once a fixture cites it (append-only, like
CC-### in §12).

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] A2-examples.md B.1–B.13 ARE the T1 valid-fixture seed (§13.5) — copy
      their fenced blocks byte-for-byte into `fixtures/valid/`; never
      hand-roll new valid content for a type an appendix example already
      covers (B.4/B.5 give two work_request valids, B.9/B.10 two
      announcement valids — use both).
- [ ] One error-code vocabulary (`schemas/errors/v1/registry.yaml`) — no
      per-schema-file ad hoc code strings; every invalid fixture cites a
      registry code.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| 8 envelope type schemas | valid fixture per type (A2 example where one exists) + ≥1 invalid fixture per type with a `SCH-###` code | missing required field, wrong `category` enum value, `to` with >1 entry on an exchange type, `decision` fixture carrying a `category` field (must reject — no such field per §5.2.1) |
| event schema | valid (A2 B.11) + invalid (bad `transition` enum, missing `subject`) | `note`/broadcast-ack kinds carry no `state` (§3.5) — a valid fixture must cover this |
| manifest schema | valid (A2 B.13) + invalid (missing `min_binary_version`) | unknown top-level key rejected unless under the manifest's own extension point, if any (flag if undefined — Open Q1) |
| consumes schema | valid (A2 B.12) + invalid (missing `major`) | empty `dependencies: []` must still validate (zero deps is legal) |
| secret-scan corpus (§13.4) | ≥1 known-pattern fixture (must block, cites a registry code once P3 exists) + ≥1 benign lookalike (must pass) | document the false-positive budget per fixture, per §13.4 |
| compat-check goldens (§13.4, 5.4b) | fixture pair: additive-minor (old valid fixtures still pass new schema) + mislabeled-minor (old valid fixture fails new schema — CC-080) | both use `json-schema-2020-12` contract fixtures only, per §5.4b scope |
| sanitization fixtures (§13.4) | envelope fixtures with script tags / raw HTML / control chars in `title`/body/`note` that are otherwise schema-**valid** | must NOT be schema-invalid — sanitization is a rendering concern (P7), this phase only supplies adversarial-but-legal content |
| template↔schema pairing | 8 templates present, one per type | AC-401.2 — see §8 row 1 |

## 7. Schema / contract delta

No prior version exists (P2 is the first authoring of these schemas) — this
phase is additive in full. Cross-phase contract for consumers: `internal/
schema` (P1/P3, out of footprint) embeds every file under `schemas/**` at
build time and exposes them by URI `envelope/v1`, `event/v1`, `manifest/v1`,
`consumes/v1`; `internal/validate` (P3) reads `schemas/errors/v1/
registry.yaml` the same way; `internal/template` (P6) embeds `schemas/
templates/v1/*`. None of those packages are touched here.

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-401 | AC-401.2 (verbatim, §14): "Given product-repo CI, when a schema changes without its template, then the build fails. [5.6]" | this phase supplies the *data* the gate checks (8 schemas ↔ 8 templates under `schemas/envelope/v1/` and `schemas/templates/v1/`); the CI gate itself is product-repo CI, README-mapped to P1 — this phase's own check is structural: `ls schemas/templates/v1/*.md` names exactly the 8 §3.1 types |
| 2 | — | Every one of the 11 schemas (8 envelope types + event + manifest + consumes; `base.schema.json` is not separately fixture-tested — it's exercised transitively) has ≥1 valid and ≥1 invalid fixture, the invalid one annotated with the `registry.yaml` code it triggers | directory listing `schemas/**/fixtures/{valid,invalid}/*` cross-referenced against `schemas/errors/v1/registry.yaml` |
| 3 | — | Where a plan Appendix B example exists for a type/event/manifest/consumes (B.1–B.13), the corresponding valid fixture is byte-for-byte identical to the appendix's fenced block (§13.5 no-drift) | `diff` fixture file vs the extracted A2-examples.md block |
| 4 | — | Secret-scan corpus present: `schemas/fixtures/secret-corpus/{positive,negative}/` with ≥1 fixture each | directory listing; §13.4 |
| 5 | — | Compat-check goldens present: `schemas/fixtures/compat/{additive-minor,mislabeled-minor}/` fixture pairs | directory listing; §13.4, CC-080 |
| 6 | — | Sanitization fixtures present: `schemas/fixtures/sanitization/` with adversarial-but-schema-valid content | fixtures validate clean against their type schema despite raw HTML/script/control-char content; §13.4 |
| 7 | — | `schemas/errors/v1/registry.yaml` has no orphaned entries (unused by any fixture) and no fixture cites an undefined code | manual cross-reference at authoring time (automated cross-ref script is P3's, out of footprint) |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | a 9th object type = one new extension schema + template + fixture pair; no edit to `base.schema.json` or any existing type file |
| Coupling | soft only — every consumer (`internal/schema`, `internal/validate`, `internal/template`) reads these files by convention path/embed, no Go symbol coupling from this phase |
| Migration path | low — envelope `v2` would live in a sibling `schemas/envelope/v2/`, binary understands N and N−1 per §5.4's one-cycle-overlap rule; `v1` fixtures untouched |
| Roadmap conflicts | none identified; P3 (validation engine) and P6 (templates embedding) are the only consumers and are downstream-blocked on this phase per tracker.yaml |

## 10. Implementor entry point

Author schemas first (they are the contract) then fixtures that assert
them — an invalid fixture is the "red" a future P3 test turns "green";
wiring that assertion is out of this footprint, so treat each invalid
fixture's registry code annotation as the durable contract, not a runnable
check. Framework: stdlib-first `encoding/json` + JSON Schema 2020-12 is a
data format choice (D-009), not a library choice — this phase produces no
code, so no library selection is made here (P3's concern). Full loop:
[docs/features/README.md](../../README.md).

## 11. Amendments

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — from coherence audit (pre-implementation)
- Resolved Open Q2 (registry home tension): marked RESOLVED 2026-07-21 by
  ADR-001 (as amended) + P3's amendment — `schemas/errors/v1/registry.yaml`
  is the authored SSOT; P3 adds non-schema-class rows to the same file and
  `internal/validate` embeds it. Question text preserved above, resolution
  appended in place.

---

## Open questions

- **Q1 — manifest field completeness.** §5.1 names `space.yaml` as a
  product schema layer but no §5 section tabulates its fields the way §5.2/
  §5.2.1–§5.2.3 do for envelope/event/consumes; the only concrete sources
  are §4.2's layout comment and the A2.13 excerpt. This phase types
  unlisted-but-mentioned fields (full `gates` sub-shape, "notification
  routes") permissively pending a normative table. Not tracked as an
  existing plan Q-###; flagging fresh. Owner: whoever authors P9 (space
  template/CI, which also touches `space.yaml` semantics) or a plan
  amendment to §5.
- **Q2 — error-registry location vs ADR-001 wording.** ADR-001's package
  table states "machine error codes come from a single registry in
  `internal/validate`" (a Go package), while this epic's own phase table
  (README.md P2 row: "... + error-code registry") and this brief's Footprint
  assign the registry's authored SSOT to `schemas/**` as data (consistent
  with how `schemas/` already owns "SSOT JSON Schema sources... embedded via
  `internal/schema`"). This spec treats ADR-001 as describing the Go-level
  single *access point* (internal/validate embeds and is the only reader),
  not the authoring location — consistent with the schema-embedding
  precedent — but the wording tension is unresolved in the source documents
  themselves. Owner: lead, at ADR-001 review or P3 kickoff.
  **RESOLVED 2026-07-21** (coherence audit, pre-implementation): by ADR-001
  (as amended) plus P3's amendment, `schemas/errors/v1/registry.yaml` in
  `schemas/**` is the authored SSOT for error codes; P3 adds the
  `referential`/`lifecycle`/`policy` (non-schema-class) rows to that same
  file and embeds it, with `internal/validate` remaining the single
  Go-level access point per ADR-001. No further ADR-001 wording change
  needed.
