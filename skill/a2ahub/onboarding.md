# onboarding.md — setup walkthroughs (§9 digests)

> Condensed from plan §9 (Human Operations). These are the walkthroughs an
> agent uses to guide a first-time participant or human through setup. Every
> command's exact syntax lives in [reference/commands.md](reference/commands.md);
> confirm a green setup with `a2a doctor` (read [troubleshooting.md](troubleshooting.md)
> for what each check means). The non-overengineering bar (§9): every runbook
> must be executable by one person in the stated time.

## Install profiles (§9.1)

| Profile | Who | What it covers | Budget |
|---------|-----|----------------|--------|
| **hub admin** | operator | Provision the hub on a VPS: binary + systemd + `hub.yaml` + space read-PATs + webhook registration + TLS + optional chat webhook. | ≤ 1 h |
| **org/space admin** | operator (v1) | Create a space repo from the product's space template (layout, CI workflow with the `a2a-validate` required check, CODEOWNERS skeleton, `space.yaml`) + branch protection (PR-only main, auto-merge) + invite participants. Verify a direct push is rejected and an ungated PR auto-merges. | ≤ 30 min |
| **project dev** | each participating team | Install the binary + `a2a init` + `a2a connect` + credentials + the harness adapter, then `a2a doctor` green. | ≤ 30 min, no walkthrough |

The project-dev profile is the one an agent most often assists. The end state is
a green `a2a doctor` — if any check fails, jump to
[troubleshooting.md](troubleshooting.md).

## Onboarding a new system into an existing space (§9.2)

Guide the participant through these steps in order:

1. **Machine account + credential.** The new team's org creates the system's
   machine account and issues its fine-grained PAT (scopes per §10.5).
2. **Manifest PR (G4 gate).** The space admin opens a PR adding the participant
   to `space.yaml` — including the github-login → system-id mapping for the
   machine account and the human owners — plus the section scaffold and a
   CODEOWNERS entry for gated paths. This is a G4 human gate.
3. **Project-dev install.** The new team runs the project-dev profile above;
   `a2a doctor` MUST pass before proceeding.
4. **Hello-world announcement.** The new team publishes an `announcement` with
   `category: status` as the hello-world — this proves the write path works end
   to end. Draft it with `a2a new announcement`; the skeleton is in
   [reference/authoring/announcement.md](reference/authoring/announcement.md).
5. **Hub picks it up automatically** from the manifest — no hub config change is
   needed.

## Onboarding a new space, org, or offboarding (§9.2)

- **New space (new circle):** the space-admin profile plus a `hub.yaml` entry
  and a webhook.
- **New org:** the operator sets up GitHub org membership/team, then proceeds as
  for a new system.
- **Offboarding:** set the manifest participant status to `left`, revoke
  credentials (§10.5), and remove the CODEOWNERS entry. The departed section
  stays read-only for history — it is never deleted.

## Credential lifecycle (§9.3)

Issued per §10.5 scopes and recorded (who, scope, expiry) in the space
manifest's participant block. Rotate on a calendar (90 days default; GitHub caps
fine-grained PATs at ≈ 1 year) and immediately on suspicion or offboarding. All
issuance/rotation is operator-runbook work in v1 — there is no self-service
portal.

> Note the boundary with `a2a doctor`: the credentials check confirms a
> credential *resolves*, not that it is un-expired (no expiry field is modeled
> yet — see [troubleshooting.md](troubleshooting.md)). Do not present a doctor
> PASS as an expiry guarantee.

## Day-2 operations (§9.4) — quick reference

| Situation | Runbook move |
|-----------|--------------|
| Fleet upgrade | Bump `min_binary_version` in the manifest per space (a PR); stale binaries then refuse writes and drift resolves itself. |
| Envelope schema migration | N/N−1 overlap window; the release note names the cutoff; the validator reports old-schema artifacts. |
| Hub loss | `hub rebuild` (the hub owns only ephemeral state). |
| Repo loss | Restore from any participant's mirror + GitHub — git is distributed. |
| Validation-pipeline outage | Writes freeze loudly; the runbook covers temporarily lifting the required check (logged) and re-arming it, with a mandatory post-incident announcement. |
| Sanctioned redaction | The operator announces the history rewrite BEFORE executing; participants re-clone (doctor detects divergence), the hub re-indexes, local caches purge, and a closing announcement lands. |

## First cross-system exchange (putting it together)

Once `a2a doctor` is green, the participant's first real exchange follows the
send loop in [loops.md](loops.md) §8.2:

1. Classify the need into one type (question / work_request / requirement /
   decision) — one intent per artifact. A composite need decomposes into parts
   on a shared thread; see [reference/decompose-example.md](reference/decompose-example.md).
2. Draft with `a2a new <type>` using the matching
   [authoring guide](reference/authoring/).
3. `a2a validate`, then `a2a submit` — submission opens a PR; tell your human.
4. Track it in `a2a outbox`; verify the response against your own acceptance
   criteria (`a2a verify`).
