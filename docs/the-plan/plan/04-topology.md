# §4 Topology & SSOT

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).

## 4.1 Authority map (D-001)

Exactly one writable authority per data class. This table is normative; any
feature that would create a second writable authority for a class is a design
defect.

| Data class | SSOT | Writer | Rebuildable from |
|---|---|---|---|
| All 8 artifact types + lifecycle events | space git repo | owning system's actors (via the PR funnel, D-002) | — (is the source) |
| Space membership & config | `space.yaml` in the space repo | space admin via PR | — |
| Indexes, folded states, cross-space views | hub DB / local cache | hub / binary (derived) | full replay of git history |
| Delivery state: watermarks, notification status, subscriptions | hub DB | hub | safe to lose (at-least-once redelivery; re-ack is idempotent) |
| Presence / live dashboard state | hub memory | hub | discardable |
| Runtime data payloads (catalog, content, facts) | NOT in a2ahub (NG-1) | teams' own APIs | n/a |

Failure independence (S-8): with the hub down, every read and write above the
derived line still works via git + binary; agents lose only push
notifications and fall back to on-demand sync (§8 watch loop). The hub can be
destroyed and rebuilt from git with zero durable loss.

## 4.2 Exchange space layout (D-003)

One space = one git repository = one circle of participants. Normative
layout:

```
<space-repo>/
  space.yaml                      # manifest: space id, schema version, participants,
                                  #   gates config, notification routes (§5 schema)
  <system-id>/                    # one section per participant; sole writer = that system
    provides/
      <contract-slug>/
        contract.md               # descriptor + envelope (XC id, version, policy)
        schema/                   # machine schemas of the interface (format per §5.3)
        fixtures/                 # golden examples: valid/ and invalid/
    requires/
      <XR-...>.md                 # requirements toward other systems' contracts
    consumes.yaml                 # declared contract dependencies (registered-consumer
                                  #   registry, §5.4) — space-visible, written by `a2a`
    exchanges/
      <XQ|XW|XH|XA-...>.md        # authored exchange & broadcast artifacts
      <XS-...>.md                 # responses this system authored
    events/
      <year>/<ulid>.yaml          # lifecycle events authored by this system (3.5)
    docs/                         # free-form supporting docs (non-normative, referenced by artifacts)
  decisions/
    <XD-...>.md                   # multi-party decisions; CODEOWNERS = all required parties
  vendored/
    <vendor-name>/                # read-only mirror of a non-participant's spec; the
                                  #   maintaining system is named in space.yaml
```

Rules:

- **Inbox is a query, not a folder.** A system's inbox = all artifacts across
  the space where `to` includes it, joined with folded state. The binary and
  hub compute it; nothing is copied (no spool duplication in the space).
- A system writes ONLY under its own section (plus `decisions/` via the
  decision flow). Enforcement: single write funnel below + V3 diff-authz.
- **Single write funnel (D-002, revised post-audit).** `main` is the only
  normative branch and accepts NO direct pushes from anyone (admin bypass
  reserved for incident recovery, alarmed via F-7). Every write — by the
  binary, always — is: commit to an ephemeral branch `a2a/<system>/<id>`,
  push, open a PR with auto-merge enabled. Branch protection: PRs required
  for all actors; required status check `a2a-validate` (V3); force-push
  forbidden; "require branches up to date" OFF (concurrent event PRs must
  not serialize; post-merge V3 on main + V4 are the backstop for cross-PR
  races, CC-095). Ungated PRs merge automatically on green V3 with zero
  human involvement; latency is seconds-to-a-minute of fire-and-forget.
- **CODEOWNERS lists ONLY gated paths:** `/<system>/provides/**` → that
  system's human owners (G1/G2); `/space.yaml` → space admins (G4);
  `/decisions/**` → all participants' humans (advisory listing — the real
  quorum is V3 checking approve events against the decision's
  `required_approvers`). All other paths (`exchanges/`, `events/`,
  `requires/`, `consumes.yaml`, `docs/`) have no owner entry and need no
  human. NOTE: this over-gates minor/patch contract publishes (any
  `provides/**` change gets owner review) — accepted as intentional
  conservatism per D-010.
- **Section single-writer is enforced by V3 as required check:** changed
  paths MUST be ⊆ the authoring system's section (plus `decisions/` via the
  decision flow), where the author is the PR's GitHub identity mapped to a
  system via the `space.yaml` participant block (github-login → system-id).
  An unmapped identity fails the check with an actionable error (CC-097).
- Artifact files are named by their ID (`<id>.md`), guaranteeing uniqueness
  and greppability. Events shard by year to keep directories bounded.
- The a2ahub product repo (binary, schemas, templates, hub) is NOT a space;
  spaces contain only exchange content + manifest.

## 4.3 Federation (D-003)

- A system MAY participate in any number of spaces. Its local config (in the
  project, §7.4) lists connected spaces; the binary aggregates them into one
  inbox/outbox view, and the hub aggregates all spaces it is granted read
  access to.
- Every artifact lives in exactly one space. Cross-space references use the
  full form `space-id:artifact-id@version` and are resolvable only by actors
  with access to both spaces; the validator marks unresolvable cross-space
  refs as warnings (access asymmetry is legal, see CC catalog).
- Adding a circle = new repo + manifest + invite; existing spaces are never
  restructured (R-006). A bilateral private topic between two systems that
  are already co-members of a bigger space SHOULD still be a separate space
  if its content must be hidden from other members — privacy boundary is the
  repo (D-003).
- Space IDs are globally unique within an operator's fleet (registry kept in
  the hub config; collision is an onboarding-time check, §9.2).

## 4.4 Vendored mirrors

For a dependency whose owner is not a participant (true external vendor):

- A designated participant (named in `space.yaml`) maintains a read-only
  mirror of the vendor's spec under `vendored/<vendor>/`, with envelope
  metadata recording source URL, retrieval date, and digest.
- Mirrors are caches, never sources: no requirements can be `satisfied` by a
  mirror change; agents treat mirror content as informational.
- Staleness of mirrors is surfaced on the dashboard like any other staleness.

## 4.5 GitHub hosting profile (v1)

v1 binds to GitHub as the git host (org, CODEOWNERS, branch protection,
webhooks, fine-grained PATs). The binding is isolated: everything above
speaks plain git + a thin host adapter (webhooks + permissions), so a later
GitLab/Gitea profile is an adapter, not a redesign (the PR + required-check
+ owner-approval funnel maps 1:1 to GitLab MR + pipeline + approval rules).
Host-specific setup steps live in §9 runbooks; security mechanics in §10.

**Hosting prerequisite:** branch protection / required reviews / rulesets on
PRIVATE repos require a paid GitHub plan (Team+). The operator org MUST be
on such a plan; space creation (§9.1) verifies protections are actually
active, and `a2a doctor --space` re-checks it (§7.2).
