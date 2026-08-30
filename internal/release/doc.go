// Package release implements the P19 (OP-217) self-update primitives:
// fetching the latest product-repo release (Source), verifying a downloaded
// asset (Verifier — an unconditional ChecksumVerifier plus a T2-interim
// UNVERIFIED signature slot, checksum-only per the 2026-07-22 amendment),
// downloading the platform asset triplet (Download), the post-download
// version self-check (SelfCheckVersion), the atomic same-filesystem binary
// swap (Swap), version-floor resolution (Resolve), and the TTL'd
// machine-level "latest known release" cache (CheckState/ReadCheck/
// WriteCheck/ReadLatest) plus its background checker (NewChecker).
//
// This package is the SEAM the CLI verb (`a2a update`, a later wave) and
// every advisory surface (statusline, inbox/outbox, doctor, MCP a2a_read)
// build on. It imports internal/version for the bare-version comparator and
// otherwise stdlib only (checksum-only interim — no sigstore-go, spec 19 T2
// amendment #1); it NEVER imports internal/space, internal/cli, internal/mcp,
// or internal/cache (spec 19 footprint) — the version floor is computed by
// the CLI from connected-space manifests and passed into Resolve, never read
// here.
//
// Nothing in this package ever swaps or downloads outside an explicit
// caller-driven Download/Swap invocation (D-021): NewChecker's background
// checker closes over a Source only, so it is structurally unable to reach
// either.
//
// THE SIGSTORE TRUSTED ROOT IS A COMPILE-TIME INPUT TO THIS PACKAGE. cosign.go
// //go:embed-s it, so changing its bytes changes what this package compiles,
// and this package's own tests are what judge the result (cosign_test.go
// verifies fully offline against exactly these frozen keys). Declared below
// for the reason .claude/rules/check-convention.md gives for internal/notes
// <- releasenotes/**: an invariant that lives only in Go tests has to be
// reachable from a diff that touches no Go file.
//
// Before this declaration the file was CLAIMED by release-notes-freshness's
// `internal/**` glob and read by nothing — that gate invokes only git
// subcommands, an inline awk program and shell builtins, so
// computed-not-listed-2026-08 P1b's proven-empty arm now reports it as an
// unbacked claimant rather than backing it on zero evidence. A path a gate
// claims but cannot judge is worse than an unclaimed one, because the claim
// reads as coverage.
//
// lane-inputs:
//
//	internal/release/trusted_root.json
package release
