package release

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
)

// ApplyOptions carries everything Apply needs to turn a resolved target
// release into an atomically swapped binary.
type ApplyOptions struct {
	// Client is the HTTP client for the asset fetch (nil => http.DefaultClient).
	Client *http.Client
	// Token selects the fetch path (T3): non-empty => private release-asset
	// API + Bearer; "" => public tokenless BrowserDownloadURL.
	Token string
	// Target is the resolved target release (Resolve's Decision, re-fetched
	// into a full Release with assets).
	Target Release
	// ExecPath is the running binary's resolved path — the Swap target. Its
	// directory is the download dir, so the final rename stays same-filesystem.
	ExecPath string
	// AllowUnsigned permits proceeding ONLY when the signature material is
	// ABSENT (ErrSignatureUnverified — no .cosign.bundle for the asset). It
	// NEVER permits proceeding on a checksum mismatch (ErrChecksumMismatch) or a
	// present-but-FAILED signature (ErrSignatureInvalid); both are hard stops.
	AllowUnsigned bool
	// Verifier is the verification chain (nil => DefaultVerifier: checksum
	// always-first + the repo-less UNVERIFIED signature fallback — the real CLI
	// path passes a repo-pinned KeylessVerifier, so this fallback is only hit by
	// a caller that omits one).
	Verifier Verifier
	// Run is the self-check exec seam (nil => DefaultRunner).
	Run Runner
}

// ApplyResult reports the completed swap's version delta (T1 step 5).
type ApplyResult struct {
	FromVersion string
	ToVersion   string
	Commit      string
}

// Apply runs the T1 mechanical pipeline in the ONLY safe order —
// Download → Verify (checksum ALWAYS first, signature per policy) →
// SelfCheckVersion (exec the temp binary) → Swap — cleaning up the
// downloaded temp on any failure so a verified-bad or unswapped binary never
// lingers on disk.
//
// This is the seam's single safe entry point, and callers (the `a2a update`
// verb, and any future consumer) MUST use it rather than sequencing the
// primitives by hand: SelfCheckVersion execs the downloaded asset as a
// subprocess, and that asset is signature-UNTRUSTED until Verify has returned
// nil — Apply is what guarantees Verify runs first. --allow-unsigned gates
// ONLY ErrSignatureUnverified; a checksum mismatch is never gateable (the
// CompositeVerifier runs ChecksumVerifier first and returns ErrChecksumMismatch
// before the signature slot is ever reached).
func Apply(ctx context.Context, currentVersion string, opts ApplyOptions) (ApplyResult, error) {
	verifier := opts.Verifier
	if verifier == nil {
		verifier = DefaultVerifier()
	}
	destDir := filepath.Dir(opts.ExecPath)

	dl, err := Download(ctx, opts.Client, opts.Token, opts.Target, destDir)
	if err != nil {
		return ApplyResult{}, err
	}

	if err := verifier.Verify(ctx, dl.AssetPath, opts.Target); err != nil {
		// A checksum mismatch is never gateable. An UNVERIFIED signature is
		// gateable ONLY by an explicit --allow-unsigned (and the checksum has
		// already passed by the time the signature slot runs).
		if !opts.AllowUnsigned || !errors.Is(err, ErrSignatureUnverified) {
			Cleanup(dl)
			return ApplyResult{}, err
		}
	}

	if err := SelfCheckVersion(ctx, dl.AssetPath, opts.Target.Version, opts.Run); err != nil {
		Cleanup(dl)
		return ApplyResult{}, err
	}

	if err := Swap(opts.ExecPath, dl.AssetPath); err != nil {
		Cleanup(dl)
		return ApplyResult{}, err
	}

	// Swap renamed the asset over ExecPath, so only the sums/bundle temps
	// remain — remove them (the asset path no longer exists there).
	Cleanup(DownloadResult{SumsPath: dl.SumsPath, BundlePath: dl.BundlePath})

	return ApplyResult{FromVersion: currentVersion, ToVersion: opts.Target.Version, Commit: opts.Target.Commit}, nil
}

// Cleanup best-effort removes the temp files a DownloadResult names. Exported
// so a caller that runs Download separately (and then fails at its own step)
// can drop the temps without re-implementing the removal.
func Cleanup(dl DownloadResult) {
	cleanupPaths(dl.AssetPath, dl.SumsPath, dl.BundlePath)
}
