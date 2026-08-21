# comment-hostile — one adversarial corpus, asked of every reader

A feedback record's meaning is the VALUES its lines carry, never the lines.
Four independent shell readers of `feedback/inbox/**` compared LINES, and on
2026-08-20 one of them was repaired while the other three were never asked the
question — a named `docs/validator-backlog.md` row (*gate-sibling agreement*).
This directory is the half that asks all of them, in two languages, every time
their teeth run (judge-the-thing-2026-08 P10 §T1.3).

It is a THIRD sibling of `valid/` and `invalid/`, deliberately: those two are
globbed by name (`…/valid/*.yaml`, `…/invalid/*.yaml`) by
`internal/feedback/validate_test.go` and `schemas/feedback/v1/schema_test.go`,
so nothing here is swept into the schema oracle. These files are a READER
oracle, not a schema oracle.

## The contract

| File shape | What it asks |
|---|---|
| `<stem>.dirty.yaml` | a record carrying authoring scaffolding: a `^#` header block, trailing comments on top-level and nested keys, commented-out optional keys, a comment on a list item |
| `<stem>.clean.yaml` | its byte-clean twin, **exactly the same values** |
| `body-diverged.base.yaml` / `.head.yaml` | the NEGATIVE case: scaffolded vs comment-free AND a genuinely rewritten `summary`. Every reader must still report `summary`, and only `summary`, as differing |

**The assertion is the PAIR**: for every `<stem>`, `reader(dirty)` and
`reader(clean)` must agree — no top-level key differs. A reader that cannot
say that about this directory is the next instance of the defect.

## Its consumers

| Reader | Driven from |
|---|---|
| R1 `key_records` / `diff_keys` | `docs/runbooks/feedback-sync.sh --teeth` |
| R2 `_record_field`, R3 `_record_resolution_text` | `scripts/check-feedback-corpus.sh --teeth` |
| R4 `top_level_keys` / `key_block` / `verdict_diff` | `scripts/feedback-intake-policy.sh --teeth` |
| R9 `feedback_blob_key_section` / `feedback_diff_keys` | `docs/runbooks/publish-to-public.sh --teeth` |
| the Go submit-time normalization | `internal/feedback` (`TestNormalizeRecord*`) |

## Two things a later reader will want to "fix", and must not

1. **`scaffolded.clean.yaml` and `body-diverged.head.yaml` begin with a BLANK
   line.** That is not sloppiness. Normalization deletes only the lines a
   comment occupied and changes no other byte (P10 §T1.2's non-reflow
   property, AC #4), so the blank line that separated the twelve-line header
   from `feedback: v1` survives the header's deletion. Deleting it here would
   make the clean twin stop being the byte-exact output of `Normalize`, which
   `internal/feedback`'s golden test asserts.

2. **The nested blocks are indented FOUR spaces, not two.** That is what
   `yaml.v3` re-emission produced in the six real scaffolded records, and
   leaving it at four is the point: normalization must not reflow. A fixture
   re-indented to two would silently stop testing the property this phase was
   cut for.

## What is deliberately NOT here

A record whose `#` sits inside a multi-line *flow* (quoted) scalar wrapped
across lines. The shell readers protect block scalars (`|`, `>`) and
single-line quoted scalars; the Go normalizer protects all three. No record in
the corpus of record has ever carried that shape, and inventing a fixture the
shell half cannot satisfy would buy a red gate rather than a guarantee.
