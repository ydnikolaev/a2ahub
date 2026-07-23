# Branch protection checklist

For the org/space admin runbook (plan §9.1 "org/space admin" profile,
executed at real space creation — P11). This phase documents the settings;
it does not (and cannot) apply them, since that requires a live GitHub repo.
Cite: plan §4.2 write funnel, §10.3 AuthZ matrix.

| Setting | Value | Cite |
|---|---|---|
| Direct pushes to `main` | forbidden for all actors, including admin (bypass reserved for incident recovery, alarmed via F-7) | §4.2 |
| Required status check | `a2a-validate / validate` (compound context — the caller job `a2a-validate` in `.github/workflows/a2a-validate.yml` calls a2ahub's reusable `validate` job; P33 amended spec 09 §4.2 / AC row 6, which named the flat `a2a-validate`) | §4.2 |
| Require branches up to date before merge | OFF (concurrent event PRs must not serialize) | §4.2 |
| Force-push | forbidden | §4.2 |
| Require review from Code Owners | ON, applies only to CODEOWNERS-listed paths (`/space.yaml`, `/decisions/**`, and each system's `/provides/**` once onboarded) | §4.2 |
| Auto-merge (repo setting) | ON — `a2a submit`'s PRs (OP-205) open with auto-merge enabled and merge unattended on green `a2a-validate / validate` for ungated paths | §4.2 |
| Private-repo protections require a paid plan | verified before space creation; `a2a doctor --space` (v2) re-checks it later | §4.5 |

Notes:

- The `a2a-postmerge-audit` job in the same workflow file MUST NEVER be
  added as a required status check (flag-only per §5.5's V3 row) — it runs
  post-merge and never blocks a merge. Its own surfaced context is
  `a2a-postmerge-audit / validate`, distinct from the gate's.
- No repo secret is required (P33): a2ahub is public and the reusable
  workflow acquires the validator via `go run …@<ver>` (Go checksum DB), so
  the pre-P33 `A2A_BINARY_FETCH_TOKEN` secret is gone. Set nothing.
- Migrating a pre-P33 space: the required check RENAMES from the flat
  `a2a-validate` to `a2a-validate / validate` — update the branch-protection
  rule in the same change that swaps the workflow to the caller, or PRs hang
  "Expected — Waiting for status to be reported" (spec 33 §3, the getvisa
  migration).
