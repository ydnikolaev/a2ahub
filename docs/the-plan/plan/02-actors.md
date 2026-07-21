# §2 Actors & Identity Model

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).

## 2.1 Identity hierarchy

```
org  →  system  →  actor (human | agent)
```

| Level | Definition | Examples |
|---|---|---|
| **org** | The owning organization/party. Boundary of trust and of private code. | `yura`, `seomatrix` |
| **system** | A software project that participates in exchanges. The addressing unit: every artifact is addressed system→system. One org MAY own many systems. | `axon`, `seomatrix`, `sot` |
| **actor** | The concrete identity that authored an action: a human or an agent working for a system. | `yura` (human), an agent session of Claude Code or Codex |

Rules:

- Addressing (`from`/`to`) MUST use system IDs only. Orgs and actors are
  attribution, never addressing.
- Actor attribution MUST be recorded on every artifact and lifecycle event
  (see §5 envelope): `actor.kind` (`human`|`agent`), `actor.name` (human
  handle or agent harness name), and for agents: `actor.model` and
  `actor.session` when available. RATIONALE: R-005 demands full provenance;
  vendor/model is metadata, never protocol (R-018).
- Attribution in v1 is **declared, honestly, by trusted participants** and
  cross-checked against the git commit author. Cryptographic verification of
  attribution is a public-mode concern (§10.6), not v1.

## 2.2 Roles and permissions

| Role | Scope | May do |
|---|---|---|
| **operator** | global | Administer the hub, create spaces, approve org onboarding, hold the org/repo ownership. Currently: Yura (single operator; transferable, see §9.5). |
| **space admin** | one space | Approve participant join/leave, edit the space manifest, configure branch protection/CODEOWNERS. Defaults to the operator. |
| **system owner** | one system | CODEOWNERS target for the system's GATED paths (`provides/**` — G1/G2); pass approval gates (breaking changes, decisions) for that system. Human. |
| **member** | one system | Read the whole space; write only the system's own section. Humans and agents. |
| **machine, read-only** | one space | Read-only credential for pollers/CI/dashboards. MUST NOT be able to write. |

Enforcement in v1 is the §4.2 write funnel (D-002): PR-only `main`, V3
diff-authz for section containment (github-login→system mapping in the
manifest), CODEOWNERS required review on gated paths only (`provides/**`,
`space.yaml`, `decisions/`). The authz matrix with its enforcement points
is normative in §10.3.

## 2.3 Participant registry

Each space carries a manifest file (`space.yaml`, schema in §5) — the SSOT of
membership. Per participant it records: system ID, org, section path, human
owners (git handles), machine actors and their credential scopes, join date,
status (`active`|`suspended`|`left`), and the runtime channels the system
provides/consumes (informational). The hub and the binary read membership
ONLY from the manifest; nothing is hardcoded (R-021).

Adding a participant = one PR to the manifest + section scaffold, approved by
the space admin (runbook in §9.2). Removing = status flip to `left`; the
section is retained read-only for history (see CC catalog, §12).

## 2.4 Agent-harness adapters

The core is harness-agnostic (R-018). Harness-specific integration is
packaged as thin adapters over the same binary:

| Harness | Adapter surface |
|---|---|
| Claude Code | statusline provider hook, expert skill (R-019), session-start check rule — packaged for inheritance via mate (Appendix A) |
| Codex | same binary via CLI and/or MCP (stdio); instructions file equivalent of the skill |
| CI worker | CLI in validation mode with read-only credential |
| any other | anything that can run the binary or speak MCP qualifies; no adapter is ever required for correctness, only for convenience |

NOTE: adapters change *how an agent is prompted to use* a2ahub, never the
formats or lifecycle semantics.
