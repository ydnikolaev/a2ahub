<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
# Onboard a project

**What it is.** Bringing a new (or existing) repo under the mate harness — so it
pulls the shared doctrine, skills, and gates, and conforms to the make-ABI and the
docs/registry conventions.

**When to use.** A repo that has no `.mate/` yet, or a brownfield repo with its own
hand-rolled harness you want to reconcile against the SSOT.

**When NOT to use.** For a throwaway script or a repo that will never carry gates
or skills — the harness earns its weight only once there is a surface to guard
(see the doctrine's §0 tiering).

**How.**
1. Run the `/mate-adopt` skill in the target repo. It inventories the repo, produces a
   conformance report (the make-ABI delta, the docs-skeleton delta, the facet
   list), and classifies each local artifact as local / duplicate-of-core /
   promote-candidate.
2. Fill `.mate/config.yaml` with the project's *variable* keys only (cli name,
   providers, facets, commit scopes, coverage floors, paths) — never a `commands:`
   map; the make-ABI target names are fixed by convention.
3. Run the mechanics (`mate new` / first `mate pull`), review the working-tree
   change like any PR, and commit it.
4. Wire `make mate-check` into the repo's `check` ceiling so drift reddens CI.

**Which providers does the repo run?** `providers:` in `.mate/config.yaml` lists
every provider environment you open this repo in. A pull compiles the manifest
through each one and locks the *union*, so every provider's surface is drift-gated:

```yaml
providers: [claude-code, codex]   # both surfaces land; drop one and its files are pruned
```

It is a property of the **repo**, not of the invocation — a project pull takes no
`--provider` flag (an invocation-scoped provider would rebuild the lock from a
one-provider plan and leave the other provider's files on disk, unmanaged). Omit
the key and you get `claude-code`. `mate new --providers claude-code,codex` seeds it.

**The docs skeleton seeds itself — once.** The first pull lays the structure
standard's file homes (`docs/status.md`, `docs/backlog.md`, `docs/decisions.md`)
*only if you don't have them at the default paths* (v1 seeds defaults-only — a
`paths:`-relocated home still gets the default seeded if that path is empty),
unstamped — they are yours from birth: edit,
restructure, or delete them and mate never touches them again (a pre-existing
file is adopted untouched). Dir homes (`features/`, `audits/`, …) are created by
the tools that write there; the full table + the `paths:` override seam live in
the structure doctrine (`.mate/doctrine/code/structure.md`).

**Each provider gets the same rules, in the shape it can load.** The always-on
reflexes land as files under `.claude/rules/` for Claude Code, which auto-loads
them. Codex has no rules directory at all, so the same rules are composed into one
managed block in `AGENTS.md` — its only always-on channel. Both are drift-gated; the
pull reports the footprint **per provider**, because a session opens one provider and
pays one column.

`AGENTS.md` is *your* file: mate merges its block in and leaves every other line
alone (it creates the file only if you have none). Two things follow.

- **Codex reads `AGENTS.md`; Claude Code does not** ("Claude Code reads `CLAUDE.md`,
  not `AGENTS.md`" — Claude Code docs, *memory*). So a repo you open in both wants
  its project brief reaching both — and that is the **entry composer's** job
  (v0.65.0), not a hand migration.
- **Never make `CLAUDE.md` import `@AGENTS.md`** — Claude would load the reflexes
  **twice** (once from `.claude/rules/`, once inside the imported block).

**The entry composer — one brief, both providers, no double-load.** Keep the
project brief in ONE file you own and declare it:

```yaml
# .mate/config.yaml
entry:
  brief: docs/brief.md
```

```
docs/brief.md  (yours — the ONE source)
   ├─→ CLAUDE.md:  "@docs/brief.md"          ← ONE line, yours; mate never writes it
   └─→ AGENTS.md#project-brief               ← inlined managed block, composed on pull
       AGENTS.md#mate-reflexes               ← the always-on rules block (as before)
```

The loop: **edit `docs/brief.md` → `mate pull` recomposes.** An edited brief that
has not been re-pulled shows in `mate status` as `brief-stale` (the drift gate sees
it); removing the `entry.brief` declaration retracts the block on the next pull —
nothing lingers. Claude reads the brief via your import + the rules as native
files; Codex reads the composed `AGENTS.md`. The same words, one home, zero
duplication.

**Typical mistakes.**
- Putting project values into a skill body instead of `.mate/config.yaml` (the
  cardinal rule: principle in the doctrine, value in the config).
- Reaching for `mate pull --provider` to "switch" providers — it is refused. Edit
  `providers:` instead; that is the seam.
- Skipping the `make mate-check` wiring — then drift is invisible until a human
  notices.
- Expecting a collision to auto-resolve. If a skill name collides with an existing
  local one, the pull fails closed; resolve delete-local → re-pull → stamped.

**Links.**
- [`/adopt` conformance](../../../doctrine/code/interface.md)
- [validation doctrine (why gates)](../../../doctrine/code/validation.md)
- [CLI reference](../reference/cli.md)
