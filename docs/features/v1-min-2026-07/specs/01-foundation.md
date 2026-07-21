# P1 — Foundation: Go module, `cmd/a2a` skeleton, `internal/artifact`, product CI — Specification

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Plan**: [plans/01-foundation.plan.md](../plans/01-foundation.plan.md)
**Footprint**: `go.mod`, `go.sum`, `cmd/a2a/`, `internal/artifact/`, `.golangci.yml`, `.github/workflows/ci.yml` —
Go module init (module path `github.com/ydnikolaev/a2ahub`, per the GitHub
remote), the `a2a` binary's subcommand-dispatch skeleton + version stamp,
artifact ID mint/parse/validate + frontmatter parse/serialize + digest
primitives + ULID mint/parse for lifecycle event IDs (§5.2.2), the
`.golangci.yml` lint config (once it exists, `make check` refuses to run
without `golangci-lint` installed — a configured gate never silently skips),
and the product-repo CI workflow running `make check`.
**Engineering rails**: all code in this phase — and every phase after it —
conforms to root `AGENTS.md § a2ahub engineering rails` (stdlib-first stack,
ISP/DI, idempotency-by-design, error flow, concurrency, testing rails,
anti-pattern table). P1 sets the patterns every later wave copies; the
S6.c.3 early-audit checks THIS spec's output against that file first.
`internal/artifact` imports **stdlib + ADR-002 deps only** (`gopkg.in/yaml.v3`
for frontmatter, `github.com/oklog/ulid/v2` for event IDs — ADR-001,
ADR-002). `cmd/a2a` may import anything under `internal/` (ADR-001).

---

## 0. User stories (local to this phase — infrastructure, not user-facing)

| ID | User story |
|----|------------|
| US-1 | As a later-phase implementor (P2–P14), I want a stable `internal/artifact` package for ID mint/parse/validate and digests, so every downstream package builds on one correct primitive instead of re-deriving §3.3/§5.7 rules. |
| US-2 | As the `a2a` binary's entry point, I want a subcommand-dispatch skeleton with a version stamp, so later phases (P6–P8) add verbs without re-plumbing `main`. |
| US-3 | As the product repo, I want CI to run `make check` on every push, so a broken build/format/vet/test never lands on `main` unnoticed. |
| US-4 | As an operator running `a2a update` in a later phase (§7.3, D-013), I want the binary to carry a build-time version stamp, so `min_binary_version` pin checks have something to compare against. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Artifact ID scheme | [03-domain.md §3.3](../../../the-plan/plan/03-domain.md) | two ID classes: standing `<PREFIX>-<system>-<slug>`; exchange/broadcast `<PREFIX>-<system>-<YYYYMMDD>-<rand4>`; immutable, never reused, survive archival |
| ID envelope guard | [05-schemas.md §5.2](../../../the-plan/plan/05-schemas.md) | `id` field row: "validated against filename and section" — the exact guard this phase's `Validate` implements |
| Digests | [05-schemas.md §5.7](../../../the-plan/plan/05-schemas.md), D-029 | SHA-256 over raw committed bytes, string form `sha256:<full-hex>`; display MAY truncate, storage MUST NOT; computed, never stored inside the artifact |
| Schema stack | [05-schemas.md §5.1](../../../the-plan/plan/05-schemas.md) (D-009) | artifacts are `.md` = YAML frontmatter + Markdown body — the round-trip shape `internal/artifact`'s parse/serialize owns |
| Client binary shape | [07-client.md §7.1](../../../the-plan/plan/07-client.md) (D-005, R-004) | one binary `a2a`, multiple modes (CLI/MCP/validator/statusline/html); P1 stands up only the dispatch skeleton, no verb behavior |
| Distribution & version pin | [07-client.md §7.3](../../../the-plan/plan/07-client.md) (D-013) | `min_binary_version` pin + verify-before-swap self-update — the reason `cmd/a2a` carries a version stamp starting now |
| Project config | [07-client.md §7.4](../../../the-plan/plan/07-client.md) | `.a2a/config.yaml` / `~/.config/a2a/config.yaml` — not implemented in P1; cited so the skeleton doesn't preclude it |
| Package layout & import boundaries | [ADR-001](../../../decisions.md) | normative footprint + import rules for `cmd/a2a` and `internal/artifact` |
| Naming | D-005 ([17-decisions.md](../../../the-plan/plan/17-decisions.md)) | product `a2ahub`, binary/CLI `a2a`; single rename constant — do not hardcode `a2a` string literals in more than one place |

---

## T1. CLI surface (track: cli)

> P1 stands up dispatch only — no OP-2xx verb has real behavior yet (those
> land P2–P14 per the epic README's phase table). This is infrastructure.

| Command | Flags | Input | Output | Notes |
|---------|-------|-------|--------|-------|
| `a2a` (no subcommand) | — | none | usage text to stderr, exit 2 | stdlib `flag`-based dispatch |
| `a2a version` | none | none | binary version stamp (build-time `-ldflags -X`) + commit SHA to stdout, exit 0 | feeds §7.3 `min_binary_version` checks in later phases; plain text, one line |
| `a2a <unknown>` | — | none | `unknown command "<x>"` to stderr, exit 2 | placeholder — the dispatch table is the single seam later phases (OP-2xx, §7.2) append to |

### T1b. `internal/artifact` package contract

> Table describes behavior, not Go signatures — the implementor picks
> idiomatic names. Do not paste implementation code here (template rule).

| Capability | Behavior | Plan ref |
|---|---|---|
| Mint standing ID | Given `prefix` + `system` + `slug`, produce `<PREFIX>-<system>-<slug>` | §3.3 |
| Mint exchange/broadcast ID | Given `prefix` + `system`, produce `<PREFIX>-<system>-<YYYYMMDD>-<rand4>` — UTC date, 4-char base32 random suffix, no central counter (federation-safe) | §3.3 |
| Parse ID | Given an ID string, return its class (standing / exchange-broadcast), prefix, system, and slug-or-date+rand; reject malformed strings with a typed error, never panic | §3.3 |
| Validate against filename + section | Given a parsed ID + the artifact's file path: confirm (a) the filename stem matches the ID exactly, AND (b) the ID's `<system>` matches the owning section (the path's system-owned subtree, per the "section" glossary term, [00-meta.md §0.4](../../../the-plan/plan/00-meta.md)). Both guards MUST hold independently — a false pass on either is a defect | §5.2 `id` row, §0.4 |
| Parse frontmatter | Given raw `.md` bytes, split into YAML frontmatter block + Markdown body; malformed/missing frontmatter delimiters is a typed error | §5.1 (D-009) |
| Serialize frontmatter | Given frontmatter data + body, produce the exact byte layout `---\n<yaml>\n---\n<body>` | §5.1 |
| Digest | Given raw file bytes, return SHA-256 and its string form `sha256:<full-hex>`; never truncate the returned/stored form; computed on demand, never persisted into the artifact itself | §5.7, D-029 |
| Mint lifecycle event ID | Given nothing (or a caller-supplied timestamp/entropy source for testability), produce a ULID via `github.com/oklog/ulid/v2` for use as an `event` field value; intra-commit tiebreak only, not one of the two §3.3 artifact-ID classes | §5.2.2, ADR-002 |
| Parse lifecycle event ID | Given a ULID string, parse/validate it via `github.com/oklog/ulid/v2`; reject malformed strings with a typed error, never panic | §5.2.2, ADR-002 |

Frontmatter parse/serialize here is **structural only** (YAML block ↔ body
split, round-trip fidelity) — JSON-Schema validation of envelope *field
content* (types per §5.2, category enums per §5.2.1) is
`internal/schema`/`internal/validate`'s concern (ADR-001, P2/P3), out of this
phase's footprint.

ULID mint/parse is a third ID capability alongside the two §3.3 artifact-ID
classes (standing, exchange/broadcast) — `internal/artifact` owns all three,
per ADR-002's assignment of ULID to `artifact`. Downstream, P4's `fold`
consumes minted ULIDs only as opaque ordering keys for intra-commit tiebreak
(§3.5 rule 1) and stays stdlib-pure (ADR-001) — it never mints or parses
ULIDs itself.

## T5. CI pipeline (track: ci)

| Workflow | Trigger | Steps | Gate it enforces |
|----------|---------|-------|-------------------|
| `.github/workflows/ci.yml` | `push`, `pull_request` on the product repo | checkout, Go setup pinned to the repo's Go version, `make check` | the gate `make check` already defines once `go.mod` exists (root [Makefile](../../../../Makefile)): `gofmt -l` (fails on unformatted files), `go vet ./...`, `go test ./... -race -count=1`, plus the pre-existing repo gates `feature-lint`/`epic-drift` |

This phase is what flips `make check`'s `if [ -f go.mod ]` branch on — before
P1, CI (if it ran at all) only exercised the repo gates. The workflow calls
`make check` as a single step; it does not reimplement the Makefile's steps
inline (§5 below).

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Root [Makefile](../../../../Makefile) already defines `make check` /
      `make check-validators`. The CI workflow MUST invoke `make check`, not
      reimplement `gofmt`/`go vet`/`go test` inline.
- [ ] `scripts/lib/gate-lib.sh` conventions are for the shell-side repo gates
      (`feature-lint`, `epic-drift`). Do not port that error-reporting
      convention into Go — use idiomatic `error` / `errors.Is`/`As`.
- [ ] stdlib `flag` package for `cmd/a2a` dispatch — no CLI framework
      (cobra/urfave/etc.) per stdlib-first; confirm against
      [.claude/rules/go-conventions.md](../../../../.claude/rules/go-conventions.md)
      before adding any third-party flag parser.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| ID mint (standing) | round-trip `<PREFIX>-<system>-<slug>` | slug/system containing hyphens; empty slug rejected |
| ID mint (exchange/broadcast) | round-trip `<PREFIX>-<system>-<YYYYMMDD>-<rand4>` | rand4 charset is base32; a tight-loop smoke test that consecutive mints don't trivially collide (not a formal proof) |
| ID parse | valid standing + valid exchange forms both parse | malformed prefix, missing system, wrong date length, non-base32 suffix — all typed errors, no panic |
| Validate against filename + section | matching filename + matching section passes | filename stem ≠ ID (fail); ID system ≠ owning section (fail); both correct (pass); each guard tested independently of the other |
| Frontmatter parse/serialize | round-trip: `Serialize(Parse(x)) == x` | missing `---` delimiters, empty body, empty frontmatter block, CRLF line endings |
| Digest | known input → known `sha256:<hex>` value (hand-computed test vector) | empty-byte input; string form is never truncated |
| `cmd/a2a` skeleton | `a2a version` exits 0 with a non-empty stamp; `a2a nonsense` exits 2 | no subcommand → usage text + exit 2 |
| CI workflow | workflow YAML's only project-specific step is `make check` | not exercised by GitHub Actions itself in self-verify — inspected statically; the Go-side gates it wraps are proven by the scoped `go test` run below |

## 7. Schema / contract delta

None. P1 introduces no JSON Schema. `internal/schema` (envelope/event/manifest
schemas, §5.1) is P2's footprint (ADR-001). This phase's frontmatter parse is
structural (YAML block ↔ body split), not schema-validating.

## 8. Acceptance criteria

> All rows are phase-local (no plan AC-###.# applies to an infrastructure
> phase) — US column is `—` throughout per the epic brief's rule for
> phase-local criteria.

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | — | `go.mod` declares module `github.com/ydnikolaev/a2ahub`; `go build ./...` succeeds from repo root | `go build ./...` |
| 2 | — | `.github/workflows/ci.yml` triggers on `push` and `pull_request` and runs `make check` | inspect workflow YAML; `make check` exits 0 locally on a clean checkout |
| 3 | — | `internal/artifact` mints a standing ID matching `<PREFIX>-<system>-<slug>` and an exchange ID matching `<PREFIX>-<system>-<YYYYMMDD>-<rand4>` (§3.3) | `go test ./internal/artifact/... -race -count=1` |
| 4 | — | Parsing rejects a malformed ID (wrong prefix shape, non-base32 rand suffix) without panicking | same test binary, table-driven malformed-input cases |
| 5 | — | `Validate` fails when the ID's system does not match the artifact's owning section, and separately fails when the filename stem does not match the ID — each guard exercised in isolation | same test binary |
| 6 | — | Frontmatter round-trip `Serialize(Parse(x)) == x` holds for a fixture `.md` with YAML frontmatter + Markdown body | same test binary, byte-equality assertion |
| 7 | — | Digest of a known fixture byte string equals a hand-computed `sha256:<hex>` value; the returned/stored string form is never truncated | same test binary, fixed test vector |
| 8 | — | `a2a version` exits 0 and prints a non-empty version stamp; `a2a` with an unrecognized subcommand exits 2 | `go run ./cmd/a2a version`; `go run ./cmd/a2a bogus; echo $?` |
| 9 | — | `gofmt -l .` reports zero files under this phase's footprint; `go vet ./cmd/a2a/... ./internal/artifact/...` is clean | scoped `gofmt -l cmd/a2a internal/artifact` + scoped `go vet` (full-repo `make check` is the CI workflow's job, per this worker's concurrency rule — not self-verify's) |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | ID mint/parse is type-prefix-agnostic (takes the prefix as an opaque string) — P2's 8-type enum (§3.1) layers on top without touching this package |
| Coupling | Soft: downstream packages (`schema`, `fold`, `validate`, `space`, ...) depend on `artifact`'s exported types/functions only, never its internals (ADR-001 import graph) |
| Migration path | low — stdlib + ADR-002 deps surface (ADR-002 closed Open questions §1), no schema-versioning concern lives here |
| Roadmap conflicts | none — the YAML-frontmatter/stdlib-only tension in Open questions §1 is resolved by ADR-002 |

## 10. Implementor entry point

Execute as a single spec — P1 has no `blocked_by` in `tracker.yaml`; it is the
epic's root phase, every other phase depends on it transitively. TDD default:
write the `internal/artifact` table-driven tests (§6) against the
not-yet-existing package first, then implement. Framework-first: stdlib
`flag` for `cmd/a2a`, stdlib `crypto/sha256` for digests; the YAML
frontmatter codec uses `gopkg.in/yaml.v3` per ADR-002 (unblocked — see
Open questions §1); ULID mint/parse uses `github.com/oklog/ulid/v2` per
ADR-002. Both are already in `go.mod` terms (ADR-002 is a lead-level
decision, not a wave-level vendoring call). Log-or-return per
[.claude/rules/go-conventions.md](../../../../.claude/rules/go-conventions.md).
Full loop (README/tracker/specs shapes, lint gate):
[docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it
> here AND amend any downstream spec.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

### 2026-07-21 — from coherence audit (pre-implementation)

- Open questions §1 closed as RESOLVED by ADR-002 (question text kept
  verbatim; resolution appended) — ADR-002 approves `gopkg.in/yaml.v3`,
  `github.com/santhosh-tekuri/jsonschema/v6`, `github.com/oklog/ulid/v2` and
  ADR-001's `internal/artifact` row now reads "stdlib + ADR-002 deps only."
- Footprint line updated from "stdlib only (tension in Open questions §1)" to
  "stdlib + ADR-002 deps only," and scope prose extended to name ULID
  mint/parse for lifecycle event IDs.
- Future-proof considerations §9 Migration-path and Roadmap-conflicts rows
  updated from "stdlib only / pending Open questions §1" to reflect ADR-002
  resolution.
- Implementor entry point §10 unblocked: the frontmatter codec now names
  `gopkg.in/yaml.v3` instead of reporting a blocker; ULID work names
  `github.com/oklog/ulid/v2`.
- T1b `internal/artifact` package contract gains two capability rows (mint
  lifecycle event ID / parse lifecycle event ID via `github.com/oklog/ulid/v2`,
  §5.2.2) plus a note that this is a third ID capability alongside the two
  §3.3 classes, and that P4's `fold` consumes ULIDs only as opaque ordering
  keys and stays stdlib-pure — per ADR-002's assignment of ULID to
  `internal/artifact`, which P1's original scope omitted.

---

## Open questions

1. **ADR-001's `internal/artifact` = "stdlib only" conflicts with D-009's
   YAML-frontmatter requirement.** [ADR-001](../../../decisions.md) assigns
   `internal/artifact` "md+YAML frontmatter parse/serialize (§3.3, §5.7)" and
   "May import: stdlib only" in the same row. Go's standard library ships no
   YAML decoder (`encoding/json`/`xml`/`csv`/`gob` only). [05-schemas.md
   §5.1](../../../the-plan/plan/05-schemas.md) (D-009) mandates artifacts are
   ".md = YAML frontmatter + Markdown body." As written, T1b's "parse/serialize
   frontmatter" rows cannot be satisfied literally within "stdlib only." This
   is a `go.mod`-dependency decision, explicitly reserved to the lead (this
   worker's hard invariant: no new dependencies). Corroborating signal: `go.sum`
   is already in this phase's footprint, which only exists once an external
   module is added. Naming the conventional candidate for the lead to
   evaluate, without picking it: a YAML v3-class library (e.g.
   `gopkg.in/yaml.v3`). **Flagging per the epic's "do not resolve plan
   contradictions silently" rule — not resolved here.**

   **RESOLVED (2026-07-21) by ADR-002**: [docs/decisions.md](../../../decisions.md)
   ADR-002 approves exactly three third-party modules for the v1 core —
   `gopkg.in/yaml.v3` (YAML parse/serialize; `artifact`, `schema`, `space`),
   `github.com/santhosh-tekuri/jsonschema/v6` (JSON Schema 2020-12 engine;
   `validate` via `schema`), `github.com/oklog/ulid/v2` (ULID mint/parse;
   `artifact`) — and ADR-001's `internal/artifact` row now reads "May import:
   stdlib + ADR-002 deps only." The frontmatter codec uses `gopkg.in/yaml.v3`;
   it is no longer blocked. `go.sum`'s presence in this phase's footprint is
   now explained by ADR-002, not a dangling signal.
2. **The P1/P2 seam for the type-prefix enum is implicit.** §3.1 defines the 8
   type prefixes (`XC/XR/XQ/XW/XD/XS/XH/XA`, [00-meta.md
   §0.3](../../../the-plan/plan/00-meta.md)); ADR-001 gives `internal/schema`
   (P2) "version handling" and `internal/artifact` (P1) "IDs (§3.3)" without
   stating which package owns type-enum closedness at mint/parse time. This
   spec resolves it locally (T1b: prefix is an opaque string at the
   `artifact` layer; enum-closedness deferred to P2/`internal/validate`) —
   noted here so a P2 author can check the interpretation against their own
   reading, not because the plan itself is contradictory.

### 2026-07-21 — from wave 1: shipped-reality deltas (internal/artifact)

- **ID grammar narrowed for parseability**: `system` is hyphen-free at the
  mint/parse layer (a hyphenated system would make `<PREFIX>-<system>-<slug>`
  ambiguous; both §3.3 examples are single-token). Consequence: standing
  slugs beginning with a digit-run + hyphen (e.g. `24-7-monitoring`) are
  rejected as malformed exchange-form attempts — the digit-run trigger is
  what makes wrong-date-length / non-base32 suffixes reject per §6. Revisit
  only if a real participant needs either shape (backlog row).
- **CRLF**: `Serialize` emits the LF-normative `---\n<yaml>\n---\n<body>`
  layout; CRLF inputs are parse-tolerant but do NOT byte-round-trip (§6's
  round-trip AC holds on LF fixtures).
- **Digest** returns the string form `sha256:<hex>` only (no raw-bytes
  variant yet — additive later if a consumer needs it).
- **ParseFrontmatter** additionally verifies the YAML block is well-formed
  (rejects with `ErrMalformedFrontmatter`) while still storing raw bytes for
  byte-faithful Serialize.
- **Sentinel-error set** beyond the plan's examples: `ErrEmptyField`,
  `ErrSectionMismatch`, `ErrMalformedFrontmatter`, `ErrMalformedULID`; all
  wrapped by one typed `*Error{Op, Input, Err}`.
- **CI** carries an install step for golangci-lint v2.12.2 (pinned) because
  `make check` hard-fails when `.golangci.yml` exists without the binary;
  the only project-specific step remains `make check`.

### 2026-07-21 — engineering rails + lint gate (pre-implementation)

- **Root `AGENTS.md § a2ahub engineering rails` created** (ported/adapted
  from the axon backend rails, server/DB specifics stripped) and made the
  SSOT for Go discipline; `.claude/rules/go-conventions.md` demoted to a
  pointer. This phase's Footprint gains `.golangci.yml`; `make check` now
  fails loudly if the config exists but `golangci-lint` is missing.
  Conformance to the rails is a phase-entry requirement, and the
  pattern-setting early-audit (teamlead S6.c.3) audits against that file.
