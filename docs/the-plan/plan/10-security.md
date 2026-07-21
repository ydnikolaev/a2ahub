# §10 Security & Privacy

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Priority requirement R-011. v1 threat context: a private circle of trusted
> teams; the model is designed so hardening for public mode adds enforcement,
> not redesign (R-007).

## 10.1 Assets

Private code and prompts of each org (must never cross the boundary),
credentials, the integrity of contracts and lifecycle states (YMYL product
chain depends on them), availability of the exchange, the exchange metadata
itself (business-sensitive: who builds what).

## 10.2 Threat model

| T | Threat | Primary mitigations |
|---|---|---|
| T-1 | leaked read credential (space PAT / hub token) | fine-grained, read-only, per-actor, expiring (10.5); rotation runbook (9.3); blast radius = confidentiality of one space |
| T-2 | leaked write credential | NOTE: a write PAT is repo-scoped — GitHub cannot path-scope it; the real containment is the PR funnel: no direct push lands on `main`, and a cross-section PR is unmergeable (V3 diff-authz, §4.2). History is append-only reviewable; rotation + git revert |
| T-3 | prompt injection via inbound artifact | D-014 data-not-instructions rule (8.3); schema validation rejects unexpected structures; suspicious-content flag flow (10.7); output sanitization for rendered surfaces (10.8) |
| T-4 | secret/private-code exfiltration in an outbound artifact | forbidden-payload classes + validator secret-scan at V2/V3 (10.4) — best-effort DETECTION, not proof (encoded secrets/PII can pass); human gate on classification escalation (G5); redaction runbook (10.7) for what slips through |
| T-5 | compromised partner system writes malicious content | PR-only funnel + V3 diff-authz makes cross-section writes unmergeable (§4.2); folded-state authorization (3.5 rule 3) prevents cross-section state forgery; V4 flags anomalies |
| T-6 | malicious/compromised hub | integrity: hub is read-only and non-normative — wrong notifications/dashboard, detectable against git. CONFIDENTIALITY: the hub aggregates every space it mirrors behind one VPS — compromise leaks all that content and the hub API tokens. Mitigations: per-space read PATs (not one fleet PAT), hub API tokens stored hashed with expiry (10.5), VPS hardening is a v1 duty (§6.6), spaces with especially sensitive content MAY be excluded from hub mirroring |
| T-7 | forged webhook to hub | signature verification (F-8); fetch debounce/rate-limit (§6.3) against webhook floods |
| T-8 | dependency/supply chain of the binary | signed releases (cosign/minisign), public key pinned in the binary, `a2a update` verifies BEFORE swapping and fails closed (7.3) — a v1 requirement, not deferred; checksums alone authenticate nothing |
| T-9 | GitHub account takeover of a participant | org 2FA requirement; gates require review by other humans for high-impact actions |

## 10.3 AuthZ matrix (enforcement points in braces)

| Action | member agent (own system) | system owner (human) | space admin | machine RO |
|---|---|---|---|---|
| read space | ✔ {git} | ✔ | ✔ | ✔ {PAT scope} |
| write own section artifacts/events | ✔ {branch protection: PR-only + V3 required check} | ✔ | ✔ | ✘ {PAT scope} |
| write other section | ✘ {V3 diff-authz fails required check → PR unmergeable} | ✘ | ✘* | ✘ |
| approve G1/G2 (contract publish/breaking) | ✘ | ✔ {CODEOWNERS required review} | — | ✘ |
| approve/reject decision (G3) | ✘ | ✔ {approve event merged from a PR authored/approved by the human's own GitHub account; V3 checks the PR identity against the owner list — not just the self-declared `actor.kind`} | — | ✘ |
| G5 classification override | ✘ | ✔ {same PR-identity mechanism as G3} | — | ✘ |
| edit space.yaml (G4) | ✘ | ✘ | ✔ {CODEOWNERS required review} | ✘ |
| hub read APIs | ✔ {hub token, per-space scope} | ✔ | ✔ | ✔ |

*space admin repairs (e.g. fixing a broken manifest ref) go through PR
review visible to all — no silent superuser edits.

Enforcement layering: git-host mechanics enforce at MERGE time (PR-only
`main`, required V3 check, CODEOWNERS on gated paths — §4.2); V4 re-checks
the same rules content-wise post-merge so a host misconfiguration surfaces
as flags, not silent trust; hub-write mode later reuses the same engine as
a third, server-side enforcement point (D-002).

Attribution honesty (v1): the `actor` block and git author line are
self-asserted provenance-for-humans, NOT a security control — except where
the PR's authenticated GitHub identity provides ground truth (section
authorship, G1–G5). Cryptographic event signing arrives with public mode
(10.6).

## 10.4 Data classification & forbidden payloads

| Level | Meaning | Handling |
|---|---|---|
| `public` | publishable as-is | default for opensource-era templates |
| `internal` | visible to the whole space (default) | normal flow |
| `restricted` | named recipients within the space only | v1: MUST NOT be placed in a space whose membership exceeds the recipient set — the validator enforces "restricted ⇒ bilateral space" (privacy boundary = repo, D-003); per-artifact encryption is explicitly deferred (Q-open) |

Forbidden payload classes (validator blocks at V2/V3, G5 human override
required): credentials/tokens/keys (secret-pattern scan), private source
code beyond agreed interface excerpts, raw agent prompts/system messages,
personal data of end users, absolute local filesystem paths. RATIONALE:
through the boundary travel only data and specifications (partner spec
principle 1, adopted).

Scan scope and honesty: the secret/forbidden-payload scan covers ALL text
content crossing the boundary — envelopes, bodies, event notes,
`provides/**/schema/`, `fixtures/**`, `docs/**` — not only `.md` envelopes.
It is best-effort detection with a documented false-negative reality
(encoded secrets, UUID-shaped keys, PII cannot be reliably pattern-matched);
prevention claims elsewhere in the plan are bounded by this, and the 10.7
redaction runbook is the remedy for what slips through.

## 10.5 Credentials (v1)

| Actor | Credential | Scope |
|---|---|---|
| human | own GitHub account (org member, 2FA enforced) | per teams/CODEOWNERS |
| project agent | **per-system machine account** with fine-grained PAT (or GitHub App installation), contents RW on the specific space repos, held in env/keychain, never in files (7.4). Machine account ≠ any human owner's account — CODEOWNERS forbids self-approval, and separate identity makes the §2.1 attribution cross-check meaningful. PAT is repo-scoped (no path scoping exists); section containment comes from the §4.2 funnel | write = PRs only; merge gated by V3 diff-authz |
| hub | fine-grained PATs, read-only, **one per space** (no fleet-wide PAT — T-6 blast radius) | RO |
| CI of a space | default GITHUB_TOKEN of that repo + read-only token for fetching the pinned validator binary from the (private) product repo, stored as a repo secret (§9.1) | validation only |
| hub API clients | bearer tokens issued by operator in `hub.yaml`, per-system, read scope per space, **stored hashed, with expiry + rotation runbook (9.3)** | OP-1xx |

NOTE: org settings MUST allow member fine-grained PATs; GitHub caps their
lifetime (≈1 year) — expiry tracking in §9.3 covers this.

## 10.6 Public-mode readiness checklist (designed-for, not v1)

Before any space/product goes public or untrusted parties join: hub-write
path with server-side authz (the D-002 upgrade) replacing direct push for
untrusted actors; real identity verification (signed commits or hub-issued
identities); per-artifact signatures on events; rate limiting; secret-scan
hardening; classification `restricted` re-review; security audit of the
binary. Tracked as a §17 open line; nothing in v1 may contradict it.

## 10.7 Audit & incident response

- Audit log = git history (authoritative, per artifact/event, forever) +
  hub notification/delivery log (operational). No separate audit store in v1.
- Suspicious inbound content: recipient declines with reason
  `security-concern`, flags `classification` violation; hub notifies both
  systems' owners; thread frozen until humans clear it. This flow MUST be in
  the skill text (8.7).
- Credential compromise: rotate (9.3), `git revert` offending commits (the
  space stays append-only — reverts, not history rewrites; force-push is
  alarmed by F-7), post-incident `announcement` to the space.
- **Sanctioned redaction** (leaked secret/private code/PII that must not
  remain readable): `git revert` does not remove history, so a
  operator-coordinated redaction runbook exists (§9.4) — agreed history
  rewrite of the affected space, participant re-clone, hub full re-index
  (F-7 machinery), local cache purge, and a mandatory `announcement`
  recording the redaction. A redaction is scheduled and announced BEFORE the
  rewrite so it is distinguishable from a hostile force-push (CC-098);
  rotation still applies to credentials regardless.

## 10.8 Output sanitization (rendered surfaces)

Artifact-controlled strings (titles, bodies, notes, reasons) are authored by
other orgs and are untrusted. Every rendering surface — hub dashboard, local
HTML, statusline output — MUST HTML-escape untrusted fields, render Markdown
through a sanitizing renderer (no raw HTML/scripts/embedded external
resources, consistent with 7.6's no-external-requests rule), and keep
statusline text plain (control characters stripped). This is an enforcement
point with malicious-content fixtures in T1/T3 (§13.4).
