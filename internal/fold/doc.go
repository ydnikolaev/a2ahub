// Package fold implements the a2ahub lifecycle fold engine (spec
// docs/features/v1-min-2026-07/specs/04-fold-engine.md, plan
// docs/the-plan/plan/03-domain.md §3.4-3.5).
//
// It is a pure package: no I/O, no time.Now(), no logging, no goroutines.
// Given the same inputs it always returns the same outputs (ADR-001). Its
// only allowed import beyond the standard library is internal/artifact
// (ID parsing reuse) — see docs/decisions.md ADR-001 and the phase's own
// purity acceptance criterion (spec 04 §8 AC 9).
//
// Callers (internal/validate, internal/cache, internal/space, the v2 hub)
// translate their own validated event/v1 documents into this package's
// Event/Envelope input shapes; fold never parses YAML/JSON, never reads
// git or space.yaml, and never rejects a committed event — illegal or
// unauthorized events are ignored and flagged (3.5 rules 2-3), never an
// error or a panic. The one surface that DOES reject something is
// CheckLegality, used pre-write (before an event is committed) by
// internal/validate's V2 path.
package fold
