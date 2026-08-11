package space

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// P10 (agent-exchange-2026-08) spec 10 §3's third instance of the shape
// data_delivery.go/data_resolve.go already establish for `DP-` packages:
// this file is the WRITER, blob_resolve.go is the READER, and (spec 10 §3
// seam 2, dataPackageDir's own doc comment) the storage convention lives
// HERE, beside the writer, because one writer and one reader must agree on
// it and it must not be invented twice.
//
// blobDir is the space-relative directory one blob attachment's own payload
// lives under — <system>/blobs/<BL-id>/, beside dataPackageDir's own
// <system>/data/<DP-id>/. A blob is simpler than a data package: it carries
// no data-package/v1-shaped manifest of its own. What it DOES carry is
// blobDigestPath's own tiny sidecar, below — decided, not left unspecified,
// because "the digest and the bytes are the whole of it" (this brief's own
// framing of the alternative) turns out not to be reachable from `a2a fetch
// <BL-id> --to <dir>` (spec 10 §3 seam 6, wave B): fetch takes ONLY the id.
// The declared digest lives in the artifact that DECLARES the attachment
// (attachments[].digest, schemas/envelope/v2/work_request.schema.json), and
// a by-id fetch never reads that artifact — so without a record committed
// beside the bytes themselves, spec 10 AC3/AC7's "digest-verified... a
// corrupted blob on fetch" would have nothing to verify against on that
// path. Submit-time possession (possession.go's CheckAttachmentPossession)
// does not strictly need this — it already holds the declared digest — but
// reads through ResolveBlob exactly like everything else, so it gets the
// same self-verification for free rather than a second, divergent check.
func blobDir(system, blobID string) string {
	return path.Join(system, "blobs", blobID)
}

// blobDigestPath is blobDir's own reserved sidecar path. DeliverBlob (below)
// is the one writer that produces it; ResolveBlob (blob_resolve.go) is the
// one reader that consumes it. Named "blob.json", never "manifest.json": it
// is not a data-package/v1 document or any other schema-governed wire
// shape, and no caller should ever have to disambiguate the two by content.
func blobDigestPath(system, blobID string) string {
	return path.Join(blobDir(system, blobID), "blob.json")
}

// BlobForPath mirrors DataPackageForPath's own structural (not manifest-
// reading) predicate (data_delivery.go), applied to blobDir's own
// "<system>/blobs/<BL-id>/..." grammar instead of a data package's
// "<system>/data/<DP-id>/...". P10 (agent-exchange-2026-08) spec 10 wave
// A's own handoff, closed by wave B: `a2a validate --ci` carves a data
// package's payload out of artifact discovery (spec 04 §11 AC8) but had no
// blob equivalent, so a blob payload's own frontmatter-bearing .md file
// would be walked as an envelope draft. internal/cli's isBlobPayloadPath
// (cmd_validate_ci.go, off this package) is the one caller.
//
// It returns the BL- id and the blob's own digest sidecar path
// (blobDigestPath, this file) so a caller that separately wants that path
// has it without recomputing it; this function itself never reads a file.
func BlobForPath(p string) (blobID, sidecarPath string, ok bool) {
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
	}
	// system, "blobs", <BL-id>, <at least one path segment beneath it> —
	// mirroring DataPackageForPath's own len(parts) < 4 floor.
	if len(parts) < 4 || parts[1] != "blobs" {
		return "", "", false
	}

	system, candidate := parts[0], parts[2]
	if _, idOK := blobPrefixParsed(candidate); !idOK {
		return "", "", false
	}
	return candidate, blobDigestPath(system, candidate), true
}

// blobDigestSidecar is blobDigestPath's own on-disk shape: the id (so a
// sidecar copied or committed under the wrong directory is caught by
// content, not merely a bare digest string with no self-identification)
// and the aggregate digest DeliverBlob computed over the payload it
// committed alongside this file (recomputeAttachmentDigest,
// possession.go — the SAME two-branch algorithm CheckAttachmentPossession
// already uses for `DP-` attachments, reused rather than reimplemented so
// possession and fetch can never compute two different digests for the
// same bytes).
type blobDigestSidecar struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

var (
	// ErrBlobDeliveryInvalid guards DeliverBlob's own required fields, its
	// reserved sidecar name, and its ceilings — refused before the funnel,
	// section paths, or any git action, mirroring ErrDataDeliveryInvalid's
	// own doc comment (data_delivery.go).
	ErrBlobDeliveryInvalid = errors.New("space: invalid blob delivery request")

	// ErrBlobTooManyEntries refuses a blob whose payload (write side) or
	// whose committed directory listing (read side, blob_resolve.go)
	// carries more than maxBlobEntries files — spec 04 §9's ceiling,
	// delivered here for the first time (spec 10 §3 seam 4; P4 never
	// shipped it).
	ErrBlobTooManyEntries = errors.New("space: blob exceeds the maximum entry count")

	// ErrBlobEntryTooLarge refuses a single payload file over
	// maxBlobEntryBytes.
	ErrBlobEntryTooLarge = errors.New("space: blob entry exceeds the maximum per-entry byte ceiling")

	// ErrBlobTooLarge refuses a payload whose TOTAL bytes exceed
	// maxBlobPackageBytes, even when no single entry does.
	ErrBlobTooLarge = errors.New("space: blob exceeds the maximum total byte ceiling")
)

// maxBlobEntries/maxBlobEntryBytes/maxBlobPackageBytes mirror
// data_resolve.go's own maxDataPackageEntries/maxDataPackageEntryBytes/
// maxDataPackagePackageBytes (spec 10 §3 seam 4), with one reasoned
// departure on the first constant: maxDataPackageEntries is
// datapackage.DefaultBounds().MaxEntries (2048) + 1 for the data-package's
// own manifest.json; maxBlobEntries applies the identical "+1 for the one
// extra file this writer always adds" reasoning to blobDigestPath's own
// sidecar instead. Concretely: DeliverBlob checks len(req.Payload) against
// maxBlobEntries-1 (the sidecar is not part of the caller's payload — it is
// added after this check), and ResolveBlob's raw pre-read listing check
// compares the FULL git-listed count (payload + blob.json) against
// maxBlobEntries directly, the same relationship data_resolve.go's own
// pre-read check has to maxDataPackageEntries.
//
// maxBlobEntryBytes/maxBlobPackageBytes are copied UNCHANGED rather than
// tightened: attach's own use case (an ad hoc bundle accompanying a
// request or announcement, 03-bytes-note.md §4.2) has no property that
// argues its own bytes need a different per-file or aggregate bound than a
// contract-pinned data package's — both are the same git-backed storage
// this phase's own §7 keeps un-pluggable, and the ceiling exists to bound
// THAT, not to distinguish the two callers' typical sizes.
const (
	maxBlobEntries      = 2049 // 2048 payload files + the blob.json sidecar
	maxBlobEntryBytes   = 128 << 20
	maxBlobPackageBytes = 2 << 30
)

// blobCeilingBounds is checkBlobPayloadCeilings' own parameterized shape —
// extracted from the three named constants above so this package's own
// tests can drive TINY bounds directly against the boundary comparisons
// (">", not ">="; the aggregate accumulates across entries) without
// committing gigabyte-scale fixtures to prove the identical comparison
// holds at 128 MiB or 2 GiB. DeliverBlob and ResolveBlob always call
// checkBlobPayloadCeilings with defaultBlobCeilingBounds, below — the real
// three named ceilings, proven wired correctly by
// TestDefaultBlobCeilingBoundsMatchTheNamedConstants (blob_delivery_test.go).
type blobCeilingBounds struct {
	maxEntries    int
	maxEntryBytes int64
	maxTotalBytes int64
}

// defaultBlobCeilingBounds is the ceilings DeliverBlob and ResolveBlob
// actually enforce. maxEntries is maxBlobEntries-1 (the caller-visible
// PAYLOAD budget — see maxBlobEntries's own doc comment for why the
// sidecar is the "+1" that constant already accounts for).
var defaultBlobCeilingBounds = blobCeilingBounds{
	maxEntries:    maxBlobEntries - 1,
	maxEntryBytes: maxBlobEntryBytes,
	maxTotalBytes: maxBlobPackageBytes,
}

// checkBlobPayloadCeilings applies bounds to payload — a plain in-memory
// map, never a git listing — so DeliverBlob (write side) and ResolveBlob
// (read side, blob_resolve.go, after splitting the sidecar out of what it
// read) share exactly one check rather than two copies that could drift
// apart.
func checkBlobPayloadCeilings(payload map[string][]byte, bounds blobCeilingBounds) error {
	if len(payload) > bounds.maxEntries {
		return fmt.Errorf("%w: %d entries, ceiling is %d", ErrBlobTooManyEntries, len(payload), bounds.maxEntries)
	}
	var total int64
	for name, raw := range payload {
		if int64(len(raw)) > bounds.maxEntryBytes {
			return fmt.Errorf("%w: %q is %d bytes, ceiling is %d", ErrBlobEntryTooLarge, name, len(raw), bounds.maxEntryBytes)
		}
		total += int64(len(raw))
		if total > bounds.maxTotalBytes {
			return fmt.Errorf("%w: exceeds %d bytes, ceiling is %d", ErrBlobTooLarge, total, bounds.maxTotalBytes)
		}
	}
	return nil
}

// BlobWriteRequest is one blob-attachment write: the already-minted BL- id
// and its payload bytes. Unlike DataDeliveryRequest (data_delivery.go), it
// carries no handoff and no lifecycle event: 03-bytes-note.md §6's own
// shape for `a2a attach` is "mints a content-addressed blob, writes the
// attachments: entry into the draft, and stages the bytes wherever the
// space stores them" — the attachments[] entry is a LOCAL draft edit the
// caller (wave B's `a2a attach`) makes to the artifact file itself, never a
// file THIS request commits. This request's only job is landing the bytes
// (spec 10 §1's ordering fork: "it writes them into the space... before
// anything references them").
type BlobWriteRequest struct {
	// System is the attaching system — the section this write commits into
	// (funnel.go's sectionOK) and the system blobDir/blobDigestPath are both
	// keyed by.
	System string

	// BlobID is the BL- id this write carries. Immutable from mint time;
	// OperationKey is minted from this value alone (blobDeliverOperationKey,
	// below), mirroring dataDeliverOperationKey's own "identity, not
	// content" discipline (data_delivery.go).
	BlobID string

	// Payload is every file this blob carries, blob-root-relative. Must not
	// contain the reserved sidecar name ("blob.json") — DeliverBlob refuses
	// rather than silently letting a payload entry collide with the digest
	// record it is about to write beside it.
	Payload map[string][]byte

	// SubmitTemplate carries the host/author/presentation values this write
	// needs beyond the files themselves, exactly as DataDeliveryRequest's
	// own field does. System, Verb, ArtifactID, ArtifactIDs, OperationKey
	// and Files are all overwritten by DeliverBlob.
	SubmitTemplate SubmitRequest
}

// DeliverBlob commits a blob's payload plus its own digest sidecar
// (blobDigestPath) as ONE write-funnel commit — mirroring
// DeliverDataPackage's shape (spec 10 §3 seam 2), minus the handoff/event
// DataDeliveryRequest carries, per BlobWriteRequest's own doc comment
// above.
//
// It refuses BEFORE the funnel, entirely locally: required fields, the
// reserved sidecar name, and the three named ceilings
// (checkBlobPayloadCeilings) — spec 10 AC4's "a blob over the ceiling is
// refused at attach".
//
// OperationKey is minted from BlobID alone (blobDeliverOperationKey,
// below), the same reasoning dataDeliverOperationKey's own doc comment
// gives for PackageID: a blob is immutable from mint time, so any number of
// retried attach attempts against the same staged bytes must collide on
// the same branch and prove the same committed tree, never mint a second
// one.
func DeliverBlob(ctx context.Context, req BlobWriteRequest, funnel *WriteFunnel) (WriteResult, error) {
	if req.System == "" || req.BlobID == "" || len(req.Payload) == 0 || funnel == nil {
		return WriteResult{}, fmt.Errorf("%w: every field is required", ErrBlobDeliveryInvalid)
	}
	if err := checkBlobPayloadCeilings(req.Payload, defaultBlobCeilingBounds); err != nil {
		return WriteResult{}, err
	}
	if _, err := NewLayout(req.System); err != nil {
		return WriteResult{}, fmt.Errorf("%w: %w", ErrBlobDeliveryInvalid, err)
	}

	root := blobDir(req.System, req.BlobID)
	digestSidecar := blobDigestPath(req.System, req.BlobID)

	files := make([]FileWrite, 0, len(req.Payload)+1)
	for relative, raw := range req.Payload {
		if !isCleanRelativePath(relative) {
			return WriteResult{}, fmt.Errorf("%w: payload entry %q is not a clean relative path", ErrBlobDeliveryInvalid, relative)
		}
		p := path.Join(root, relative)
		if p == digestSidecar {
			return WriteResult{}, fmt.Errorf("%w: payload entry %q collides with the reserved digest sidecar", ErrBlobDeliveryInvalid, relative)
		}
		files = append(files, FileWrite{Path: p, Content: raw})
	}

	sidecar, err := json.Marshal(blobDigestSidecar{ID: req.BlobID, Digest: recomputeAttachmentDigest(req.Payload)})
	if err != nil {
		return WriteResult{}, fmt.Errorf("%w: %w", ErrBlobDeliveryInvalid, err)
	}
	files = append(files, FileWrite{Path: digestSidecar, Content: sidecar})

	submit := req.SubmitTemplate
	submit.System = req.System
	submit.Verb = "attach"
	submit.ArtifactID = req.BlobID
	submit.ArtifactIDs = []string{req.BlobID}
	submit.OperationKey = blobDeliverOperationKey(req.BlobID)
	submit.Files = files

	return funnel.Submit(ctx, submit)
}

// blobDeliverOperationKey mints the deterministic op-v1 retry identity for
// one blob write, keyed on BlobID ALONE — mirroring dataDeliverOperationKey
// (data_delivery.go) exactly, including its own DEVIATION: this reproduces
// internal/operation's private op-v1 domain-separated SHA-256 shape rather
// than calling it, because internal/operation is not in this brief's
// allowlist. See dataDeliverOperationKey's own doc comment for why the
// reproduced shape round-trips through the funnel's OperationKey machinery
// without needing a shared implementation.
func blobDeliverOperationKey(blobID string) string {
	const domain = "a2ahub-operation-v1"
	const kind = "attach"
	sum := sha256.Sum256([]byte(domain + "\x00" + kind + "\x00" + blobID))
	return "op-v1-" + hex.EncodeToString(sum[:])
}
