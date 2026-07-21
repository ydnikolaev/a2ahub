# Compat-check goldens (§13.4, §5.4b, CC-080)

Fixture pairs proving the 5.4b compat-check rule: for `json-schema-2020-12`
contracts, a minor/patch bump REQUIRES that all prior-version valid fixtures
still validate against the new schema; any failure ⇒ major required. Both
pairs use `json-schema-2020-12` contract fixtures only, per §5.4b scope
(the fictitious "widget" payload schema is this phase's own minimal
contract-shaped example — not tied to any real product contract).

- `additive-minor/` — `old.schema.json` (v1.0.0, requires `name`) →
  `new.schema.json` (v1.1.0, adds optional `description`). The v1.0.0-valid
  fixture (`fixtures/valid/widget-1.json`) still validates against
  `new.schema.json`: genuinely additive, minor bump is correct.
- `mislabeled-minor/` — same `old.schema.json` → `new.schema.json` (v1.1.0)
  makes `description` REQUIRED: a breaking change mislabeled as a minor
  bump. The same v1.0.0-valid fixture now FAILS against `new.schema.json`:
  this is exactly the CC-080 scenario V3's compat check (§5.4b) must catch
  and reject as "major required."
