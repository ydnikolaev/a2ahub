package datapackage

import (
	"fmt"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// EntrySet is the deterministic digest half of a package's entries[]: the
// same discipline internal/contract's carried set uses (artifact.Digest +
// artifact.CombineDigestPairs — spec 05a §5, plan D-3: there is exactly
// one digest implementation in this repo, and this file calls it; it does
// not reimplement it).
//
// Entries here carry only what THIS file can prove from bytes alone:
// Path, SizeBytes, Digest. Role, MediaType, ConformsTo and RecordCount are
// contract-conformance knowledge this file does not have — LocalFile
// (doc.go) carries no such metadata — and are the caller's (pack's, a
// later wave's) to fill in once each file has been classified against the
// contract, before the manifest is emitted.
type EntrySet struct {
	Entries         []Entry
	PerFileDigest   map[string]string
	AggregateDigest string
}

// BuildEntrySet is the pack-time half of the digest discipline: given
// locally-read files (already proven contained and symlink-free by the
// caller's own safe-walk — this function trusts LocalFile.RelPath, it does
// not re-walk a filesystem), it computes each file's digest and the
// aggregate over all of them.
//
// Determinism (L-4): entries are sorted by path before anything else
// happens, so two calls over the same input — regardless of the order
// LocalFile was supplied in — produce byte-identical output (same entry
// order, same PerFileDigest, same AggregateDigest). Nothing but path and
// bytes reaches the digest or the entry order: no map iteration with
// unstable order survives into the return value, no wall clock, no
// filesystem order.
func BuildEntrySet(files []LocalFile) (EntrySet, error) {
	sorted := append([]LocalFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RelPath < sorted[j].RelPath })

	seen := make(map[string]struct{}, len(sorted))
	entries := make([]Entry, 0, len(sorted))
	perFile := make(map[string]string, len(sorted))
	for _, f := range sorted {
		if _, dup := seen[f.RelPath]; dup {
			return EntrySet{}, fmt.Errorf("datapackage: BuildEntrySet: %w: %q", ErrDuplicateEntryPath, f.RelPath)
		}
		seen[f.RelPath] = struct{}{}
		digest := artifact.Digest(f.Bytes)
		perFile[f.RelPath] = digest
		entries = append(entries, Entry{
			Path:      f.RelPath,
			SizeBytes: int64(len(f.Bytes)),
			Digest:    digest,
		})
	}
	return EntrySet{
		Entries:         entries,
		PerFileDigest:   perFile,
		AggregateDigest: artifact.CombineDigestPairs(perFile),
	}, nil
}

// VerifyEntrySet is the fetch/verify-time half of the digest discipline
// (L-3): each declared entry's bytes are checked against ITS OWN declared
// digest as they are read, in sorted-path order, and the aggregate is
// recomputed from the verified entries — never the other way round.
//
// There is no code path here that ever compares to an externally supplied
// aggregate before every declared entry has individually verified: an
// aggregate the caller might otherwise have trusted is not even an input
// to this function. A package whose aggregate would match some claimed
// value while one entry's bytes do not match ITS declared digest is
// refused at that entry, by construction, before any aggregate is
// computed at all — there is no way to reach an aggregate comparison
// first, because none is ever made.
//
// declared is the manifest's own entries[] (already decoded, already
// schema-valid); files is what was actually received. Every declared path
// must have a matching file; every declared digest and size must match
// what that file's bytes actually hash to and measure — a mismatch on
// either refuses the whole verification, naming the offending path and
// wrapping ErrDigestMismatch; a declared entry with no delivered bytes at
// all wraps ErrEntryMissing instead, because nothing disagreed — the file
// is simply absent, and the producer's next move differs accordingly. An
// extra file present in files but not
// declared is not itself refused here — containment of what may be
// present on disk is fetch's own walker's concern (plan D-4), not this
// digest-only function's.
func VerifyEntrySet(declared []Entry, files []LocalFile) (EntrySet, error) {
	sortedDeclared := append([]Entry(nil), declared...)
	sort.Slice(sortedDeclared, func(i, j int) bool { return sortedDeclared[i].Path < sortedDeclared[j].Path })

	byPath := make(map[string][]byte, len(files))
	for _, f := range files {
		byPath[f.RelPath] = f.Bytes
	}

	verified := make([]Entry, 0, len(sortedDeclared))
	perFile := make(map[string]string, len(sortedDeclared))
	for _, entry := range sortedDeclared {
		raw, present := byPath[entry.Path]
		if !present {
			return EntrySet{}, fmt.Errorf("datapackage: VerifyEntrySet: %w: %q", ErrEntryMissing, entry.Path)
		}

		actualDigest := artifact.Digest(raw)
		if actualDigest != entry.Digest {
			return EntrySet{}, fmt.Errorf("datapackage: VerifyEntrySet: %w: entry %q declared digest %s, computed %s", ErrDigestMismatch, entry.Path, entry.Digest, actualDigest)
		}
		if actualSize := int64(len(raw)); actualSize != entry.SizeBytes {
			return EntrySet{}, fmt.Errorf("datapackage: VerifyEntrySet: %w: entry %q declared %d bytes, received %d", ErrDigestMismatch, entry.Path, entry.SizeBytes, actualSize)
		}

		verifiedEntry := entry
		verifiedEntry.Digest = actualDigest
		verifiedEntry.SizeBytes = int64(len(raw))
		verified = append(verified, verifiedEntry)
		perFile[entry.Path] = actualDigest
	}

	return EntrySet{
		Entries:         verified,
		PerFileDigest:   perFile,
		AggregateDigest: artifact.CombineDigestPairs(perFile),
	}, nil
}

// digestForPayload lives HERE, beside BuildEntrySet/VerifyEntrySet, rather
// than in attach.go where it is used, because this file is the one place in
// this package that composes an aggregate digest — check_contract_carried_set.sh
// holds CombineDigestPairs to a closed per-file allowlist for exactly that
// reason, and the honest way past that gate is to put the third composition
// where the other two already are, not to widen the allowlist by a file.
//
// digestForPayload computes the SAME digest internal/space's own possession
// check (recomputeAttachmentDigest, possession.go) computes over an
// identical payload — reproduced here, never called, because ADR-001
// forbids internal/space from importing internal/datapackage (the same
// mirror-not-import idiom possession.go's own doc comment already states
// for blobPrefix/dataPackagePrefix). Two branches, matching exactly: a
// single-entry payload's digest is that ONE file's own artifact.Digest
// (mirroring NewAttachmentFromBytes' own shape); a multi-entry payload's
// digest is artifact.CombineDigestPairs over each entry's own
// artifact.Digest (mirroring NewAttachmentFromDirectory/BuildEntrySet's
// shape, entryset.go).
//
// P10 (agent-exchange-2026-08) spec 10 wave B's own hazard, found by that
// wave's advisor before it was shipped: BuildEntrySet's own AggregateDigest
// ALWAYS takes the multi-entry CombineDigestPairs branch, even when the
// walked directory holds exactly one file — so NewAttachmentFromDirectory's
// existing Digest (entrySet.AggregateDigest) disagrees with
// recomputeAttachmentDigest's single-entry branch for that one case, and a
// blob attach sourced from a one-file directory would submit with a digest
// that CheckAttachmentPossession (space, on origin/main) can never match —
// B32 again, with a different message. NewAttachmentFromDirectoryWithPayload
// (below) computes its Digest via THIS function, over the same payload it
// returns, specifically to close that hole; NewAttachmentFromDirectory
// itself is UNCHANGED (its own doc comment's entrySet.AggregateDigest shape
// stays as documented — no existing caller of that function is touched by
// this wave).
func digestForPayload(payload map[string][]byte) string {
	if len(payload) == 1 {
		for _, raw := range payload {
			return artifact.Digest(raw)
		}
	}
	perFile := make(map[string]string, len(payload))
	for path, raw := range payload {
		perFile[path] = artifact.Digest(raw)
	}
	return artifact.CombineDigestPairs(perFile)
}
