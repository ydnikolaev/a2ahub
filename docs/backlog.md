# Backlog

<!-- Seeded once by mate (structure standard: .mate/doctrine/code/structure.md).
     This file is YOURS: mate will never touch it again. Role: the open-items
     queue — one bullet per item, `- [ ]` open / `- [x]` resolved; archive
     resolved rows to a sibling file when this grows. -->

## Open

- [ ] `internal/artifact` ID grammar narrows the plan: `system` is
      hyphen-free and standing slugs may not start with a digit-run +
      hyphen (e.g. `24-7-monitoring` rejects) — required for unambiguous
      `<PREFIX>-<system>-<slug>` parsing. Revisit only if a real
      participant needs either shape (spec 01 §Amendments, wave 1).
- [ ] `internal/artifact.Digest` returns the string form only; add a
      raw-bytes variant if a downstream consumer (P5 host, P8
      verify-export) turns out to need it.
- [ ] Manifest referential/policy checks (github-login→system map
      integrity, participant/owner sanity) are UNOWNED after wave 2: P3
      shipped schema-class manifest validation only; `internal/space`'s
      seam is schema-class-only today. Assign at wave-3 (P6/P9) planning —
      candidate home: a manifest surface in `internal/validate` (D-011).
- [ ] `validate.Result`/`Violation` carry no JSON tags; the spec 03 §7
      snake_case wire shape (incl. how `Severity` serializes or is
      omitted) is the CLI renderer's to produce — pin at P6.
- [ ] Format assertions are OFF in `internal/schema` (2020-12 default;
      AssertFormat turned a bad `created` into a hard error and no SCH-
      code exists). Decide whether RFC-3339 field enforcement gets its own
      SCH row + fixtures (P2-style commit) or stays annotation-only.
- [ ] fold.CheckLegality has no verify/dispute case (they target a
      response, not the parent — need Kind=KindResponse + response
      substate + parent envelope); naive P8 wiring fails closed+loud but
      the D-024 closure model is untested at the pre-write reject surface.
      WAVE-3/P8 INPUT: wire + test verify/dispute legality with the real
      caller (auditor gave the exact repro). No live defect today.
- [ ] fold has no exported helper to gather a parent exchange's events
      PLUS its attached responses' verify/dispute events (those carry the
      response ID as Subject). A P7-cache query by `Subject == parentID`
      alone silently misses them → stuck substate. WAVE-3/P7 INPUT: add
      the event-gathering helper (or document the query contract) when P7
      builds the cache that needs it.
- [ ] Decision-supersede successor-authorship is structurally unverifiable
      in `internal/fold` (membership-only check shipped) — real
      enforcement gap; P8 (lifecycle verbs) should decide where it lands.
- [ ] fold `CandidateEvent` carries no envelope; P6's LegalityAdapter
      works around it with `RegisterEnvelope`. Consider adding envelope
      facts to CandidateEvent (or a fold API refinement) at P8/hub era so
      the pre-write legality seam doesn't need a side-channel inject.
- [ ] `internal/host` has no generic repo-reachability or branch-protection/
      required-check read primitive (all methods PR-scoped). Doctor works
      around it via git mirror + fs reads; a future `a2a doctor --space`
      host-drift diff (v2) needs the new primitive.
- [ ] P8 `a2a contract new <slug>` must translate the positional slug into
      `--slug` when delegating to P6's `a2a new` (not verbatim passthrough).
- [ ] Doctor credential-expiry check unimplementable — no expiry field on
      any config/manifest type; add one (§9.3 "warns on approaching
      expiry") if/when the credential model grows it.
- [ ] V3 CI workflow invokes placeholder `a2a validate --ci --mode=...`
      flags P6's validate verb doesn't have — reconcile at P10 (grow a
      `--ci` V3 mode, or rewrite the workflow step).
- [ ] `a2a init` derives the connected-space id from the repo URL's last
      path segment (`initSpaceIDFromURL`), but the id that must match an
      artifact's `space` field (and space.yaml's `space:`) is the space's
      declared id, not the repo name. When repo-name ≠ space-id, `submit`
      space resolution fails. Surfaced by the wave-3 live e2e. Fix at P11
      (real getvisa space) or add an explicit `--space-id`/derive from
      space.yaml after connect.
- [ ] Plan §5.7 says the multi-file digest tree is over "repo-relative-path"
      but the shipped (and only workable) basis is CONTRACT-ROOT-relative
      (verify-export operates on an arbitrary local dir). Amend §5.7 to say
      contract-root-relative. P12 must match this basis.
- [ ] Plan D-023 says "each publish event records commit SHA" but the SHA is
      only known post-commit (WriteFunnel returns it after the event is
      already committed) — publish events carry `digest` only, `commit` is
      empty; `contract diff` resolves versions via git history instead.
      Amend D-023 to drop/defer the commit-SHA clause. (Pre-existing P6 gap
      too — cmd_submit never set CommitSHA.)
- [ ] fold `Apply` has no dispatch for a response's own draft→submit
      mini-lifecycle (§3.4.6) — verify/dispute legality is closed but a
      response sub-state beyond verified/disputed would need a fold pass.
- [ ] Load-induced flake: `TestSubmitEndToEndSingleArtifact` (P6, cli) fails
      ~1-in-6 under full `make check` parallel load with `-race` — the
      funnel's `git checkout -B <branch>` subprocess occasionally errors
      under heavy concurrency. Passes cleanly in isolation and on re-run.
      Harden the funnel git ops (retry on transient / robust branch reset)
      or serialize the git-heavy e2e submit tests. Anti-pattern #17/#18.
- [ ] Statusline TTL-refresh is unreliable in the one-shot CLI process
      (wave-4 audit, MED, downgraded from HIGH). `triggerRefreshIfStale`
      (`internal/cache/statusline.go`) spawns a detached, unwaited
      `go func(){...}()` running `git fetch`; but `main()` is
      `os.Exit(run(...))`, so the process tears down the instant `statusline`
      returns — the goroutine (and its `git fetch` subprocess) races process
      exit with no guarantee it is scheduled long enough to finish. Render
      itself stays correct (returns stale-but-valid data); only the freshness
      optimization degrades. Correct primitive for a CLI is a DETACHED
      SUBPROCESS (`exec.Command` without `Wait`, freed from the parent's
      process group), not an in-process goroutine. Harden when the statusline
      refresh path is revisited (P10 perf work touches this file).
- [ ] `respond`/`contract deprecate` deterministic-id idempotency embeds the
      UTC date (via `MintExchangeIDAt`), so a retry that crosses midnight
      mints a different id → a duplicate PR. Never fires in a
      seconds-to-minutes retry window. Deferred: derive the date
      deterministically too (or key the branch on a pure content hash) if a
      cross-midnight retry ever proves real. (Spec 08 §11 wave-4 audit-fix.)
- [ ] LOW wording cleanup: `internal/cache/*.go` code comments (inbox.go,
      mirror.go, statusline_test.go) still cite a nonexistent "Deviations
      report"; the real referent is now specs 07/08 §11. Reword the comments
      to point at §11 next time those files are touched.
- [ ] Proposal (operator decision, D-021-sensitive): `a2a init` offers
      (consent-gated Y/n, `--yes` for automation) to append a ~3-line
      a2ahub pointer block (8.1 session-start floor + skill reference) to
      the consumer repo's own `AGENTS.md`/`CLAUDE.md` — project docs, not
      harness config, readable by both provider surfaces. Skill onboarding
      mode makes the same offer when the block is absent. Would need a
      spec 06/09 amendment; complements (not replaces) the §8.8 adapter
      distribution.
