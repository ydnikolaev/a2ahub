package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeAssetAndSums(t *testing.T, dir, assetName string, content []byte, sumsLine string) string {
	t.Helper()
	assetPath := filepath.Join(dir, assetName)
	if err := os.WriteFile(assetPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile asset: %v", err)
	}
	if sumsLine != "" {
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sumsLine), 0o644); err != nil {
			t.Fatalf("WriteFile SHA256SUMS: %v", err)
		}
	}
	return assetPath
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestChecksumVerifier_MatchingSumPasses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", content, sha256Hex(content)+"  a2a-linux-amd64\n")

	if err := (ChecksumVerifier{}).Verify(context.Background(), assetPath, Release{}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestChecksumVerifier_TamperedAssetFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	original := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", original, sha256Hex(original)+"  a2a-linux-amd64\n")

	// tamper: one byte changed after the sum was recorded.
	if err := os.WriteFile(assetPath, []byte("Real asset bytes"), 0o644); err != nil {
		t.Fatalf("tamper WriteFile: %v", err)
	}

	err := (ChecksumVerifier{}).Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Verify() error = %v, want ErrChecksumMismatch", err)
	}
}

func TestChecksumVerifier_MissingLineFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", content, sha256Hex([]byte("other file"))+"  some-other-asset\n")

	err := (ChecksumVerifier{}).Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Verify() error = %v, want ErrChecksumMismatch", err)
	}
}

func TestChecksumVerifier_MalformedSumsFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", content, "not-a-valid-line-at-all\n")

	err := (ChecksumVerifier{}).Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Verify() error = %v, want ErrChecksumMismatch", err)
	}
}

func TestChecksumVerifier_MissingSumsFileFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", content, "") // no SHA256SUMS written at all

	err := (ChecksumVerifier{}).Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Verify() error = %v, want ErrChecksumMismatch", err)
	}
}

func TestUnverifiedSignatureVerifier_AlwaysUnverified(t *testing.T) {
	t.Parallel()
	err := (unverifiedSignatureVerifier{}).Verify(context.Background(), "/whatever", Release{})
	if !errors.Is(err, ErrSignatureUnverified) {
		t.Fatalf("Verify() error = %v, want ErrSignatureUnverified", err)
	}
}

func TestCompositeVerifier_ChecksumRunsFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	original := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", original, sha256Hex(original)+"  a2a-linux-amd64\n")
	// tamper after recording the sum.
	if err := os.WriteFile(assetPath, []byte("TAMPERED bytes!!"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	v := DefaultVerifier()
	err := v.Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Verify() error = %v, want ErrChecksumMismatch (checksum must run first)", err)
	}
	if errors.Is(err, ErrSignatureUnverified) {
		t.Fatalf("Verify() error unexpectedly also matches ErrSignatureUnverified: %v", err)
	}
}

func TestCompositeVerifier_ValidChecksumYieldsUnverifiedSignature(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := []byte("real asset bytes")
	assetPath := writeAssetAndSums(t, dir, "a2a-linux-amd64", content, sha256Hex(content)+"  a2a-linux-amd64\n")

	v := DefaultVerifier()
	err := v.Verify(context.Background(), assetPath, Release{})
	if !errors.Is(err, ErrSignatureUnverified) {
		t.Fatalf("Verify() error = %v, want ErrSignatureUnverified", err)
	}
	if errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Verify() error unexpectedly also matches ErrChecksumMismatch: %v", err)
	}
}
