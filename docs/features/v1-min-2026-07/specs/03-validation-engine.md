# P3 — Validation Engine — Specification

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Plan**: [plans/03-validation-engine.plan.md](../plans/03-validation-engine.plan.md)
**Footprint**: `internal/schema/`, `internal/validate/` — embed + load product
schemas from `schemas/**` (owned by P2) and implement THE one validation
engine (schema / referential / lifecycle / policy classes) with the V1
(authoring) and V2 (pre-write) invocation points. Imports per ADR-001:
`internal/schema` → `artifact`; `internal/validate` → `artifact`, `schema`,
`fold`. Narrow exception: `schemas/errors/v1/registry.yaml` (P2-owned SSOT
file) — this phase adds `referential`/`lifecycle`/`policy` data rows to it
(data only, no schema authoring). **Not in scope**: `internal/cli`/`cmd/a2a`
wiring of `a2a new` / `a2a validate` / `a2a submit` onto this engine (P6);
`schemas/**` schema authoring and golden fixtures (P2); `internal/fold`'s
transition-table implementation (P4, built in parallel against the seam in
§7).

---

## 0. User stories

Full text in [14-us-ac.md](../../../the-plan/plan/14-us-ac.md) §E2; not
restated here (SSOT rule).

| ID | User story |
|----|------------|
| US-201 | IA: every drafted artifact is validated before it can leave my machine — I can never publish garbage |
| US-203 | IA: secret-looking content in an outbound artifact is blocked before it crosses the boundary |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|---|---|---|
| Validation matrix (D-011) | [05-schemas.md §5.5](../../../the-plan/plan/05-schemas.md) | one engine, 5 points, 4 classes; this phase ships V1 + V2 only |
| Envelope/event fields | [05-schemas.md §5.2](../../../the-plan/plan/05-schemas.md), §5.2.1, §5.2.2 | field set the schema class checks; per-type required-field extras |
| Digests | [05-schemas.md §5.7](../../../the-plan/plan/05-schemas.md) | referential class digest-match rule |
| Identity & lifecycle | [03-domain.md §3.3](../../../the-plan/plan/03-domain.md), §3.4, §3.5, §3.7 | referential ID-form check; lifecycle-legality class calls the fold seam (§7 below); G1–G5 gate list |
| Security | [10-security.md §10.3](../../../the-plan/plan/10-security.md), §10.4 | authz matrix (from==own section, decision exception); forbidden-payload classes, secret scan, G5 override |
| Corner cases | [12-corner-cases.md §A](../../../the-plan/plan/12-corner-cases.md) CC-001…011 | document-level cases this phase's classes must catch, mapped to invocation point in §6 |
| ADR-001 | [decisions.md](../../../decisions.md) | package/import boundaries this footprint must respect |
| Go conventions | [.claude/rules/go-conventions.md](../../../../.claude/rules/go-conventions.md) | one validator core (#7), sentinel/typed errors, coverage floor |

---

## T1. CLI surface (track: cli) — engine contract only, no verb wiring

This phase ships a Go library, not a command. `a2a new` / `a2a validate` /
`a2a submit` are wired in **P6**; this table documents the exported entry
points and output shape those verbs will call, so P6 can integrate without
re-deriving the contract.

| Entry point (conceptual) | Invocation point | Scope (§5.5) | Consumed by (later phase) |
|---|---|---|---|
| `ValidateDraft(artifact)` | V1 authoring | schema class only, on the single drafted artifact | P6 `a2a new` / `a2a validate` |
| `ValidateForSubmit(artifact, events, localCtx)` | V2 pre-write | schema + referential + authz (from==own section, decision exception) + lifecycle legality of the accompanying events (§7 seam) | P6 `a2a submit` |

Both return the same result shape (§7 JSON output shape) — this is the
"identical results everywhere" property AC-201.2 asserts once V3/V4 mount the
same library in later phases (D-011).

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `internal/artifact` (P1): ID parsing/validation (§3.3), frontmatter
      parse, digest computation (§5.7) — the schema/referential classes call
      into these, never re-parse frontmatter or re-hash bytes locally.
- [ ] The machine-code registry's SSOT is the data file
      `schemas/errors/v1/registry.yaml` (P2-authored, ADR-001): P2 populates
      `schema`-class entries; this phase ADDS the `referential`/`lifecycle`/
      `policy` entries to that same file — never a second registry.
      `internal/validate` embeds/loads this data (typed accessors generated
      or hand-written over the embedded data) and is the only code surface
      that reads it; code mirrors the data, it never becomes a second,
      Go-side SSOT.
- [ ] Sentinel/typed errors, `errors.Is/As` at boundaries (go-conventions.md
      "Error handling") for every machine-readable code below.
- [ ] Log-or-return discipline: `internal/validate` returns a result value
      (never logs) — CLI-facing rendering is P6's concern.

## 6. Testing requirements

| Area | What to test | Edge cases / CC mapping |
|---|---|---|
| `internal/schema` load/embed | schemas embed correctly, `schema: envelope/v<N>` resolves, N and N−1 accepted (§5.4 last bullet) | CC-005 unknown/newer version → refuse write, read-only + warning |
| V1 schema class | every product schema × valid + invalid golden fixture (P2-owned corpus) returns the expected machine code | CC-001 (malformed YAML — structural, V1-catchable), CC-003 (ID/filename/prefix mismatch), CC-006 (oversized artifact), CC-007 (non-UTF-8), CC-011 (`blocking:false` w/o `interim_behavior`) |
| V2 referential class | IDs/refs resolve against local cache; digest match (§5.7) on pinned refs | unresolvable ref → referential-class code; unpinned ref → drift-prone warning (§3.8), not a hard reject |
| V2 authz class | `from` == configured own section; **decision-type exception**: `from` = drafting system, authz routed via the decision flow (§5.2), not the generic from==section check | CC-002 (wrong section) — AC-201.3; CC-008 (`to` unknown/`left` system) against local manifest cache |
| V2 lifecycle class | accompanying events checked against the fold seam (§7) before any write | CC-020/021 shape (illegal transition / unauthorized actor) surfaced pre-write, not just post-fold |
| V2 policy class — secret scan | secret-pattern corpus (§13.4) blocks each pattern; benign lookalikes pass (documented false-positive budget) | CC-010; G5 override hook present but requires the PR-identity mechanism (§10.3) — out of local-CLI reach, so V2 only *flags the override path*, never grants it itself |
| Result identity | same invalid content run through `ValidateDraft` (V1) and `ValidateForSubmit` (V2) — schema-class violations agree | phase-local: supports AC-201.2's cross-tier identity once V3/V4 land |
| Coverage floor | ≥70% per go-conventions.md | table-driven per class |

CC-004 (duplicate ID) and CC-009 (multi-intent smuggling) are explicitly
**not** covered by this phase's tests: CC-004 requires a full-repo scan (V3,
P9); CC-009 is process/template guidance, not a validator code (§3.2, §12).

## 7. Schema / contract delta

**JSON output shape** for both entry points (implementor may refine field
names; the shape below is this phase's own contract, not a plan quote — every
consumer, P6 and P9's V3 wiring, must agree on it):

| Field | Type | Notes |
|---|---|---|
| `valid` | bool | true iff zero violations across all classes run for that invocation point |
| `artifact_id` | string | echoed for correlation |
| `invocation_point` | enum `V1`\|`V2` | which scope ran (§5.5) |
| `violations[]` | array | empty when `valid: true` |
| `violations[].code` | string | machine-readable, from the single registry (§5) |
| `violations[].class` | enum `schema`\|`referential`\|`lifecycle`\|`policy` | §5.5 |
| `violations[].path` | string | JSON-pointer-style field path, or `event[N]` for lifecycle violations |
| `violations[].message` | string | human-readable, one line |
| `violations[].cc_ref?` | string | corner-case ID this rule enforces, when applicable (§12) — traceability for `cc-coverage.yaml` (§13.2, P10) |

**Fold seam** (P4 builds `internal/fold` in parallel against this contract;
both phases anchor to §3.4/§3.5, not to each other):

| Concern | Contract | SSOT |
|---|---|---|
| Folded state input | given a subject artifact's committed event history, `fold` returns its current state per the type's transition table | §3.4 per-type tables |
| Legality check | given (current state, candidate transition, actor block, actor's system, manifest as of the relevant commit) → verdict ∈ `legal` \| `illegal-transition` \| `unauthorized-actor`, with a machine code | §3.5 rules 2–3 |
| V2 usage | `validate` calls the legality check once per event accompanying a submit batch, using the manifest as staged locally (submit is pre-merge; V3 re-derives against merged history post-merge, per §5.5) | §5.5 V2 row |
| decision-type approve/reject | legality check alone does not prove G3 human-gate identity (PR-authenticated GitHub login vs `space.yaml` owners) — that binding is host-side (§10.3), out of this pure-fold/validate seam; V2 only checks transition-table legality + actor-class shape | §3.7 G3, §10.3 |
| Ownership | `internal/fold` (P4) implements & owns transition tables + the legality function; `internal/validate` (P3) imports and calls, never re-implements a second copy | ADR-001, go-conventions #7 |

The fold-time flag set is a superset of this pre-write verdict enum: it also
includes `state-claim-mismatch` (fold-only; P4 defines it, since it requires
post-fold state comparison V2's pre-write call cannot perform). The pre-write
legality-check verdict enum above stays 3-valued.

## 8. Acceptance criteria

Rows 1–4 are copied verbatim from
[14-us-ac.md](../../../the-plan/plan/14-us-ac.md) §E2; the implementor does
NOT modify them. Rows 5–8 are phase-local (build/test/observable behavior).

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| AC-201.1 | US-201 | Given any of the invalid golden fixtures, when `a2a validate` runs, then it fails with the expected machine-readable code. [T1, CC-001…011] | `go test ./internal/validate/... -run TestGoldenFixtures -race -count=1` against P2's fixture corpus, scoped to the CC subset V1's schema class actually covers (§6 mapping; see Open questions) |
| AC-201.2 | US-201 | Given the same content, when validated locally (V2), in space CI (V3), and by the hub (V4), then results are identical. [T1/T4/T6] | this phase proves the V1/V2 half: same fixture through `ValidateDraft` and `ValidateForSubmit` agrees on shared (schema-class) violations; V3/V4 identity is proven when P9/hub mount the same library (D-011) |
| AC-201.3 | US-201 | Given an artifact with `from` not matching my configured system, when I submit, then the write is refused locally. [CC-002] | `go test ./internal/validate/... -run TestAuthzFromOwnSection -race -count=1`, including the decision-type exception case (§5.2, §6) |
| AC-203.1 | US-203 | Given the secret-pattern corpus, when submitted, then V2 blocks each, overridable only via the G5 flow. [CC-010, T1 corpus 13.4] | `go test ./internal/validate/... -run TestSecretScan -race -count=1` against the §13.4 corpus; assert benign lookalikes pass |
| — | — | `internal/validate` is a single Go package that embeds/loads the one `schemas/errors/v1/registry.yaml` SSOT; no second copy of any class's logic, and no Go-side duplicate registry, exists elsewhere in the footprint | code review + `go vet ./internal/validate/...` |
| — | — | `internal/schema` accepts `envelope/v<N>` and `v<N-1>` (one-cycle overlap, §5.4) and refuses (read-only) an unrecognized newer version | unit test: CC-005 fixture |
| — | — | `internal/validate` imports only `artifact`, `schema`, `fold` (ADR-001) — no `host`/`space`/`cli`/`mcp` import | `go list -deps ./internal/validate/...` checked against the allowlist |
| — | — | Every violation returned carries a non-empty `code` from the registry; unknown/uncatalogued codes fail a package-level test | unit test enumerating the registry against emitted codes |

## 9. Future-proof considerations

| Aspect | Assessment |
|---|---|
| Extensibility | New validation classes or CCs slot in as new rule functions inside the same four classes; V3/V4/V5 (P9/hub, later) mount the identical library — no new engine, per D-011 |
| Coupling | Soft: `validate` depends on `fold`'s legality function (interface, §7) and `artifact`'s parse/digest helpers — no shared mutable state, no I/O in either dependency (both pure) |
| Migration path | low — schema/registry additions are additive; a breaking envelope version bump follows §5.4's own N/N−1 overlap rule, already designed for |
| Roadmap conflicts | P8 (adds a named retire-precondition policy hook file), P9 (V3 CI), and hub (v2, deferred) all touch `internal/validate` — P8's hook and P9/hub's unchanged mount — any drift found there is a defect in this phase, not a new decision |

## 10. Implementor entry point

Execute as one wave of the v1-min epic, after P1 (`internal/artifact`) and P2
(`schemas/**` + fixture corpus) land — both are `blocked_by` dependencies per
`tracker.yaml`. Runs in parallel with P4 (`internal/fold`): build against the
§7 fold seam table, not against P4's implementation; if P4's actual function
signatures diverge from the seam, that is a P4 defect to reconcile via the
epic's Amendments, not a silent local workaround here. TDD default:
golden-fixture-first (P2 supplies fixtures; this phase writes the rule
functions that make them pass/fail correctly), framework-first (stdlib
`encoding/json` + a JSON Schema library already chosen by P2's footprint —
reuse it, do not add a second schema validator dependency), log-or-return.
Full loop: [docs/features/README.md](../../../features/README.md).

## Open questions

- **V1's literal CC-001…011 coverage vs §5.5's schema-only scope.**
  AC-201.1 ([14-us-ac.md](../../../the-plan/plan/14-us-ac.md) §E2) states
  `a2a validate` (V1) fails "any of the invalid golden fixtures" tied to
  CC-001…011, but [05-schemas.md §5.5](../../../the-plan/plan/05-schemas.md)
  scopes V1 to the schema class only, and the corner-case catalog itself
  ([12-corner-cases.md](../../../the-plan/plan/12-corner-cases.md)) assigns
  CC-002 (authz) and CC-010 (secret scan) to V2/V3, and CC-004 (duplicate ID)
  explicitly to V3's full-repo check — none of which a single-artifact V1
  call can perform without repo/manifest context §5.5 does not grant it.
  §6 above resolves this operationally (CC-by-CC invocation-point mapping,
  used for this phase's own tests), but the literal AC-201.1 wording is
  broader than §5.5's V1 scope. Flagged per the epic's ambiguity rule rather
  than narrowed silently; a T1-tier reconciliation (P10, `cc-coverage.yaml`)
  should confirm whether AC-201.1's T1 golden run is meant to invoke
  `a2a validate` alone or the full V1+V2 chain per fixture.

## 11. Amendments

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — wave-2 planning adjudications (lead, pre-implementation)

- **AC-401.2 gate lands here** (shift-left plan review MED-3, recorded in
  spec 02 Amendments): an embedded Go test in `internal/schema` asserts the
  8 envelope schemas ↔ 8 templates pairing and the invalid-fixture-sidecar ↔
  registry closure (both directions). It runs under `go test ./...` inside
  `make check`, so product CI enforces it with no separate script.
- **Embed placement**: `go:embed` cannot traverse `..`, so the corpus is
  embedded by a minimal `package schemas` in `schemas/embed.go` (excluding
  `fixtures/` trees), consumed by `internal/schema`; `schemas/embed.go` is
  added to this phase's footprint as a narrow grant.
- **Fold seam consumed via a consumer-side interface** (rails ISP/DI), not a
  package import: P4 builds `internal/fold` in the same wave;
  `internal/validate` defines a 1-method LegalityChecker interface matching
  §7's 3-valued verdict and takes it via constructor DI; cmd/a2a (P6) wires
  fold's implementation. AC row 7's import allowlist is satisfied from
  below; §7's "imports and calls" ownership rule is honored at wiring time.

### 2026-07-21 — from coherence audit (pre-implementation)

- Rewrote §5's registry bullet and Footprint line: the error-code registry's
  SSOT is the data file `schemas/errors/v1/registry.yaml` (per ADR-001), not
  a Go-side registry in this package; this phase ADDS
  `referential`/`lifecycle`/`policy` rows to P2's file and embeds/loads it,
  never re-authoring it as Go constants — corrects a direct contradiction of
  ADR-001 and P2's spec.
- Reworded AC row 5 (§8) to match: no Go-side duplicate registry, not just
  "one registry" (same SSOT-direction fix, applied to the acceptance
  criterion's wording).
- Added `schemas/errors/v1/registry.yaml` to the Footprint line as a narrow,
  explicit exception (data rows only, no schema authoring) so the footprint
  accurately reflects the write this phase performs.
- §7: added one sentence noting the fold-time flag set is a superset
  including `state-claim-mismatch` (fold-only, defined by P4); the pre-write
  legality-check verdict enum in this spec stays 3-valued — avoids implying
  P3's 3-valued enum is the complete fold-time vocabulary.
- §9 Roadmap conflicts: added P8 (retire-precondition policy hook file) to
  the list of later phases touching `internal/validate`, alongside P9/hub —
  the row previously omitted a phase that touches this footprint.
