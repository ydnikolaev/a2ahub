package release

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/verify"
)

// The valid-path fixtures are the REAL v0.1.1 release signature: the committed
// testdata bundle and the published SHA-256 of a2a-linux-amd64 (from the
// release's SHA256SUMS). Verification runs fully offline — the frozen embedded
// trusted root + the v0.3 bundle's own inclusion proof need no network — so
// these tests are deterministic ground truth, not a live smoke test.
const (
	liveBundleName = "a2a-linux-amd64.cosign.bundle"
	// sha256(a2a-linux-amd64) from v0.1.1 SHA256SUMS.
	liveAssetDigestHex = "0fb070578b82d8a073bc1a99751f51375d739115fd3e346831ca92edbd8cbc24"
	// the repo whose release.yml workflow identity the bundle was signed under.
	liveRepo = "ydnikolaev/a2ahub"
)

func liveArtifactDigestOpt(t *testing.T) verify.ArtifactPolicyOption {
	t.Helper()
	digest, err := hex.DecodeString(liveAssetDigestHex)
	if err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	return verify.WithArtifactDigest("sha256", digest)
}

// TestKeylessCosign_ValidLiveBundle is the ground-truth valid path. It drives
// verifyDetached with WithArtifactDigest — which is EXACTLY the artifact-policy
// option the production Verify() now builds (it streams the asset's sha256 and
// passes WithArtifactDigest too), so this test covers the shipped path, not a
// parallel one.
func TestKeylessCosign_ValidLiveBundle(t *testing.T) {
	t.Parallel()
	k := NewKeylessCosignVerifier(liveRepo)
	err := k.verifyDetached(filepath.Join("testdata", liveBundleName), liveArtifactDigestOpt(t))
	if err != nil {
		t.Fatalf("verifyDetached(real v0.1.1 bundle, correct repo): want nil, got %v", err)
	}
}

func TestKeylessCosign_WrongIdentityRejected(t *testing.T) {
	t.Parallel()
	// Correct signature + correct digest, but pinned to a DIFFERENT repo's
	// workflow identity -> the SAN regexp cannot match -> hard fail.
	k := NewKeylessCosignVerifier("attacker/evil")
	err := k.verifyDetached(filepath.Join("testdata", liveBundleName), liveArtifactDigestOpt(t))
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("wrong-identity: want ErrSignatureInvalid, got %v", err)
	}
	if errors.Is(err, ErrSignatureUnverified) {
		t.Fatalf("a present-but-wrong signature must NOT be the gateable ErrSignatureUnverified: %v", err)
	}
}

func TestKeylessCosign_WrongDigestRejected(t *testing.T) {
	t.Parallel()
	// Correct repo, but the artifact digest does not match the one the bundle
	// signed -> the messageSignature binding fails.
	k := NewKeylessCosignVerifier(liveRepo)
	wrong, _ := hex.DecodeString("deadbeef" + liveAssetDigestHex[8:])
	err := k.verifyDetached(filepath.Join("testdata", liveBundleName), verify.WithArtifactDigest("sha256", wrong))
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("wrong-digest: want ErrSignatureInvalid, got %v", err)
	}
}

func TestKeylessCosign_AbsentBundleIsUnverified(t *testing.T) {
	t.Parallel()
	// An asset on disk with NO sibling .cosign.bundle -> the overridable
	// ErrSignatureUnverified (the sole --allow-unsigned case), never
	// ErrSignatureInvalid.
	dir := t.TempDir()
	assetPath := filepath.Join(dir, "a2a-linux-amd64")
	if err := os.WriteFile(assetPath, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := NewKeylessCosignVerifier(liveRepo).Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrSignatureUnverified) {
		t.Fatalf("absent bundle: want ErrSignatureUnverified, got %v", err)
	}
	if errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("absent bundle must be UNVERIFIED (gateable), not INVALID: %v", err)
	}
}

func TestKeylessCosign_MalformedBundleRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "x.cosign.bundle")
	if err := os.WriteFile(bundlePath, []byte("{not a real bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := NewKeylessCosignVerifier(liveRepo).verifyDetached(bundlePath, liveArtifactDigestOpt(t))
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("malformed bundle: want ErrSignatureInvalid, got %v", err)
	}
}

// TestKeylessVerifier_ChecksumRunsBeforeSignature pins the composite order:
// the repo-pinned production chain still runs ChecksumVerifier first, so a
// checksum failure preempts any signature work.
func TestKeylessVerifier_ChecksumRunsBeforeSignature(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", content, sha256Hex(content)+"  a2a-linux-amd64\n")
	if err := os.WriteFile(assetPath, []byte("TAMPERED bytes!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := KeylessVerifier(liveRepo).Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("checksum must run first: want ErrChecksumMismatch, got %v", err)
	}
}
