---
schema: envelope/v2
id: XC-<system>-<slug>              # e.g. XC-axon-ingest — standing ID grammar §3.3
type: contract
title: <human/agent-scannable title, <=120 chars>
space: <space-id>
from: <owning-system>
to: [<consumer-system>]              # standing type (D6): any length, or the literal `all` to address the whole space — no cardinality rule either way
actor: {kind: agent, name: <filled by a2a>, model: <filled by a2a>}   # kind: human|agent
created: <RFC-3339 UTC, e.g. 2026-07-28T10:00:00Z>
category: <api|data-feed|vocabulary|event-feed|other>
priority: p3
blocking: false
classification: internal
version: 0.0.0                       # set by `a2a contract publish`, never by hand — see the note at the bottom
schema_format: json-schema-2020-12
compat_policy: default
# generated_from:                    # include only for a code-generated contract
#   tool: "<generator name and version>"
#   source_digest: "sha256:<export-source-v1 digest>"
#   Compute source_digest in YOUR OWN generator — a value copied out of a2a's
#   own refusal proves only that a2a agrees with itself. `a2a contract
#   publish` is the guard: it refuses a mismatched assertion before writing
#   anything. `a2a contract verify-export --local <dir> <XC-id>` prints the
#   export-source-v1 digest a2a computes for a local candidate, which is for
#   CHECKING your own implementation of the combine, never for filling this
#   field in from.
#
#   export-source-v1, in full, so a third party never needs to run a2a as an
#   oracle to reproduce it:
#     1. Per-file value: read each declared artifact's raw bytes (the
#        `schema/`, `fixtures/valid/`, `fixtures/invalid/` files this
#        descriptor's own `artifacts:` list carries — never contract.md
#        itself), take SHA-256 over those exact bytes, and encode the sum as
#        `"sha256:" + lowercase-hex(32 bytes)` — a STRING, the same shape
#        this field's own value takes. Never raw bytes, never raw hex.
#     2. Build the (path, per-file-value) pair list: path is the
#        contract-root-relative, forward-slash-joined path (e.g.
#        `schema/ingest.schema.json`); per-file-value is the full string
#        from step 1, prefix included.
#     3. Sort order: Go byte-lexicographic ascending on the path string
#        (`sort.Strings` — plain UTF-8 byte comparison, no locale
#        collation).
#     4. Combine: over the sorted list, write for each pair
#        path-bytes + 0x00 (one NUL byte) + per-file-value-bytes + '\n' (one
#        LF byte) into a single SHA-256 hash, in order.
#     5. Output encoding: the combined hash is `"sha256:" + lowercase-hex(32
#        bytes)` — the same `"sha256:" + hex` shape as every per-file value,
#        applied one more time to the combined sum.
#   The empty set (no declared artifacts) is a real, well-formed digest: the
#   combine still runs, over zero pairs, and yields SHA-256's own
#   empty-input digest, `sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`.
# x_binding:                          # OPTIONAL — what this contract IS and whether it may be adopted or pinned
#   artifact_class: <author's own vocabulary, e.g. non_binding_review>
#   compatibility_status: <the claim being made, or "none" to make no claim at all>
#   adoptable: <true|false — may `a2a contract adopt` target this>
#   runtime_pinnable: <true|false — may a consumer pin a runtime dependency against this>
#   Or the literal `x_binding: none` to declare non-binding in one line. Commented,
#   like generated_from above, and deliberately: ABSENCE IS `undeclared` AND IS
#   DISTINCT FROM A DECLARED `none` (P-1, and the schema's own words). A live
#   block here would make every fresh contract author a declaration nobody
#   made, and a live block with placeholder booleans would additionally render
#   a draft that cannot validate (spec 05, the same reversal work_request's
#   `binding` measured).
# x_operational:                      # OPTIONAL — readiness of the operational half this contract describes
#   - name: endpoint                  # e.g. endpoint, credential-channel, registration — a fourth kind is data
#     state: <ready|absent>
#     eta: <YYYY-MM-DD>                # a declared date, not a clock (§7) — omit if none
#   Commented, like x_binding above and deliberately: a named item ABSENT from
#   this array, or the field absent altogether, reads as `undeclared`
#   downstream (P-1) — distinct from a declared `state: absent` — and a live
#   block here would make every fresh contract author a declaration nobody
#   made. Cleared by `a2a contract activate`, never by hand-editing this file
#   after publication (a descriptor is immutable once published).
# x_identity:                         # OPTIONAL — the producer's own keying rule, so a consumer does not mis-key and silently accumulate duplicates
#   keys: [<field name>, ...]          # the field(s) that make one record unique
#   dynamic_keys_from: <field name>     # OPTIONAL — an axis whose VALUES each name an additional per-value key, when keys[] alone under-specifies identity
#   on_redelivery: <upsert|append>       # what a consumer does when the same key arrives again
#   Commented, like x_binding above and deliberately: a contract carrying no
#   x_identity at all reads as `undeclared` downstream (P-1) — distinct from a
#   declared keying rule — and a live block here would make every fresh
#   contract author a declaration nobody made.
# x_guarantees:                       # OPTIONAL — closed, checkable claims this contract makes about its own data, never free-text prose
#   - <required_non_null|deterministic_keys>
#   Commented and deliberate, same rule as x_identity: a contract carrying no
#   x_guarantees at all reads as `undeclared`, never as "no guarantees made" —
#   that is a declared EMPTY array, a distinct, different state.
# x_schema_location: <repo-relative path, e.g. provides/ingest/schema/main.schema.json>
#   OPTIONAL — where the machine-checkable schema for this contract's payload
#   lives, so automation does not have to parse a sentence to find it.
#   Unlike x_identity and x_guarantees above, this one is a plain string, so
#   `a2a new contract --field x_schema_location=...` sets it at authoring time;
#   the other two are an object and an array, which the --field append pass
#   cannot open, so they are hand-edited. Measured by the reachability gate.
#   NB the example above spells a literal slug on purpose. The renderer
#   substitutes the slug token in YAML SCALARS and never inside a comment, so
#   one written here ships to every scaffolded contract unresolved — which is
#   what internal/template's own test refuses, and it refused this line twice:
#   once for the example, once for the sentence explaining the example.
#   Commented and deliberate: absence reads as `undeclared`, not "the schema
#   is co-located with this file by convention" — see x_binding's note above.
thread: <thread:system-YYYYMMDD-rand4 — a2a new mints this>
# refs:
#   - {ref: "<XC-id>@<version>", note: "<why>"}
artifacts:
  - path: schema/<slug>.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/<slug>.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/<slug>.schema.json
  - path: fixtures/invalid/<slug>.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/<slug>.schema.json
# Declare every other regular carried file exactly once. Companion roles live
# under artifacts/: errors, vocabulary, limits, changelog, example, or other.
# `conforms_to` is required only on valid-fixture and invalid-fixture entries.
#  - path: artifacts/errors.yaml
#    role: errors
#    normative: true
#    media_type: application/yaml
---
# <Contract name>

<One paragraph describing the contract in the consumer's terms.>

## What it covers

<The operations, feeds, or vocabulary in scope, plus deliberate exclusions.>

## Error shape

<How failures are represented and which codes or statuses consumers handle.>

## Compatibility intent

<What is breaking beyond the computed schema-shape check, and what may grow.>

## Owner and support

<Who owns this contract and where consumers can ask for help.>

<!-- THE VERSION FIELD IS NOT YOURS TO SET. Leave it at 0.0.0.
     `a2a contract publish` finalizes this descriptor with the version you are
     publishing, and its commit — the one where the version FLIPS — is what
     makes that version resolvable ever after. `a2a contract materialize`
     depends on it, and so does every later version, which resolves the
     earlier one as its compatibility baseline.

     Set this to the version you intend to publish and there is nothing left
     to flip: publish writes byte-identical bytes, its commit carries only the
     publish event, and no commit establishes the version. `publish` refuses
     that up front — "publication-would-not-establish" — while it is still a
     one-line fix here. Bump by publishing (`--version 1.1.0`, or
     `--bump minor`), never by editing this field. -->
