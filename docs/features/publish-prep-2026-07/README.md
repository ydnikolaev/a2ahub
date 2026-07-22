---
slug: publish-prep-2026-07
title: Publication prep — make a2ahub a public, hardened, cross-platform Go binary
kind: epic
status: draft
owner: yura
created: 2026-07-22
updated: 2026-07-22
related: []
---

# Publication prep — a2ahub goes public

## Goal

Turn `ydnikolaev/a2ahub` (currently PRIVATE, harness + product + planning in
one tree and in git history) into a PUBLIC, hardened, cross-platform Go-binary
repository modeled 1:1 on the sibling `ydnikolaev/sporo` — so both repos in the
operator's account run the same publish model. The public repo tracks only the
`a2a` product; the mate-synced harness and project planning are hidden behind a
sporo-style publish boundary, and the binary ships with full release,
cross-platform, supply-chain, and vulnerability hardening.

## Why

The operator wants `a2a` (the binary) and the getvisa contract space public,
but NOT the agent harness (`.claude/.mate/.agents/.codex/AGENTS.md`) or the
project planning (`docs/the-plan`, `docs/features`, trackers). Sporo already
solves this exact problem — a public Go binary with a gitignore + classify-guard
publish boundary and a goreleaser/cosign/SBOM/SLSA + govulncheck/gitleaks/CodeQL
hardening stack. This epic ports sporo's proven model. Load-bearing constraint:
a2ahub's git HISTORY contains the harness (bootstrap `727309b` onward), which
classify-guard (a working-tree gate) does not cover — so publication requires
history remediation, not just a visibility flip.

## Scope

**In:**
- Sporo-style publish boundary: `.gitignore` block + `scripts/classify-guard.sh`
  (local) + `.github/workflows/classify-guard.yml` (CI backstop).
- Product/harness Makefile + CI split so the public `make check` runs only
  product gates (harness gates feature-lint/epic-drift stay private).
- Cross-platform release via goreleaser v2 (darwin/linux/windows × amd64/arm64,
  `CGO_ENABLED=0`, `-trimpath` + version ldflags) with checksums, cosign keyless
  signing, syft SBOM, and SLSA build provenance.
- Security CI: govulncheck (allowlist gate), gitleaks, CodeQL, workflow SHA-pin
  gate.
- Lint + coverage hardening toward sporo's `.golangci.yml` (keeping a2ahub's
  `-race` and 70% floor) + a coverage-policy SSOT shared by local + CI.
- `scripts/install.sh` (checksum-verified), `LICENSE` = Apache-2.0 (+ NOTICE),
  public `README.md`, `SECURITY.md`, dependabot.
- History remediation (re-root to a clean product-only history; full dev history
  preserved privately), harness/planning private delivery, and the guarded
  public flip + first release.

**Out (explicit non-goals):**
- The getvisa contract space bootstrap (that is v1-min P11).
- Any change to the `a2a` binary's behavior/features (this is packaging, not
  product code — the product is frozen as shipped by the v1-min epic).
- Homebrew tap / self-update / a docs website (sporo has these; deferred).
- Making the harness itself public in any form.

## Approach

Six phases. The boundary + release + security + hardening + meta are built and
verified while the repo stays PRIVATE (fully reversible), then P6 performs the
one-way, user-approved history remediation and public flip. No irreversible git
operation or visibility change happens without an explicit, user-approved plan.
Sporo (`/Users/yuranikolaev/Developer/projects/sporo`) is the reference for
every artifact; each spec cites the concrete sporo path to port.

## Phases

| Phase | Spec | Outcome |
|---|---|---|
| P1 | [specs/01-publish-boundary.md](specs/01-publish-boundary.md) | Publish boundary gate + product/harness Makefile & CI split |
| P2 | [specs/02-release-crossplatform.md](specs/02-release-crossplatform.md) | goreleaser v2 6-target build + release workflow (cosign/SBOM/SLSA) |
| P3 | [specs/03-security-ci.md](specs/03-security-ci.md) | govulncheck + gitleaks + CodeQL + workflow SHA-pin gate |
| P4 | [specs/04-lint-coverage.md](specs/04-lint-coverage.md) | golangci-lint hardening + coverage-policy SSOT |
| P5 | [specs/05-install-meta.md](specs/05-install-meta.md) | install.sh + Apache-2.0 LICENSE + README + SECURITY.md + dependabot |
| P6 | [specs/06-history-flip.md](specs/06-history-flip.md) | History re-root + private delivery + guarded public flip + first release |

## Acceptance criteria

- [ ] A public checkout of the repo contains ZERO harness/planning paths
      (`.claude/.mate/.agents/.codex/AGENTS.md/docs/{the-plan,features,…}`) — in
      the working tree AND in the entire git history — enforced by classify-guard
      locally and in CI.
- [ ] `goreleaser release --snapshot --clean` produces 6 platform archives +
      `checksums.txt` + cosign signature + SBOM locally without error.
- [ ] The release workflow (on a `v*` tag) publishes signed, checksummed,
      SBOM'd, provenance-attested archives to GitHub Releases.
- [ ] `govulncheck`, `gitleaks`, and CodeQL run in CI; a new vuln reds the build,
      an allowlisted one stays green; every workflow `uses:` is SHA-pinned.
- [ ] The product `make check` (public) passes with NO harness gate present;
      `-race` and the 70% coverage floor are retained.
- [ ] `scripts/install.sh` refuses to install on a checksum mismatch.
- [ ] `LICENSE` (Apache-2.0), `README.md`, `SECURITY.md` present and accurate.
- [ ] Full pre-publication dev history is preserved in a private archive; the
      public history starts clean.

## Verification

Local: `goreleaser check`, `goreleaser release --snapshot --clean`,
`bash scripts/classify-guard.sh`, `make check` (product lane), `govulncheck ./...`,
`gitleaks detect`, `golangci-lint run`. CI: the workflow set green on a PR + a
dry-run tag. Publication: a fresh `git clone` of the public repo grep'd for any
harness path returns nothing; `git log --all --name-only | grep -E '(^|/)(\.claude|\.mate|\.agents|AGENTS\.md)'`
returns nothing.

## Open questions

- **Harness/planning private delivery mechanism** (P6): mate-synced harness
  (`.claude/.agents/.mate/AGENTS.md`) is re-deliverable via `mate pull`, so it
  can be gitignored and re-pulled. Project planning (`docs/the-plan`,
  `docs/features`, trackers) is NOT mate-synced — it needs a private home
  (recommended default: a private archive repo, e.g. keep the pre-flip history
  there). P6 pins the exact mechanism after a deeper `.mate/` capability check.
- **History remediation technique** (P6): re-root to a single clean initial
  commit (recommended — simplest, product-only) vs `git filter-repo` path-strip
  (keeps granular history but rewrites every SHA and still must drop planning).
  Pinned in P6's plan, user-approved before execution.
- **Cosign/SBOM signing identity**: keyless Sigstore OIDC (sporo's choice, no key
  management) — confirm at P2.
