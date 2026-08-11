package space

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// This file is blob_delivery.go's own reader — spec 10 §3 seam 3, the
// third instance of ResolveDataPackage's shape (data_resolve.go). Neither
// result is a datapackage.* type for the same ADR-001 reason
// data_resolve.go's own top-of-file doc states: internal/space may not
// import internal/datapackage, so BlobBytes is handed back as plain
// bytes/maps, never a decoded document.

var (
	// ErrBlobInvalidReference refuses a blob id that is not exactly the
	// BL- exchange-broadcast shape (spec 10 §3 seam 1), mirroring
	// ErrDataPackageInvalidReference's own doc comment (data_resolve.go).
	ErrBlobInvalidReference = errors.New("space: blob reference must be an exact BL id")

	// ErrBlobNotFound refuses a blob id with nothing committed under the
	// exact directory DeliverBlob would have written it to — including the
	// case where the directory exists but its own digest sidecar does not
	// (an incomplete or hand-tampered commit), mirroring
	// ErrDataPackageNotFound's own "the manifest is missing" branch in
	// ResolveDataPackage below.
	ErrBlobNotFound = errors.New("space: blob is not present in the local mirror")

	// ErrBlobDigestMismatch refuses a blob whose payload, once resolved,
	// hashes to something OTHER than what its own committed blob.json
	// sidecar declares — the read-side half of spec 10 AC7 ("a corrupted
	// blob on fetch"), verified unconditionally by ResolveBlob itself (see
	// its own doc comment for why this cannot be left to a caller with
	// only the id in hand).
	ErrBlobDigestMismatch = errors.New("space: blob resolves to bytes that do not match its own committed digest sidecar")
)

// blobPrefix mirrors datapackage.BlobPrefix ("BL") — internal/space may not
// import internal/datapackage (ADR-001's table; data_resolve.go's own
// top-of-file doc and dataPackagePrefix's own doc comment already carry
// this note for "DP"), so this constant duplicates only the two-character
// prefix string itself, never the mint/parse machinery
// (datapackage.ParseBlobID/MintBlobID) that owns it. Same mirror idiom as
// dataPackagePrefix (data_resolve.go), Attachment (possession.go) and
// deliverableKindData (delivery_possession.go) already use in this
// package.
const blobPrefix = "BL"

// blobPrefixParsed reports whether blobID is exactly the BL- exchange-
// broadcast shape, mirroring dataPackagePrefixParsed's own check
// (data_resolve.go) without importing datapackage.ParseBlobID.
func blobPrefixParsed(blobID string) (artifact.ID, bool) {
	parsed, err := artifact.ParseID(blobID)
	if err != nil || parsed.Prefix != blobPrefix || parsed.Class != artifact.ClassExchangeBroadcast {
		return artifact.ID{}, false
	}
	return parsed, true
}

// BlobBytes is one resolved blob's payload bytes — the digest sidecar
// itself is never included (mirroring DataPackageBytes's own manifest
// exclusion) — plus the digest ITS OWN sidecar declared, already verified
// against the payload by the time ResolveBlob returns it (see ResolveBlob's
// own doc comment). Root is the space-relative directory both this
// function and DeliverBlob agree a blob lives under.
type BlobBytes struct {
	System  string
	Root    string
	Digest  string
	Payload map[string][]byte
}

// ResolveBlob resolves one BL- blob's payload bytes out of the local
// mirror, reading from origin/main exactly the way ResolveDataPackage does
// (contractGitResolveCommit against contractAuthoritativeMainRef,
// contractGitListTree) — spec 10 §1 reading 3: "'it works on my machine'
// cannot pass it by construction", never the author's working tree.
//
// Unlike ResolveDataPackage, it verifies the digest itself, unconditionally,
// before returning: blobDigestPath's own committed sidecar
// (blob_delivery.go) names the aggregate digest DeliverBlob computed over
// the SAME payload at write time; ResolveBlob recomputes it the identical
// way (recomputeAttachmentDigest, possession.go) and refuses on any
// mismatch (ErrBlobDigestMismatch) — spec 10 AC7's read-side half ("a
// corrupted blob on fetch"). This has to live HERE, not only in
// CheckAttachmentPossession, because wave B's `a2a fetch <BL-id> --to
// <dir>` (spec 10 §3 seam 6) takes ONLY the id and has no declared
// attachments[].digest to check against; the sidecar this function reads
// is the one record a by-id caller has. CheckAttachmentPossession
// (possession.go) still separately compares the result's Digest against
// the ARTIFACT's own declared digest — the two checks answer different
// questions ("did the bytes survive?" vs "are these the bytes the artifact
// actually declared?") and neither substitutes for the other.
//
// The attaching system is parsed out of blobID itself
// (BL-<system>-<YYYYMMDD>-<rand4>) — a caller never has to supply it
// separately, and cannot supply a different one than the id itself names.
func ResolveBlob(ctx context.Context, mirrorDir, blobID string) (BlobBytes, error) {
	parsed, ok := blobPrefixParsed(blobID)
	if !ok {
		return BlobBytes{}, fmt.Errorf("%w: %q", ErrBlobInvalidReference, blobID)
	}
	if _, err := NewLayout(parsed.System); err != nil {
		return BlobBytes{}, fmt.Errorf("%w: %q", ErrBlobInvalidReference, blobID)
	}

	commit, err := contractGitResolveCommit(ctx, mirrorDir, contractAuthoritativeMainRef)
	if err != nil {
		return BlobBytes{}, err
	}
	root := blobDir(parsed.System, blobID)
	entries, err := contractGitListTree(ctx, mirrorDir, commit, true, root)
	if err != nil {
		return BlobBytes{}, err
	}
	if len(entries) == 0 {
		return BlobBytes{}, fmt.Errorf("%w: %q", ErrBlobNotFound, blobID)
	}
	// The RAW listing (payload + the blob.json sidecar) is bounded against
	// maxBlobEntries directly — see that constant's own doc comment
	// (blob_delivery.go) for why its "+1" already accounts for the
	// sidecar, the same relationship data_resolve.go's own pre-read check
	// has to maxDataPackageEntries.
	if len(entries) > maxBlobEntries {
		return BlobBytes{}, fmt.Errorf("%w: %d entries, ceiling is %d", ErrBlobTooManyEntries, len(entries), maxBlobEntries)
	}

	sidecarPath := blobDigestPath(parsed.System, blobID)
	prefix := root + "/"
	payload := make(map[string][]byte, len(entries))
	var sidecarRaw []byte
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return BlobBytes{}, err
		}
		if !strings.HasPrefix(entry.Path, prefix) {
			return BlobBytes{}, fmt.Errorf("%w: Git listed a blob path outside its root", ErrContractHistoryInvalid)
		}
		// maxBlobEntryBytes+1 lets contractGitReadBlob's own generic
		// ErrContractBoundedResult stand only as a last-resort backstop;
		// the exact-boundary refusal below is this file's own named
		// ErrBlobEntryTooLarge, so a caller can errors.Is against the
		// specific ceiling that fired rather than the shared contract
		// sentinel.
		raw, err := contractGitReadBlob(ctx, mirrorDir, entry, maxBlobEntryBytes+1)
		if err != nil {
			return BlobBytes{}, err
		}
		if int64(len(raw)) > maxBlobEntryBytes {
			return BlobBytes{}, fmt.Errorf("%w: %q is %d bytes, ceiling is %d", ErrBlobEntryTooLarge, entry.Path, len(raw), maxBlobEntryBytes)
		}
		if entry.Path == sidecarPath {
			sidecarRaw = raw
			continue
		}
		payload[strings.TrimPrefix(entry.Path, prefix)] = raw
	}
	if sidecarRaw == nil {
		return BlobBytes{}, fmt.Errorf("%w: %q has no digest sidecar at %s", ErrBlobNotFound, blobID, sidecarPath)
	}
	var sidecar blobDigestSidecar
	if err := json.Unmarshal(sidecarRaw, &sidecar); err != nil || sidecar.ID != blobID || sidecar.Digest == "" {
		return BlobBytes{}, fmt.Errorf("%w: %q's digest sidecar is malformed", ErrBlobNotFound, blobID)
	}
	if err := checkBlobPayloadCeilings(payload, defaultBlobCeilingBounds); err != nil {
		return BlobBytes{}, err
	}

	got := recomputeAttachmentDigest(payload)
	if got != sidecar.Digest {
		return BlobBytes{}, fmt.Errorf("%w: %q declares %s, the space resolves it to %s", ErrBlobDigestMismatch, blobID, sidecar.Digest, got)
	}
	return BlobBytes{System: parsed.System, Root: root, Digest: sidecar.Digest, Payload: payload}, nil
}
