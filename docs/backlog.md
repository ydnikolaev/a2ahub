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
- [ ] Proposal (operator decision, D-021-sensitive): `a2a init` offers
      (consent-gated Y/n, `--yes` for automation) to append a ~3-line
      a2ahub pointer block (8.1 session-start floor + skill reference) to
      the consumer repo's own `AGENTS.md`/`CLAUDE.md` — project docs, not
      harness config, readable by both provider surfaces. Skill onboarding
      mode makes the same offer when the block is absent. Would need a
      spec 06/09 amendment; complements (not replaces) the §8.8 adapter
      distribution.
