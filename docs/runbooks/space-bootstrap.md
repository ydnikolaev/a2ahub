# Runbook — getvisa space bootstrap (P11, L0)

> P11 footprint (spec 11 §Footprint). Captures the exact steps to stand up the
> real `getvisa` space; seeds plan §9 for v2. Status as of 2026-07-22:
> **execution-ready but BLOCKED on three operator prerequisites** (below) — the
> live bootstrap has NOT been run; nothing has been pushed to the space repo.

## Target

- **Space repo:** `r22d222/a2a` (org `r22d222`, private). Confirmed 2026-07-22
  by the operator as the getvisa space (the name encodes the *meaning* — a space
  where a2a agents exchange — not the service). Currently EMPTY.
- **Participants (D-007):** `axon` (owner `ydnikolaev`) + `seomatrix` (owner
  `xpressmike` / Misha). Both are already admin collaborators on the repo.

## BLOCKERS — resolve before running the bootstrap (all operator-only)

| # | Blocker | Evidence | Remediation |
|---|---------|----------|-------------|
| B1 | **No released validator binary.** The CI gate `a2a-validate` fetches a PINNED `v0.1.0` `a2a-linux-amd64` release asset from `ydnikolaev/a2ahub`; `gh release list` is empty. Without it the gate fails loudly (by design, §9 "token/binary missing → CI fails loudly"). | `gh release list --repo ydnikolaev/a2ahub` → empty | Cut a signed `v0.1.0` release (D-013: cosign/minisign, public key pinned) with linux/darwin assets. This is a distinct release step (§7.3), not part of P11 — P11 *consumes* it. |
| B2 | **Branch protection unavailable on the private repo.** The org/plan does not permit protected branches on a private repo. Without protection the write funnel's PR-only guarantee (§4.2) cannot hold, so L0 exit is not reachable. | `gh api repos/r22d222/a2a/branches/main/protection` → `403: Upgrade to GitHub Pro or make this repository public` | Upgrade the `r22d222` org to GitHub Team (§4.5 prereq), **or** make the space repo public (ties into the `publish-prep-2026-07` epic — a public space is the simpler path and may be the intended one). |
| B3 | **Missing `A2A_BINARY_FETCH_TOKEN` repo secret.** The CI gate needs a read-only PAT to fetch the pinned binary from the private product repo (§10.5). | not set on `r22d222/a2a` | Create a fine-grained read-only PAT scoped to `ydnikolaev/a2ahub` release-read; set it as the `A2A_BINARY_FETCH_TOKEN` repo secret on `r22d222/a2a`. (Moot if the product repo goes public.) |

> B2+B3 both dissolve if the product repo + space go **public** — which the
> sibling `publish-prep-2026-07` epic already plans. Recommended sequencing:
> resolve publish-prep (public repos) → cut the v0.1.0 release (B1) → run this
> runbook. Confirm the write-identity decision below at the same time.

## Open operator decision — write identity

For L0, the simplest is **personal accounts** as the write identity: `ydnikolaev`
writes as `axon`, `xpressmike` writes as `seomatrix` (matches the `owners` below;
the `github-login → system` authz map is `space.yaml`'s `owners`, enforced by V3
diff-authz). Machine/bot accounts + their PATs (§10.5) are a hardening step, not
required for L0. **This runbook assumes personal accounts** unless changed.

## Ready-to-push content

### `space.yaml` (at repo root)
```yaml
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
gates: default
participants:
  - system: axon
    org: r22d222
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-22
  - system: seomatrix
    org: r22d222
    section: seomatrix/
    owners: [xpressmike]
    status: active
    joined: 2026-07-22
vendored: []
```

### `.github/CODEOWNERS`
```
# Gated paths (§4.2, D-011). Owners are the github-login → system map's humans.
/space.yaml            @ydnikolaev
/decisions/**          @ydnikolaev @xpressmike
/axon/provides/**      @ydnikolaev
/seomatrix/provides/** @xpressmike
```

### Section scaffold (per §4.2, T3) — one per system
```
axon/provides/.gitkeep       axon/requires/.gitkeep       axon/exchanges/.gitkeep
axon/events/.gitkeep         axon/docs/.gitkeep           axon/consumes.yaml   (empty: "consumes: []")
seomatrix/provides/.gitkeep  seomatrix/requires/.gitkeep  seomatrix/exchanges/.gitkeep
seomatrix/events/.gitkeep    seomatrix/docs/.gitkeep      seomatrix/consumes.yaml
decisions/.gitkeep           vendored/.gitkeep
```

### `.github/workflows/a2a-validate.yml`
Copy verbatim from `space-template/.github/workflows/a2a-validate.yml` (unchanged).

## Bootstrap sequence (run ONLY after B1–B3 cleared)

```bash
# 1. clone the (empty) space repo
git clone git@github.com:r22d222/a2a.git getvisa-space && cd getvisa-space

# 2. lay down content: space.yaml + CODEOWNERS + sections + workflow (above)
#    (copy from space-template/, then overwrite space.yaml/CODEOWNERS with the
#     filled versions above and create the two section trees)

# 3. first commit DIRECT to main (before protection — else the empty repo
#    can't accept its own bootstrap)
git add -A && git commit -m "chore(getvisa): bootstrap space — axon + seomatrix (L0)"
git push -u origin main

# 4. set the A2A_BINARY_FETCH_TOKEN repo secret (B3)
gh secret set A2A_BINARY_FETCH_TOKEN --repo r22d222/a2a --body "<read-only PAT>"

# 5. arm branch protection (B2 — needs Team plan or a public repo). Settings per
#    space-template/BRANCH-PROTECTION.md: PR-only main, required check
#    `a2a-validate`, no force-push, CODEOWNERS review on, auto-merge on,
#    "up to date before merge" OFF. Do this ONLY after v0.1.0 is released
#    (B1) — arming a required check the CI can't pass bricks all merges.

# 6. verify locally (as axon)
a2a init && a2a connect git@github.com:r22d222/a2a.git && a2a doctor   # want green
#    interpret any red per skill/a2ahub/troubleshooting.md
```

## L0 exit checklist (AC-101/AC-102)

- [ ] space.yaml valid, 2 participants, CI green on the empty/bootstrapped space
- [ ] branch protection armed with `a2a-validate` required (B2)
- [ ] a test PR from each system auto-merges on green; a direct push to main is rejected
- [ ] `a2a doctor` green for both axon and seomatrix
- [ ] this runbook updated with the ACTUAL executed steps + any deviation

## Status log

- 2026-07-22 — runbook authored (execution-ready). Live bootstrap NOT run —
  blocked on B1 (no release), B2 (no branch-protection plan), B3 (no fetch
  token). Surfaced to operator; awaiting the three prerequisites (recommended
  path: publish-prep → release v0.1.0 → run this runbook).
