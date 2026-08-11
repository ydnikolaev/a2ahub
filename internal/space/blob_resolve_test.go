package space

import (
	"encoding/json"
	"errors"
	"testing"
)

// commitBlobFixture commits payload + the correctly-computed blob.json
// sidecar to origin/main, exactly the layout DeliverBlob's own FileWrite
// assembly produces — so a resolve test proves this function reads back
// what a deliver actually writes, not an invented shape. Mirrors
// commitDataPackageFixture (data_resolve_test.go).
func commitBlobFixture(t *testing.T, repo, system, blobID string, payload map[string][]byte) {
	t.Helper()
	root := blobDir(system, blobID)
	for name, raw := range payload {
		writeContractCandidateFile(t, repo, root+"/"+name, string(raw))
	}
	sidecar, err := json.Marshal(blobDigestSidecar{ID: blobID, Digest: recomputeAttachmentDigest(payload)})
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	writeContractCandidateFile(t, repo, blobDigestPath(system, blobID), string(sidecar))
	contractTestGit(t, repo, "add", "--", root)
	contractTestGit(t, repo, "commit", "-q", "-m", "attach "+blobID)
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
}

func TestResolveBlobReturnsPayloadAndDigest(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-ab12"
	payload := map[string][]byte{"review.md": []byte("bundle body\n")}
	commitBlobFixture(t, repo, "axon", blobID, payload)

	got, err := ResolveBlob(t.Context(), repo, blobID)
	if err != nil {
		t.Fatalf("ResolveBlob: %v", err)
	}
	if got.System != "axon" || got.Root != "axon/blobs/"+blobID {
		t.Fatalf("System/Root = %q/%q", got.System, got.Root)
	}
	if len(got.Payload) != 1 || string(got.Payload["review.md"]) != "bundle body\n" {
		t.Fatalf("Payload = %v", got.Payload)
	}
	wantDigest := recomputeAttachmentDigest(payload)
	if got.Digest != wantDigest {
		t.Fatalf("Digest = %q, want %q", got.Digest, wantDigest)
	}
	// The sidecar itself must never leak into Payload.
	if _, ok := got.Payload["blob.json"]; ok {
		t.Fatalf("Payload leaked the digest sidecar: %v", got.Payload)
	}
}

func TestResolveBlobRefusesUnknownBlob(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	commitBlobFixture(t, repo, "axon", "BL-axon-20260811-ab12", map[string][]byte{"a.txt": []byte("x")})

	_, err := ResolveBlob(t.Context(), repo, "BL-axon-20260811-zz99")
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("error = %v, want ErrBlobNotFound", err)
	}
}

func TestResolveBlobRefusesMissingDigestSidecar(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-cd34"
	// Deliberately only the payload lands, never blob.json — proves this
	// function does not treat "the directory exists" as "the blob is
	// resolvable", mirroring TestResolveDataPackageRefusesMissingManifest's
	// own seeded-red receipt (data_resolve_test.go).
	root := blobDir("axon", blobID)
	writeContractCandidateFile(t, repo, root+"/review.md", "body\n")
	contractTestGit(t, repo, "add", "--", root)
	contractTestGit(t, repo, "commit", "-q", "-m", "attach without sidecar")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	_, err := ResolveBlob(t.Context(), repo, blobID)
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("error = %v, want ErrBlobNotFound", err)
	}
}

func TestResolveBlobRefusesMalformedID(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	for _, id := range []string{"", "not-an-id", "XC-axon-widget", "DP-axon-20260811-ab12"} {
		if _, err := ResolveBlob(t.Context(), repo, id); !errors.Is(err, ErrBlobInvalidReference) {
			t.Fatalf("ResolveBlob(%q) error = %v, want ErrBlobInvalidReference", id, err)
		}
	}
}

// TestResolveDataPackageRefusesBlobID and TestResolveBlobRefusesPackageID
// are AC5's own "no third, silently-accepting branch" proof: each resolver
// refuses the OTHER primitive's id shape rather than accidentally resolving
// it (both ids are exchange-broadcast class; only the prefix differs).
func TestResolveDataPackageRefusesBlobID(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	if _, err := ResolveDataPackage(t.Context(), repo, "BL-axon-20260811-ab12"); !errors.Is(err, ErrDataPackageInvalidReference) {
		t.Fatalf("ResolveDataPackage(BL- id) error = %v, want ErrDataPackageInvalidReference", err)
	}
}

func TestResolveBlobRefusesPackageID(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	if _, err := ResolveBlob(t.Context(), repo, "DP-axon-20260811-ab12"); !errors.Is(err, ErrBlobInvalidReference) {
		t.Fatalf("ResolveBlob(DP- id) error = %v, want ErrBlobInvalidReference", err)
	}
}

// TestResolveBlobRefusesDigestMismatch is spec 10 AC7's read-side proof:
// bytes committed to the space that no longer match their own committed
// blob.json sidecar (simulated here by a SECOND commit that corrupts the
// payload without touching the sidecar — "mutate one byte in the space",
// spec 10 AC3) are refused, not silently served.
func TestResolveBlobRefusesDigestMismatch(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-ef56"
	payload := map[string][]byte{"review.md": []byte("original bytes\n")}
	commitBlobFixture(t, repo, "axon", blobID, payload)

	// Corrupt the committed payload in a SECOND commit, pushed to
	// origin/main, without touching blob.json — the sidecar still declares
	// the digest of the ORIGINAL bytes.
	root := blobDir("axon", blobID)
	writeContractCandidateFile(t, repo, root+"/review.md", "corrupted bytes\n")
	contractTestGit(t, repo, "add", "--", root+"/review.md")
	contractTestGit(t, repo, "commit", "-q", "-m", "corrupt")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	_, err := ResolveBlob(t.Context(), repo, blobID)
	if !errors.Is(err, ErrBlobDigestMismatch) {
		t.Fatalf("error = %v, want ErrBlobDigestMismatch", err)
	}
}

// TestResolveBlobIgnoresWorkingTreeOverwrite and
// TestResolveBlobRefusesWorkingTreeOnlyBlob are the pair spec 10 §1
// reading 3's own property rests on: ResolveBlob reads origin/main via Git,
// NEVER the author's working tree. Each is the "seeded-red receipt" for a
// mutation that would make the OTHER one silently pass:
//   - overwriting the working tree after a real commit would flip the
//     first test's expectation if ResolveBlob read the filesystem instead
//     of Git;
//   - writing a COMPLETE, resolvable-looking blob to the working tree only
//     (never pushed) would flip the second test's expectation the same way
//     — this replays the founding incident (03-bytes-note.md) directly:
//     "the bundle is referenced but not received... the bytes are not
//     present" is exactly what a working-tree read would have missed.
func TestResolveBlobIgnoresWorkingTreeOverwrite(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-gh78"
	payload := map[string][]byte{"review.md": []byte("committed bytes\n")}
	commitBlobFixture(t, repo, "axon", blobID, payload)

	// Overwrite the WORKING TREE copy after the commit, without a second
	// commit — origin/main still carries the original bytes.
	writeContractCandidateFile(t, repo, blobDir("axon", blobID)+"/review.md", "never committed bytes\n")

	got, err := ResolveBlob(t.Context(), repo, blobID)
	if err != nil {
		t.Fatalf("ResolveBlob: %v", err)
	}
	if string(got.Payload["review.md"]) != "committed bytes\n" {
		t.Fatalf("Payload[review.md] = %q, want the COMMITTED bytes (working-tree overwrite must be ignored)", got.Payload["review.md"])
	}
}

func TestResolveBlobRefusesWorkingTreeOnlyBlob(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	// Establish origin/main with an UNRELATED commit first, so this test's
	// refusal is "not present under this commit" (ErrBlobNotFound), not
	// merely "origin/main has no commits at all" (a different, generic git
	// failure that would also make TestResolveBlobRefusesUnknownBlob's own
	// sibling scenario indistinguishable from this one).
	writeContractCandidateFile(t, repo, "README.md", "unrelated\n")
	contractTestGit(t, repo, "add", "--", "README.md")
	contractTestGit(t, repo, "commit", "-q", "-m", "unrelated")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	blobID := "BL-axon-20260811-jk90"
	payload := map[string][]byte{"review.md": []byte("local only\n")}

	// A COMPLETE, resolvable-looking blob (payload AND digest sidecar) —
	// written to the working tree, never committed or pushed.
	root := blobDir("axon", blobID)
	writeContractCandidateFile(t, repo, root+"/review.md", string(payload["review.md"]))
	sidecar, err := json.Marshal(blobDigestSidecar{ID: blobID, Digest: recomputeAttachmentDigest(payload)})
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	writeContractCandidateFile(t, repo, blobDigestPath("axon", blobID), string(sidecar))

	_, err = ResolveBlob(t.Context(), repo, blobID)
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("error = %v, want ErrBlobNotFound (working-tree-only blob must not resolve)", err)
	}
}

// TestResolveBlobReadsOriginMainRegardlessOfLocalCheckout is this file's
// most direct mutation proof for spec 10 §1 reading 3: ResolveBlob resolves
// against refs/remotes/origin/main (contractAuthoritativeMainRef), NEVER
// whatever commit the mirror's local checkout (HEAD) happens to be parked
// on — funnel.go's restoreTreeToBase doc explains why that can vary between
// writes. This is the discriminating case the other two working-tree tests
// above do NOT catch: contractGitListTree always reads committed Git
// objects, by commit, regardless of which ref names that commit, so
// "uncommitted content on disk" is invisible to either "origin/main" or
// "HEAD" alike. Only a mutation that swaps WHICH COMMIT is read (origin/main
// -> HEAD/local branch tip) shows up here: after the blob is committed and
// pushed, this test moves the LOCAL checkout to an unrelated orphan commit
// that does not contain it at all — proving ResolveBlob still finds the
// blob through origin/main's own ref, independent of the local checkout.
func TestResolveBlobReadsOriginMainRegardlessOfLocalCheckout(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-rd12"
	payload := map[string][]byte{"review.md": []byte("committed bytes\n")}
	commitBlobFixture(t, repo, "axon", blobID, payload)

	// Move the LOCAL checkout to a fresh orphan branch with no blob at all
	// — origin/main (the remote-tracking ref) is untouched by this. `git
	// checkout --orphan` alone carries the PREVIOUS branch's index/working
	// tree forward (it does not clear staged content), so `git rm -rf
	// --cached .` is required too, or the "empty" commit below would
	// silently reproduce main's own tree and this test would prove nothing.
	contractTestGit(t, repo, "checkout", "-q", "--orphan", "elsewhere")
	contractTestGit(t, repo, "rm", "-rf", "-q", "--cached", ".")
	contractTestGit(t, repo, "commit", "-q", "--allow-empty", "-m", "elsewhere, no blob here")

	got, err := ResolveBlob(t.Context(), repo, blobID)
	if err != nil {
		t.Fatalf("ResolveBlob (local checkout moved away from main) = %v, want nil (must resolve through origin/main)", err)
	}
	if string(got.Payload["review.md"]) != "committed bytes\n" {
		t.Fatalf("Payload[review.md] = %q, want the committed bytes", got.Payload["review.md"])
	}
}

func TestBlobPrefixParsedMirrorsDatapackageBlobPrefix(t *testing.T) {
	t.Parallel()

	// blobPrefix mirrors datapackage.BlobPrefix ("BL") without importing
	// internal/datapackage (ADR-001) — this test is the drift catch for
	// that mirror, exactly as blob_delivery.go's own doc comment says a
	// wrong prefix "would not error at mint or parse time".
	if blobPrefix != "BL" {
		t.Fatalf("blobPrefix = %q, want %q", blobPrefix, "BL")
	}
	if _, ok := blobPrefixParsed("BL-axon-20260811-ab12"); !ok {
		t.Fatalf("blobPrefixParsed did not accept a well-formed BL- id")
	}
	if _, ok := blobPrefixParsed("XC-axon-widget"); ok {
		t.Fatalf("blobPrefixParsed accepted a standing XC- id")
	}
}

func TestResolveBlobRefusesMalformedSidecar(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-hk12"
	root := blobDir("axon", blobID)
	writeContractCandidateFile(t, repo, root+"/review.md", "body\n")
	writeContractCandidateFile(t, repo, blobDigestPath("axon", blobID), "not json at all")
	contractTestGit(t, repo, "add", "--", root)
	contractTestGit(t, repo, "commit", "-q", "-m", "malformed sidecar")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	_, err := ResolveBlob(t.Context(), repo, blobID)
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("error = %v, want ErrBlobNotFound", err)
	}
}

func TestResolveBlobRefusesSidecarNamingADifferentID(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	blobID := "BL-axon-20260811-mn34"
	payload := map[string][]byte{"review.md": []byte("body\n")}
	root := blobDir("axon", blobID)
	writeContractCandidateFile(t, repo, root+"/review.md", string(payload["review.md"]))
	// A sidecar with the CORRECT digest but a DIFFERENT declared id — a
	// copied-from-elsewhere sidecar, the exact case this file's own
	// blobDigestSidecar.ID field doc comment names.
	sidecar, err := json.Marshal(blobDigestSidecar{ID: "BL-axon-20260811-zzzz", Digest: recomputeAttachmentDigest(payload)})
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	writeContractCandidateFile(t, repo, blobDigestPath("axon", blobID), string(sidecar))
	contractTestGit(t, repo, "add", "--", root)
	contractTestGit(t, repo, "commit", "-q", "-m", "sidecar naming a different id")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	_, err = ResolveBlob(t.Context(), repo, blobID)
	if !errors.Is(err, ErrBlobNotFound) {
		t.Fatalf("error = %v, want ErrBlobNotFound", err)
	}
}
