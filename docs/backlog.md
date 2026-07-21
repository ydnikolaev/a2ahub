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
