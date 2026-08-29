// Package datapackage implements the contract data exchange loop: packing a
// result against an exact contract version, delivering it as one immutable
// attempt, fetching it back with its digests verified, and judging it into a
// structured verification report.
//
// Spec: docs/features/archive/agent-ops-2026-07/specs/05a-contract-data-exchange-loop.md
//
// The package owns no transport and no lifecycle. A delivery reaches the space
// through the existing write funnel, carried by an existing handoff; the fold
// table is untouched. What lives here is the part that must be exactly right
// about bytes: which files are in an attempt, what each one hashes to, whether
// it conforms to the contract that was agreed, and in what order those
// questions are answered.
//
// # Digest discipline
//
// There is exactly one digest implementation in this repository and it is not
// in this package: artifact.Digest and artifact.CombineDigestPairs. A package
// manifest and a contract carried set are hashed by the same two functions, so
// a verdict can always name bytes the other side can reproduce. Nothing here
// may grow a second one.
package datapackage

// LocalFile is one file read from a local directory before it becomes a
// manifest entry: the safe-walk half of packing produces these, and the
// entry-set half consumes them.
//
// RelPath is package-root-relative, already proven contained and symlink-free
// by the walker. Bytes is the file's exact content — entries are bounded well
// below the point where holding one resident matters, and every digest and
// conformance check needs the same bytes, so reading once is both simpler and
// safer than re-opening a path that may have changed underneath us.
type LocalFile struct {
	RelPath string
	Bytes   []byte
}
