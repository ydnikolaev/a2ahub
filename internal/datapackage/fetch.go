package datapackage

import (
	"context"
	"fmt"
	"time"
)

// Fetch is `a2a data fetch`'s pure core (spec 05a §T1, §T2.4, §T2.5 L-3,
// plan D-4/D-5): given a decoded manifest, the delivered bytes and a
// destination, it proves the delivery before a single byte is written to
// destination, then installs it atomically. It owns no transport — the
// space-side half of `a2a data fetch` (reading the package out of the local
// mirror) is a later wave's concern; Fetch is called with files already read
// into memory.
//
// now is the caller's own "now", never read from the wall clock inside this
// function — the same injectable-time shape id.go's MintPackageIDAt already
// uses — so the expiry check is deterministic in tests.
//
// Order is the guarantee (L-3, AC-4, AC-11), and each step below runs only
// if every earlier one refused nothing:
//
//  1. expires_at is checked first, against now. Before anything else: an
//     expired package is not fetched, not verified, not partially examined.
//  2. bounds are re-checked against the DELIVERED bytes (files), never
//     against the manifest's own claimed size_bytes or entries[] sizes —
//     the manifest is the producer's claim, and a consumer that trusts a
//     claimed size has no bound at all.
//  3. every declared entry's digest is verified one at a time, before
//     assembly, via VerifyEntrySet (entryset.go, L-3): a package whose
//     aggregate would match while one entry does not is refused AT THAT
//     ENTRY, because VerifyEntrySet never reaches an aggregate computation
//     before every declared entry has individually verified. This function
//     does not recompute the aggregate any other way.
//  4. only once every entry has individually verified is the manifest's own
//     declared aggregate_digest compared against what step 3 just verified
//     — catching a manifest whose aggregate_digest field disagrees with its
//     own entries, a distinct defect from a tampered entry and reached only
//     after, never instead of, the per-entry check.
//  5. only then is InstallLocalFiles called, which owns containment, symlink
//     refusal and the atomic no-replace install; it accepts a byte-identical
//     destination as AlreadyPresent and refuses a divergent one with
//     ErrDestinationDiverged, leaving it untouched.
//
// No byte reaches destination before step 5, and step 5 is the only step
// that writes.
func Fetch(ctx context.Context, doc Document, files []LocalFile, destination string, bounds Bounds, now time.Time) (InstallOutcome, EntrySet, error) {
	if err := ctx.Err(); err != nil {
		return "", EntrySet{}, err
	}

	// Step 1: expiry, first and unconditionally.
	expiresAt, err := time.Parse(time.RFC3339, doc.ExpiresAt)
	if err != nil {
		// Not ErrExpired: this function cannot tell whether an unparsable
		// timestamp is past or future, and claiming "expired" when the
		// honest answer is "unreadable" would be a lie the next attempt
		// could not act on correctly. No sentinel in errors.go covers a
		// malformed timestamp; see this brief's deviation report.
		return "", EntrySet{}, fmt.Errorf("datapackage: Fetch: parse expires_at %q: %w", doc.ExpiresAt, err)
	}
	if now.After(expiresAt) {
		return "", EntrySet{}, fmt.Errorf("datapackage: Fetch: %w: expires_at %s, now %s", ErrExpired, doc.ExpiresAt, now.Format(time.RFC3339))
	}

	// Step 2: bounds re-checked at fetch, against the delivered bytes.
	if err := fetchCheckBounds(files, bounds); err != nil {
		return "", EntrySet{}, err
	}

	// Step 3: per-entry digest verification, before assembly and before any
	// write. VerifyEntrySet owns the entry-before-aggregate ordering; this
	// function makes no other digest comparison until it returns.
	verified, err := VerifyEntrySet(doc.Entries, files)
	if err != nil {
		return "", EntrySet{}, fmt.Errorf("datapackage: Fetch: %w", err)
	}

	// Step 4: the manifest's own declared aggregate, checked against what
	// was just verified — reached only if every entry above already
	// individually matched its own declared digest.
	if verified.AggregateDigest != doc.AggregateDigest {
		return "", EntrySet{}, fmt.Errorf("datapackage: Fetch: %w: manifest aggregate_digest %s, verified %s", ErrDigestMismatch, doc.AggregateDigest, verified.AggregateDigest)
	}

	// Step 5: the only step that writes.
	outcome, err := InstallLocalFiles(ctx, destination, files, bounds)
	if err != nil {
		return "", EntrySet{}, fmt.Errorf("datapackage: Fetch: %w", err)
	}
	return outcome, verified, nil
}

// fetchCheckBounds re-checks the configured Bounds against the bytes
// actually delivered — never against the manifest's own claims — before a
// single digest is computed. InstallLocalFiles enforces the same bounds
// again on the same files immediately before it writes (defense at the
// write boundary too), but that alone would let an oversized or
// too-numerous delivery pay the full per-entry digest cost of step 3 before
// ever being refused; checking here keeps "bounds before digesting" true as
// well as "bounds before writing".
func fetchCheckBounds(files []LocalFile, bounds Bounds) error {
	if err := bounds.CheckEntryCount(len(files)); err != nil {
		return fmt.Errorf("datapackage: Fetch: %w", err)
	}
	var aggregate int64
	for _, f := range files {
		size := int64(len(f.Bytes))
		if err := bounds.CheckEntryBytes(size); err != nil {
			return fmt.Errorf("datapackage: Fetch: entry %q: %w", f.RelPath, err)
		}
		aggregate += size
		if err := bounds.CheckPackageBytes(aggregate); err != nil {
			return fmt.Errorf("datapackage: Fetch: %w", err)
		}
	}
	return nil
}
