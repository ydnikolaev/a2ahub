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
package release
