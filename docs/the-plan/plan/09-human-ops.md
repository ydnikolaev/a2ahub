# §9 Human Operations

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Runbooks are specified here as required contents; final runbooks ship as
> product docs generated at release. Non-overengineering bar: every runbook
> must be executable by one person in the stated time (R-021).

## 9.1 Install profiles

| Profile | Who | Steps (runbook must cover) | Budget |
|---|---|---|---|
| hub admin | operator | provision on VPS: binary + systemd + `hub.yaml` + space read-PATs + webhook registration + TLS + chat webhook (optional) + OP-111 check | ≤1 h |
| org/space admin | operator (v1) | verify org plan supports private-repo protections (§4.5 prerequisite); create space repo from the product's space template (layout 4.2, CI workflow incl. `a2a-validate` required check + release-fetch token secret, CODEOWNERS skeleton, `space.yaml`) + branch protection (PR-only main, auto-merge) + invite participants; verify a direct push is rejected and an ungated PR auto-merges | ≤30 min |
| project dev | each participating team | install binary + `a2a init` + `a2a connect` + credentials (10.5) + harness adapter (8.8) + `a2a doctor` green | ≤30 min, no walkthrough (S-6) |

## 9.2 Onboarding runbooks

**New system into an existing space:**
1. New team's org: create the system's **machine account** (§10.5), issue
   its fine-grained PAT.
2. Space admin: PR adding the participant to `space.yaml` — including the
   github-login→system-id mapping for the machine account and human owners
   — + section scaffold + CODEOWNERS entry for gated paths (G4 gate).
3. New team: project-dev install profile; `a2a doctor` MUST pass.
4. New team: publish an `announcement` (category `status`) as the
   hello-world (proves write path end to end).
5. Hub picks the section up automatically from the manifest — no hub config.

**New space (new circle):** space-admin profile + hub `hub.yaml` entry +
webhook. **New org:** GitHub org membership/team by operator, then as above.
**Offboarding:** manifest status → `left`, revoke credentials (10.5),
CODEOWNERS entry removed; section stays read-only for history (CC-covered).

## 9.3 Credential lifecycle

Issue: per §10.5 scopes, recorded (who, scope, expiry) in the space manifest
participant block. Rotate: calendar-driven (90 days default; GitHub caps
fine-grained PATs at ≈1 year) + immediate on suspicion/offboarding; hub API
bearer tokens carry expiry and are stored hashed (10.5). `a2a doctor` warns
on approaching expiry; `a2a doctor --space` diffs manifest vs actual host
state (teams, CODEOWNERS, protections, collaborators) so revocation misses
surface instead of lingering (CC-100). All issuance/rotation is
operator-runbook work in v1 — no self-service portal (R-013).

## 9.4 Day-2 operations

- **Fleet upgrades:** release notes state whether `min_binary_version` in
  manifests should be bumped; bumping it is a PR per space; stale binaries
  then refuse writes (7.3) — drift resolves itself.
- **Envelope schema migration:** N/N−1 overlap window (5.4); the release's
  migration note names the cutoff; validator reports old-schema artifacts.
- **Space growth:** if a space's event/artifact volume degrades tooling,
  the year-sharding (4.2) plus index-side filtering is expected to hold to
  the 10-system horizon; beyond that is a documented open question (Q-scale)
  rather than premature machinery (R-013).
- **Hub loss:** `hub rebuild` (6.4). **Repo loss:** restore from any
  participant's mirror + GitHub; git is distributed by nature.
- **V3 pipeline outage (F-10):** writes freeze loudly; the runbook covers
  temporarily lifting the required check (logged) and re-arming it, with a
  mandatory post-incident announcement.
- **Sanctioned redaction (10.7):** operator schedules and announces the
  rewrite BEFORE executing; steps: history rewrite of the affected space →
  each participant re-clones (doctor detects divergence) → hub full
  re-index → local cache purge → closing announcement. Distinguishable from
  a hostile force-push by the prior announcement (CC-098).
- **Release signing key rotation:** new key ships pinned in a binary release
  signed by the old key; runbook covers compromise (out-of-band key
  distribution).

## 9.5 Succession

Everything an operator needs is: org ownership, VPS access, `hub.yaml`, and
this plan. The runbook set MUST include a succession note (transfer org,
rotate tokens, swap webhook secrets) so the operator role is transferable in
one sitting — the operator is a role, not a person (interview: admin may
change).
