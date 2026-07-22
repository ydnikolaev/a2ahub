# P5 — Install script + public meta files — Specification

**Slug**: `publish-prep-2026-07`  ·  **Track**: docs  ·  **Status**: draft
**Plan**: (created at dispatch)
**Created**: 2026-07-22  ·  **Owner**: yura
**Footprint**: `scripts/install.sh` (new — the ONE product script, allowlisted past the boundary), `LICENSE` (new, Apache-2.0), `NOTICE` (new), `README.md` (new, public-facing), `SECURITY.md` (new), `.github/dependabot.yml` (new). Reference: sporo `scripts/install.sh:125-141`, `SECURITY.md`, `README.md`, `LICENSE`, `.github/dependabot.yml`.

---

## 0. User stories

| ID | User story |
|----|------------|
| US-1 | As a user, `curl … | sh` downloads the right platform archive and REFUSES to install if the SHA256 does not match `checksums.txt`. |
| US-2 | As a user, a public `README.md` tells me what `a2a` is, how to install, basic usage, and how to verify the release. |
| US-3 | As a security researcher, `SECURITY.md` gives me a private disclosure channel + the exact cosign/attestation verify commands. |
| US-4 | As the operator, the project is Apache-2.0 licensed (with NOTICE), and dependabot keeps deps + actions current. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Sporo install.sh | `/Users/yuranikolaev/Developer/projects/sporo/scripts/install.sh` (esp. :125-141) | the checksum-verify block: fetch `checksums.txt`, `grep` the asset's line, compare `sha256sum`/`shasum`, `die` on mismatch |
| Sporo meta | `/Users/yuranikolaev/Developer/projects/sporo/{SECURITY.md,README.md,.github/dependabot.yml}` | disclosure SLA + verify commands; dependabot gomod+github-actions weekly grouped |
| Apache-2.0 | the canonical LICENSE text + NOTICE convention | full license text verbatim; NOTICE names the project + copyright |
| a2a product surface | [docs/features/v1-min-2026-07/README.md](../../v1-min-2026-07/README.md) | what the binary does — the README's feature list projects from here (do not overclaim beyond shipped v1-min) |

---

## T4. Docs changes (track: docs)

| Doc | Section | Change |
|-----|---------|--------|
| `scripts/install.sh` | new | POSIX `sh` installer: detect OS/arch → resolve the GitHub Release asset → download + `checksums.txt` → SHA256 verify (fail-closed) → extract → install to a PATH dir. Allowlisted in the publish boundary (`!scripts/install.sh`). |
| `LICENSE` | new | Apache License 2.0, full text. |
| `NOTICE` | new | project name + copyright holder + year. |
| `README.md` | new | public-facing: what a2ahub/`a2a` is (agent-to-agent contracts exchange), install (`curl`/`go install`/release archives), quick usage (a few verbs), verification (checksum/cosign/attestation), license. NO harness/planning references. |
| `SECURITY.md` | new | supported-versions policy, private-advisory disclosure flow + SLA, and the `gh attestation verify` + `cosign verify-blob` commands for the release. |
| `.github/dependabot.yml` | new | ecosystems: `gomod` (root) + `github-actions`, weekly, grouped minor/patch. |

## 5. Existing patterns to reuse (anti-duplication)

- [ ] Sporo `install.sh` checksum-verify block — port near-verbatim; adjust asset naming to a2ahub's goreleaser output (P2).
- [ ] Sporo `SECURITY.md` verify commands — port, update the repo slug + workflow-identity regexp.
- [ ] Sporo `dependabot.yml` — port, drop the `npm`/`web` ecosystem (a2ahub has no web dir).

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| install.sh checksum | a tampered archive (wrong hash) → the script `die`s, does not install | missing `sha256sum` → falls back to `shasum -a 256` |
| install.sh platform | os/arch detection resolves the correct asset name matching P2's goreleaser archive template | windows path (or documented as unsupported by the shell installer) |
| README accuracy | every command/claim in the README matches a shipped v1-min verb (no overclaim) | — |
| meta presence | `LICENSE`, `NOTICE`, `SECURITY.md`, `dependabot.yml` present and well-formed | dependabot YAML parses |

## 7. Schema / contract delta

None (docs + shell installer).

## 8. Acceptance criteria

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-1 | `scripts/install.sh` verifies SHA256 against `checksums.txt` and refuses on mismatch | feed it a mismatched checksum in a local harness → it `die`s |
| 2 | US-2 | `README.md` describes `a2a`, install, usage, verification — no harness references, no overclaim beyond shipped verbs | read it; grep for any `.claude`/`.mate`/harness mention → none |
| 3 | US-3 | `SECURITY.md` carries the disclosure flow + the cosign/attestation verify commands matching P2's release | read it; the commands reference the real workflow identity |
| 4 | US-4 | `LICENSE` = Apache-2.0 + `NOTICE`; `.github/dependabot.yml` covers gomod + github-actions | inspect files |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | README/SECURITY grow with features; install.sh absorbs new platforms via P2's archive naming. |
| Coupling | install.sh depends on P2's asset names + `checksums.txt` — the only cross-phase coupling (hence `blocked_by: [P2]`). |
| Migration path | Homebrew/self-update are additive later (out of scope). |

## 10. Implementor entry point

`blocked_by: [P2]` (install.sh + SECURITY reference the release checksums/attestation P2 defines). Port sporo's install.sh + SECURITY + dependabot; author the Apache-2.0 LICENSE/NOTICE + a public README from the v1-min surface (no overclaim, no harness). Private repo only.

## 11. Amendments

> Append-only.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
