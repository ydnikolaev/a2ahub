package space

import "github.com/ydnikolaev/a2ahub/internal/version"

// versionOlderThan reports whether binaryVersion is strictly older than
// minVersion, comparing dotted-integer components (major.minor.patch — the
// shape space.yaml's min_binary_version is constrained to,
// schemas/manifest/v1/space.schema.json's pattern). A leading "v" is
// tolerated on either input (common release-tag convention) and missing
// trailing components default to 0. Either input failing to parse as
// dotted integers returns (false, ErrInvalidVersion) — the CC-085 guard
// treats an unparseable version as "cannot verify" and fails CLOSED
// (refuses the write) rather than silently permitting it.
//
// This is a thin wrapper over the SSOT comparator internal/version.OlderThan
// (spec 19 §7 anti-dup, moved verbatim there): it remaps the leaf's own
// sentinel back to this package's ErrInvalidVersion so existing callers and
// tests keep observing errors.Is(err, space.ErrInvalidVersion).
func versionOlderThan(binaryVersion, minVersion string) (bool, error) {
	older, err := version.OlderThan(binaryVersion, minVersion)
	if err != nil {
		// Name both inputs (the leaf does not report which one failed to
		// parse) rather than always blaming binaryVersion.
		return false, &Error{Op: "versionOlderThan", Input: binaryVersion + " / " + minVersion, Err: ErrInvalidVersion}
	}
	return older, nil
}
