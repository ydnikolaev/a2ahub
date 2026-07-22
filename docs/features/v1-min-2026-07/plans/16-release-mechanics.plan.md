---
slug: v1-min-2026-07
phase: P16
spec: ../specs/16-release-mechanics.md
wave: 9
status: closed
---

# Phase plan — P16 Release mechanics

## Goal

Ship `.github/workflows/release.yml`: on a `v*` tag, cross-build `a2a` for the
first-class targets and publish a GitHub release whose `a2a-linux-amd64` asset
the getvisa space CI gate fetches — unblocking P11 B1. Signing (D-013) deferred.

## Placement / decisions (lead)

- **Lead-inline** (no code-wave): a single CI workflow file, judgment is in the
  asset-name contract + the safe env-var pattern for tag/sha — done directly.
- **Asset name `a2a-<os>-<arch>` is a behavior contract** with
  space-template's a2a-validate `--pattern 'a2a-linux-amd64'`. Fenced by comment.
- **`gh` CLI, no third-party release action** (framework-first; same tool the
  space gate uses to download).
- **Signing is a marked placeholder**, per operator ("подпись — отдельное
  решение") — deferred to backlog, releases UNSIGNED until wired.
- Safe workflow-injection pattern: tag/sha via `env:` + quoted `"$VAR"`, never
  `${{ }}` interpolated into the run script.

## Allowlist

- `.github/workflows/release.yml`

## Acceptance (spec 16 §8)

- [x] Workflow cross-builds 4 targets + publishes release with `a2a-<os>-<arch>` + SHA256SUMS.
- [x] `a2a-linux-amd64` name matches the space gate's fetch pattern.
- [x] ldflags inject tag version + commit sha.

## Phase log

### Wave 9 — 2026-07-22 (lead-inline)

- Author: lead-inline (single CI file). Verified: YAML parses; the exact build
  line (linux/amd64 + darwin/arm64, `-trimpath -ldflags "-s -w -X main.version
  … -X main.commit …"`, CGO off) builds a clean 8.4M static binary locally.
- Files / Commits: `.github/workflows/release.yml` / (this commit).
- Deviations: signing (D-013) NOT wired — marked placeholder, operator decision,
  backlog row added. End-to-end (a real tag) is operator-triggered; build steps
  locally verified, `gh release create` is standard.
- Notes: unblocks P11 B1 once the operator pushes a `v0.1.0` tag. Orthogonal to
  the public/private decision.

## Deferred / follow-ups

- Signing (D-013 cosign/minisign, public key pinned, `a2a update` verify) — backlog.
