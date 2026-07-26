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
| Required approving review count | `0` — any positive value applies to EVERY PR regardless of path, which blocks every legal artifact PR and defeats the auto-merge row further below | spec 42 §T3 |
| Require review from Code Owners | ON (`require_code_owner_reviews: true`), applies only to CODEOWNERS-listed paths (`/space.yaml`, `/decisions/**`, and each system's `/provides/**` once onboarded) — this is the whole of the G4 safety argument, and production runs it as the only thing gating G4 | §4.2, spec 42 §T3 |
| Enforce admins | OFF (`enforce_admins: false`) — a sole code owner must be able to merge their own `space.yaml` edits; this is a DIFFERENT setting from "Direct pushes to `main`" above (that one blocks `git push` outright for every actor; this one is whether an admin may bypass PR requirements like reviews/checks when merging) — stated with its cost, not silently: an admin PR can merge without the review this table otherwise requires | spec 36 §T6-a, spec 42 §T3 |
| Auto-merge (repo setting) | ON (`allow_auto_merge: true`) — `a2a submit`'s PRs (OP-205) open with auto-merge enabled and merge unattended on green `a2a-validate / validate` for ungated paths; GitHub ships this OFF by default on every newly created repo, and `a2a doctor` FAILs the "auto-merge enabled" check when it is off | §4.2, spec 42 §T3, spec 45 §T1 |
| Delete branch on merge (repo setting) | ON (`delete_branch_on_merge: true`) — every write opens an ephemeral `a2a/<system>/<verb>/<id>` branch, so without this a busy space accumulates one dead branch per write forever | spec 36 reset.sh |
| Branch deletion | forbidden (`allow_deletions: false`) — the counterpart to the force-push row: protection that blocks rewriting `main` but permits deleting it is not protection | spec 36 reset.sh |
| Push restrictions | none (`restrictions: null`) — the direct-push row above already forbids pushing to `main` for everyone; an actor allowlist on top of it would silently re-permit what that row forbids | spec 36 reset.sh |
| Private-repo protections require a paid plan | verified before space creation; `a2a doctor --space` (v2) re-checks it later | §4.5 |

## Order matters — two steps brick a space when taken early

Both were internal-runbook knowledge until 2026-07-26, and both fail in ways
that do not name their cause:

1. **Push the bootstrap commit BEFORE arming protection.** A protected empty
   `main` cannot accept its own first commit: direct push is forbidden and
   there is no branch to open a pull request from. Create the repo, push
   `main`, *then* arm.
2. **Arm the required check only after the pinned reusable-workflow release
   exists.** `.github/workflows/a2a-validate.yml` pins
   `a2a-validate-reusable.yml@vX.Y.Z`. Requiring `a2a-validate / validate`
   while that tag is unpublished means no pull request can ever satisfy the
   check, and every merge hangs on "Expected — Waiting for status to be
   reported". `a2a space init` pins the ref to its OWN released version, so
   this is satisfied by default and only bites when the pin is hand-edited.

Notes:

- The `a2a-postmerge-audit` job in the same workflow file MUST NEVER be
  added as a required status check (flag-only per §5.5's V3 row) — it runs
  post-merge and never blocks a merge. Its own surfaced context is
  `a2a-postmerge-audit / validate`, distinct from the gate's.
- No repo secret is required (P33): a2ahub is public and the reusable
  workflow acquires the validator via `go run …@<ver>` (Go checksum DB), so
  the pre-P33 `A2A_BINARY_FETCH_TOKEN` secret is gone. Set nothing.
- **Actions policy (restrictive orgs only).** Calling a2ahub's reusable
  workflow is subject to the space org's Actions policy. If the org restricts
  Actions to "only actions/workflows in this organization," add
  `ydnikolaev/a2ahub` once under Settings → Actions → General → *Allow
  specified actions and reusable workflows* — **one setting, not a token**. A
  public space repo has no such restriction by default. A **private** space in
  a private org may also need Dependabot enabled (Settings → Code security) for
  the version-bump PRs to open.
- Migrating a pre-P33 space: the required check RENAMES from the flat
  `a2a-validate` to `a2a-validate / validate` — update the branch-protection
  rule in the same change that swaps the workflow to the caller, or PRs hang
  "Expected — Waiting for status to be reported" (spec 33 §3, the getvisa
  migration).
