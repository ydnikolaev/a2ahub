# Validator backlog

<!-- Seeded once by mate (validation doctrine §7: .mate/doctrine/code/validation.md).
     This file is YOURS: mate will never touch it again. Role: the capture → build
     queue for validation GATES. The moment an unguarded invariant is spotted, one
     row lands here; it is built later, in batches, via `/mate-validator drain`. -->

Capture points are wired into the lifecycle, not a separate ritual: self-review,
implementation close-out, the audit loop, and the design-time trigger ("new
entity / SSOT / boundary / convention → does it need a gate?").

## Drain brake

brake: wip-limit=8

Exactly one brake, per validation §7 — capture is cheap and ambient *by design*, so
fill-rate runs hot; building is expensive (gate + teeth + wiring + maintenance
forever). Un-braked, this file rots into a graveyard, and a graveyard backlog is
theater wearing a process costume.

The three legal brakes are `wip-limit=<n>` · `cadence=<text>` · `expiry=<n>w`.
**Prefer `wip-limit`**: it is the only one a deterministic gate can *enforce* rather
than merely confirm you declared (a cadence needs an epic tracker; an expiry needs a
clock, and a clock-reading gate is non-deterministic). At the limit, a new capture
reddens the gate until you drain one — build it, or close it as §0-economics "not
worth it". The backpressure forcing that verdict *is* the brake.

## Row template

- **Layer** — `core` (harness/sync machinery → promote upstream) · `stack`
  (language/framework-generic → a profile) · `project` (this repo only).
- **Dimension** — one row of the validatable-dimension taxonomy (validation doctrine).
- **Tier** — `fast` (host-side, <1s) · `slow` (docker/e2e lane) · `nightly`.

## Open

| Date | Source | Layer | Dimension | Invariant → proposed gate | Tier | ref |
|---|---|---|---|---|---|---|

## Done

| Date | Source | Layer | Dimension | Invariant → proposed gate | Tier | ref |
|---|---|---|---|---|---|---|
