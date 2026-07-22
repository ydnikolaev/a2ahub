package release

import (
	"context"
	_ "embed"
	"os"
	"regexp"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// trustedRootJSON is the FROZEN Sigstore public-good trusted root (Fulcio CAs
// + Rekor/CT log public keys), fetched TUF-verified by gen_trustedroot.go and
// embedded so signature verification is fully OFFLINE — no TUF/network call at
// `a2a update`. Regenerate (rare — only on a Sigstore key rotation) via
// `go run ./internal/release/gen_trustedroot.go`.
//
//go:embed trusted_root.json
var trustedRootJSON []byte

// cosignOIDCIssuer is the GitHub Actions OIDC issuer every keyless release
// signature is minted under (release.yml runs with id-token: write). Pinned
// exactly; the workflow identity (the SAN) is pinned by regexp per-repo below.
const cosignOIDCIssuer = "https://token.actions.githubusercontent.com"

// KeylessCosignVerifier is the real T2 signature layer (replaces the interim
// unverifiedSignatureVerifier on the same CompositeVerifier.Signature seam):
// it verifies the asset's `<asset>.cosign.bundle` (keyless Sigstore, produced
// by the release workflow's `cosign sign-blob`) against the FROZEN trusted
// root, pinning the certificate identity to THIS product repo's release
// workflow. A present-but-failing bundle (bad signature, tamper, wrong
// identity/issuer, malformed) is ErrSignatureInvalid — a hard stop, never
// gateable by --allow-unsigned. A truly-ABSENT bundle is ErrSignatureUnverified
// — the sole overridable case.
type KeylessCosignVerifier struct {
	// repo is "<owner>/<name>" of the update repo; the SAN identity regexp is
	// derived from it so the signer must be this repo's release.yml workflow.
	repo string
}

// NewKeylessCosignVerifier builds a verifier pinned to repo's release-workflow
// OIDC identity. repo comes from the resolved update_repo (compiled default or
// machine `defaults.update_repo`), so a repo rename flows through unchanged.
func NewKeylessCosignVerifier(repo string) *KeylessCosignVerifier {
	return &KeylessCosignVerifier{repo: repo}
}

// KeylessVerifier is the production, repo-pinned verification chain: the
// unconditional ChecksumVerifier first, then keyless-cosign signature. The CLI
// update path passes this (repo known); Apply's nil-Verifier fallback stays the
// repo-less DefaultVerifier, which cannot pin an identity and so reports the
// signature UNVERIFIED.
func KeylessVerifier(repo string) *CompositeVerifier {
	return NewCompositeVerifier(NewKeylessCosignVerifier(repo))
}

// Verify implements Verifier. The bundle is expected beside the asset, named
// "<asset>.cosign.bundle" (Download's own layout: download.go writes
// asset.Name + ".cosign.bundle" into the asset's dir).
func (k *KeylessCosignVerifier) Verify(_ context.Context, assetPath string, _ Release) error {
	const op = "KeylessCosignVerifier.Verify"
	bundlePath := assetPath + ".cosign.bundle"

	// Absent bundle → UNVERIFIED (the ONE case --allow-unsigned may override).
	if _, err := os.Stat(bundlePath); err != nil {
		return &Error{Op: op, Input: bundlePath, Err: ErrSignatureUnverified}
	}

	f, err := os.Open(assetPath)
	if err != nil {
		return &Error{Op: op, Input: assetPath, Err: ErrSignatureInvalid}
	}
	defer func() { _ = f.Close() }()

	// The asset bytes bind the messageSignature; the verifier recomputes and
	// compares the digest.
	return k.verifyDetached(bundlePath, verify.WithArtifact(f))
}

// verifyDetached is the core: load the bundle, build an OFFLINE verifier from
// the frozen trusted root, pin (issuer, SAN-regexp), and verify. artifactOpt
// supplies the signed bytes — WithArtifact(reader) in production, or
// WithArtifactDigest("sha256", digest) in tests (deterministic, no 10 MB
// asset needed). Any failure maps to ErrSignatureInvalid (fail-closed,
// non-gateable); only Verify's absent-bundle pre-check yields the overridable
// ErrSignatureUnverified.
func (k *KeylessCosignVerifier) verifyDetached(bundlePath string, artifactOpt verify.ArtifactPolicyOption) error {
	const op = "KeylessCosignVerifier.Verify"

	b, err := bundle.LoadJSONFromPath(bundlePath)
	if err != nil {
		return &Error{Op: op, Input: bundlePath, Err: ErrSignatureInvalid}
	}

	tr, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		// A bad embedded root is a build defect, but fail closed regardless —
		// we cannot establish trust, so we refuse (never gateable).
		return &Error{Op: op, Input: "trusted_root", Err: ErrSignatureInvalid}
	}

	sev, err := verify.NewVerifier(root.TrustedMaterialCollection{tr},
		verify.WithSignedCertificateTimestamps(1), // Fulcio SCT in the cert
		verify.WithTransparencyLog(1),             // ≥1 Rekor inclusion proof
		verify.WithObserverTimestamps(1),          // Rekor integrated time
	)
	if err != nil {
		return &Error{Op: op, Err: ErrSignatureInvalid}
	}

	// SAN identity: the signer MUST be this repo's release.yml workflow, for
	// some tag. regexp.QuoteMeta guards a repo name that ever contains a regex
	// metacharacter; the literal path segments stay anchored.
	sanRegexp := `^https://github\.com/` + regexp.QuoteMeta(k.repo) + `/\.github/workflows/release\.yml@refs/tags/`
	certID, err := verify.NewShortCertificateIdentity(cosignOIDCIssuer, "", "", sanRegexp)
	if err != nil {
		return &Error{Op: op, Err: ErrSignatureInvalid}
	}

	if _, err := sev.Verify(b, verify.NewPolicy(artifactOpt, verify.WithCertificateIdentity(certID))); err != nil {
		return &Error{Op: op, Input: k.repo, Err: ErrSignatureInvalid}
	}
	return nil
}
