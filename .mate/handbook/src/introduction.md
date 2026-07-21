<!-- SYNCED FROM mate@v0.98.0 · mode:sync · DO NOT EDIT — change upstream then `mate pull` -->
# Introduction

**mate is the cross-project harness product**: one SSOT of stack-agnostic
doctrine, neutral Tier-0 skills, machine-readable registries, provider adapters,
and the Go `mate` CLI that syncs it all into consumer projects (drift-gated) and
back (promote).

This handbook is the operator-facing reference — plain-language, searchable,
offline. It has **two page classes**, and the split is load-bearing (see the
[documentation doctrine](../../doctrine/code/documentation.md)):

- **Reference** pages (CLI, skills, registries, doctrine index, gates) are
  **generated** from the machine sources and drift-gated. Never hand-edit them —
  run `mate docs gen`.
- **Guides** are hand-written to a fixed template and *link* to the doctrine for
  the precise statement. A guide never restates a rule — that would be a second
  source of truth, the disease this product cures.

Build the book with `mate docs build`, read it with `mate docs open`, and gate it
with `mate docs check` (coverage + dead-link + generated-drift — no mdBook
needed). The handbook is **synced into consumers** (a tree artifact): `mate pull`
lays the release-pinned book under the consumer's handbook path, and `mate docs
open` from that consumer resolves it via the lock — so every project reads the
handbook matching its own locked release, offline. Authoring (`gen`/`check`) stays
SSOT-side; consumers receive the pre-generated pages.
