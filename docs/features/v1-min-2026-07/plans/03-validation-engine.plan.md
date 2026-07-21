---
slug: v1-min-2026-07
phase: P3
spec: ../specs/03-validation-engine.md
wave: 2
status: planned
---

# Phase plan — P3 Validation engine

## Goal

Ship `internal/schema` (embedded product schemas, family-URI keyed, N/N−1
seam) and `internal/validate` (THE one engine: schema / referential / authz /
lifecycle / policy classes, V1+V2 entry points, §7 result shape), append the
non-schema-class rows to the error registry, and land the AC-401.2 embedded
gate test — spec 03 AC rows 1–8.

## Allowlist (repo-relative)

- `internal/schema/**`
- `internal/validate/**`
- `schemas/embed.go` (NEW — see placement note)
- `schemas/errors/v1/registry.yaml` (append non-schema-class rows ONLY)

## Lead-reserved / off-limits deltas

- `internal/fold/**` (P4 writes it IN PARALLEL — never import or read-depend)
- `internal/host/**`, `internal/space/**`, `testkit/**` (P5's)
- `cmd/a2a/**` (wiring is P6/lead), `go.mod`/`go.sum` (jsonschema/v6 already
  added by the lead), `schemas/**` beyond the two grants above

## Placement decisions (lead, binding)

- **Embed package**: `go:embed` cannot traverse `..`, so the schema corpus is
  embedded by a minimal `package schemas` in `schemas/embed.go` exposing an
  `embed.FS` over `envelope/ event/ manifest/ consumes/ errors/ templates/`
  (EXCLUDING all `fixtures/` trees — fixtures are test data, read from disk
  in tests, never compiled into the binary). `internal/schema` is its only
  v1 consumer (P6's `internal/template` reuses the same FS later).
- **Fold seam = consumer-side interface, NOT an import.** P4 builds
  `internal/fold` concurrently; importing it would compile a half-written
  sibling. `internal/validate` defines its own 1-method `LegalityChecker`
  interface (3-valued verdict per spec 03 §7 seam table) + its own minimal
  candidate-event input type, taken via constructor DI. cmd/a2a (P6) wires
  fold's implementation. Spec 03 AC row 7's import allowlist is satisfied
  from below (fewer imports than granted).

## Brief

```
Stack: Go 1.26. Deps: gopkg.in/yaml.v3 and github.com/santhosh-tekuri/
jsonschema/v6 (both already in go.mod — import jsonschema ONLY inside
internal/schema). NOTHING else new.
All file paths are REPO-RELATIVE.

## Goal
Implement internal/schema + internal/validate per
docs/features/v1-min-2026-07/specs/03-validation-engine.md (read it end to
end FIRST, including §6 CC mapping, §7 result shape + fold seam, §8 ACs,
Open questions), plus the placement decisions in
docs/features/v1-min-2026-07/plans/03-validation-engine.plan.md.

## Context (read in order)
- Spec 03 (above) and this plan file's Placement decisions.
- Root AGENTS.md "a2ahub engineering rails" — binding (error idiom set by
  internal/artifact: sentinels + typed wrapper + errors.Is/As; copy it).
- docs/features/v1-min-2026-07/plans/02-product-schemas.plan.md §Phase log —
  P2's shipped notes you MUST honor: per-file $id values
  (envelope/v1/base.schema.json etc.), allOf+unevaluatedProperties
  composition (VERIFY santhosh-tekuri/v6 enforces it — write the test first;
  if it does not, STOP and report, do not silently re-close schemas),
  invalid-fixture sidecars <name>.expect.yaml with code:, secret corpus has
  no sidecars (policy codes are YOURS to add).
- Plan corpus: 05-schemas.md §5.4/§5.5, 10-security.md §10.3/§10.4,
  12-corner-cases.md CC-001..011, 03-domain.md §3.3/§3.8.
- internal/artifact's exported API (read the source) — reuse ID parse,
  frontmatter parse, digest; NEVER re-implement.

## Allowed files — REPO-RELATIVE ONLY
- internal/schema/**, internal/validate/**
- schemas/embed.go (new, package schemas, embed.FS per placement note)
- schemas/errors/v1/registry.yaml — APPEND REF-/LFC-/POL- rows only; never
  edit existing SCH- rows

## Off-limits (NEVER touch)
- internal/fold (a parallel agent is writing it — do not import, do not
  read it, build against the spec 03 §7 seam only)
- internal/host, internal/space, testkit (parallel agent), cmd/a2a,
  go.mod, go.sum, Makefile, docs/**, schemas/** beyond the two grants

## What to do
1. schemas/embed.go: package schemas; //go:embed for the six non-fixture
   trees; export the FS.
2. internal/schema: load + compile the embedded corpus with jsonschema/v6;
   key families by URI (envelope/v1 → type-dispatched extension schemas,
   event/v1, manifest/v1, consumes/v1); registry loader for
   schemas/errors/v1/registry.yaml (typed rows {code,class,title,
   applies_to}); version seam: accept envelope/v<N> and v<N-1> (today N=1 —
   design the seam, CC-005 test refuses an unknown newer version).
   FIRST TEST: allOf+base-$ref+unevaluatedProperties actually rejects a
   stray field and the decision+category fixture — if the library does not
   enforce it, STOP and report.
3. internal/validate: ValidateDraft (V1: schema class) and
   ValidateForSubmit (V2: schema + referential + authz + lifecycle via the
   LegalityChecker DI seam + policy/secret-scan). Result shape exactly per
   spec §7 table (violations[].code/class/path/message/cc_ref?). Inputs are
   parsed artifacts via internal/artifact primitives.
   - referential: refs/IDs resolve via a consumer-side Resolver interface
     (localCtx) — fake in tests; digest match on pinned refs (§5.7);
     unpinned ref → warning-severity violation, not a reject (§3.8).
   - authz: from == own section; decision-type exception per §5.2.
   - policy: secret scan over P2's corpus (schemas/fixtures/secret-corpus);
     add POL- codes; benign lookalikes pass; G5 override is FLAGGED only.
   - lifecycle: call LegalityChecker once per accompanying event; map the
     3-valued verdict to LFC- codes.
4. Registry rows: add REF-/LFC-/POL- entries for every code you emit; a
   package-level test enumerates emitted codes against the registry (AC 8)
   and fails on unknown/orphaned codes in either direction.
5. AC-401.2 embedded gate test (lead-assigned, shift-left review MED-3): a
   test in internal/schema asserting (a) the 8 envelope type schemas pair
   1:1 with schemas/templates/v1/*.md, and (b) every invalid-fixture
   sidecar cites a registry code and every SCH- code is cited by >=1
   sidecar (read fixtures from disk, path-relative).
6. Golden-fixture test: every P2 valid fixture passes, every invalid
   fixture fails with EXACTLY its sidecar's code (V1); V1/V2 agree on
   schema-class violations (AC-201.2 half).
7. Sanity: gofmt; go vet ./internal/schema/... ./internal/validate/...;
   go test ./internal/schema/... ./internal/validate/... -race -count=1;
   go list -deps ./internal/validate/... | grep a2ahub — must show only
   artifact + schema + validate's own packages.

## Constraints
- Copy the P1 error idiom (sentinels + typed wrapper). validate NEVER logs
  (returns values); no os.Getenv anywhere; t.Parallel() everywhere (or
  // reason:); coverage floor 70% per package; bounded reads (CC-006
  oversized artifact → its own code); non-UTF-8 (CC-007) → code.
- The registry data file stays SSOT: Go code mirrors it (loads embedded
  copy), never carries a duplicate hand-written code table.

## DO NOT
- DO NOT commit. DO NOT run git. DO NOT run make check /
  check-validators / repo-wide go build|test. Scope to your two packages.
- DO NOT import internal/fold (or any parallel sibling's package).
- DO NOT touch files outside the allowlist.

## Acceptance
- Spec 03 §8 rows 1–8 (AC-201.1/.2-half/.3, AC-203.1, single-registry,
  N/N−1 seam, import allowlist, code-registry closure).

## Report back
- Files, tests, scoped test output; registry rows added (codes).
- Deviations — REQUIRED (esp. any place the P2 schema corpus fought the
  library, any seam-shape choice future P6 wiring must know).
- Anything skipped + why.
```

## Acceptance

- [ ] Spec 03 §8 rows 1–8 green (lead re-runs scoped tests + import check).

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- Concrete fold→validate wiring (LegalityChecker impl injection) lands in
  P6's cmd/a2a DI; propagation probe must check seam-shape agreement with
  P4's shipped exports.
