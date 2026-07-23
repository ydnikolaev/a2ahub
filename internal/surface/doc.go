// Package surface holds the provider-surface registry: the verified facts
// about where each AI coding agent looks for a skill (its "skills home")
// and where it reads always-on context (CLAUDE.md, AGENTS.md, …), plus the
// detection and link logic that use those facts.
//
// # Why a registry, not a name check
//
// Every fact in this package was read from the provider's own current docs
// and is written down here with its SourceURL and VerifiedOn date, so it can
// be re-verified instead of recalled — the harness-discipline rule ("a
// provider fact is read, written down, and branched on by surface — never
// recalled") applied to a2a's own product code (spec 32 §2.1). Code that
// needs a provider fact asks a Surface row a question ("does this surface
// read AGENTS.md?"); it never branches on a provider id string inline. A new
// provider is a new row, not a new if-statement.
//
// This package imports nothing of a2ahub's own; internal/cli is the (later)
// consumer.
package surface
