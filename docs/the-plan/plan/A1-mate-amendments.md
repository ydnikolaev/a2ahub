# Appendix A — mate Amendments

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).
> Self-contained proposal for the mate repo, framed per mate's own gating:
> a new universal home requires (a) a second consumer and (b) an approved
> external protocol. This plan, once approved, satisfies (b); axon +
> future mate consumers satisfy (a). Ships to mate as an amendment
> (mate's AM-### flow); Q-008 tracks acceptance.

## A1.1 What mate already holds (verified against the repo)

- Research: `docs/features/development-pipeline-v2/research/contract-exchange-placement.research.md` — three-layer model (feature refs → project exchange home → team authority), envelope/state-machine sketch, explicitly gated "capability design, not a universal folder".
- Backlog seed: `BLG-002` in `development-pipeline-v2.backlog.yaml` (status `candidate`) — "Build the team A2A contract/request exchange service and MCP facade".
- No exchange structures exist on disk; the homes table (`doctrine/code/structure.md` §1, enforced in `internal/homes/homes.go`) is closed and fails closed.

This plan **supersedes the research doc's protocol sketch** (the research
doc itself says lifecycle authority belongs to the epic context registry and
the protocol needed team approval). Alignment notes: the three-layer model
survives intact; `XREQ-` IDs are replaced by §3.3 IDs; the candidate
`outbox/`/`inbox/` spool folders are replaced by D-018 (inbox as query — the
local spool is a *cache*, not a registry).

## A1.2 Proposed amendments (for mate's AM flow)

**AM-a2a-1 — new optional home: `exchange`.**
Add a homes-table row: role "a2ahub project seat", default path `.a2a/`,
existence class `Seeded` (create-if-absent, project-owned content, never
drift-gated). Contents: `config.yaml` (7.4, schema-validated by the `a2a`
binary, not by mate), gitignored `cache/`. Porting checklist per
structure.md §6 (paths seam key `exchange`, seeding via manifest template
dst, conformance report line, no cleanup wiring — not ephemeral).
RATIONALE: matches mate's synced-vs-seeded discriminator — mate fixes the
*location and role*; a2ahub owns the *content contract*.

**AM-a2a-2 — synced adapter artifacts.**
Add to `harness/manifest.yaml` as provider-loaded artifacts (adapter dsts):
the a2ahub rule file (8.8 loops, terse) and the `a2ahub` skill — both
**vendored from a2ahub product releases at a pinned version** recorded in
the manifest entry (mate syncs them out; a2ahub generates them, D-015).
Skill naming: `a2ahub` is a *native/project-tier* name per mate's rule
(it operates a project capability, not mate machinery — not `mate-*`).
The statusline embedding snippet ships in the same artifact set as
*reference material only* (D-021): the skill proposes adding an `a2a`
segment to whatever statusline the user already has; mate never syncs a
statusline config and nothing edits the user's setup without consent.

**AM-a2a-3 — feature-tracker linkage (lightweight).**
Adopt the research doc's feature-level reference shape in pipeline docs:
epics/specs reference exchange artifacts by a2ahub ID in an
`external_refs`-style field (opaque strings — mate never parses spaces),
with the blocking/informational relation. Compiled status resolves them via
`a2a show`. No new mate machinery beyond the field convention; archival
rules from the research doc (blocking open request prevents completion)
enter pipeline doctrine unchanged.

**AM-a2a-4 — BLG-002 disposition.**
Mark BLG-002 as superseded-by-external: the service is built as the
standalone a2ahub product (this plan), not a mate epic. mate's remaining
scope = AM-a2a-1..3 (seat, artifacts, linkage).

## A1.3 Non-goals for mate

mate does not: validate a2ahub content, mirror space state, wrap `a2a`
commands in mate CLI verbs, or own any exchange lifecycle. One seam, one
binary, no double bookkeeping (the research doc's own "no second normative
copy" principle).
