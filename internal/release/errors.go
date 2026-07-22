package release

import "errors"

// Sentinel errors, one per failure class (P1 idiom: internal/artifact).
// Callers use errors.Is against these; a typed *Error carries the
// operation and offending input on top.
var (
	// ErrNoRelease is returned when the source has no usable release: no
	// releases published at all, or every candidate is a draft, a
	// pre-release, or carries a tag that does not match the v*.*.*
	// grammar (spec 19 T3/§6 "no releases at all; pre-release/malformed
	// tags skipped").
	ErrNoRelease = errors.New("release: no usable release found")

	// ErrAssetNotFound is returned when a required asset (the platform
	// binary or SHA256SUMS) is absent from a release's asset list.
	ErrAssetNotFound = errors.New("release: asset not found")

	// ErrDownloadFailed is returned when fetching an asset fails at the
	// transport/status level (network error, non-2xx response).
	ErrDownloadFailed = errors.New("release: download failed")

	// ErrChecksumMismatch is returned by ChecksumVerifier for any failure
	// that keeps it from establishing integrity: a tampered asset, a
	// missing SHA256SUMS line for the asset, an unreadable SHA256SUMS
	// file, or a malformed SHA256SUMS file. Fail-closed: this sentinel is
	// NEVER gateable by --allow-unsigned (T2, T-8, D-013).
	ErrChecksumMismatch = errors.New("release: checksum mismatch")

	// ErrSignatureUnverified means the signature could not be checked because
	// the material is ABSENT — no `.cosign.bundle` alongside the asset (or, in
	// the repo-less DefaultVerifier fallback, no identity to pin against). It is
	// distinct from a checksum failure AND from a signature that was checked and
	// FAILED (ErrSignatureInvalid), so the verb can gate ONLY this sentinel
	// behind --allow-unsigned (an absent signature is the sole overridable case).
	ErrSignatureUnverified = errors.New("release: signature bundle absent — verification skipped")

	// ErrSignatureInvalid is returned by KeylessCosignVerifier when a bundle IS
	// present but verification FAILS — a bad/tampered signature, an unreadable
	// or malformed bundle, an untrusted-root or transparency-log failure, or a
	// certificate identity/issuer that does not match the pinned release-workflow
	// OIDC identity. Fail-closed: this sentinel is NEVER gateable by
	// --allow-unsigned (a present-but-wrong signature is a hard stop, T-8, D-013).
	ErrSignatureInvalid = errors.New("release: signature verification failed")

	// ErrSelfCheckFailed is returned when the downloaded (temp) binary's
	// `version` output cannot be parsed, or its stamped bare version does
	// not equal the target release's bare version (catches wrong-arch or
	// corrupt-but-summed assets before any swap, T1 step 3).
	ErrSelfCheckFailed = errors.New("release: post-download version self-check failed")

	// ErrSwapFailed is returned when the atomic rename over the running
	// binary's path fails (e.g. an unwritable, system-owned install dir).
	ErrSwapFailed = errors.New("release: swap failed")

	// ErrCacheUnavailable is returned when the update-check cache's
	// directory cannot be resolved or the cache file cannot be written.
	// Reading a corrupt/absent cache is NEVER an error (see ReadCheck) —
	// this sentinel covers only CachePath/WriteCheck failures.
	ErrCacheUnavailable = errors.New("release: update-check cache unavailable")
)

// Error is the small typed error every exported operation in this package
// returns on failure. It always wraps one of the sentinels above so
// callers can use errors.Is/As; it never panics on bad input.
type Error struct {
	// Op names the failing operation (e.g. "Resolve", "Download").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel (see the vars above).
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "release: " + e.Op + ": " + e.Err.Error()
	}
	return "release: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
