# bindings.md — where a contract lands in YOUR code

> **The rule this demonstrates**: a producer can already prove their
> published export matches their code (`generated_from` +
> `a2a contract verify-export`, §5.3). A consumer has no equivalent — it can
> declare in the space that it depends on a contract (`consumes.yaml`), but
> nothing local records *where in its own repo* that dependency actually
> lands. This page is that missing half: a tracked file, in the consumer's
> OWN repo, that a rename and its binding update travel together in one
> reviewable commit.

## What it is — and what it is NOT

A **tracked** YAML file, versioned in the consumer's own repository, next to
the code it describes. Suggested path: `.a2a/bindings.yaml`, alongside the
project's `.a2a/config.yaml`. **This file must be committed, not
gitignored** — unlike `.a2a/cache/` (a refresh-on-`a2a sync` mirror cache)
and drafts under `.a2a/staging/`, both of which are throwaway. A bindings
file that only exists on one machine answers nothing.

**No schema ships for this file.** `schema: bindings/v1` below is a
convention marker, not a validated version — nothing in this corpus checks
this file today (see "the guard, deliberately deferred"). Copy the shape
below verbatim; review is the only check it gets until that changes.

**Local-tracked, never space-visible — and this is deliberate, not an
oversight.** The producer's decisions that matter to THEM are already
visible in `consumes.yaml`: **who** consumes a contract, and **which major**
version. Publishing the paths and URLs on top of that gives the producer
zero additional decision-power — knowing that `getvisa` renders a contract
at `internal/render/fee.go` line whatever doesn't change what the producer
does differently — while it leaks the consumer's private repo structure
into a shared, cross-organization space. An aggregate/counts middle
ground ("N sites depend on this, 0 paths shown") was considered and
rejected too: it is data with no consumer, since nobody on the producer
side acts differently on a count either.

## Fields

| Field | Meaning |
|---|---|
| `contract` + `major` | Joins the consumer's own `consumes.yaml` entry, at its own granularity — same contract ID, same major version. |
| `sites[]` — `{path, role}` | Where in THIS repo the contract is actually read or rendered. `path` is repo-relative; `role` is free text (`read`, `render`, `cache`, whatever the project's own vocabulary is — no closed enum here, it never leaves this repo). |
| `surfaces[]` | User-visible URLs — what makes a retraction's `applied` evidence citable (see [retraction.md](retraction.md)) and a deprecation's migration checklist concrete, rather than "somewhere on the site". |

## Template

```yaml
# .a2a/bindings.yaml — tracked, NOT gitignored (unlike .a2a/cache/)
schema: bindings/v1        # convention marker only — no schema validates this file yet
system: <your-system-id>   # matches this same field in your consumes.yaml
bindings:
  - contract: <XC-id>
    major: <int>            # joins consumes.yaml's entry for the same contract at this granularity
    sites:
      - path: <repo-relative path>
        role: <read|render|cache|...>   # your own vocabulary — never validated, never space-visible
      - path: <another repo-relative path>
        role: <...>
    surfaces:
      - <public URL where this contract's data appears to an end user>
  # one entry per contract dependency this repo actually binds to code —
  # a consumes.yaml dependency with no entry here has no known landing site
```

## When an agent writes or updates it — the anchored moments

Two, both named in the conventions spec, neither is "keep it current":

1. **At `a2a contract adopt`** — the moment this system registers itself as
   a consumer (writes the `consumes.yaml` entry), add or update the
   matching `bindings` entry in the same commit, even if `sites`/`surfaces`
   start as a single placeholder line. The registration and the binding
   should never be two separate, driftable acts.
2. **In any commit that moves a cited `sites[].path`** — a rename, a file
   split, a move to a new package. The binding update travels in the SAME
   commit as the code change, not a follow-up. This is the whole point of
   putting the file in the consumer's own repo instead of the space: the
   thing that keeps it honest is the same PR review that already looks at
   the code change, not a separate cross-repo synchronization step.

A project MAY generate this file with its own tooling (a script that greps
for contract references, a codegen step) instead of hand-authoring it — the
future guard (below) does not care about provenance, exactly as
`generated_from` does not care whether a contract's schema was hand-written
or exported by a build step.

## Measurement moment, and what would falsify this — read this before building more

This is the one piece of this phase with a stated experiment, not just a
convention (conventions spec §10): **the first real deprecation in
`getvisa`**, one of the operator's two real repos, tracked from week one.

- **Bindings pass**: when that deprecation lands, the bindings file answers
  "what changes, am I done" without falling back to `grep` across the repo.
  That is the hand-verification earning its gate — build the `doctor` row
  described below.
- **Bindings fail**: the file was stale exactly when the migration needed
  it. **This is decisive evidence AGAINST building more** — not a bug to
  patch with a stricter format or a reminder. If a two-repo, agent-operated,
  skill-instructed setting cannot keep five lines of YAML current through
  its own review process, a schema and a verb would not have saved it
  either; the fix belongs in that project's own harness (a pre-commit hook,
  a stricter local review habit), not in a2ahub's corpus. Record the
  outcome in the conventions spec's §11 (append-only amendments) either
  way — both outcomes are informative, and neither costs a release.

## The guard, deliberately deferred

Three checks, all stack-agnostic (paths and joins, no language semantics),
proposed as one future `doctor` row in PASS-with-advisory shape, **built
only if the experiment above passes**:

- **Coverage**: every `consumes.yaml` dependency has at least one
  `bindings` entry.
- **Existence**: every `sites[].path` cited actually exists on disk.
- **Reverse coverage**: no `bindings` entry cites a contract absent from
  `consumes.yaml`.

**Explicitly rejected, not merely postponed**: a content-freshness digest
(hashing the cited file and flagging drift on every edit). It would red on
every unrelated change to a cited file — a row that fires weekly on
perfectly healthy code trains its reader to ignore it. The residual rot a
digest would catch — "the path still exists but no longer actually reads
the contract" — is semantic and stack-specific (R-018's hard boundary); the
moment that actually catches it is the deprecation migration walk itself,
which is exactly what the experiment above measures.
