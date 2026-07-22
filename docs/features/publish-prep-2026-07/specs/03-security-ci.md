# P3 — Security & supply-chain CI — Specification

**Slug**: `publish-prep-2026-07`  ·  **Track**: ci  ·  **Status**: draft
**Plan**: (created at dispatch)
**Created**: 2026-07-22  ·  **Owner**: yura
**Footprint**: `.github/workflows/govulncheck.yml` (new), `.govulncheck-allow.txt` (new), `.github/workflows/gitleaks.yml` (new), `.gitleaks.toml` (new), `.github/workflows/codeql.yml` (new), plus a `workflow-lint` gate (Makefile target + a small script) that asserts every workflow `uses:` is SHA-pinned. Reference: sporo `.github/workflows/{govulncheck,gitleaks,codeql}.yml`, `.govulncheck-allow.txt`, `.gitleaks.toml`, `Makefile:81-85`.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-1 | As the operator, a newly-disclosed vulnerability in a dependency reds the build, but a triaged-unfixable one stays green (allowlist, never silent suppression). |
| US-2 | As the operator, a committed secret is caught by gitleaks on every PR and push, across full history. |
| US-3 | As the operator, CodeQL static analysis runs on the Go code on a schedule + PRs. |
| US-4 | As the operator, every GitHub Action `uses:` is pinned to a 40-hex commit SHA, so a tag-hijack supply-chain attack on an Action cannot land. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Sporo security workflows | `/Users/yuranikolaev/Developer/projects/sporo/.github/workflows/{govulncheck,gitleaks,codeql}.yml`, `.govulncheck-allow.txt`, `.gitleaks.toml` | port the allowlist-gate design (new vuln = red, accepted = green), gitleaks full-history, CodeQL init/autobuild/analyze |
| Sporo SHA-pin gate | `/Users/yuranikolaev/Developer/projects/sporo/Makefile:81-85` | `make workflow-lint`: grep every `uses:` for a 40-hex pin + trailing `# vX.Y.Z`, plus `actionlint` |
| gosec placement | `/Users/yuranikolaev/Developer/projects/sporo/.golangci.yml:25` | gosec runs INSIDE golangci-lint (P4), not a standalone step — do not duplicate |

---

## T5. CI pipeline (track: ci)

| Artifact | Purpose | Contract |
|----------|---------|----------|
| `.github/workflows/govulncheck.yml` + `.govulncheck-allow.txt` | vuln gate | daily cron + PRs touching `**.go`/`go.mod`/`go.sum`/the allowlist/the workflow + `workflow_dispatch`; runs `govulncheck ./...`, extracts `GO-YYYY-NNNN` ids, reds on any id NOT in the allowlist; the allowlist file carries one `GO-…` per line with an inline justification. |
| `.github/workflows/gitleaks.yml` + `.gitleaks.toml` | secret scan | PR + push to main, `fetch-depth: 0` (full history); `.gitleaks.toml` allowlists only known-public non-secrets with a reason. |
| `.github/workflows/codeql.yml` | SAST | push/PR to main + weekly cron; `github/codeql-action` init/autobuild/analyze for Go; `GOWORK=off`. |
| `workflow-lint` gate | supply-chain pin | a Makefile target + script: every `uses:` across `.github/workflows/**` is `owner/action@<40-hex>` (+ `# vX.Y.Z` comment); `actionlint` syntax check. Wired into the product `make check`. |

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Sporo's `make vulncheck` allowlist-diff logic (extract ids, diff against `.govulncheck-allow.txt`, red on new) — port near-verbatim.
- [ ] Sporo's gitleaks + CodeQL workflows — port, update the repo slug + Go version.
- [ ] Sporo's `workflow-lint` grep — port; it also enforces the SHA-pins THIS phase's own new workflows must carry.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| govulncheck gate | an allowlisted id stays green; a non-allowlisted id reds | empty allowlist + a real vuln → red |
| gitleaks | a planted fake secret in a throwaway commit is caught | an allowlisted public id passes |
| workflow-lint | an unpinned `uses: foo/bar@v1` reds; a 40-hex pin passes | every new workflow in this epic passes its own gate |

## 7. Schema / contract delta

None (CI only).

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1 | govulncheck workflow + allowlist gate: new vuln reds, allowlisted stays green | inspect `make vulncheck`-equivalent logic; a planted non-allowlisted id fails |
| 2 | US-2 | gitleaks workflow scans full history on PR+push | inspect `fetch-depth: 0`; a planted secret is caught |
| 3 | US-3 | CodeQL workflow analyzes Go on PR + schedule | inspect the workflow |
| 4 | US-4 | `workflow-lint` asserts every `uses:` SHA-pinned; wired into product `make check` | an unpinned action reds the gate |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | a new accepted vuln = one allowlist line + reason; a new workflow inherits the SHA-pin gate automatically. |
| Coupling | soft — these are independent CI jobs; gosec lives in P4's golangci config, not duplicated here. |
| Migration path | additive to the product CI P1 establishes. |

## 10. Implementor entry point

`blocked_by: [P1]` (uses the product-CI structure P1 sets). Port sporo's three security workflows + the allowlist + the SHA-pin gate. All new `uses:` MUST be SHA-pinned (dogfood the gate). Private repo only.

## 11. Amendments

> Append-only.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
