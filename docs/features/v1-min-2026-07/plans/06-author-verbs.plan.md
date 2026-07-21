---
slug: v1-min-2026-07
phase: P6
spec: ../specs/06-author-verbs.md
wave: 3
status: planned
---

# Phase plan — P6 Author verbs & embedded templates

## Goal

Ship `internal/template` (embed + render the 8 P2 templates) and the author
verbs `init/connect/disconnect/new/validate/submit/submit --batch/sync` +
`template list/show` as `internal/cli` command files, plus the DI adapters
that bridge the wave-2 engines to the CLI (LegalityChecker folding local
history, SubmitValidator over ValidateForSubmit). Spec 06 AC rows 1–7.

## Allowlist (repo-relative)

- `internal/template/**`
- `internal/cli/cmd_init.go` (OP-201 init, OP-202 connect/disconnect)
- `internal/cli/cmd_new.go` (OP-203 new, OP-219 template list/show)
- `internal/cli/cmd_submit.go` (OP-204 validate, OP-205 submit, OP-220 batch)
- `internal/cli/cmd_sync.go` (OP-206 sync)
- `internal/cli/adapters.go` (NEW — the DI adapter types, see placement)

## Lead-reserved / off-limits deltas

- `internal/cli/cli.go` — LEAD-OWNED shared seam (already created); read it,
  build against `cli.Command`/`cli.IO`, never edit it.
- `internal/cli/cmd_doctor.go` — P9's, parallel this wave; never touch.
- `cmd/a2a/**` — LEAD wires the dispatch registration after the wave.
- `internal/cache/**` — P7, does not exist yet; leave the seam (below).
- `go.mod`, `schemas/**` (embed.go exists from P3 — reuse read-only),
  `internal/{artifact,schema,fold,validate,host,space}` (consume, never edit).

## Placement decisions (lead, binding)

- **Command shape**: each verb file defines a command type implementing
  `cli.Command` (Name/Synopsis/Run) + a constructor `NewXCommand(deps...)`
  taking exactly its core deps (rails DI). No package-level mutable state in
  any verb file. cmd/a2a (lead) constructs each with real services and
  registers Name()→Run into the dispatch map.
- **DI adapters live in `internal/cli/adapters.go`** (P6-owned, granted): the
  concrete `validate.LegalityChecker` and `space.SubmitValidator`/
  `space.ManifestValidator` implementations. The propagation probe flagged
  the LegalityChecker as heavy — it must derive (kind, current folded state,
  envelope, membership) for each candidate event by folding the subject's
  committed history read from the connected space's MIRROR CLONE on disk
  (via internal/space layout + internal/fold), NOT from internal/cache
  (P7, absent). Verify/dispute legality is a KNOWN GAP (backlog, P8) — the
  adapter handles the submit/publish/propose first-transitions this phase's
  verbs actually emit; a candidate verify/dispute event is out of P6's verb
  set.
- **Cache seam (spec 06 Open Q-A, binding)**: `cmd_submit`/`cmd_sync` leave
  an explicit no-op interface call-site for the future `internal/cache`
  pending-merge marking — a 1-method interface (e.g. `PendingMarker`) whose
  nil/no-op impl this phase injects, NOT a silent skip and NOT a fake. P7
  supplies the real impl later.
- **Template embed**: `internal/template` reuses `schemas/embed.go`'s FS
  (P3) for `templates/v1/*.md` — no second embed of the same tree.
- **Actor resolution** (§7.4 order): explicit flag > A2A_ACTOR_* env >
  harness default > config; default `actor.kind=agent`. os.Getenv only in
  this resolution path (config layer), injected into commands.

## Brief

```
Stack: Go 1.26, stdlib-first (embed.FS, flag-based verbs, encoding/json for
the validate output). NO new deps.
All file paths are REPO-RELATIVE.

## Goal
Implement internal/template + the five author-verb files + the DI adapters
per docs/features/v1-min-2026-07/specs/06-author-verbs.md — read it END TO
END first (T1 catalog, T1.1 submit semantics, T1.2 idempotency+refusal, §6
tests, §8 ACs, Open Q-A), then the Placement decisions in
docs/features/v1-min-2026-07/plans/06-author-verbs.plan.md (BINDING).

## Context (read in order)
- The lead-owned seam internal/cli/cli.go (cli.Command, cli.IO) — build
  against it, do NOT edit it.
- Root AGENTS.md rails (idempotency-by-design, one write shape, config &
  secrets, error flow, testing rails — all load-bearing).
- Exported APIs you WIRE (read the source, reuse verbatim — never
  re-implement): internal/artifact (ID mint, frontmatter serialize),
  internal/schema (Corpus, DecodeYAMLInstance), internal/validate
  (Engine.New/ValidateDraft/ValidateForSubmit, Draft, LocalContext,
  Resolver, LegalityChecker, CandidateEvent, Result/Violation, Severity),
  internal/fold (Fold, CheckLegality, Kind/State/Event/Envelope,
  MembershipView), internal/space (Layout, LoadProjectConfig/
  LoadMachineConfig, ResolveCredential, CloneOrFetch, WriteFunnel.Submit,
  SubmitRequest, WriteResult, ManifestValidator, ParseManifest), internal/
  host (Host, NewGitHubHost), and schemas/embed.go's FS.
- Plan corpus: 07-client.md §7.2 (OP semantics — normative), §7.4 (config +
  actor order), 03-domain.md §3.3/§3.4, 05-schemas.md §5.6, 04-topology.md §4.2.

## Allowed files — REPO-RELATIVE ONLY
- internal/template/**
- internal/cli/cmd_init.go, cmd_new.go, cmd_submit.go, cmd_sync.go
- internal/cli/adapters.go (new — the DI adapter types)

## Off-limits (NEVER touch)
- internal/cli/cli.go (lead-owned seam — read only)
- internal/cli/cmd_doctor.go (a parallel agent owns it)
- cmd/a2a/** (lead wires dispatch after the wave — you write NO main.go line)
- internal/cache (P7, absent — leave the seam), go.mod, schemas/** (reuse
  embed.go read-only), any internal/* core package (consume, never edit)

## What to do
1. internal/template: embed.FS over schemas/templates/v1/*.md via
   schemas/embed.go; Render(type, fields) → filled draft bytes (minted ID,
   resolved actor, current date — date/id/actor supplied by the caller for
   testability, never time.Now() inside render). Build-time check (AC row
   7): a test asserting every P2 envelope type has exactly one template and
   template field tokens ⊆ schema field set.
2. internal/cli/adapters.go: concrete validate.LegalityChecker (folds the
   subject's committed events from the mirror via internal/space layout +
   internal/fold; handles submit/publish/propose; verify/dispute → return a
   documented "unsupported in P6" path, NOT a silent legal), validate.
   Resolver (refs/digests/systems from mirror + manifest), space.
   SubmitValidator (wraps Engine.ValidateForSubmit; maps a non-Valid Result
   to an error carrying the violation list), space.ManifestValidator
   (wraps schema manifest validation — schema-class only per its corrected
   doc comment), and the PendingMarker no-op cache seam.
3. Verb files, each a cli.Command implementation + constructor:
   - cmd_init.go: init (non-interactive --system --space is normative;
     writes .a2a/config.yaml; idempotent "already configured"; missing
     required flag on non-TTY errors, never hangs; prints the "run a2a
     doctor" suggestion, does NOT invoke doctor). connect/disconnect
     (register/clone mirror; remove config+mirror; disconnect calls the
     cache-removal seam as a no-op).
   - cmd_new.go: new <type> (--field k=v, --body-file; $EDITOR only on TTY;
     mints ID §3.3, resolves actor §7.4, renders template, writes under
     .a2a/staging/; drafts never enter the space). template list/show
     (read-only over the embedded templates).
   - cmd_submit.go: validate (--all; delegates to Engine, JSON output),
     submit (V2 → space.WriteFunnel.Submit; first-transition dispatch by
     type submit/publish/propose; fire-and-forget; pending-merge via the
     seam; idempotent "already submitted"; foreign-section refusal BEFORE
     any git — AC-201.3), submit --batch/--drafts (validate ALL first,
     all-or-nothing, ONE commit + ONE PR + N events).
   - cmd_sync.go: sync (fetch all connected mirrors, re-run fold; cache
     population is the seam no-op).
4. Idempotency + refusal (T1.2): every mutating verb keyed by artifact/
   event ID, re-run is "already done"; foreign-section refused locally
   before git.
5. Sanity: gofmt; go vet ./internal/template/... ./internal/cli/...;
   go test ./internal/template/... ./internal/cli/... -race -count=1;
   go list -deps ./internal/cli/... | grep a2ahub — no internal/cache.

## Constraints
- cli.Command.Run returns an exit code; NEVER os.Exit, NEVER touch real
  os.Std* — only cli.IO. JSON output modes stay machine-parseable on error.
- Copy the P1 error idiom; log-or-return (verbs return/print via IO, no
  slog in the verb body); os.Getenv only in actor/credential resolution.
- t.Parallel() everywhere (or // reason: for env-mutating tests); coverage
  floor 70%; idempotent-re-run test lands with each mutating verb.
- No new deps, no new config files beyond .a2a/config.yaml shape P5 defined.

## DO NOT
- DO NOT commit / run git against THIS repo / run make check /
  check-validators / repo-wide go build|test. Scope to your two packages.
- DO NOT edit cli.go, cmd_doctor.go, cmd/a2a, or any core package.
- DO NOT import internal/cache (absent — use the seam).

## Acceptance
- Spec 06 §8 rows 1–7, each with a named go test target.

## Report back
- Files, tests, scoped output.
- The EXACT constructor signatures of your 5 commands and the adapter
  types (cmd/a2a wiring + P7/P8 build against them — feeds the probe).
- Deviations — REQUIRED (esp. any dep cli.go/Deps should carry that it
  doesn't; any core API that fought the wiring; the verify/dispute
  LegalityChecker gap shape).
- Anything skipped + why.
```

## Acceptance

- [ ] Spec 06 §8 rows 1–7 green (lead re-runs scoped tests; lead wires
      cmd/a2a and runs the verbs end-to-end in the integration check).

## Phase log

(detail blocks per S6.f)

## Deferred / follow-ups

- cmd/a2a dispatch wiring = lead, post-wave.
- verify/dispute LegalityChecker + parent-response event gathering = P7/P8
  (backlog rows already filed).
- Real PendingMarker + cache-removal impls = P7.
