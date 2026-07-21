---
slug: v1-min-2026-07
phase: P1
spec: ../specs/01-foundation.md
wave: 1
status: planned
---

# Phase plan — P1 Foundation

## Goal

Stand up the Go module (`github.com/ydnikolaev/a2ahub`), the `a2a` binary's
subcommand-dispatch skeleton with a build-time version stamp, the
`internal/artifact` package (two §3.3 artifact-ID classes + ULID event IDs,
frontmatter parse/serialize, SHA-256 digests), `.golangci.yml`, and the
product CI workflow — spec 01 AC rows 1–9.

## Allowlist (repo-relative)

- `go.mod`, `go.sum`
- `cmd/a2a/**`
- `internal/artifact/**`
- `.golangci.yml`
- `.github/workflows/ci.yml`

## Lead-reserved / off-limits deltas

- After this wave, `go.mod`/`go.sum` become lead-reserved (lead runs
  `go get` for jsonschema/v6 and testscript between waves).
- `schemas/**` is P2's grant (parallel sibling) — never touch.

## Brief

```
Stack: Go 1.26, stdlib-first (flag-based CLI dispatch, log/slog, encoding/json).
Deps allowed IN THIS PHASE (already lead-approved by ADR-002 — add them to
go.mod yourself): gopkg.in/yaml.v3, github.com/oklog/ulid/v2. NOTHING else.
All file paths are REPO-RELATIVE — they resolve against the repo root.

## Goal
Create the Go module, the a2a dispatch skeleton with a version stamp,
internal/artifact (IDs, frontmatter, digests, ULIDs), .golangci.yml, and the
product CI workflow — per docs/features/v1-min-2026-07/specs/01-foundation.md.

## Spec / context links (READ FIRST, in order)
- Spec: docs/features/v1-min-2026-07/specs/01-foundation.md (end to end — the
  T1/T1b tables and §6 testing table are your work list; §8 is acceptance).
- Rails: root AGENTS.md section "a2ahub engineering rails" — binding. Your
  code sets the pattern every later phase copies; the early-audit checks
  against that file first.
- ADR-001/ADR-002: docs/decisions.md (layout, import boundaries, deps).
- Domain: docs/the-plan/plan/03-domain.md §3.3 (ID grammar),
  docs/the-plan/plan/05-schemas.md §5.1/§5.2 id row/§5.7 (frontmatter shape,
  filename+section guard, digests), docs/the-plan/plan/00-meta.md §0.4
  ("section" glossary term for the Validate guard).

## Allowed files (allowlist) — REPO-RELATIVE ONLY
- go.mod, go.sum
- cmd/a2a/** (main.go + dispatch; keep wiring minimal — later phases append)
- internal/artifact/**
- .golangci.yml
- .github/workflows/ci.yml

## Off-limits (NEVER touch)
- schemas/** (a parallel agent owns it this wave)
- Makefile, docs/**, scripts/**, .claude/**, .agents/**, .mate/**

## What to do
1. `go mod init github.com/ydnikolaev/a2ahub`; add gopkg.in/yaml.v3 and
   github.com/oklog/ulid/v2 (the only two deps this phase imports).
2. TDD default: write internal/artifact table-driven tests from spec §6
   first, then implement: standing-ID mint, exchange/broadcast-ID mint (UTC
   date + 4-char base32 rand), Parse (class/prefix/system/rest), Validate
   (filename-stem guard AND owning-section guard — two INDEPENDENT checks),
   frontmatter Parse/Serialize (structural split only, byte-round-trip
   `---\n<yaml>\n---\n<body>`, CRLF cases), Digest (`sha256:<full-hex>`,
   never truncated), ULID Mint/Parse (caller-suppliable timestamp/entropy
   for testability).
3. cmd/a2a: stdlib flag dispatch — no subcommand → usage to stderr exit 2;
   `version` → one-line stamp (ldflags -X vars + commit SHA) exit 0; unknown
   → `unknown command "<x>"` stderr exit 2. Dispatch table = the single seam
   later phases append verbs to.
4. .golangci.yml: a sane strict baseline for this repo (govet, staticcheck,
   errcheck, revive or equivalent); no lints that fight the rails.
5. .github/workflows/ci.yml: push + pull_request → checkout, Go setup pinned
   to the repo Go version, single project step `make check`. Do NOT
   reimplement Makefile steps inline.
6. Sanity: `gofmt -l cmd internal` (empty), `go vet ./cmd/... ./internal/...`,
   `go test ./internal/artifact/... -race -count=1`.

## Constraints
- ERROR IDIOM (pinned — sets the repo pattern): exported sentinel errors per
  failure class (e.g. ErrMalformedID, ErrIDMismatch, ErrNoFrontmatter) +
  a small typed error carrying context that wraps them; callers use
  errors.Is/As; never panic on input. These are STRUCTURAL errors — fully
  distinct from the schemas/errors/v1 registry codes (P2/P3's universe);
  do not invent code strings here.
- Prefix is an opaque string at this layer (spec Open Q2): no 8-type enum
  here — enum-closedness is P2/P3's concern.
- t.Parallel() in every test (or `// reason:`); no t.Skip, no //nolint
  without reason; log-or-return (slog only injected — but this phase should
  need no logger in internal/artifact at all).
- No new dependencies beyond the two named. No new config files beyond
  .golangci.yml.

## DO NOT
- DO NOT commit. DO NOT run git at all.
- DO NOT run `make check` / `make check-validators` / repo-wide
  `go build|test ./...`. Scope self-verification to your own packages
  (./cmd/... ./internal/artifact/...).
- DO NOT touch files outside the allowlist.

## Acceptance
- Spec §8 rows 1–9 (verbatim from the spec — go build, workflow shape, mint/
  parse/validate/round-trip/digest vectors, version/unknown exit codes,
  scoped gofmt/vet clean).

## Report back
- Files modified, tests added, scoped test output.
- Deviations from the spec — REQUIRED; "none" is a real answer, but only if
  you mean it. A different shape, path, or silently-closed open question —
  report it even if obviously right; it feeds the propagation probe.
- Anything skipped + why; any off-limits file you wanted.
```

## Acceptance

- [ ] Spec 01 §8 rows 1–9 green (verified lead-side: diff + scoped tests re-run).

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- go.mod/go.sum become lead-reserved after this wave (jsonschema/v6 before
  W2, testscript before W5).
