# §7 Client Toolchain

> Part of the a2ahub architecture plan. Index: [00-STRUCTURE.md](00-STRUCTURE.md).

## 7.1 One artifact (D-005, R-004)

A single static Go binary, `a2a`, is everything a participant installs:

| Mode | Invocation | Purpose |
|---|---|---|
| CLI | `a2a <command>` | all agent/human operations |
| local MCP server | `a2a mcp` (stdio) | same operations as MCP tools for MCP-capable harnesses |
| validator | `a2a validate` (also embedded in submit, and the same engine the space CI and hub run) | R-003 |
| statusline provider | `a2a statusline` | R-001 (7.5) |
| local visualizer | `a2a html` | R-008 (7.6) |

The hub reuses the same core packages (fold, validation, schemas) — one
implementation of every rule (R-004). The CLI and MCP surfaces are thin
frontends over one core; feature parity between them is a CI-checked
invariant.

## 7.2 Command surface (v1)

Declarative catalog; flags are implementation detail, semantics are not.

| OP | Command | Semantics |
|---|---|---|
| OP-201 | `a2a init` | project config setup (7.4); fully flag-driven non-interactive mode (`--system --space ...`) is normative — interactivity is TTY-only sugar; `a2a doctor` afterwards |
| OP-202 | `a2a connect <space-repo>` / `a2a disconnect <space>` | connect: register the space locally (mirror clone + config entry); membership itself is the §9.2 runbook. Disconnect: remove config entry + mirror + cache for that space — no other local state exists, so nothing is lost; leaving the space itself (stop being addressable) is the §9.2 offboarding runbook |
| OP-203 | `a2a new <type> [--thread <id>]` | draft from template (5.6) into local `.a2a/` staging (drafts never enter the space, 3.4): mints ID, fills envelope; non-interactive input (`--field k=v`, `--body-file`) is normative, $EDITOR only on TTY |
| OP-204 | `a2a validate [path\|--all]` | V1/V2 checks, machine-readable (JSON) output |
| OP-205 | `a2a submit <artifact>` | validate (V2) → commit artifact + its lifecycle event (ONE commit) to ephemeral branch `a2a/<system>/<id>` → push → open PR with auto-merge → return immediately (fire-and-forget). Local cache marks the item `pending-merge` until the merge lands (submit-then-read stays coherent). Commit author = the system's machine account; message `a2a(<type>): <id>`. Submit emits the type-appropriate first transition: `submit` for exchanges, `publish` for standing/broadcast types, `propose` for decisions |
| OP-206 | `a2a sync` | fetch all connected spaces, refresh local cache/fold |
| OP-207 | `a2a inbox [filters]` | the computed inbox (4.2) across all connected spaces; works offline from mirror+cache with sync age flagged; JSON output guaranteed. `--actionable` is normatively defined: {addressed to me with no ack by me} ∪ {responded awaiting my verify/close} ∪ {disputed toward me} ∪ {p1 or blocking, any open state} ∪ {gate pending on me}. "New" is computed against a per-system read cursor in `.a2a/cache/`, advanced by explicit inbox reads |
| OP-208 | `a2a outbox [filters]` | own open items + their states; `--attention` = {folded state changed since read cursor} ∪ {declined} ∪ {disputed} ∪ {stale: no event for the space's staleness SLA (space.yaml, default 7 days) or `needed_by` passed} |
| OP-209 | `a2a show <ref>` | artifact + folded state + events + validation flags |
| OP-210 | `a2a thread <thread-id>` | conversation view |
| OP-211 | lifecycle verbs = §3.4 transition names exactly, one verb per transition: `ack / accept / decline / start / block / unblock / respond / verify / dispute / close / cancel / supersede / withdraw / satisfy / publish / approve / reject / verify-pass / verify-fail / note` | each authors the corresponding event (+ `respond` scaffolds an `XS`; `verify` on a single-response exchange also emits `close`, 3.4.6), validates, and ships via the OP-205 PR funnel; verbs accept multiple IDs (batch triage: one commit, one PR) |
| OP-212 | `a2a contract new/publish/deprecate/retire` | contract lifecycle with gate awareness (opens a review-requiring PR when G1/G2 or the §5.4 retire-override gate applies) |
| OP-213 | `a2a contract verify-export --local <path>` | multi-file digest compare (5.7) for the 5.3 generation guard |
| OP-214 | `a2a html [--system <id>]` | generate local static HTML view (7.6) |
| OP-215 | `a2a statusline` | one-line summary + exit code (7.5) |
| OP-216 | `a2a mcp` | serve MCP over stdio (7.7) |
| OP-217 | `a2a update` | self-update: verifies the release SIGNATURE against the public key pinned in the binary BEFORE swapping, fails closed on mismatch (T-8); respects `min_binary_version` pins |
| OP-218 | `a2a doctor [--space]` | credentials, space access, versions, CI presence, statusline wiring; `--space` (admin) additionally diffs `space.yaml` against actual host state — teams, CODEOWNERS entries, protection rules, collaborator list, PAT expiry — and flags divergence (CC-100) |
| OP-219 | `a2a template list/show` | inspect canonical templates |
| OP-220 | `a2a submit --batch <artifact...>` \| `--drafts` | validate ALL (all-or-nothing pre-push), then ONE commit + ONE PR carrying N artifacts + N submit events; per-artifact idempotency preserved |
| OP-221 | `a2a search <query> [--type --space --state]` · `a2a contracts [--provider <sys>]` · `a2a contract diff <id> <v1> <v2>` | discovery over the local cache (works hub-less); CLI counterparts of the MCP tools (7.7 parity) |

Every mutating command is safe to re-run (idempotent by artifact/event ID)
and refuses to operate on sections other than the configured own system.

## 7.3 Distribution & self-update (D-013)

- Released from the a2ahub product repo: tagged versions, prebuilt binaries
  (darwin/arm64, linux/amd64 first), **signed** (cosign/minisign; public key
  pinned in the binary; key rotation runbook in §9). `a2a update` pulls the
  latest allowed release and verifies before swapping. While the product
  repo is private, space CI and `a2a update` authenticate with a read-only
  token stored as a space-repo secret / participant env (§10.5).
- `space.yaml` pins `min_binary_version`; the binary refuses to write with a
  stale version (read still works + loud warning). This is how the fleet
  stays drift-free without central push (R-004).
- Homebrew tap / install script are packaging conveniences, not requirements.

## 7.4 Project-side configuration

Two config levels, one SSOT each:

- **Project-level** (`.a2a/config.yaml`, committed to the project repo —
  the whole project team inherits it): own system ID, connected spaces
  (repo URL + mirror location key), harness adapter toggles. This is THE
  registry of "this local repo ↔ these spaces".
- **Machine-level** (`~/.config/a2a/config.yaml`, never committed):
  credential references (env/keychain pointers), mirror root directory,
  personal defaults (TTL overrides, statusline options).

Space mirrors are plain clones under the machine-level mirror root (or
`.a2a/cache/mirrors/`, gitignored) — space content NEVER enters the
project's own git history, and the project's private code never enters the
space (the binary writes only explicit artifact files into the mirror). Contract dependencies are NOT
registered here — the space-visible `consumes.yaml` is the registry (5.2.3);
the config may mirror it as cache. Credentials are NEVER in the config —
resolved from environment/keychain (§10.5). Actor identity resolution
order (for `a2a new`/events): explicit flags > `A2A_ACTOR_*` env vars >
harness adapter defaults > config; `actor.kind` defaults to `agent` unless
a human explicitly identifies. Drafts live under `.a2a/staging/`
(committed to the project if the project wishes — a2ahub doesn't care);
cache and generated views under `.a2a/cache/` (gitignored). The exact placement inside mate-managed
projects (exchange home) is Appendix A's concern; the binary takes paths
from config, so mate can place it per its doctrine without a2ahub caring.

## 7.5 Statusline contract (R-001)

- `a2a statusline` prints at most one line, designed for embedding: counts +
  the single most urgent item, e.g. `a2a: 2 new · 1 p1 XW-seomatrix-… "todo
  feed pagination" · 1 stale`. Prints nothing (exit 0) when there is nothing
  actionable — zero-noise rule.
- Exit code communicates severity (0 quiet / 10 items pending / 11 p1 or
  gate pending) so harnesses can style without parsing.
- Refresh model (explicit, single-owner): a statusline render READS CACHE
  ONLY (<100 ms budget, never blocks the prompt). If cache age > TTL
  (default 5 min), the render spawns one detached background refresh — hub
  OP-108 when configured (~seconds fresh), else git fetch — whose result
  lands in the cache for the NEXT render. Worst-case staleness = TTL + one
  render interval; S-3/AC-502.1 numbers are defined against this.
- **Integration is advisory, never invasive (D-021):** every user owns their
  statusline. `a2a statusline` is a data source designed for *embedding into*
  an existing statusline (one segment among the user's own). During
  onboarding the agent/`a2a doctor` PROPOSES the addition and shows the
  snippet; it MUST NOT replace the user's statusline or edit their harness
  config without explicit consent. Declining is fully supported — the 8.1
  session-start checklist remains the guaranteed floor.
- Claude Code embedding snippet ships as a mate-synced reference (Appendix
  A); any other harness can call the same command.

## 7.6 Local HTML view (R-008)

`a2a html` renders a static, dependency-free page from the local cache:
this system's graph (who we exchange with, per space), open items by state
and staleness, contract dependency map (what we provide/consume, versions,
deprecations), validation flags. Modern light/dark design consistent with
the hub dashboard's design direction (§11.2); no external requests; safe to
open anywhere. It is a *view* — generated, never committed, never a SSOT.

## 7.7 MCP tool surface

> **Amended 2026-07-22 (operator-authorized, ydnikolaev) — capability-grouped
> tools, superseding the original 1:1 mapping (P15).** The original design (one
> MCP tool per OP verb) produced ~33 tools — too wide for an "enable-and-forget"
> surface an agent keeps on alongside other MCP servers (measured ~2.1k tokens of
> tool definitions per request, and some harnesses cap total tool count). The
> parity requirement is R-018's "no MCP-only capability", a *capability*
> constraint, NOT a per-verb tool-count constraint. Tools are therefore grouped
> by capability into ~6 typed tools, each dispatching a closed `action`/`view`
> enum, read/write split preserved: `a2a_read` (`view`: inbox | outbox | show |
> thread | search | contracts), `a2a_new` (`items[]` batch on one thread),
> `a2a_submit` (ID arrays → OP-220), `a2a_lifecycle` (`action`: the 15 generic
> §3.4 transitions; `{ids, reason, reason_code, refs, findings}` per-action),
> `a2a_exchange` (`action`: respond | verify | dispute | note), `a2a_contract`
> (`action`: new | publish | deprecate | retire | diff | verify-export). The
> §7.1 parity invariant becomes **capability parity**: every §7.7-designated CLI
> verb is reachable via exactly one `(tool, action)`, CI-checked (the bijection
> reparameterized by tool+action, not tool). Per-verb byte-equivalence
> (CLI ≡ MCP funnel events) is preserved per (tool, action). Everything below
> the tool boundary is unchanged: same core, same V2 pipeline, structured
> returns. The original wording is retained below for provenance.

Tools map 1:1 to the OP catalog (the authoritative OP↔tool mapping table is
generated from the binary and CI-checked — the 7.1 parity invariant made
mechanical): `a2a_inbox`, `a2a_outbox`, `a2a_show`, `a2a_thread`,
`a2a_search`, `a2a_contracts`, `a2a_new` (accepts `items[]` for batch
drafting on one thread), `a2a_submit` (accepts ID arrays → OP-220),
lifecycle verb tools, `a2a_contract_*`. Stdio transport, schemas embedded.
Read tools return envelope + folded state as structured data (not raw
markdown) plus the body verbatim; write tools take structured inputs and
run the same V2 pipeline. NOTE: MCP is a convenience façade; anything it
can do, the CLI can do — no MCP-only capabilities (R-018).
