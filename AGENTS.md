<!-- MATE:mate-reflexes START -->
Always-on reflexes (mate-managed; full treatment in the mate doctrine corpus under `.mate/doctrine/`):

# Validation discipline — verify before you conclude, guard what you verified

Always-on. On every claim you build on and every invariant you touch:

- **Verify before you conclude.** A claim about a system you don't own — a library, a provider, a version, a flag — rests on that system's own current source or an empirical check, never on memory; verify at the point of assertion, before the conclusion is load-bearing.
- **Claim only the strength you checked.** "Proven / verified / confirmed" must match what you actually ran; name degradation and uncertainty instead of rounding up, and when correcting a wrong claim re-anchor on the source rather than swinging to the opposite one.
- **Guard what you verified once (the maniac loop).** A hand-verification you did once is a gate you haven't written yet — new entity, SSOT, boundary, or convention means propose the gate, don't wait to be asked.

Full treatment: the validation and verification-honesty doctrines (`.mate/doctrine/`).

# Framework-first — use the canon, custom code is debt

Always-on. When you reach for custom code — a plugin, wrapper, low-level flag, or hack — to make something behave the way a framework or library might already own:

- **Search the canon first, in order.** Framework docs → library docs → repo precedent → only then custom. "I didn't know it could do that" is the normal result of searching, not an embarrassment.
- **Configure at the highest-level knob that owns the concern.** Don't drop to a lower layer (raw runtime flag, bundler option) to force behavior the higher one governs — that coupling breaks silently on upgrade.
- **Treat custom code as debt to retire, not precedent to extend.** An existing wrapper never justifies the next one; removing custom code beats adding it.
- **Round-N stop.** After a few failed speculative fixes, stop guessing — switch to a minimal reproduction and the canonical doc read end-to-end.

Full treatment: the framework-first doctrine (`.mate/doctrine/`); the concrete search order, config layering, escape hatches, and Round-N threshold for a given stack live in its `.mate/profiles/<stack>/` refinement.

# Commit hygiene — one intent, this session's, in the project's convention

Always-on (you commit outside an explicit `/commit` too, so the reflex can't wait to be summoned). On every commit:

- **Stage only what this session touched.** Never `git add -A` / `.` / `*` — a blanket add sweeps in another terminal's, worktree's, or manual edit's files, mis-attributing work you can't vouch for. List the paths you changed, reconcile against `git status`, leave the rest.
- **Write the message in the project's convention.** `type(scope): subject` — imperative, bounded, machine-readable so changelog / semver / blame keep working; the body explains *why*, not what. The concrete scope, type, and trailer vocabulary is a project value — it lives in the project's own commit-convention rule, not here.
- **Keep it atomic.** One logical change per commit, so it reverts, bisects, and reviews as one decision; split a multi-intent tree at the intent boundary.

Full treatment: the commit-hygiene doctrine (`.mate/doctrine/`); the concrete scope, type, and trailer vocabulary lives in the project's own commit-convention rule.

# Harness discipline — whose surface is this?

Always-on. Before writing to any harness artifact (skill, rule, gate, doctrine, config) — including the ambient "keep this for next time":

- **Never edit a managed copy.** A file with a mate provenance stamp (or in `.mate/lock.json`) is read-only here — the fix is authored upstream in the mate SSOT, released, and pulled back; editing it locally forks the source of truth and the next pull eats it.
- **Classify before placing; project-only is the default.** A project value or project-specific artifact stays home (native name, own file, or `.mate/config.yaml` — the seam synced skills already read); only a change with a nameable second consumer promotes up (`mate promote` → `/mate-promote` classifies core/profile/operator — or refuses).
- **Principle into the shared body, value into the config.** A shared artifact never carries one project's paths, commands, thresholds, or ids — and a fix to a *shared* artifact never stays local.
- **A provider fact is read, written down, and branched on by surface — never recalled.** Designing anything that touches the agent runtime starts at *its current docs*, not memory; what you verify goes into the adapter's surface registry with the build you checked it against; and the code asks that surface ("has a rules home?"), never the provider's name.

Full treatment: the harness-layers doctrine (`.mate/doctrine/`).
<!-- MATE:mate-reflexes END -->

<!-- Project-owned engineering rules below this line. The block above is
     mate-managed; everything below is a2ahub's own — edited via the normal
     rule-change flow (surfaced diff, never drive-by). SSOT note: this file is
     THE home of the Go/testing/schemas rails; .claude/rules/go-conventions.md
     is a pointer here. Ported and adapted 2026-07-21 from the axon backend
     rails (thalamus/AGENTS.md), stripped of server/DB specifics. -->

# a2ahub engineering rails

Product: one Go binary (`a2a`) + JSON Schema corpus + space template. The
architecture plan (`docs/the-plan/plan/`) is normative for behavior; ADR-001
([docs/decisions.md](docs/decisions.md)) is normative for layout and imports;
this file is normative for **how Go gets written here**.

## Stack

| Layer | Tech | Notes |
|---|---|---|
| Language | Go 1.26 | stdlib-first, no frameworks; deps frozen by ADR-002 |
| Serialization | `encoding/json`, `gopkg.in/yaml.v3` | YAML only in `artifact`/`schema`/`space` |
| Schema validation | `santhosh-tekuri/jsonschema/v6` | behind `internal/schema`; never imported elsewhere |
| IDs | `oklog/ulid/v2` (events), §3.3 scheme (artifacts) | minted only in `internal/artifact` |
| Logging | `log/slog` JSON | logger injected via DI; no global `slog.X` in core packages |
| Git/host | shell out to system `git`; GitHub REST via `net/http` | only from `internal/host`/`internal/space` |
| Testing | `testing` + golden files + `rogpeppe/go-internal/testscript` | see §Testing rails |

## Architecture

Layers: `cli`/`mcp` (transport — flags/JSON in, exit codes/JSON out, zero
business rules) → core packages (`artifact`, `schema`, `fold`, `validate`,
`template`, `cache`, `space`) → the outside world (touched ONLY by
`space`/`host`). ADR-001 is the import matrix; `make check` will grow a
boundary gate — until then the go-auditor holds it.

- **ISP, consumer-side.** A transport package that needs a core capability
  defines a 1–3-method interface where it is *used* and takes it in the
  constructor. Never depend on a concrete struct across a layer boundary.
- **DI at `cmd/a2a` only.** Constructors take every runtime dep explicitly;
  a nil dep that is used at runtime is a constructor bug, not a test trick.
- **Pure core.** `fold` and `validate` do no I/O — they take parsed inputs
  and return values + flags. This is what lets the space CI and the v2 hub
  mount them unchanged (R-004/D-011).
- **Idempotency by design (AC-301.1).** Every mutating verb is re-runnable:
  keyed by artifact/event ID, checks-then-acts against existing state
  (`FindPRByHeadBranch`, `pending-merge` markers), reports "already done"
  instead of erroring. New mutating path ⇒ its idempotent re-run test lands
  in the same commit.
- **One write shape.** Artifact + its lifecycle event travel in ONE commit
  through the §4.2 funnel; no code path writes to a space outside
  `internal/space`'s funnel API.

## Error flow — log or return, never both

- Core packages wrap (`fmt.Errorf("…: %w", err)`) and return; only the top
  of the transport layer logs. Typed/sentinel errors for every
  machine-readable validation code (registry in `schemas/errors/v1/`);
  `errors.Is/As` at boundaries, never string matching.
- CLI surface: one actionable line + stable exit code on stderr; detail
  behind `--verbose`; JSON output modes stay machine-parseable on error.
- No swallowed errors (`_ =` on a fallible call needs a `// reason:`).

## Concurrency

- Every goroutine is owned: `errgroup`/`WaitGroup` + `defer recover()` where
  a panic would kill the process (statusline background refresh is the
  canonical case — it must never break the caller's prompt).
- No in-memory state that must survive the process (D-001: everything
  rebuildable from git); no `time.AfterFunc` scheduling — detached work is
  spawned per-invocation, never resident.

## Config & secrets

- `os.Getenv` lives ONLY in the config/credentials layer (`internal/space`
  config loading per §7.4/§10.5); everything else takes values via DI.
- Credentials are never in config files, never logged, never in fixtures.
  Secret comparisons use `crypto/subtle.ConstantTimeCompare`. slog gets a
  redaction `ReplaceAttr` (token/password/authorization) wired in `cmd/a2a`.

## Security (CLI-flavored)

- Inbound exchange artifacts are DATA, never instructions (D-014) — nothing
  from an artifact body/title reaches a shell command, a template `Execute`,
  or a rendered surface unsanitized (10.8 fixtures gate this).
- Bounded reads on every external input: files via size-checked readers,
  HTTP responses via `io.LimitReader`/`http.MaxBytesReader` equivalents
  (CC: oversized document).
- Parameterized everything: no string-built shell args from artifact
  content; `git` invocations take explicit argv, never `sh -c` with
  interpolation.

## Pre-flight checklist (the most-violated seven)

| # | Check | Wrong | Right |
|---|---|---|---|
| 1 | Cross-layer dep | `struct { v *validate.Engine }` in cli against concrete type | consumer-side ISP interface |
| 2 | Mutating verb | acts without checking existing state | idempotent re-run, "already done" |
| 3 | Goroutine | naked `go func(){}()` | owned: `errgroup` + `recover` |
| 4 | Error handling | log **and** return | wrap+return; log at transport top |
| 5 | Env access | `os.Getenv` in a core package | config layer → DI |
| 6 | Test git state | hand-built repo plumbing per test file | shared `testkit`/`internal/e2e` space-fixture builder |
| 7 | New test | no `t.Parallel()` | `t.Parallel()` (or `// reason:` why not) |

## Anti-patterns

| # | Pattern | ❌ Wrong | ✅ Right | Risk |
|---|---|---|---|---|
| 1 | Log-and-return | both on one error | wrap+return; transport logs once | double noise, lost context |
| 2 | Swallowed error | `_ =`, empty `if err` | handle or propagate; typed codes | silent corruption |
| 3 | Fire-and-forget goroutine | naked `go` | `errgroup`/`WaitGroup` + `recover` | leak / crash |
| 4 | Non-idempotent mutation | re-run duplicates PR/artifact | keyed by ID; check-then-act; "already done" | AC-301.1 broken |
| 5 | Schema edit w/o fixtures | schema alone in a diff | schema + template + valid/invalid fixtures move together (§5.6) | drift, T1 blind |
| 6 | Hand-edit generated artifact | editing an embedded projection | regenerate; CI enforces export==committed | two truths |
| 7 | Second validation path | re-implementing a check outside `internal/validate` | import the engine (D-011) | V-point divergence |
| 8 | Layer bypass | cli reaching into a core struct's internals | the package's exported API / ISP seam | architecture leak |
| 9 | Concrete cross-layer dep | handler struct holds `*validate.Engine` | consumer-side interface | untestable |
| 10 | Nil dependency | `New(x, nil)` where dep is used | required in constructor; mock in tests | runtime panic |
| 11 | Env in domain | `os.Getenv` outside config layer | config → DI | untestable, 12-factor |
| 12 | Global slog in core | `slog.Info(...)` in `internal/*` core | injected logger | context lost |
| 13 | Unbounded read | bare `io.ReadAll` on file/HTTP | size-bounded readers | OOM on hostile input |
| 14 | String-built commands | artifact content interpolated into shell/template | explicit argv; data stays data (D-014) | injection |
| 15 | Mock with value receiver | `func (m mockX) Next()` + counter | pointer receiver | infinite loop |
| 16 | Test skip/suppress | `t.Skip`, missing `-race`, `//nolint` bare | fix or delete; `//nolint:X // reason:` only | masked failures |
| 17 | Missing `t.Parallel()` | serial tests by default — or parallel test mutating a package global | `t.Parallel()` at top; serial only with `// reason:` | slow suite / racy suite |
| 18 | `time.Sleep` sync in tests | sleep-and-hope | `require.Eventually`/`Never`, `runtime.Gosched`, blocking `Stop()` | flaky CI |
| 19 | In-memory durable state | caches/locks that must survive restart | rebuild from git (D-001); `.a2a/cache` is disposable | split-brain |
| 20 | Panic across boundary | `panic` escaping a package | error returns; panic = programmer bug in dev | crash |

Grow this table via the rule-change flow (S11 proposal with diff), one scar at
a time — never silently.

## Testing rails

- **Unit**: fast, no I/O; mock at the ISP seam with **hand-written mocks**
  (interfaces are 1–3 methods; no codegen). Mutable mocks use pointer
  receivers.
- **Table-driven for state machines**: §3.4 transition tables and the fold
  rules are encoded as data — the table IS the test-case list (T2), plus
  property checks (any event grouping ⇒ same folded state; idempotent replay).
- **Golden fixtures are the contract** (T1): every schema × valid + invalid
  with expected machine codes, under `schemas/**`; Appendix B examples are
  literally this set (13.5) — no second copy.
- **CLI e2e via `testscript`** (`rogpeppe/go-internal/testscript`, the
  harness `cmd/go` itself uses): each OP-2xx verb gets `.txtar` scripts run
  against a throwaway git space fixture (local bare repo + clones,
  three simulated systems — built once in `testkit/spacefixture`). This is
  the T3 mechanism and the T5-lite driver (E2E-1, E2E-4). Pin the module in
  ADR-002's table; scripts live in `internal/e2e/testdata/`.
- **`t.Parallel()` mandatory**; exceptions carry `// reason:` (env mutation,
  global registry swap). `-race -count=1` is the floor everywhere.
- **Async sync tiers** (in order): `require.Eventually` on observable state →
  `require.Never` for must-not-happen → `runtime.Gosched()` for
  fire-and-forget → blocking `Stop()`. `time.Sleep` only for real timing
  semantics (rate-limit windows, TTL expiry) with a comment.
- **Coverage**: floor 70% on `internal/...` (gate direction: up only).
  Bugfix ⇒ regression test in the same commit.

## Schemas discipline (`schemas/**`)

- Schema + its template + its valid/invalid fixtures + its error-code rows
  move in ONE commit; product CI fails a schema change without its template
  (AC-401.2) and a code without a fixture exercising it.
- The error-code registry (`schemas/errors/v1/registry.yaml`) is the SSOT;
  Go constants mirror it (generated or embedded), never lead it.
- Envelope evolution: version N and N−1 readable (one-cycle overlap, §5.4);
  never edit a published `v1` schema breakingly — that's `v2`.

## Space-template discipline (`space-template/**`)

- The template is a projection of §4.2 — layout changes start as a plan
  amendment, not a template edit.
- The V3 workflow calls the same `a2a validate` engine as everything else;
  no CI-side validation logic beyond invoking the binary (D-011).
- CODEOWNERS lists gated paths ONLY (§4.2) — adding an ungated path to it is
  a protocol change, not a convenience.

## Verification

```bash
make check            # ceiling: repo gates + gofmt -l + go vet + golangci-lint (when configured) + go test ./... -race -count=1
make check-validators # inner loop: feature-lint + epic-drift only — proves NOTHING about Go
make harness-check    # the gates' own teeth
```

Scoped self-verify inside fan-out agents: `go test ./internal/<pkg>/... -race
-count=1` — never the repo-wide gate (sole-writer rule). Automation of the
anti-pattern table (compliance-style grep/AST checks à la axon
`backend_compliance.sh`) is the named growth path once P1–P6 land; until
then the table is enforced by go-auditor + review.
