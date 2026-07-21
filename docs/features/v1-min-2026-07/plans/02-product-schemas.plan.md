---
slug: v1-min-2026-07
phase: P2
spec: ../specs/02-product-schemas.md
wave: 1
status: dispatched  # shipped 6bf4205; held open on A2 B.7/B.10/B.11 (operator decision)
---

# Phase plan — P2 Product schemas v1

## Goal

Author the complete `schemas/**` corpus: 11 JSON Schemas (base + 8 envelope
types + event + manifest + consumes), the error-code registry (schema-class
rows), 8 canonical templates, and the four fixture corpora (golden
valid/invalid, secret-scan, compat, sanitization) — spec 02 AC rows 1–7.
Data only, zero Go code.

## Allowlist (repo-relative)

- `schemas/**`

## Lead-reserved / off-limits deltas

- Everything outside `schemas/**` (P1 runs in parallel on the Go side).

## Brief

```
Track: schemas — DATA ONLY, no Go code anywhere in this phase.
All file paths are REPO-RELATIVE — they resolve against the repo root.

## Goal
Author schemas/** in full: 11 JSON Schemas (2020-12), the error-code
registry (schema-class rows only), 8 per-type templates, and the golden /
secret / compat / sanitization fixture corpora — per
docs/features/v1-min-2026-07/specs/02-product-schemas.md.

## Spec / context links (READ FIRST, in order)
- Spec: docs/features/v1-min-2026-07/specs/02-product-schemas.md end to end —
  its T2 file-layout table IS your file list; §6 the fixture matrix; §8 the
  acceptance rows.
- Normative field tables (cite, do not retype from memory):
  docs/the-plan/plan/05-schemas.md §5.1, §5.2, §5.2.1–§5.2.3, §5.6;
  docs/the-plan/plan/03-domain.md §3.1 (8 types + prefixes), §3.3 (ID grammar
  for fixture filenames); docs/the-plan/plan/13-testing.md §13.1/§13.4/§13.5;
  docs/the-plan/plan/04-topology.md §4.2 (manifest grounding).
- Golden seed: docs/the-plan/plan/A2-examples.md B.1–B.13 — these fenced
  blocks ARE the valid fixtures, copied BYTE-FOR-BYTE (§13.5 no-drift).
  Never hand-roll a valid fixture a B-example already covers; B.4/B.5 and
  B.9/B.10 both go in.

## Allowed files (allowlist) — REPO-RELATIVE ONLY
- schemas/** (everything under it, per the spec's T2 layout table)

## Off-limits (NEVER touch)
- Anything outside schemas/ — go.mod, cmd/, internal/, Makefile, docs/**,
  .github/**, scripts/**

## What to do
1. Schemas first (they are the contract): envelope base + 8 extension
   schemas (`allOf` base + per-type extra required fields + closed category
   enum per §5.2.1; `decision` has NO category field — omit the enum
   entirely, and its schema must REJECT a category field), event, manifest
   (permissive open-object for fields named only in prose — spec Open Q1),
   consumes.
2. schemas/errors/v1/registry.yaml: entries {code, class, title, applies_to},
   schema-class only, format SCH-### (zero-padded, monotonic, append-only).
   Every code cited by ≥1 fixture; no orphans (AC row 7).
3. Fixtures per schema: fixtures/{valid,invalid}/. Valid = A2 byte-for-byte
   where an example exists. INVALID-FIXTURE ANNOTATION (pinned format — P3's
   cross-ref consumes it): each invalid fixture gets a SIDECAR file
   `<fixture-name>.expect.yaml` next to it with at least `code: SCH-###`.
   Fixture bodies stay byte-pure — never annotate inside the fixture.
4. Special corpora: schemas/fixtures/secret-corpus/{positive,negative}/
   (≥1 each, false-positive budget documented per §13.4);
   schemas/fixtures/compat/{additive-minor,mislabeled-minor}/ (5.4b pairs,
   CC-080); schemas/fixtures/sanitization/ (adversarial-but-schema-VALID
   content — script tags/raw HTML/control chars that must still pass).
5. Templates: schemas/templates/v1/<type>.md — exactly the 8 §3.1 types,
   projections of the schemas (§5.6): every required field present, no field
   a schema would reject.
6. Edge fixtures from §6: >1 `to` entry on an exchange type (invalid),
   decision+category (invalid), event without state for note/broadcast-ack
   kinds (valid), empty consumes dependencies [] (valid), manifest missing
   min_binary_version (invalid).
7. Self-verify (no Go): `ls` the tree against the spec T2 table; diff each
   A2-seeded fixture against its fenced block; cross-check every invalid
   sidecar code exists in registry.yaml and every registry code is cited.

## Constraints
- JSON Schema draft 2020-12; `$id` URIs per the spec (envelope/v1 etc.).
- Byte-fidelity of A2-seeded fixtures beats prettiness — if an example
  looks wrong, REPORT it as a deviation, do not "fix" the bytes.
- No code, no scripts, no Makefile edits — a pure data corpus.

## DO NOT
- DO NOT commit. DO NOT run git at all.
- DO NOT run make check / make check-validators or any go command.
- DO NOT touch files outside schemas/.

## Acceptance
- Spec §8 rows 1–7 (8 templates present; every schema ≥1 valid + ≥1
  annotated invalid; A2 byte-identity; secret/compat/sanitization corpora
  present; registry no-orphans both directions).

## Report back
- Files created (tree summary), fixture counts per schema.
- Deviations — REQUIRED; "none" only if you mean it: any A2 example that
  contradicts a §5 field table, any field you had to type permissively
  beyond the spec's manifest note, any silently-closed open question.
- Anything skipped + why.
```

## Acceptance

- [ ] Spec 02 §8 rows 1–7 green (verified lead-side: tree + A2 byte-diffs +
      registry cross-ref re-checked).

## Phase log

### Wave 1 — 2026-07-21

- Agent: coder/sonnet/high, brief above executed as written; self-verified
  with a scratch jsonschema-2020-12 harness (39 checks, 14 byte-diffs,
  compat pair reproduced CC-080).
- Files / Commits: 86 / 6bf4205
- Verify: lead confirmed no out-of-allowlist writes; propagation probe
  independently re-confirmed the registry cross-ref and the three A2
  findings; lead fixed the rand4 charset drift (base.schema.json exchange
  suffix → Crockford base32, matching internal/artifact — generator is
  normative) before commit.
- Deviations + downstream amendments: (accepted) response/handoff also
  reject `category` (5.2.1 dashes); per-file `$id`s — P3 embedder must key
  families accordingly; manifest `schema: space/v1` (matches A2.13);
  event schema adds optional `commit`/`digest` from §5.2.2 prose;
  `unevaluatedProperties` composition — P3 must confirm library support.
  (ESCALATED, phase held open) A2-examples defects: B.7 response lacks
  priority/blocking → the only response valid-fixture fails base schema
  (AC-2 unmet for response); B.10 title is unparseable YAML (ParseFrontmatter
  rejects it); B.11 satisfy-event ULID contains forbidden 'U'.
- Epic-direction reconcile: STOP raised on the three plan-corpus defects —
  §5.2/§13.5 invariants collide; operator decision requested at wave-1
  handoff (options recorded there).
- Notes: secret-corpus carries no code sidecars by design (policy-class
  codes are P3's); false-positive budget documented in its README.

## Deferred / follow-ups

- AC-401.2 CI enforcement (schema↔template pairing + fixture↔registry
  cross-ref) ships as an embedded Go test in P3 (shift-left review MED-3);
  spec 03 to be amended at wave-2 planning.
