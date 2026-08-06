// Package lane is the P12 lane derivation: parse the `lane-inputs:`
// declarations that live next to the gates they describe, discover the
// corpus of phases that MUST carry one, reconcile the two directions,
// intersect declared globs against a changed-file set, and estimate the
// selected phases' duration from telemetry.
//
// It owns the DERIVATION only. Whether a gate script reads a path it did
// not declare (the "honesty extractor") is a later phase and is out of
// scope here — spec docs/features/active/agent-ops-2026-07/specs/12-lane-derivation.md
// §3, plan .../plans/12-lane-derivation.plan.md D-1..D-9.
//
// Every extractor in this package refuses loudly — via a returned error or
// a Refusal value — on a shape it does not understand. It never returns a
// silently-empty result (testkit/contractroots is the idiom this package
// copies, not literally imports: internal/lane may import no other
// internal/ package — it is harness, not product).
package lane
