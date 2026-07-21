---
name: mate-adopt
description: >-
  Bring an existing (brownfield) repo into conformance with the harness doctrines
  it already seeds. Use on a repo that has its own Makefile/skills/docs and needs
  to obey the make-ABI, the config seam, and the sync contract without a rewrite.
  Produces a conformance report, fills .mate/config.yaml, runs the mechanics, and
  files renames as tracked backlog rows — never drive-by. Re-running is a
  conformance re-audit.
requires_capabilities: [run-shell, read-files, edit-files]
cites: [code/interface.md, code/validation.md, code/structure.md]   # doctrines this skill restates — citation-lint checks they resolve
mate_synced: v0.98.0
---

# mate-adopt

> **Thesis.** Greenfield is the easy case; the fleet is ~fifteen *existing* repos,
> so **brownfield is the main path**. Adoption is the *judgment* half of
> bootstrapping — `mate new` lays the mechanical skeleton (idempotent in a
> non-empty repo), but deciding what each existing artifact *is* (keep / already
> covered by core / worth promoting) and what must be *renamed* to conform is
> reasoning, so it is a skill. The rule that keeps adoption honest: **surface
> divergence as a tracked report and file renames as backlog rows — never a
> silent rewrite of the project's own work.**

## When to run

- A repo has its own harness surface (Makefile targets, skills, docs) and you want
  it to obey the doctrines it distributes.
- After a breaking harness release, to re-audit an already-adopted repo.
- Before promoting an artifact out of a repo — adoption tells you what's local vs.
  duplicate-of-core.

Skip for a truly empty repo with an obvious stack — that's plain `mate init`, and
you pass the facets straight as flags (`--lang go --framework nuxt --kind web-app`).
Adopt earns its keep when there is existing structure to reconcile, **or** when even
a greenfield stack is non-trivial enough that you want the facets *inferred from the
repo and confirmed* rather than typed blind — the interview proposes the vector, you
approve it, and the mechanics (step 5) seed it in one pull.

**Refuse on the SSOT itself.** If the target repo holds `harness/manifest.yaml`,
it is the mate source, not a consumer — stop before writing anything. Adoption's
step 4 writes `.mate/config.yaml`, and that file is the consumer-checkout marker
the fleet probes key on: a stray one would surface the SSOT as an undeclared
consumer. (The binary guards its own verbs the same way — this mirrors
`refuseIfSSOT` at the skill layer, where the write happens earlier.)

## The procedure — in order

1. **Inventory.** Enumerate what the repo already has: Makefile targets, the
   agent-config tree (skills, rules, agents, hooks — wherever the active provider
   loads them), the docs tree, generated artifacts, the operator CLI. Read, don't
   assume.
2. **Generate the conformance report.** This is the deliverable. Emit the
   **make-ABI delta table** (interface.md §1) — three columns, one row per
   canonical surface that diverges:

   | Canonical | This repo today | Conformance action |
   |---|---|---|
   | `check` = the ceiling | *(what plays that role here)* | *(rename/alias, tracked)* |
   | `fmt` / `clean` / `gen` | *(local names)* | *(alias to canonical)* |

   Then the **docs-skeleton delta** — check the repo against the structure
   standard's homes table (`.mate/doctrine/code/structure.md` §1: status, backlog,
   decisions, architecture, features, audits, operations, inbox, artifacts,
   harness), resolving each home seam-first (`paths:` override, then the default).
   The three FILE homes seed automatically at pull (`mode: template` — absent →
   laid unstamped; present → adopted untouched); the delta therefore reports dir
   homes, seam overrides, and the artifacts home + its clean target, never
   re-lays a file. And **facets detection** (infer
   `lang` / `framework` / `kind` / `infra` from the repo for the config seam —
   `go.mod`→`lang:go`, `nuxt.config`→`framework:nuxt`, and so on; `framework` is the
   axis that drives the `when:` pull filter, so a missed one silently skips a whole
   profile). **Propose, then confirm with the operator** — the repo evidences the
   stack, but the operator owns the final vector. A non-vacuous
   report always produces rows for its own repo; "already conformant" is a valid,
   and the idempotent, result.
3. **Classify every existing artifact** into exactly one of: **local** (project-
   specific, stays and is declared), **duplicate-of-core** (a hand-rolled copy of
   a Tier-0 artifact → retire the local, pull the synced one; expect a *collision*
   the pull surfaces — resolve delete-local → re-pull → stamped), or **promote
   candidate** (genuinely reusable → file for `/mate-promote`, don't lift it here).
4. **Fill `.mate/config.yaml`.** Write only the *variable* keys the seam defines —
   `cli_name`, `providers`, `facets`, commit `scopes`, coverage floors,
   `env.schema_dir`, standard `paths`. **No `commands:` / `check:` map**: the
   make-ABI is mandatory, so skills hardcode `make check` and the Makefile is the
   adapter. Config carries the variable; the ABI carries the invariant.
   **`providers:` is where you ask which environments this repo is actually opened
   in** — list every one (a pull compiles through each and locks the union, so each
   provider's surface is drift-gated). Omitting the key defaults to one provider;
   an unlisted provider is served *nothing*, silently, from that environment's point
   of view — so confirm it with the operator rather than inferring it from the repo.
5. **Run the mechanics.** `mate new` (alias `mate init`) is idempotent in a
   non-empty repo — it create-if-absent seeds the agent-config, runs the first
   transactional `pull`, and registers the project in the SSOT (textual append —
   the curated registry comments survive). **Always pass `--headless`** — a bare
   `mate init` opens the interactive wizard for a *human*; `--headless` is the
   agent/non-interactive contract, reading only the flags you pass (and
   `--source`/`$MATE_SSOT_REMOTE` for the SSOT), never a prompt. Without it a
   non-terminal stdin errors by design and a terminal one would drop you into the
   form. **Pass the facets you confirmed in step 2/4 as flags** —
   `mate init --headless --lang go --framework nuxt --kind web-app` — so they seed
   into BOTH the config seam and the registry entry and the FIRST pull lands the
   right profiles (no placeholder vector, no corrective second pull). The flags are
   the mechanical contract this interview drives; the CLI takes only the explicit
   values you decided, never a repo probe of its own. It then **reports** the stack-specific follow-ons it does not
   do for you — claim a port block, wire `make mate-check`, review the seeded
   docs skeleton and declare the artifacts home — because those depend on whether
   the repo serves and how its build is shaped. Do those next, aligning the
   interface surface (delegating make-ABI targets, the CLI spine, the remaining
   structure homes) to what the repo actually is.
6. **Check the entry topology (the composer seam, v0.65.0).** The healthy shape
   for a multi-provider repo: the project brief lives in ONE file the repo owns
   (e.g. `docs/brief.md`), declared as `entry.brief:` in `.mate/config.yaml`;
   `CLAUDE.md` is the one-line `@<brief>` import (consumer-owned — mate reports
   this line, never writes it); the pull composes the brief into the no-imports
   provider's entry as the `#project-brief` block. REPORT as migration steps any
   divergence: a `CLAUDE.md = @AGENTS.md` import (loads the reflex block twice),
   a brief hand-written inside `AGENTS.md` (move it to the brief file and pull),
   or a declared brief with no `CLAUDE.md` import (Claude never sees it).
7. **File renames as tracked backlog rows — never drive-bys.** A canonical rename
   (e.g. `all-check`→`check`) rewrites every doc/rule/muscle-memory reference and
   changes what a human types daily — a real retraining cost. Capture each as a
   `harness-backlog` row with the reference list, and let it be done deliberately,
   not silently mid-adoption.

## Idempotent re-audit

Re-running `/mate-adopt` on an adopted repo is a **conformance re-audit**: the report
should come back with an empty (or shrinking) delta and classify no new
duplicates. A repo that reports "conformant" on re-run is the acceptance signal
that adoption held. If new divergence appears, it's real drift — a row, not a
surprise.

---

This skill reads `${CLAUDE_PROJECT_DIR}/.mate/config.yaml` for what genuinely
varies (names, scopes, paths, floors) and hardcodes `make check` for what doesn't.
It speaks semantic model tiers, never provider model ids or provider-only paths.
Authority: the interface doctrine (`interface.md`, the make-ABI + config seam) and
the sync contract (architecture spec §5d — `mate new` / collision policy).
