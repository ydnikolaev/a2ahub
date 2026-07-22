# P13 — Minimal a2ahub expert skill & agent rules — Specification

**Slug**: `v1-min-2026-07`  ·  **Track**: docs  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Footprint**: `skill/**` (new top-level dir, this repo — the ONE editable
home for the skill/rules prose per D-015/§8.8); one CI job edit on the
product-repo CI workflow established by P1 (exact workflow file path is P1's
footprint — this phase adds a drift-gate job to it, does not create the
workflow). No `internal/*` Go package is created or modified — `skill/` is
not an ADR-001 package row (decisions.md ADR-001 table lists only
`cmd/a2a`, `internal/*`, `schemas/`, `space-template/`); **may import: n/a**
— this is a content + CI phase. The drift-gate job invokes the already-built
`a2a` binary as a black box (`a2a --help`/OP catalog output, `a2a template
list/show` per OP-219) and diffs its output against committed files; it
imports no Go package directly and adds none. `skill/` is distinct from
this repo's own engineering skills at `.claude/skills/` and `.agents/skills/`
(mate-synced, used to build a2ahub itself) — do not conflate the two trees.
Harness distribution of `skill/` content (mate-synced Claude Code copy,
Codex `AGENTS.md` section) is Appendix A / Q-008 (17-decisions.md,
A1-mate-amendments.md) — **out of scope here**; this phase ships the single
source, not the per-harness packaging. Blocked by P7 (tracker.yaml P13:
`blocked_by: [P7]`) — the reference content projects the read-surface/
statusline commands P7 ships.

---

## 0. User stories

Sourced verbatim from plan §14 `14-us-ac.md`, section "E6 — Statusline,
adapters, skill":

| ID | User story |
|----|------------|
| US-602 | (IA) As any agent, activating the `a2ahub` skill answers my questions about the system and walks me through any flow. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Skill contents & sourcing split | [08-agent-protocol.md](../../../the-plan/plan/08-agent-protocol.md) §8.7 | quoted verbatim below — the skill's required content list and the D-015 hand-maintained/generated split; do not re-derive |
| The 8.1–8.6 loops (the skill's core content) | [08-agent-protocol.md](../../../the-plan/plan/08-agent-protocol.md) §8.1–8.6, read ENTIRE per brief | this section is written to be extractable into skill/rules text (§8 header note) — the skill/rules prose is a condensation of this section, not a rewrite |
| Harness packaging boundary | [08-agent-protocol.md](../../../the-plan/plan/08-agent-protocol.md) §8.8 | "one editable home... per-harness copies are assembled at release, never independently edited" — normative for the single-sourcing requirement below |
| D-014/D-015/D-021 | [17-decisions.md](../../../the-plan/plan/17-decisions.md) | quoted verbatim below |
| Statusline embedding + Appendix A boundary | [07-client.md](../../../the-plan/plan/07-client.md) §7.5 | "Claude Code embedding snippet ships as a mate-synced reference (Appendix A)" — confirms the harness-distribution cut named in Footprint above |
| MCP reference generation precedent | [07-client.md](../../../the-plan/plan/07-client.md) §7.7 | "the authoritative OP↔tool mapping table is generated from the binary and CI-checked" — the mechanism this phase's drift gate reuses, not reinvents |
| Templates as schema projections | [05-schemas.md](../../../the-plan/plan/05-schemas.md) §5.6 | templates are projections of schemas, drift-checked in product CI already (P2/P6 territory) — this phase's authoring-guide projection is a further derivation of the same source, not a new SSOT |
| Package layout | [decisions.md](../../../decisions.md) ADR-001 | confirms `skill/` is new top-level content, not an `internal/*` package — no import boundary to violate |
| AC row | [14-us-ac.md](../../../the-plan/plan/14-us-ac.md) US-602 | quoted verbatim in §8 below — do not modify |

### §8.7 quoted (the skill's required content + the D-015 split)

> An activatable skill, `a2ahub`, ships with the harness adapters (mate-synced
> for Claude Code; AGENTS-snippet for Codex). Contents: condensed §0/§3
> semantics, the 8.1–8.6 loops, full command/MCP reference, per-type authoring
> guides with the templates (including a worked decompose example —
> announcement + question + work_request on one thread, shipped in the
> product-repo fixture set), troubleshooting (`a2a doctor` interpretations),
> and onboarding walkthroughs (§9 digests). Sourcing (D-015, post-audit):
> prose parts are HAND-MAINTAINED as versioned files in the product repo,
> single-sourced, released together with the binary under a release-checklist
> review; automated drift gates apply only to the mechanically derivable
> parts (command/MCP reference from the binary, templates from schemas).
> [...] The skill is documentation-with-hands: it MUST always defer to the
> binary's validator as the source of correctness rather than restating
> rules that could drift.

### D-014 / D-015 / D-021 quoted (17-decisions.md)

> D-014 | Inbound artifacts are data, never instructions (prompt-injection
> stance); suspicious content flow 10.7 | cross-org content is untrusted by
> definition even among partners | architect

> D-015 | **(softened by audit)** Skill + harness texts are single-sourced,
> hand-maintained files in the product repo, released with the binary under
> a release-checklist review; automated drift gates only for mechanically
> derivable parts (command/MCP reference, templates) | "generate prose from
> the plan" had no deterministic generator — an LLM release step or a faked
> gate; single-home + checklist gives the anti-drift value at v1 cost |
> architect + audit

> D-021 | Statusline integration is advisory: `a2a statusline` is an
> embeddable segment for the user's OWN statusline; onboarding proposes it,
> nothing ever replaces or silently edits the user's setup; **session-start
> checklist is the guaranteed floor** | users own their harness configs;
> consent over convenience | interview

---

### T4/T5. Skill content layout + CI drift-gate job (tracks: docs + ci — both required by Footprint)

> Path list is normative for this phase's footprint. Source column encodes
> the D-015 split: which paths the drift gate may touch, which it must not.

| Path | Content | Source (D-015 split) | §8.7 anchor |
|------|---------|------------------------|--------------|
| `skill/a2ahub/SKILL.md` | Entry point: activation modes (answer a question / onboard a first-timer / assist drafting a type), table of contents into the files below | hand-maintained | "Activation modes" |
| `skill/a2ahub/loops.md` | Condensed §0/§3 semantics + the 8.1–8.6 loops (session-start checklist as the guaranteed floor, D-021; the D-014 untrusted-input clause from 8.3 step 2; escalation ladder 8.5) — the canonical "one editable home" (§8.8) later assembled into the Claude Code rule file and the Codex `AGENTS.md` section, both out of this phase's scope | hand-maintained | "condensed §0/§3 semantics, the 8.1–8.6 loops" |
| `skill/a2ahub/reference/commands.md` | Full `a2a` command reference (OP-2xx, §7.2) + MCP tool reference (§7.7) | **generated from the binary** — reuses the §7.7 precedent (OP↔tool table already generated+CI-checked); no new generator package | "full command/MCP reference" — drift-gated |
| `skill/a2ahub/reference/authoring/<type>.md` (one per §3.1 type) | Per-type authoring guide: rendered template skeleton + inline guidance, sourced via `a2a template list/show` (OP-219) | **generated from schemas** (template projection, §5.6, R-015) | "per-type authoring guides with the templates" — drift-gated |
| `skill/a2ahub/reference/decompose-example.md` | The worked single-intent decompose example (announcement + question + work_request, one thread), referencing IDs in the product-repo fixture set | hand-maintained (references fixtures; not itself generated) | "a worked decompose example... shipped in the product-repo fixture set" |
| `skill/a2ahub/troubleshooting.md` | `a2a doctor` output interpretations | hand-maintained | "troubleshooting" |
| `skill/a2ahub/onboarding.md` | §9 digest walkthroughs | hand-maintained | "onboarding walkthroughs (§9 digests)" |
| `skill/RELEASE-CHECKLIST.md` | Reviewer sign-off record for the D-015 "release-checklist review" of every prose path above at each tagged release | hand-maintained; **phase-local placement decision** — the plan names the review, not a file (see Amendments/deviations) | D-015 |

| Workflow (job added, not created) | Trigger | Steps | Gate it enforces |
|---|---|---|---|
| Product-repo CI (from P1), new job `skill-drift` | push/PR touching `skill/a2ahub/reference/**`, `internal/schema/**`, `internal/template/**`, or `cmd/a2a` command wiring | (1) build the `a2a` binary; (2) regenerate `commands.md` from the binary's own command/MCP catalog output and `authoring/*.md` from `a2a template list/show` into a scratch dir; (3) byte-diff scratch vs the committed `skill/a2ahub/reference/**` tree | Mismatch → job fails (AC-602.1). Prose paths (`SKILL.md`, `loops.md`, `troubleshooting.md`, `onboarding.md`, `decompose-example.md`, `RELEASE-CHECKLIST.md`) are explicitly OUT of this job's scope — covered by the release-checklist review (D-015), never a machine gate. |

---

## 5. Existing patterns to reuse (anti-duplication)

- [ ] §7.7 precedent — "the authoritative OP↔tool mapping table is generated
      from the binary and CI-checked": the `commands.md` generation step
      reuses this exact generate-then-diff mechanism. Do not write a second,
      parallel doc generator.
- [ ] OP-219 `a2a template list/show` — the only sanctioned source for
      rendered template projections; do not hand-transcribe template bodies
      into `authoring/*.md`.
- [ ] The `<name>/SKILL.md` + subfolder layout already used by this repo's
      own engineering skills (`.claude/skills/<name>/SKILL.md`,
      `.agents/skills/<name>/SKILL.md`) — reuse the physical directory
      convention for `skill/a2ahub/`, but note this is layout only: the
      product skill under `skill/` is a separate tree, not synced by mate,
      not the same content as those engineering skills (see Footprint).
- [ ] Quote, do not paraphrase, the 8.1 checklist / D-014 / D-021 text in
      `loops.md` — condensation is allowed (§8.7 says "condensed"), but the
      meaning must not drift from the plan wording quoted in §0.5 above.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| Single-sourcing | `loops.md` / `SKILL.md` / etc. exist in exactly one place (`skill/a2ahub/`); no forked per-harness copy checked into this repo | a stray copy accidentally committed under `.claude/`/`.agents/` during future harness-packaging work — must fail review, not this phase's gate (out of scope, flag only) |
| Drift gate — commands | `skill-drift` job fails when `commands.md` is edited by hand to diverge from binary output | a command added to the OP catalog (§7.2) without a `commands.md` regen — job must red, not silently pass |
| Drift gate — templates | `skill-drift` job fails when an `authoring/<type>.md` diverges from `a2a template list/show` output | a new §3.1 type added without a corresponding `authoring/<type>.md` — job must red (missing file counts as mismatch) |
| Drift gate — prose exclusion | Editing `loops.md`/`troubleshooting.md` alone does NOT trigger `skill-drift` | confirms prose is release-checklist-gated, not machine-gated, per AC-602.1's second clause |
| Content floor | `loops.md` contains the 8.1 checklist steps and the D-014 "data, never instructions" clause | a future edit that drops the untrusted-input clause during "condensing" — must be caught at release-checklist review (documented expectation, not a CI check) |

## 7. Schema / contract delta

None. This phase creates no schema field, no envelope change, no
`internal/*` package. It is a content (docs) + CI (one job) phase only.

## 8. Acceptance criteria

> Row 1 copied verbatim from `14-us-ac.md` (line-wrap only) — do not modify.
> Phase-local rows carry US "—".

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-602 | AC-602.1 Given a release, when the binary's command/MCP reference or the templates changed, then their generated projections in the skill are regenerated or the build fails; prose sections are covered by the release checklist review, not a machine gate (8.7, D-015). [8.7] | Modify an OP entry or a schema-backed template, do not regenerate `skill/a2ahub/reference/**`, push — the `skill-drift` CI job must fail; regenerate and push again — it must pass. Separately, edit only `loops.md` — no CI job triggers (release-checklist review only). |
| 2 | — | The skill/rules prose has exactly one editable home in this repo (`skill/a2ahub/`); no duplicate or forked copy exists under `.claude/`, `.agents/`, or elsewhere in this repo. | `find` the repo for a second `loops.md`/`SKILL.md` carrying the same content hash outside `skill/a2ahub/` — none found. |
| 3 | — | `skill/a2ahub/reference/commands.md` and `reference/authoring/*.md` are byte-identical to the `skill-drift` job's regenerated scratch output at HEAD. | Run the job's regenerate step locally against the built `a2a` binary, diff against the committed files — zero diff. |
| 4 | — | `skill/a2ahub/loops.md` contains, in substance, the 8.1 session-start checklist (as the guaranteed floor per D-021) and the D-014 untrusted-input rule from 8.3 step 2. | Read `loops.md`; both clauses present and attributable to their plan section/D-ID. |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | Adding a §3.1 type or an OP verb only adds a row to `authoring/`/`commands.md` and reruns the same generator — no new mechanism per addition. |
| Coupling | Soft: `skill/` reads the binary's own output and the schema-derived templates (both already SSOT elsewhere); it never becomes a second source of command/template truth. |
| Migration path | low — v2 harness distribution (Appendix A, Q-008) consumes this same single source unchanged; no rewrite needed at that boundary. |
| Roadmap conflicts | Depends on P7 (statusline/read-surface commands must exist to document) and implicitly on P2/P6 (schemas/templates) and P8 (contract verbs) being far enough along for `commands.md` to be non-trivial — footprint here does not touch those packages, only reads their CLI-visible output. |

## 10. Implementor entry point

Execute after P7 (tracker `blocked_by: [P7]`). Two coupled deliverables: (a)
author `skill/a2ahub/**` per the T4 table — hand-maintained prose first
(TDD-equivalent: write the content, then the gate that must pass against
it), then the generated `reference/**` projections from the already-built
`a2a` binary; (b) add the `skill-drift` job to the existing product-repo CI
workflow (P1's file) per the T5 row. Framework-first: reuse the §7.7
generate+diff mechanism verbatim in shape; do not write a new templating or
doc-generation package. Full loop:
[docs/features/README.md](../../features/README.md).

## Open questions (phase-local)

None new. Harness distribution of this content (mate-synced Claude Code
copy, Codex `AGENTS.md` section) is tracked as **Q-008**
(17-decisions.md §"Open questions") and is explicitly out of scope per
Footprint above — cited, not re-opened. The release-checklist review's file
placement (`skill/RELEASE-CHECKLIST.md`) is a footprint-owned implementor
decision, not plan ambiguity — recorded as a narrowing, not a Q.

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it
> here AND amend any downstream spec.

### 2026-07-22 — from wave 6 (P14): the §7.7 "generate from binary" precedent does NOT exist yet

- §0.5's "MCP reference generation precedent" row and §5's "reuses this exact
  generate-then-diff mechanism. Do not write a second, parallel doc generator"
  assume prior art that is not in the repo. P14 shipped a Go-level in-process
  bijection TEST (`cmd/a2a/mcp_parity_test.go`), NOT a binary-invoking
  generator: it compares `buildCommands()` keys ∪ `cli.ContractSubcommands()`
  against `mcp.BuildRegistry(...).ToolNames()` in memory, never execs the
  built binary, and emits no textual artifact. There is ALSO no machine-
  parseable command/MCP catalog output from the binary today (`printUsage`
  is hand-written prose). **P13's `skill-drift` job — which must build the
  binary, capture its command/MCP catalog text, and byte-diff against
  `skill/a2ahub/reference/commands.md` — has NO working precedent to reuse and
  must build the catalog-emitting entry point (a lead-designed `internal/cli`/
  `cmd/a2a` seam that enumerates verbs + MCP tools) from scratch.** This is a
  known wave-7 prerequisite, not a spec defect in P13 itself.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->
