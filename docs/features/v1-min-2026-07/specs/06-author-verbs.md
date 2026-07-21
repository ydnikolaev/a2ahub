# P6 — Author verbs & embedded templates — Specification

**Slug**: `v1-min-2026-07`  ·  **Track**: cli  ·  **Status**: draft
**Created**: 2026-07-21  ·  **Owner**: yura
**Plan**: [plans/06-author-verbs.plan.md](../plans/06-author-verbs.plan.md)
**Footprint**: `internal/template/` (embedded P2 templates + render), `internal/cli/cmd_init.go` (OP-201 `init`, OP-202 `connect`/`disconnect`), `internal/cli/cmd_new.go` (OP-203 `new`, OP-219 `template list/show`), `internal/cli/cmd_submit.go` (OP-205 `submit`, OP-220 `submit --batch`, OP-204 `validate`), `internal/cli/cmd_sync.go` (OP-206 `sync`), `cmd/a2a` wiring lines only (registering the new subcommands) — no other file in `internal/cli/` or elsewhere. Every one of P6's 9 OP entries (OP-201…OP-206, OP-219, OP-220, plus the `validate` verb) is pinned to exactly one of these four files so P7/P8 — which also add files to `internal/cli/` in the same wave-DAG position — cannot collide by creating a same-named file.

May import (per [ADR-001](../../../decisions.md)): `internal/template` → `internal/artifact`, `internal/schema` only. `internal/cli` (this phase's four files) → `internal/artifact`, `internal/schema`, `internal/fold`, `internal/validate`, `internal/host`, `internal/space`, `internal/template` (the "core packages above" the `internal/cli` row of ADR-001 grants — `internal/cache` is excluded: P7 builds it and is blocked_by P6, so it does not exist yet at this phase's build time).

---

## 0. User stories

| ID | User story |
|----|------------|
| US-201 | (IA) As an agent, every artifact I draft is validated before it can leave my machine, so I can never publish garbage. |
| US-301 | (IA) As an agent, I operate the whole exchange from one binary with idempotent commands. |
| US-401 | (IA) As an agent, `a2a new` gives me a template that cannot drift from the schema. |

## 0.5 Required domain knowledge

| Domain | Doc(s) | Notes |
|--------|--------|-------|
| Command semantics | [07-client.md §7.2](../../../the-plan/plan/07-client.md) OP-201…OP-206, OP-219, OP-220 | verbatim semantics column is normative; do not paraphrase into looser behavior |
| Config & actor resolution | [07-client.md §7.4](../../../the-plan/plan/07-client.md) | two config levels, mirror placement, actor identity order |
| Draft/submit lifecycle | [03-domain.md §3.4](../../../the-plan/plan/03-domain.md) | draft is local-only; submit collapses `draft→submit` into the first commit |
| Templates | [05-schemas.md §5.6](../../../the-plan/plan/05-schemas.md) | one canonical template per type, schema-derived, no per-project forking |
| ID scheme | [03-domain.md §3.3](../../../the-plan/plan/03-domain.md) | `a2a new` mints IDs per this table |
| Write funnel | [04-topology.md §4.2](../../../the-plan/plan/04-topology.md) | `submit`/lifecycle verbs ship via `internal/space`'s ephemeral-branch → PR funnel (D-002); this phase calls it, does not reimplement it |
| Package boundaries | [ADR-001](../../../decisions.md) | footprint above is derived from it |

---

## T1. CLI surface (track: cli)

> Declarative catalog; exact per-flag parsing is implementor discretion within these semantics (07-client.md §7.2 is normative text, quoted where load-bearing).

| Command | OP | Flags (non-interactive normative per D-030/§7.2) | Input | Output | Notes |
|---------|----|----|-------|--------|-------|
| `a2a init` | OP-201 | `--system <id> --space <repo>...` (flag-driven = normative path; interactive TTY prompt is sugar over the same code path) | flags or TTY prompts | writes `.a2a/config.yaml`; exit 0 on success | > "fully flag-driven non-interactive mode (`--system --space ...`) is normative — interactivity is TTY-only sugar; `a2a doctor` afterwards" (§7.2 OP-201). Base `a2a doctor` (OP-218) is delivered by P9 ("basic doctor", README phases table); `--space` is v2-deferred. This phase's `init` only PRINTS the suggestion to run `a2a doctor` — it does not invoke or implement it. |
| `a2a connect <space-repo>` | OP-202 | — | space repo URL | registers space in `.a2a/config.yaml` + mirror clone under machine-level mirror root (§7.4) | > "connect: register the space locally (mirror clone + config entry); membership itself is the §9.2 runbook" |
| `a2a disconnect <space>` | OP-202 | — | connected space id | removes config entry + mirror + cache for that space | > "remove config entry + mirror + cache for that space — no other local state exists, so nothing is lost; leaving the space itself... is the §9.2 offboarding runbook". `internal/cache` does not exist yet at this phase (P7 dependency) — disconnect removes the mirror + config entry only; cache removal is a P7 no-op stub call the interface reserves, not implemented here (see Open questions Q-A). |
| `a2a new <type> [--thread <id>]` | OP-203 | `--field k=v` (repeatable), `--body-file <path>` (non-interactive normative); `$EDITOR` only on TTY | type name + fields | draft file under `.a2a/staging/` | > "draft from template (5.6) into local `.a2a/` staging (drafts never enter the space, 3.4): mints ID, fills envelope; non-interactive input (`--field k=v`, `--body-file`) is normative, $EDITOR only on TTY". ID minted per §3.3; actor resolved per §7.4 order. |
| `a2a validate [path\|--all]` | OP-204 | `--all` | path or none | V1/V2 machine-readable (JSON) result | delegates to `internal/validate` (built by P3) — this phase adds no validation logic, only wires the CLI verb. |
| `a2a submit <artifact>` | OP-205 | — | staged draft path or artifact id | PR opened (fire-and-forget); local state marks item `pending-merge` | see §T1.1 below — full semantics quoted. |
| `a2a submit --batch <artifact...>` \| `--drafts` | OP-220 | `--batch`, `--drafts` | multiple staged artifacts | ONE commit + ONE PR carrying N artifacts + N submit events | > "validate ALL (all-or-nothing pre-push), then ONE commit + ONE PR carrying N artifacts + N submit events; per-artifact idempotency preserved" (§7.2 OP-220). All-or-nothing: any artifact failing V2 aborts the whole batch — zero artifacts pushed. |
| `a2a sync` | OP-206 | — | none | refreshed mirrors | > "fetch all connected spaces, refresh local cache/fold". This phase's `sync` fetches all connected mirrors and re-runs `internal/fold`; it does NOT populate `internal/cache` (P7-owned) — see Open questions Q-A. |
| `a2a template list` / `a2a template show <type>` | OP-219 | — | type name (show) | list of canonical types / rendered template body | read-only inspection of the same embedded templates `a2a new` renders. |

### T1.1 `a2a submit` — full semantics (quoted, §7.2 OP-205)

> "validate (V2) → commit artifact + its lifecycle event (ONE commit) to
> ephemeral branch `a2a/<system>/<id>` → push → open PR with auto-merge →
> return immediately (fire-and-forget). Local cache marks the item
> `pending-merge` until the merge lands (submit-then-read stays coherent).
> Commit author = the system's machine account; message `a2a(<type>):
> <id>`. Submit emits the type-appropriate first transition: `submit` for
> exchanges, `publish` for standing/broadcast types, `propose` for
> decisions"

Implementation notes binding this phase:
- The commit+push+PR mechanics are `internal/space`'s write funnel (D-002, built by P5); this phase's `cmd_submit.go` calls it, never re-implements branch/PR logic.
- First-transition selection (`submit`/`publish`/`propose`) is dispatched from the artifact's type per §3.4.1–3.4.4/3.4.7 — `cmd_submit.go` maps type → transition name, it does not encode transition legality (that is `internal/fold`'s transition table, P4).
- "Local cache marks the item `pending-merge`" — `internal/cache` is P7-owned and does not exist at this phase's build time (P7 is blocked_by P6). `cmd_submit.go` MUST leave a call-site seam (a no-op/stub hook) for this marking rather than implement or skip it silently — flagged under Open questions Q-A, not resolved here.

### T1.2 Idempotency & self-section refusal (quoted, §7.2 tail)

> "Every mutating command is safe to re-run (idempotent by artifact/event ID)
> and refuses to operate on sections other than the configured own system."

Binding for every mutating verb in this phase's footprint (`init` is idempotent by config path — re-running with identical flags is a no-op "already configured"; `connect`/`disconnect` by space id; `new` mints a fresh ID per invocation so idempotency is N/A to `new` itself, but a re-run of `submit`/`submit --batch` on an already-submitted artifact ID is a no-op "already submitted"; `validate` is read-only, always safe; `sync` is inherently idempotent — refresh has no "already done" state to detect):
- **Idempotent re-run**: `submit`/`submit --batch` on an artifact whose ID already has a submit/publish/propose event committed in the space returns a clear "already done" result (exit 0, no second PR, no duplicate event) rather than erroring or double-submitting.
- **Foreign-section refusal**: any mutating verb operating on an artifact whose `from` (or acting section) does not match the locally configured own system MUST refuse the write locally before any git operation — this is AC-201.3 (quoted §8 below), enforced client-side in addition to the `internal/validate` authz class (CC-002) that V2/V3 also check.

## 5. Existing patterns to reuse (anti-duplication)

- [ ] `internal/validate`'s V2 pipeline (P3) — `cmd_submit.go`/`cmd_new.go` call it for pre-commit checks; this phase adds zero new validation rules.
- [ ] `internal/space`'s write funnel (P5) — the single implementation of ephemeral-branch/commit/push/PR; every mutating verb in this phase routes through it, none opens a branch or PR directly.
- [ ] `internal/fold`'s transition tables (P4) — `cmd_submit.go` dispatches the first-transition NAME per type but never encodes legality; legality is `fold`'s table, checked once.
- [ ] `internal/artifact`'s ID minting + frontmatter serialize (P1) — `cmd_new.go` reuses it verbatim for draft creation; no second ID-generation routine.
- [ ] `internal/schema`'s embedded schemas (P2) — `internal/template`'s render is a projection of these schemas per §5.6; template content must not re-declare field shapes the schema already owns.

## 6. Testing requirements

| Area | What to test | Edge cases |
|------|--------------|------------|
| `internal/template` render | every type's template renders with minted ID, resolved actor, current date filled | unknown type; missing schema for a type (build-time CI catch, not runtime) |
| `a2a new` (AC-401.1) | drafted artifact passes V1 before any edits beyond placeholder fills, for every type | type requiring `--field` inputs omitted; `--body-file` pointing at nonexistent path |
| `a2a submit` idempotency (AC-301.1) | re-running `submit` on an already-submitted ID is a no-op "already done" | re-run before PR merge lands (still `pending-merge`) vs after merge |
| `a2a submit --batch` (OP-220) | all-or-nothing: one invalid artifact in the batch aborts the whole batch, zero pushed | N=1 batch (degenerates to single submit); all-valid batch produces exactly one commit + one PR + N events |
| Foreign-section refusal (AC-201.3) | artifact with `from` ≠ configured system is refused locally, before any network call | own-system artifact with a foreign `to` (must NOT be refused — refusal is about acting section, not addressee) |
| `a2a connect`/`disconnect` | connect registers config entry + clones mirror; disconnect removes both and leaves no other local state | disconnect on a space never connected; connect to an already-connected space (idempotent) |
| `a2a init` non-interactive | `--system --space ...` writes valid `.a2a/config.yaml` with zero prompts | re-run with identical flags is idempotent "already configured"; missing required flag on a non-TTY invocation errors rather than hanging on a prompt |
| Actor resolution order (§7.4) | explicit flag beats env var beats harness default beats config | all four sources absent → `actor.kind` defaults to `agent` |

## 7. Schema / contract delta

None. This phase renders templates from schemas P2 already defines (`envelope/v1`, `event/v1`) and does not introduce new JSON Schema fields. `internal/template`'s own embedding mechanism is Go code (`embed.FS` per stdlib-first, no new schema file).

## 8. Acceptance criteria

> AC-301.1, AC-401.1, AC-201.3 rows below are copied verbatim from [14-us-ac.md](../../../the-plan/plan/14-us-ac.md); Given/When/Then text is unchanged. Phase-local rows are marked US "—".

| # | US | Criterion | How to verify |
|---|-----|-----------|---------------|
| 1 | US-301 | AC-301.1 Given any OP-2xx mutating command re-run after success, then the second run is a no-op with a clear "already done" result. [T3] | re-run `a2a submit <id>` (or `submit --batch`) twice against the same mirror state; second run exits 0 with an "already done"/"already submitted" message, produces zero new commits/events |
| 2 | US-401 | AC-401.1 Given any type, when drafted, then the draft passes V1 before any edits except placeholder fills. [T3] | for every type in the P2 corpus, `a2a new <type>` with placeholder-only fills then `a2a validate` on the draft returns V1-pass |
| 3 | US-201 | AC-201.3 Given an artifact with `from` not matching my configured system, when I submit, then the write is refused locally. [CC-002] | craft a draft whose `from` differs from `.a2a/config.yaml`'s system id; `a2a submit` exits non-zero before any git/network call, error names CC-002-class refusal |
| 4 | — | `a2a submit --batch` with one V2-invalid artifact among N pushes zero artifacts (all-or-nothing, OP-220) | batch of 3 drafts, 1 intentionally invalid; `a2a submit --batch` exits non-zero, mirror shows no new commit |
| 5 | — | `a2a submit --batch` with N valid artifacts produces exactly one commit and N submit/publish/propose events | batch of 3 valid mixed-type drafts; inspect the resulting commit — one commit object, N event files |
| 6 | — | `a2a init --system --space ...` (non-interactive) never blocks on stdin | run with `A2A_...` env unset, stdin closed/non-TTY, all required flags present; process exits without hanging |
| 7 | — | `internal/template` build-time check: every P2 schema type has exactly one embedded template, and template field names are a subset of the schema's field set | `go build ./internal/template/...` in CI fails if a schema gains a field the template doesn't reference or vice versa (per §5.6 "CI... regenerates/validates templates against schemas") — this phase's implementor wires the check; the CI workflow step itself is P1-repo-CI scope, cited not owned here |

## 9. Future-proof considerations

| Aspect | Assessment |
|--------|------------|
| Extensibility | new artifact types add one template file under `internal/template/` + one schema (P2 territory); `cmd_new.go`'s type dispatch is table-driven, no per-type branch to hand-edit for the CLI surface itself |
| Coupling | soft — `internal/template` depends only on `artifact`/`schema` (IDs + field shapes), never on `cli`; `cli` files depend on the write funnel by interface (`internal/space`), not by reaching into its internals |
| Migration path | low — the hub (v2) reuses `fold`/`validate`/`schema` unchanged (D-011/D-012); this phase's `internal/cli` files are not imported by the hub at all |
| Roadmap conflicts | P7/P8 also add files under `internal/cli/` in the same wave-DAG position (both `blocked_by: [P6]`) — the per-verb file split in the Footprint line exists specifically so P7's `cmd_inbox.go`/`cmd_show.go`/etc. and P8's lifecycle-verb files never collide with this phase's four files at merge time |

## 10. Implementor entry point

Execute as one wave of the `v1-min-2026-07` epic (blocked_by: P3, P4, P5 — do not start until those three phases' `internal/validate`, `internal/fold`, `internal/space` packages exist and are stable). TDD default (draft-then-validate loop for `new`/`submit` is naturally red→green: write a failing idempotency/refusal test against a stub, then wire the real funnel call). Framework-first: Go stdlib `embed.FS` for templates, stdlib `flag` (or the repo's established CLI parsing precedent from P1's `cmd/a2a` skeleton — check it before adding a flag-parsing helper). Log-or-return per [.claude/rules/go-conventions.md](../../../.claude/rules/go-conventions.md).
Full loop (README/tracker/specs shapes, lint gate): [docs/features/README.md](../../README.md).

## 11. Amendments

> Append-only. When the shipped reality deviates from this spec, record it here AND amend any downstream spec.

### 2026-07-21 — from wave 3: shipped-reality deltas

- **`a2a new <standing-type>` takes the slug via `--slug` / `--field
  slug=`**, NOT a bare positional after the type. P8's `a2a contract new
  <slug>` (spec 08) must translate its positional `<slug>` into that flag
  when delegating, not forward args verbatim (propagation probe; backlog).
- **Cache seam shipped as two 1-method interfaces** `PendingMarker`
  (submit/sync pending-merge mark) and `CacheRemover` (disconnect), with
  no-op impls injected this phase. P7 supplies the real `internal/cache`
  impls — the constructor seams (`NewSubmitCommand`'s `pending`,
  `NewDisconnectCommand`'s `cache`) are the wiring points.
- **DI adapters live in `internal/cli/adapters.go`** (granted in the plan):
  `LegalityAdapter` (folds mirror history; exports `HasCommittedHistory` +
  `RegisterEnvelope` for cmd_submit's idempotency + the CandidateEvent
  envelope gap — see below), `MirrorResolver`, `SubmitValidatorAdapter`
  (returns `ViolationError` on a non-Valid Result), `ManifestValidatorAdapter`.
- **CandidateEvent envelope gap** (core-API friction, backlog): validate.
  CandidateEvent carries only {Subject, Transition, Actor} — no envelope,
  and a first-submit subject isn't committed yet, so the LegalityAdapter
  needs `RegisterEnvelope` to inject the drafted artifact's envelope facts
  before checking legality. Works, but the seam is a workaround for a
  missing field on CandidateEvent; flag for a possible P8/hub-era fold API
  refinement.
- **cmd/a2a wiring is closure-per-verb** (lead): config-dependent verbs
  (submit/validate/sync) resolve the target space from the artifact's
  `space` field → connected SpaceRef.ID at call time; the foreign-section
  refusal (AC-201.3) + idempotency short-circuit run BEFORE any mirror
  clone in the wiring closure.

<!-- ### YYYY-MM-DD — from wave N: <what changed & why> -->

---

## Open questions

Checked against plan §17 Q-001…Q-010 (17-decisions.md) — none of the ten tracked open questions cover the item below; it is not a duplicate of any Q-###.

- Phase-boundary seam, not a plan contradiction: §7.2 OP-205/OP-206 state `submit` "local cache marks the item `pending-merge`" and `sync` "refresh[es] local cache/fold" — but `internal/cache` is P7-owned (`tracker.yaml`: P7 `blocked_by: [P6]`), so it does not exist when P6 builds. The plan does not sequence cache-before-submit; this is an artifact of the epic's file-disjoint DAG ordering, surfaced per this brief's "if the plan is ambiguous... list it under Open questions" instruction. This spec's resolution (binding, not deferred): `cmd_submit.go`/`cmd_sync.go` leave an explicit seam — an interface call-site for the future `internal/cache` write — rather than silently skip or fake the "pending-merge" marking. Does not block P6 start (P6's actual `blocked_by` is P3/P4/P5, unaffected).
