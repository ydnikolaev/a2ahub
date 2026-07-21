---
slug: v1-min-2026-07
phase: P9
spec: ../specs/09-space-template-ci.md
wave: 3
status: planned
---

# Phase plan — P9 Space template + V3 CI + basic doctor

## Goal

Ship the `space-template/` scaffold (§4.2 layout minus per-system sections),
the V3 CI workflow (pinned-binary fetch, `a2a-validate` required check,
diff-authz + compat behavior contract), the branch-protection checklist for
P11's runbook, and the basic `a2a doctor` verb. Spec 09 AC rows 1–10.

## Allowlist (repo-relative)

- `space-template/**`
- `internal/cli/cmd_doctor.go` (OP-218 basic doctor)

## Lead-reserved / off-limits deltas

- `internal/cli/cli.go` — LEAD-OWNED seam (already created); build against
  `cli.Command`/`cli.IO`, never edit.
- `internal/cli/{cmd_init,cmd_new,cmd_submit,cmd_sync,adapters}.go` — P6's,
  parallel this wave; never touch, never import their symbols.
- `cmd/a2a/**` — LEAD wires dispatch after the wave (one doctor line).
- `go.mod`, `internal/*` core packages (doctor consumes, never edits).

## Placement decisions (lead, binding)

- **cmd_doctor.go is a `cli.Command`** exactly like P6's verbs (same
  lead-owned seam) — implements Name/Synopsis/Run, constructor
  `NewDoctorCommand(deps...)`. Its ONLY package-level symbols are its own
  command type + constructor; it defines NO shared helper, NO package var
  (those would collide with P6's parallel files in the same package). If it
  needs a helper, keep it file-private and uniquely named (doctorX).
- **Doctor deps via constructor DI**: connected-space config + mirrors
  (internal/space), credential store (internal/space ResolveCredential),
  binary version stamp (injected string). No internal/cache import (absent).
  CORRECTION (from wave-3 reality, 2026-07-21): reachability + CI-presence
  are implemented via `internal/space.CloneOrFetch` + mirror filesystem
  reads, NOT `internal/host` — the shipped Host interface exposes only
  PR-scoped methods (no generic repo-reachability or branch-protection
  read). P9 correctly wired it this way and self-disclosed; the
  `host.Host` param is retained for DI parity but unused today. A future
  `--space` host-drift diff would need a new Host primitive (core change,
  lead's call — backlog).
- **`--space` flag is REJECTED** with an explicit "not available in v1-min"
  error (D-030), never silently ignored.
- **space-template/ is pure data** (no imports); `space.yaml` is a literal
  schema-valid instance of the P2 manifest schema (zero participants →
  AC-101.1 green-on-empty). CODEOWNERS lists ONLY `/space.yaml` +
  `/decisions/**` — no `/<system>/provides/**` pre-seeding.
- **V3 workflow is a behavior contract, not a flag string** (§9 roadmap):
  the `a2a-validate` check NAME is byte-load-bearing (§4.2), but the exact
  `a2a` verb/flags it invokes are P6/P3's surface — reference the pinned
  binary + the V3 entrypoint behavior, do not hardcode a flag string that
  could drift from P6's actual CLI.

## Brief

```
Stack: GitHub Actions YAML (data) + Go 1.26 stdlib for cmd_doctor.go. NO new
deps, no custom CI framework, no shell reimplementation of validation.
All file paths are REPO-RELATIVE.

## Goal
Ship space-template/** + internal/cli/cmd_doctor.go per
docs/features/v1-min-2026-07/specs/09-space-template-ci.md — read END TO
END first (Scope, T3 layout, T5 V3 workflow, T1 doctor, the branch-
protection checklist, §6 tests, §8 ACs), then the Placement decisions in
docs/features/v1-min-2026-07/plans/09-space-template-ci.plan.md (BINDING).

## Context (read in order)
- The lead-owned seam internal/cli/cli.go (cli.Command, cli.IO) — build
  against it, do NOT edit.
- Root AGENTS.md rails (space-template discipline, error flow, testing).
- Plan corpus: 04-topology.md §4.2/§4.5, 05-schemas.md §5.4/§5.4b/§5.5 (V3
  row), 07-client.md §7.2 OP-218, 10-security.md §10.3/§10.5, 09-human-ops
  §9.1/§9.3, 15-rollout.md L0.
- Exported APIs doctor consumes (read source, reuse): internal/space
  (LoadProjectConfig, connected spaces, ResolveCredential, CloneOrFetch/
  mirror, ParseManifest/min_binary_version), internal/host (reachability +
  CI-presence read), schemas/manifest/v1 for the space.yaml instance shape.
- The P2 manifest schema at schemas/manifest/v1/space.schema.json — the
  template's space.yaml MUST validate against it (read it, don't guess).

## Allowed files — REPO-RELATIVE ONLY
- space-template/** (space.yaml, CODEOWNERS, .github/workflows/
  a2a-validate.yml, decisions/.gitkeep, vendored/.gitkeep, README.md)
- internal/cli/cmd_doctor.go

## Off-limits (NEVER touch)
- internal/cli/cli.go (lead seam — read only)
- internal/cli/{cmd_init,cmd_new,cmd_submit,cmd_sync,adapters}.go
  (parallel agent — never touch, never reference their symbols)
- cmd/a2a/** (lead wires the doctor dispatch line), go.mod, internal/*
  core packages (consume, never edit)

## What to do
1. space-template/space.yaml: a literal, schema-VALID instance of
   schemas/manifest/v1/space.schema.json — space-id placeholder, schema
   version, participants: [], default gates, notification routes. Must
   validate clean with zero participants (AC-101.1).
2. space-template/CODEOWNERS: gated paths ONLY — /space.yaml → admins
   placeholder team, /decisions/** → all-participants placeholder. NO
   /<system>/provides/** entries (added by onboarding PR, US-102).
3. space-template/.github/workflows/a2a-validate.yml: pull_request→main
   (blocking, changed files) + push→main (flag-only, full repo, never a
   required check); step 1 fetch the PINNED-version validator binary from
   the product repo releases via a read-only token repo secret (pinned,
   not latest); step 2 invoke the binary's V3 entrypoint (behavior
   contract: V2 for changed files + diff-authz + fold integrity +
   supersession linearity + policy/compat 5.4b) — reference the pinned
   binary + behavior, do NOT hardcode a flag string that will drift from
   P6's CLI; step 3 emit machine pass/fail. The emitted status-check name
   MUST be literally `a2a-validate` (§4.2 exact string; AC row 6 asserts
   byte-equality). permissions: least-privilege (contents: read + whatever
   the review query needs).
4. space-template/{decisions,vendored}/.gitkeep, README.md pointing at the
   §9.2 onboarding runbook. Branch-protection checklist: author it as
   space-template/BRANCH-PROTECTION.md (or in README) for P11 — document
   the settings table verbatim from the spec.
5. internal/cli/cmd_doctor.go: a cli.Command running the five OP-218 basic
   checks (credentials present/readable/unexpired; space access — each
   mirror fetchable; versions — local binary vs each space min_binary_
   version; CI presence — default branch carries a2a-validate.yml + the
   required check; statusline wiring presence). One line per check,
   exit 0 iff all pass, non-zero + actionable message otherwise. --space
   rejected with an explicit "v1-min: not available" error.
6. Sanity: gofmt; go vet ./internal/cli/...; go test ./internal/cli/...
   -run Doctor -race -count=1; validate space.yaml against the manifest
   schema (write a small test using internal/schema to prove AC-101.1's
   green-on-empty, OR a doctor/template test — pick the scoped one).

## Constraints
- cli.Command.Run returns an exit code; NEVER os.Exit, NEVER real os.Std*
  — only cli.IO. Actionable one-line-per-check output.
- Copy the P1 error idiom; log-or-return; t.Parallel() (or // reason:);
  coverage floor 70% for cmd_doctor.go.
- space-template/ is a projection of §4.2 — no layout invention; the
  workflow calls the same a2a binary, no CI-side validation logic (D-011).

## DO NOT
- DO NOT commit / run git / run make check / repo-wide go build|test.
  Scope to internal/cli (your doctor file) + a space.yaml schema check.
- DO NOT touch cli.go, any P6 verb file, cmd/a2a, or a core package.

## Acceptance
- Spec 09 §8 rows 1–10 (rows needing a live GitHub repo — AC-101.2 branch
  protection, AC-202.1/.4 CI runs — are proven E2E in P10/P11; here assert
  the template ARTIFACTS: workflow shape, check-name byte-equality,
  CODEOWNERS content, schema-valid space.yaml, doctor behavior).

## Report back
- Files, tests, scoped output.
- cmd_doctor.go constructor signature (cmd/a2a wiring builds it).
- Deviations — REQUIRED (esp. any doctor check that needs a core API that
  doesn't exist yet; any workflow-vs-P6-CLI coupling you had to assume).
- Anything skipped + why (rows deferred to P10/P11 live integration).
```

## Acceptance

- [ ] Spec 09 §8 rows 1–10 green where template-artifact-checkable; live-
      repo rows (AC-101.2, AC-202.*) verified E2E at P10/P11 — noted, not
      skipped silently.

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- cmd/a2a doctor dispatch line = lead, post-wave.
- Live branch-protection + CI-run ACs (101.2, 202.1, 202.4) = P10/P11.
