<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
<!-- SSOT SOURCE (mate repo). Consumers receive a provenance-stamped copy via `mate pull` — edit HERE, never the synced copies. -->

# CLI doctrine — portable operator-CLI principles

How to build a project-agnostic, modular operator CLI — the same shape, the
same muscle-memory, the same safety posture as `axon` — for any new project.

It is the **distilled architecture** of one reference implementation — axon's
`scripts/axon` + its `lib/` spine — stripped of everything specific to that codebase.
The reference lives in another repo and is named here in bare text on purpose: a
Markdown link into a foreign tree resolves in neither the SSOT nor any consumer
(`_authoring.md` §8, the link policy). Read this file for the *transferable shape*.

> **Thesis.** A single bash binary with a lazy-dispatch core, a flat
> `lib/<tool>/` of one-file-per-command modules, a shared spine (parse · env ·
> confirm · audit · infra), a tiered safety model for destructive ops, a
> tmux-based dev launcher, and a filesystem-driven shell completion — all keyed
> off a project slug it **refuses to guess**.

> **How to read this doc.** It was written *inside* the reference repo and still
> carries its scars: the examples name that repo's paths, its pg/valkey/S3/SOPS
> choices, its `projects/` layout. Those are **one instantiation**, not the
> doctrine. The **bold principle** in each section is what ports; every path,
> tool name, and slug in an example is a value to swap (see the instantiation
> seam below). Where a section still reads as if you are standing in that repo,
> that is legacy phrasing, not a claim about yours.

> **Don't build all 12 sections for a 3-command tool.** Tier by need:
> **Core (day one)** — dispatcher (§1) + flag parser (§3) + project resolution
> (§4) + `dev` launcher (§8) + completion (§9). **Add when you get prod data
> ops** — tiered safety (§6) + audit trail (§7). **Add when you go
> multi-instance** — layered env merge (§5) + onboarding flow (§11). The rest
> (§2, §10, §12) is convention that scales with surface area. Simplicity first;
> grow into the structure.

> **Harness-era note (Δ6).** Once a repo is harness-adopted, the **spine ships
> as a synced shared library** (`mode:sync`): parse/env/confirm/audit + the
> tmux dev-launcher core arrive from the SSOT, and bugfixes propagate on
> `mate pull`; only the `cmd_*.sh` modules stay project-local. The sections
> below remain the doctrine of that shape — and the porting checklist for a
> repo not (yet) on the harness.

---

## 0. When to build one (and when not)

Build a unified CLI the moment a project has **≥2 of**: multiple environments
(local/preview/prod), multiple deployable instances (multi-project), a dev stack
that needs more than one process, or destructive data ops (DB restore, S3 sync)
that you run by hand. Below that bar, a `Makefile` + a couple of scripts is
enough — don't pay the abstraction cost early.

The payoff is **one binary, one audit trail, one mental model**: every project
you own answers to `<tool> dev start`, `<tool> status`, `<tool> push -d -f local
-t prod`, with the same flags and the same guards. That cross-project muscle
memory is the entire point — keep the verb/flag vocabulary identical even when
the implementation behind a verb differs per project.

---

## 1. The dispatcher — one binary, lazy modules

A single entry script (`scripts/<tool>`) that does **only** routing. The
`scripts/axon` dispatcher is the template:

1. **`set -Eeuo pipefail`** at the top. Non-negotiable for an ops tool — a
   silent failure mid-restore is how you lose data.
2. **Re-exec under a modern bash.** macOS ships bash 3.2; associative arrays and
   `mapfile` need 4+. Detect `BASH_VERSINFO[0] < 4` → `exec` through
   `/opt/homebrew/bin/bash` (or `/usr/local/bin/bash`), else fail with an
   install hint. Write the rest of the tool freely against bash-4 features.
3. **Resolve `$0` through the symlink chain** (macOS has no `readlink -f`): walk
   `while [ -L "$src" ]` so the tool works when symlinked onto `$PATH`. Derive a
   `<TOOL>_ROOT` (repo root) from the resolved path and `export` it — every
   module and the completion script keys off it.
4. **Source only the shared spine eagerly** (parse, env, audit, confirm, db).
   Everything else is **lazy**: the `case "$cmd"` arm `source`s
   `lib/<tool>/cmd_<name>.sh` and calls `<tool>_cmd_<name> "$@"`. A 20-command
   tool stays instant because a given invocation loads ~2 files.
5. **Unknown command → exit 2 with a pointer to `--help`.** Never fall through
   to a default that does something.

```
scripts/
  <tool>                      # dispatcher — routing only
  lib/<tool>/
    parse.sh   env.sh         # shared spine: sourced eagerly
    confirm.sh audit.sh
    cmd_dev.sh cmd_push.sh …   # one file per command, lazy-sourced
  completions/_<tool>         # zsh completion
```

**Convention:** every command module exposes exactly one function,
`<tool>_cmd_<name>()`, taking raw `"$@"`. The dispatcher's job is to map a verb
to that function and nothing else. This keeps the dispatcher diff-stable as
commands come and go.

---

## 1a. Bash is the orchestrator, not the engine

**The CLI owns dispatch, flags, project resolution, safety, audit, and UX. The
actual work — DB ops, validation, encoding, data migration — lives in
language-native binaries the CLI shells out to.** Bash stays thin; the logic
stays unit-testable in a real language.

In axon this is pervasive: `feed validate` wraps a Go `cmd/feed-validate`,
`redirects import` wraps a Go `cmd/redirects`, `import wp` wraps a Python
`axon_migrate` package, `regenerate`/`scan` shell into `./bin/api --…`. The bash
module's job for each is: resolve the slug, merge the env, run the safety/confirm
gates, then `exec`/call the native binary with the right DSN and args, and audit
the result.

Why this is the scalability lever:

- **Domain logic gets a real test suite** (Go `_test.go`, pytest) instead of
  untestable bash. The 200-line bash module has no business logic to test beyond
  arg-mapping.
- **The CLI is a stable façade.** You can rewrite the encoder or the importer
  without touching the operator-facing verb or its flags.
- **No reimplementation in two languages.** A validation rule lives once, in the
  binary; the CLI never grows a second, drifting copy in shell.

Rule of thumb: **if a command does more than orchestrate (parse → resolve →
guard → shell-out → audit), the excess belongs in a native binary.** Bash that
starts parsing CSVs, doing arithmetic on rows, or building SQL is a smell — push
it down.

---

## 2. Command taxonomy — group by operator intent

Organise the surface (and the `--help` output) by **what the operator is trying
to do**, not by implementation. Axon's grouping generalises:

| Group | Verbs (examples) | Trait |
|---|---|---|
| **Local dev** | `dev start/stop/reload/status/logs/attach`, `dev-token` | Fast, safe, repeatable, no confirmation |
| **Data & environments** | `pull`, `push`, `sync`, `status`, `refresh` | Cross-env; `push`-to-prod is the dangerous one |
| **Content & media** | `import`, `regenerate`, `scan` | Long-running, idempotent, project-scoped |
| **Deploy & ops** | `deploy status`, `cdn`, … | Mostly read-only inspectors + a few prod toggles |
| **Project config** | `project new/up/validate/scaffold/…` | Onboarding & per-instance lifecycle |

**Verb stability is the product.** `dev start`, `status`, `push -d -f local -t
prod` should mean the same thing in every project you own. The *adapter* behind
the verb changes (one project restores via `pg_restore`, another via a managed
snapshot) — the verb and its flags do not.

**Sub-verbs for stateful nouns.** When a noun has a lifecycle, give it
subcommands (`project new|up|stop|seed|scaffold`, `dev start|reload|stop`)
rather than a flag soup. The module parses `$1` as the sub-verb.

---

## 3. The flag parser — predictable, fail-closed

One shared parser (`scripts/lib/axon/parse.sh`) fills a single
global associative array (`<TOOL>_OPT`) consumed by every command. Rules that
make it safe and ergonomic:

- **A `reset` function seeds every key to its zero value** before parsing, so a
  command never reads a stale flag from a previous call and every key always
  exists (no unbound-variable traps under `set -u`).
- **POSIX bundled shorts** (`-ud` ≡ `-u -d`): walk each char after the dash.
- **Value-flags (`-f`/`-t`/`-p`) are NOT bundleable** and accept both `--from X`
  and `--from=X`.
- **Unknown flag → `return 2`, never silently ignore.** This is the single most
  important rule: a typo'd `--dry-runn` must abort, not proceed with the
  destructive default. Fail-closed everywhere a default could write.
- **Mutually-exclusive flags are rejected explicitly** (e.g. `--originals-only`
  + `--variants-only`), with a one-line reason.
- Unrecognised **positionals** accumulate into an `extra` field so a command can
  still offer `<tool> status mm` positional shorthand.

---

## 4. Project resolution — refuse to guess

The keystone of multi-instance support. A shared `require_project` helper
resolves the target slug in a fixed priority and **errors if it can't**:

```
-p <slug>  >  $PROJECT env  >  first positional  >  the sole project dir
```

(In axon the shared `require_project` helper covers `-p`/`$PROJECT`/positional;
the `project` command adds the sole-dir fallback. Present this as the
recommended *unified* resolution order for a new tool — fold all four into one
helper from the start.)

- **Alias map** for muscle memory: `mm→mobilitymag`, `ts→truesharing`. Unknown
  inputs pass through unchanged so validation still runs.
- **Slug hygiene regex** (`^[a-z][a-z0-9-]*$`) — the same shape used by the
  env-shard directory layout, so a valid slug always maps to a real config path.
- **No silent default.** If nothing resolves, print the four ways to supply a
  slug and exit 2. "Refuse to guess" prevents the worst class of bug: running a
  prod op against the wrong instance.

Every project-scoped command starts with `slug="$(require_project)" || exit $?`.

---

## 5. Layered env model — one merge, one SSOT

Configuration for instance × service comes from **layered shards merged in a
fixed precedence**, never from hardcoded values:

```
deploy/env/_common/.env.<svc>.example     ← shared schema + defaults  (SSOT)
deploy/env/<project>/.env.<svc>.example   ← non-secret per-project overrides
deploy/env/<project>/.env.<svc>.sops      ← SOPS/age-encrypted secrets
```

`scripts/lib/axon/env.sh` merges these into a **mode-600
tempfile**, decrypting secrets with `sops` only when present, and either prints
the path (`load_project_env`) or sources-then-deletes it (`axon_load_env`).
Principles:

- **The merge logic is the single SSOT** — if a `Makefile` also needs the merged
  env, it calls the same function (or a byte-equivalent loop), never a second
  copy of the precedence rules.
- **Secrets never hit disk unencrypted except in a 600 tempfile that's deleted
  on the same call.** Decrypt key-file location has a sane default
  (`~/.config/sops/age/keys.txt`) overridable by env, with an actionable error
  when missing.
- **Local-DB DSN is synthesised from the slug** by convention
  (`<slug>:<slug>_dev@localhost:<port>/<slug>`) and prefers a real per-project
  `.env.<slug>` when one exists — so a `--local` flag can never accidentally
  resolve a prod credential path.
- **Env var > shard > project default > base default.** Hardcoding at the bottom
  what belongs at the top is the anti-pattern this whole layer exists to kill.

Adding a new project becomes **zero code change**: drop its env shards, add its
age recipient, and every command works for the new slug.

---

## 6. Tiered safety for destructive ops

The difference between a toy and an operator tool. Downward/read ops are safe by
default; upward/prod-writing ops pass through ordered gates
(`scripts/lib/axon/cmd_push.sh` is the reference). The tiers, in
order:

1. **Argv guard** — scan for catastrophic tokens (`--delete` on an S3 mirror)
   and abort *before anything else runs*, with an explanation of why.
2. **Default dry-run** — a prod destination shows the diff and writes nothing
   unless `-y` is passed. The safe thing is the default; danger is opt-in.
3. **Slug retype confirmation** — even with `-y`, prompt for the exact slug
   character-for-character (no abbreviations). Defeats a stale `-y` in shell
   history. Non-interactive sessions must set `<TOOL>_PROD_CONFIRM=<slug>` to
   proceed — CI never writes prod by accident.
4. **Time-window / lock guards** — refuse prod writes during a backup cron
   window; acquire the same VPS-side deploy mutex a deploy uses, so a manual op
   can't race an in-flight deploy.
5. **Path guard** — refuse to read/write dump files outside the tool's own
   `.<tool>/` working dir.

**Directionality encodes safety.** Model environments as a rank
(`local < preview < prod`). Downward = pull (always safe). Upward = push (gated).
A `sync <payload> <from> <to>` alias can then route to pull/push purely from the
rank delta — and inherit every guard automatically.

---

## 7. Audit trail — append-only, dependency-free

Every prod-touching invocation appends one **JSONL** record to
`.<tool>/audit.log` (`scripts/lib/axon/audit.sh`):

- `begin` stamps a start time; `emit` writes `{ts, user, project, cmd, result,
  duration_ms, …extra}`.
- **In-place rotation at a byte threshold** (`mv` to `.1`) — no logrotate
  dependency, identical on macOS and Linux.
- **Portable `stat`**: try BSD `stat -f %z`, fall back to GNU `stat -c %s`.
- **Never log secret values** — log variable *names* if provenance matters.
- Pair with a side-channel alert (Telegram/Slack) on prod-write success so the
  team sees the op even if no one's watching the terminal.

This is ~90 lines of bash and pays for itself the first time you need to answer
"who restored prod last Tuesday and how long did it take".

---

## 8. The dev launcher — tmux, reload-not-restart, agent-safe

The command operators live in. `scripts/lib/axon/cmd_dev.sh`
is the richest module; its patterns transfer wholesale:

- **One command boots the whole stack.** `dev start <slug>`: ensure shared infra
  (a single always-on test DB, a mail catcher) → per-project `compose up` →
  wait-for-ready (poll `pg_isready`) → idempotent migrate → conditional seed →
  open a tmux session with one pane per process. Replaces the manual
  `compose up` + `make dev` + split dance.
- **Detect freshness BEFORE migrating.** If the migrate step creates the
  bookkeeping table, probing it afterwards always looks non-fresh and seed never
  runs — check row-count first, then migrate, then seed-if-fresh.
- **Stamp each pane with a role** (`@<tool>-role back|front`) so later commands
  find the right pane structurally, not by guessing index/position.
- **`reload` ≠ `restart`.** `reload` does `tmux respawn-pane -k` — it **reuses
  the pane**, so the operator's window/layout survive and only the chosen server
  bounces. `restart/stop/start` tear the session down. This distinction is
  load-bearing for agent-safety: **an agent must only ever `reload`** — killing
  the session destroys a window the human is looking at.
- **TTY-aware attach.** Interactive + not `--detach` → `exec tmux attach`. Non-
  TTY (an agent) → stay detached, print the attach hint and log paths. The tool
  behaves correctly whether a human or a script invoked it.
- **In-tmux ergonomics**: a status-bar with **clickable chips**
  (`[front] [back] [▾ more]`) and **guarded keybindings** (`prefix r/f/b/F`) that
  dispatch back into `<tool> dev reload`. Guard every binding with
  `if-shell '#{m:<tool>-dev-*,#{session_name}}'` so stock tmux behaviour is
  untouched outside the tool's own sessions.
- **Dev logs go to known, truncated-on-start paths** under a gitignored
  `/.artifacts/<producer>/dev-<slug>.log`. Agents (and `dev status`) read the
  file directly — **never restart a server merely to see its output**.
- **`dev status`** is the fastest health snapshot: infra up?, tmux alive?,
  ports/URLs, HTTP liveness, last N log lines per pane — no slug = all projects.

---

## 9. Shell completion — filesystem-driven, zero hardcoded slugs

A zsh `#compdef` (`scripts/completions/_axon`) that makes the tool
feel native. The transferable rules:

- **Enumerate projects from the filesystem at completion time** — glob
  `deploy/env/*/`, fall back to a generated `projects.json`. **Never hardcode
  slugs in the completion** — a new project appears in tab-complete the moment
  its config dir exists, no completion edit.
- **Derive the repo root from the completion file's own path**
  (`${${(%):-%x}:A:h:h:h}`) so completion works without the env var set.
- **One `_<tool>_cmd_<name>` function per command**, mirroring the dispatcher, so
  flags/env-values/project-slugs complete contextually.
- Keep an **enum list (`prod preview local …`) in one place** in the completion
  and reuse it across `-f`/`-t`.
- **Completion is part of the contract**: a docs rule should require updating
  `completions/_<tool>` + the reference doc in the same change as any new
  command or flag. Otherwise it silently rots.

(Provide a bash-completion sibling if your audience isn't all-zsh; the
filesystem-enumeration principle is shell-agnostic.)

---

## 10. UX/DX conventions — the small things that compound

- **TTY-aware colour.** Colour codes only when `[ -t 1 ]`; piped/redirected
  output is clean plain text so it greps and parses.
- **A first-class `--help`** grouped by intent (see §2), with a Common-flags
  block and a runnable Examples block. Per-command `<tool> <cmd> --help` too.
- **`--json` on every read/inspect command.** Human table by default, machine
  JSON on demand — wires straight into CI health checks (`… --json | jq -e`).
- **`--dry-run`/`-n` everywhere a write happens.** The reference impl makes it
  the *default* for the most dangerous target.
- **Idempotency as a hard rule.** Every mutating op (migrate, seed, scaffold,
  import, scan) must be safe to re-run. `ON CONFLICT DO NOTHING`, "create if
  missing", "upsert" — never "fails on second run".
- **Aliases for muscle memory**: short project aliases (`mm`), a direction-
  routing verb (`sync`), and **thin `make <tool>-*` wrappers** for operators who
  live in `make`. The wrapper just shells into the binary — the binary stays the
  SSOT.
- **Actionable errors.** Every failure prints the fix, not just the symptom
  ("age key missing at X → see secrets.md", "slug required → here are the 4
  ways"). An ops tool's error messages are its primary docs.
- **Portability discipline**: bash-4 re-exec, BSD-vs-GNU `stat`, `readlink`-chain
  resolution, `mktemp` + `chmod 600`. Assume macOS operator, Linux CI/VPS.

---

## 11. Onboarding a new instance = config, not code

The acid test of the whole design: **adding a project must touch zero `.sh`**.
The axon flow generalises to:

```
<tool> project new <slug>          # scaffold config + allocate free ports
<tool> project scaffold <slug> --fill   # create required per-project "homes"
# edit <slug>'s config: branding, enabled modules, …
<tool> project validate -p <slug>  # check config closure/consistency
<tool> project up <slug>           # compose + migrate + seed
<tool> dev start <slug>            # everyday launcher
```

- **A single source of truth per instance** (`projects/<slug>/config.yaml`) that
  the tool *reconciles* into every derived artifact (build tags, compose files,
  the slug→domain map) — so the running system can never drift from the config.
- **Port allocation is automatic** (next free past the current max), not a
  human-tracked spreadsheet.
- **A parity contract** (`<tool> project scaffold`) enumerates the required
  per-instance files and can create the missing ones, gated by a `make`
  check — so "what does a complete project need?" is executable, not tribal.

---

## 12. Keep it honest — docs, drift gates, scope

- **The reference doc and the completion are part of the command's definition.**
  A repo rule should fail review if a new `cmd_*.sh` lands without updating
  `completions/_<tool>` and the operations doc.
- **Generated artifacts carry a `GENERATED — do not hand-edit` banner** and a
  `make <x>-drift` gate that fails CI when the source-of-truth and the rendered
  output disagree.
- **Resist scope creep in the dispatcher.** New behaviour goes in a module; the
  dispatcher only ever learns a new `case` arm. The spine (parse/env/confirm/
  audit) grows only when a pattern is needed by ≥2 commands — rule of three,
  except for a guaranteed platform seam (a new always-multi concern like env
  layering), which is designed for N from day one.

---

## Instantiation seam

What a project swaps into this frame. Every example above names the reference repo's
values; none of them is the contract:

- **The tool name and its slug vocabulary** — `cli_name` and the project's nouns. The
  *grammar* (noun-verb, §2) and the *refusal to guess a slug* (§4) are the principles; the
  words are the project's.
- **The module list** — which `cmd_*.sh` exist. The doctrine mandates one file per command
  and lazy dispatch (§1), not which commands you have.
- **The native engine behind the orchestrator** (§1a) — Go, Python, whatever. Bash
  orchestrates; the language that computes is a value.
- **The env layout and secret store** (§5) — the layering is the principle; `_common`,
  SOPS, and the file tree are `env.md`'s instantiation seam, not this one's.
- **The safety thresholds** (§6) — which ops are destructive, what the dry-run default is,
  which env var overrides it non-interactively. Tiering is the principle; the tiers are the
  project's risk model.
- **The audit log's path and rotation size** (§7).
- **The dev launcher's process map** (§8) — what runs in which pane.

**What does NOT swap:** unknown command → **exit 2** (§1, item 5), unknown flag → **fail-closed**
(§3), a slug that is not resolvable → **refuse, never default** (§4). Those three are the
doctrine; a project that "adapts" them has adopted nothing. (mate's own CLI collapsed the
first one into exit 1 until v0.76.0 — in the very tool that ships this doctrine.)

## Anti-patterns

| ❌ | ✅ |
|---|---|
| Unknown command exits 1 like any other failure | Exit **2** with a pointer to `--help` — a typo and a failure are different aborts (§1, item 5) |
| Unknown flag is ignored and the command runs anyway | Fail-closed: refuse to run a command you did not fully understand (§3) |
| No slug given, so operate on "the obvious one" | **Refuse to guess** — a silent default is how you deploy to the wrong project (§4) |
| Domain logic accretes in bash | Bash orchestrates: parse → resolve → guard → shell out to the native engine (§1a) |
| A destructive op runs on the argv it was given | Tiered safety: argv guard → dry-run default → retype the slug → lock/path guard (§6) |
| Completion hardcodes the project list | Drive completion from the filesystem — zero hardcoded slugs (§9) |
| Secrets land in the audit log "for debugging" | Append-only JSONL, never secrets (§7) |
| Every command re-parses flags its own way | One shared parser, one grammar, one failure mode (§3) |

## Checklist — porting this to a new project

- [ ] Single dispatcher: `set -Eeuo pipefail`, bash-4 re-exec, symlink-chain
      root resolution, lazy `source` per command, unknown-cmd → exit 2.
- [ ] `lib/<tool>/` with `cmd_<name>.sh` (one fn each) + spine: `parse env
      confirm audit` (+ `db`/`infra` as needed).
- [ ] Commands **orchestrate, don't compute**: parse → resolve → guard →
      shell-out to a native binary (Go/Python) → audit. No domain logic in bash.
- [ ] `<TOOL>_OPT` parser: reset-to-zero, bundled shorts, `=`/space value flags,
      **unknown-flag → fail-closed**, mutual-exclusion checks.
- [ ] `require_project`: `-p > $PROJECT > positional > sole dir`, alias map,
      slug regex, **no silent default**.
- [ ] Layered env merge (`_common` + per-project + SOPS) → 600 tempfile, one
      SSOT shared with `make`.
- [ ] Tiered prod safety: argv guard → default dry-run → slug retype →
      window/lock guard → path guard; non-interactive override via env var.
- [ ] JSONL audit log, in-place rotation, portable `stat`, never secrets.
- [ ] `dev` launcher: one-command boot, role-stamped tmux panes,
      **reload≠restart**, TTY-aware attach, known truncated log paths,
      `dev status`.
- [ ] zsh completion: filesystem-enumerated projects/slugs, root from `%x`,
      per-command arg specs.
- [ ] UX: TTY-aware colour, grouped `--help`, `--json` on reads, `--dry-run`,
      idempotency, `make <tool>-*` wrappers, actionable errors.
- [ ] Onboarding = config only: `project new/scaffold/up`, auto port alloc,
      parity gate. Zero `.sh` edits to add an instance.
- [ ] Docs+completion drift gate; dispatcher stays routing-only.
```

---

## Cross-links

[interface doctrine](interface.md) (the operator CLI is the *second face* of the uniform
interface — this doctrine says how to build it, that one says what a caller may assume) ·
[validation doctrine](validation.md) (fail-closed is the same principle pointed at gates:
a parser that ignores an unknown flag and a gate that exits 0 on its own error are one
failure) · [env doctrine](env.md) (§5's layered merge is that doctrine's subject; the CLI
is one of its two readers).
