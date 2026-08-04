package datapackage

import "fmt"

// Bounds is the configured limit set of spec 05a §T2.4 and §T2.5's L-1
// sibling row: per-entry bytes, total package bytes, entry count, and
// record count. Every one of them is enforced at BOTH pack and fetch (T2.4:
// "size, record count or entry count above the configured bound" is refused
// at both) — the bound exists once, and both directions of the loop check
// against it, rather than each side inventing its own ceiling.
//
// There is deliberately no decompression-ratio bound here. An earlier draft
// of the spec required one; it was struck (plan D-9) because nothing this
// package handles is compressed — format is json|ndjson, no compression
// wrapper is declared anywhere — and a refusal that can never fire is worse
// than none: it reads as protection it does not provide.
//
// contract.MaxFileBytes (1 MiB) is deliberately NOT inherited (plan D-2). A
// dataset entry is legitimately larger than a contract schema/fixture file,
// and the record-at-a-time ndjson validation path this package's conformance
// side uses never needs a whole entry resident to check it — so bounding an
// entry the way a contract file is bounded would refuse ordinary deliveries
// for no safety gained.
type Bounds struct {
	// MaxEntryBytes bounds one entry's exact byte size. A json entry is
	// still validated as a single document and so is held fully resident by
	// the conformance side regardless of this bound; ndjson entries never
	// need to be, but the same ceiling applies to both so one entry cannot
	// be made arbitrarily large by choosing the format that streams.
	MaxEntryBytes int64

	// MaxPackageBytes bounds the sum of every entry's size in one attempt —
	// the "size_bytes" field of the manifest. This is the ceiling on one
	// attempt, not on the loop: spec §9.3 already accepts that the growth
	// curve across repeated attempts is unbounded for the pilot (every
	// failed attempt stays in the exchange space's history), and bounding
	// one attempt's own size is what keeps that accepted growth curve
	// survivable rather than compounding it with unbounded single deliveries.
	MaxPackageBytes int64

	// MaxEntries bounds entries[]'s length. A data delivery is legitimately
	// shardier than a contract descriptor (contract.MaxExplicitFiles = 256
	// caps a schema's own explicit file list), because a dataset is commonly
	// split across many files rather than authored as a handful of schema
	// artifacts.
	MaxEntries int

	// MaxRecords bounds the sum of every entry's record_count — the
	// producer's honest claim, re-counted by the consumer at verify with
	// the same rule (records.go), so the two sides can never disagree
	// about a delivery this package itself produced.
	MaxRecords int
}

// DefaultBounds returns the shipped default limit set. Every number here is
// a size chosen to be honest for a real delivery — big enough that no
// ordinary dataset attempt is refused for being merely large, small enough
// that a permanent, ever-growing exchange-space history (§9.3) stays
// survivable across a pilot's worth of attempts.
func DefaultBounds() Bounds {
	return Bounds{
		// 128 MiB: comfortably larger than any dataset shard produced by
		// hand-driven agent work in this pilot, while still small enough
		// that a json entry — validated as one whole document and so held
		// fully resident regardless of format — never threatens the memory
		// of an ordinary agent process reading it.
		MaxEntryBytes: 128 << 20,

		// 2 GiB: one attempt's own ceiling. §9.3 already accepts unbounded
		// growth ACROSS attempts for the pilot; this bounds how large any
		// SINGLE attempt may be, so the accepted growth curve stays linear
		// in attempt count rather than compounding with unbounded per-
		// attempt size too.
		MaxPackageBytes: 2 << 30,

		// 2048 entries: eight times contract.MaxExplicitFiles's 256,
		// because a dataset delivery legitimately shards into many more
		// files than a schema descriptor's handful of artifacts, while
		// still bounding the walker's and installer's own per-node work.
		MaxEntries: 2048,

		// 10,000,000 records: large enough for a real dataset delivery
		// (a single ndjson entry can carry millions of records without
		// approaching this), bounded so an unattended future loop (L-1's
		// attempt ceiling, shipped disabled today) cannot be handed an
		// unboundedly large record claim to re-count on every verify.
		MaxRecords: 10_000_000,
	}
}

// CheckEntryBytes refuses size if it is negative or exceeds MaxEntryBytes.
// Exactly MaxEntryBytes bytes passes; MaxEntryBytes+1 refuses — the same
// inclusive-limit convention internal/contract's own MaxFileBytes check uses
// (contract/set.go's readRootContractFile: "before.Size() > MaxFileBytes"),
// so a caller reading both packages' bound checks does not have to remember
// two different conventions for what "the limit" means.
func (b Bounds) CheckEntryBytes(size int64) error {
	if size < 0 || size > b.MaxEntryBytes {
		return fmt.Errorf("%w: entry is %d bytes, exceeds the %d byte per-entry bound", ErrBoundExceeded, size, b.MaxEntryBytes)
	}
	return nil
}

// CheckPackageBytes refuses total if it is negative or exceeds
// MaxPackageBytes. Same inclusive-limit convention as CheckEntryBytes.
func (b Bounds) CheckPackageBytes(total int64) error {
	if total < 0 || total > b.MaxPackageBytes {
		return fmt.Errorf("%w: package is %d bytes, exceeds the %d byte package bound", ErrBoundExceeded, total, b.MaxPackageBytes)
	}
	return nil
}

// CheckEntryCount refuses count if it is negative or exceeds MaxEntries.
// Same inclusive-limit convention as CheckEntryBytes.
func (b Bounds) CheckEntryCount(count int) error {
	if count < 0 || count > b.MaxEntries {
		return fmt.Errorf("%w: package has %d entries, exceeds the %d entry bound", ErrBoundExceeded, count, b.MaxEntries)
	}
	return nil
}

// CheckRecordCount refuses count if it is negative or exceeds MaxRecords.
// Same inclusive-limit convention as CheckEntryBytes. Called by pack once
// every entry's records have been counted with the single rule in
// records.go.
func (b Bounds) CheckRecordCount(count int) error {
	if count < 0 || count > b.MaxRecords {
		return fmt.Errorf("%w: package has %d records, exceeds the %d record bound", ErrBoundExceeded, count, b.MaxRecords)
	}
	return nil
}
