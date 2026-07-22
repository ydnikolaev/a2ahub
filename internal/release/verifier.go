package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Verifier checks a downloaded asset before Swap ever runs (T1 step 3, T2).
// Verify returns nil only when assetPath is trusted enough to swap in.
type Verifier interface {
	Verify(ctx context.Context, assetPath string, rel Release) error
}

// ChecksumVerifier is the unconditional integrity layer (T2): assetPath's
// SHA-256 must equal its SHA256SUMS entry. It is not policy-swappable — it
// always runs, and always first in CompositeVerifier. The SHA256SUMS file
// is expected alongside assetPath (Download's own destDir layout:
// filepath.Dir(assetPath)/SHA256SUMS).
type ChecksumVerifier struct{}

// Verify implements Verifier. Any failure to establish integrity — a
// tampered asset, a missing SHA256SUMS entry for it, an unreadable or
// malformed SHA256SUMS file — returns ErrChecksumMismatch, never gateable
// by --allow-unsigned (T2, T-8, D-013).
func (ChecksumVerifier) Verify(_ context.Context, assetPath string, _ Release) error {
	const op = "ChecksumVerifier.Verify"
	base := filepath.Base(assetPath)
	sumsPath := filepath.Join(filepath.Dir(assetPath), "SHA256SUMS")

	data, err := readSums(sumsPath)
	if err != nil {
		return &Error{Op: op, Input: sumsPath, Err: ErrChecksumMismatch}
	}
	want, err := parseSums(data, base)
	if err != nil {
		return &Error{Op: op, Input: base, Err: ErrChecksumMismatch}
	}

	got, err := sha256File(assetPath)
	if err != nil {
		return &Error{Op: op, Input: assetPath, Err: ErrChecksumMismatch}
	}
	if !strings.EqualFold(got, want) {
		return &Error{Op: op, Input: base, Err: ErrChecksumMismatch}
	}
	return nil
}

// parseSums finds name's SHA-256 hex digest in a `sha256sum`-format file
// (format: "<hex64>  <filename>", one entry per line). Since the publish-prep
// goreleaser port, SHA256SUMS is produced by goreleaser's `checksum:` block and
// lists the archives alongside the raw `a2a-<os>-<arch>` binaries (a superset);
// the per-line format is unchanged, so this parser is unaffected. Any non-blank
// line that
// does not split into exactly a 64-character hex digest and a file name is
// treated as malformed and fails the whole parse — a missing or malformed
// entry for name is indistinguishable to the caller (both fail closed).
func parseSums(data []byte, name string) (string, error) {
	lines := strings.Split(string(data), "\n")
	var found string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 || !isHex(fields[0]) {
			return "", fmt.Errorf("malformed SHA256SUMS line: %q", line)
		}
		entryName := strings.TrimPrefix(fields[1], "*")
		if entryName == name {
			found = fields[0]
		}
	}
	if found == "" {
		return "", fmt.Errorf("no SHA256SUMS entry for %q", name)
	}
	return found, nil
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// maxSumsBytes bounds the locally-fetched SHA256SUMS read: a checksums file
// for a handful of release assets is well under a kilobyte, so a 1 MiB cap is
// generous while keeping the read bounded (the package's own "bounded reads
// everywhere" idiom — source.go — applied here too).
const maxSumsBytes = 1 << 20

// readSums reads at most maxSumsBytes of the SHA256SUMS file at path.
func readSums(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxSumsBytes))
}

// sha256File streams path's contents through SHA-256 (no whole-file
// buffering — release assets are binaries, not small JSON payloads).
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unverifiedSignatureVerifier is the T2 interim signature slot: this build
// does not link sigstore-go (2026-07-22 operator decision), so it always
// reports UNVERIFIED rather than "bundle absent" — P16 releases DO carry
// best-effort keyless-cosign bundles, this build simply cannot check them.
// Fail-closed: the caller (the update verb) must gate ErrSignatureUnverified
// behind --allow-unsigned; nothing here ever reports success.
type unverifiedSignatureVerifier struct{}

func (unverifiedSignatureVerifier) Verify(_ context.Context, assetPath string, _ Release) error {
	return &Error{Op: "unverifiedSignatureVerifier.Verify", Input: assetPath, Err: ErrSignatureUnverified}
}

// CompositeVerifier runs ChecksumVerifier unconditionally FIRST, then the
// policy-selected Signature verifier (T2 seam: swapping Signature for a
// keyless-cosign then pinned-key implementation later is a new Verifier +
// a policy-order change, zero call-site churn).
type CompositeVerifier struct {
	Signature Verifier
}

// NewCompositeVerifier builds a CompositeVerifier with signature as its
// policy-selected second layer.
func NewCompositeVerifier(signature Verifier) *CompositeVerifier {
	return &CompositeVerifier{Signature: signature}
}

// DefaultVerifier is the T2-interim policy: ChecksumVerifier (always) then
// unverifiedSignatureVerifier (always UNVERIFIED, this phase). The update
// verb (a later wave) gates errors.Is(err, ErrSignatureUnverified) behind
// --allow-unsigned; a checksum failure is never gateable because it is
// never ErrSignatureUnverified.
func DefaultVerifier() *CompositeVerifier {
	return NewCompositeVerifier(unverifiedSignatureVerifier{})
}

// Verify implements Verifier: checksum first, then the signature layer.
func (c *CompositeVerifier) Verify(ctx context.Context, assetPath string, rel Release) error {
	if err := (ChecksumVerifier{}).Verify(ctx, assetPath, rel); err != nil {
		return err
	}
	if c.Signature == nil {
		return nil
	}
	return c.Signature.Verify(ctx, assetPath, rel)
}
