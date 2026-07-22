# P2 — Cross-platform release (goreleaser v2) — Specification

**Slug**: `publish-prep-2026-07`  ·  **Track**: ci  ·  **Status**: draft
**Plan**: (created at dispatch)
**Created**: 2026-07-22  ·  **Owner**: yura
**Footprint**: `.goreleaser.yaml` (new), `.github/workflows/release.yml` (new), `cmd/a2a/main.go` (version-ldflags reconcile ONLY — no behavior change). Reference: sporo `.goreleaser.yaml`, `.github/workflows/release.yml`, `cmd/sporo/main.go:32-35`.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-1 | As a user on macOS/Linux/Windows (amd64 or arm64), I download a prebuilt `a2a` archive for my platform from GitHub Releases. |
| US-2 | As a security-conscious user, each release carries a checksum file, a cosign signature, an SBOM, and SLSA build provenance I can verify. |
| US-3 | As the operator, tagging `vX.Y.Z` triggers the whole release with no manual build steps. |
| US-4 | As a user, `a2a version` prints the real released version + commit, not `dev`. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Sporo release stack | `/Users/yuranikolaev/Developer/projects/sporo/.goreleaser.yaml`, `.github/workflows/release.yml` | port builds/archives/checksum/signs/sboms/changelog + the tag-trigger cosign/syft/attest step sequence wholesale |
| a2ahub version stamp | [cmd/a2a/main.go](../../../../cmd/a2a/main.go) | `versionStamp()` already reads `main.version`/`main.commit`; goreleaser must inject `-X main.version=` (and commit) to match |
| Go build hardening | goreleaser `builds` docs | `CGO_ENABLED=0` static, `-trimpath` (a2ahub ADDS this; sporo lacks it), `-s -w` strip |

---

## T5. CI pipeline (track: ci)

| Artifact | Purpose | Contract |
|----------|---------|----------|
| `.goreleaser.yaml` (v2) | cross-platform build + package + sign | `builds`: one build, `main: ./cmd/a2a`, `binary: a2a`, `env: [CGO_ENABLED=0]`, `goos: [darwin,linux,windows]`, `goarch: [amd64,arm64]` (6 targets), `flags: [-trimpath]`, `ldflags: [-s -w -X main.version={{.Version}} -X main.commit={{.Commit}}]`. `archives`: tar.gz (+ zip on windows). `checksum`: `checksums.txt` (stable name). `signs`: cosign keyless (Sigstore OIDC) over `checksums.txt`. `sboms`: syft, archive artifacts. `changelog`: git-based, grouped. `release.github` header table telling users which asset to grab. |
| `.github/workflows/release.yml` | run the release on tag | trigger `push.tags: ['v*']`; SHA-pinned actions; steps: checkout (`fetch-depth: 0`) → setup-go → cosign-installer → syft download → `goreleaser release --clean` → `actions/attest-build-provenance` over `dist/checksums.txt`. `permissions: {contents: write, id-token: write, attestations: write}`. |
| `cmd/a2a/main.go` reconcile | version injection | confirm `var version`/`var commit` names match the ldflags `-X` targets; keep the existing `versionStamp()` output shape; a plain `go build` still prints a sane default. NO other change. |

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Sporo `.goreleaser.yaml` — copy wholesale, change `project_name`/`main`/`binary`/owner-repo/asset table; ADD `-trimpath` to `flags`.
- [ ] Sporo `release.yml` step sequence + SHA-pins — copy, update the repo slug.
- [ ] a2ahub's existing `versionStamp()`/`vcsRevision()` — do not replace; just ensure the ldflags targets line up.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| goreleaser config validity | `goreleaser check` passes | schema v2 conformance |
| local snapshot build | `goreleaser release --snapshot --clean` produces 6 archives + `checksums.txt` + SBOMs | windows archive is `.zip` |
| version injection | a snapshot-built binary's `a2a version` prints the injected version, not `dev` | plain `go build` still prints a non-empty stamp |
| release workflow shape | on a dry-run `v0.0.0-rc` tag (in the private repo), the workflow reaches goreleaser without a permissions/SHA-pin error | attest step present |

## 7. Schema / contract delta

None (packaging only; no product schema or CLI-behavior change).

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1 | `.goreleaser.yaml` builds all 6 GOOS/GOARCH targets, static (`CGO_ENABLED=0`), `-trimpath` | `goreleaser release --snapshot --clean`; `dist/` has 6 archives |
| 2 | US-2 | checksum + cosign sign + syft SBOM sections present and produce artifacts in snapshot | inspect `dist/` for `checksums.txt`, `*.sigstore.json` (sign runs in CI, snapshot may skip), SBOMs |
| 3 | US-2 | `release.yml` includes `attest-build-provenance` over `dist/checksums.txt` with `id-token: write` + `attestations: write` | inspect the workflow |
| 4 | US-3/US-4 | tag `v*` triggers the workflow; ldflags inject the version so `a2a version` ≠ `dev` | dry-run tag in the private repo; version check on the built binary |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | a new platform = one `goarch`/`goos` entry; Homebrew/self-update are additive later (out of scope). |
| Coupling | soft — goreleaser reads the repo + `main.version`; no coupling to product internals. |
| Migration path | the release path is CI+goreleaser, independent of the dev Makefile. |

## 10. Implementor entry point

`blocked_by: []` (parallel with P1). Port sporo's goreleaser + release workflow; add `-trimpath`; reconcile the version ldflags with a2ahub's `main.version`/`main.commit`. Verify with `goreleaser check` + `--snapshot`. Runs entirely in the PRIVATE repo — no publication here.

## 11. Amendments

> Append-only.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
