# P16 — Release mechanics (v0.1.0 build + publish workflow) — Specification

> Unblocks P11 B1 (the space CI gate fetches a pinned released binary that did
> not exist). Ships the release WORKFLOW; cutting an actual release is an
> operator act (push a `v*` tag). Signing (D-013) is a separate operator
> decision, deferred (backlog).

**Slug**: `v1-min-2026-07`  ·  **Track**: harness  ·  **Status**: draft
**Created**: 2026-07-22  ·  **Owner**: yura
**Plan**: [plans/16-release-mechanics.plan.md](../plans/16-release-mechanics.plan.md)
**Footprint**: `.github/workflows/release.yml` (this product repo) — the only
artifact. **May import**: none — CI config, no Go code.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-P16-1 | (OP) As the operator, pushing a `vX.Y.Z` tag builds the `a2a` binary for the first-class targets and publishes a GitHub release, so the space CI gate and `a2a update` have a pinned binary to fetch. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Distribution + self-update | [07-client.md](../../../the-plan/plan/07-client.md) §7.3 (D-013) | signed releases, darwin/arm64 + linux/amd64 first, `a2a update` verifies before swap |
| The asset-name contract | `space-template/.github/workflows/a2a-validate.yml` | the space gate does `gh release download --pattern 'a2a-linux-amd64'` — asset name is a behavior contract |
| Version stamp seam | `cmd/a2a/main.go` | `-ldflags "-X main.version=… -X main.commit=…"` — the workflow injects the tag + sha |

---

## T1. The release workflow (track: harness)

| Trigger | Steps | Output |
|---|---|---|
| push tag `v*.*.*` | (1) setup Go from go.mod; (2) cross-build `a2a` for linux/amd64, linux/arm64, darwin/arm64, darwin/amd64 (CGO off, `-trimpath`, `-ldflags "-s -w -X main.version=<tag> -X main.commit=<sha>"`); (3) `sha256sum` manifest; (4) `gh release create <tag>` with the four `a2a-<os>-<arch>` assets + SHA256SUMS | a GitHub release whose `a2a-linux-amd64` asset the space CI gate fetches verbatim |

## 2. Signing (D-013) — deferred, not in this phase

The workflow carries a MARKED signing placeholder (cosign/minisign). Until an
operator decision wires it, releases are UNSIGNED and `a2a update`'s signature
check (OP-217) must not be relied upon. Tracked in docs/backlog.md.

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `gh` CLI for the release (same tool `space-template`'s a2a-validate uses
      to *download*) — no third-party release action.
- [ ] The `main.version`/`main.commit` ldflags seam already in `cmd/a2a/main.go`
      (§7.3) — the workflow injects into it, adds no new stamping mechanism.
- [ ] Asset naming `a2a-<os>-<arch>` matches the space gate's fetch pattern —
      one contract, not a second convention.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| Cross-build | each target compiles (CGO off, static) | verified locally pre-commit (linux/amd64 + darwin/arm64 built clean) |
| Asset-name contract | the produced `a2a-linux-amd64` name equals the space gate's `--pattern` | a rename here silently breaks the space gate — the workflow comment fences it |
| YAML validity | release.yml parses | — |

> End-to-end (an actual tagged release) is operator-triggered and cannot be run
> without publishing — the build steps are locally verified; the `gh release
> create` step is standard.

## 7. Schema / contract delta

None. CI config only; no schema, no Go package.

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-P16-1 | On a `v*` tag, the workflow cross-builds the four targets and publishes a release with `a2a-<os>-<arch>` assets + SHA256SUMS. | read release.yml; the four build targets + `gh release create` with the exact asset names present. |
| 2 | — | The `a2a-linux-amd64` asset name equals the space gate's fetch pattern (behavior contract). | grep `space-template/.github/workflows/a2a-validate.yml` for `a2a-linux-amd64`; matches release.yml's output name. |
| 3 | — | Build flags inject the tag version + commit sha into the `main.version`/`main.commit` stamp. | release.yml ldflags reference `${VERSION}`/`${COMMIT}` from `github.ref_name`/`github.sha`. |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Signing | the placeholder step is where D-013 cosign/minisign lands; no restructure needed. |
| More targets | add a target to the build loop; asset naming already generic. |
| Public/private | orthogonal — the workflow builds+publishes regardless; the space gate's *fetch* (token vs tokenless) is what public/private changes, not this. |

## 10. Implementor entry point

`.github/workflows/release.yml` is shipped. To cut the first release: push a
`v0.1.0` tag on this repo. Wire signing (D-013) before relying on `a2a update`
verification. Full loop: [docs/features/README.md](../../features/README.md).

## 11. Amendments

> Append-only.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
