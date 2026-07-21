package space

import (
	"strconv"
	"strings"
)

// versionOlderThan reports whether binaryVersion is strictly older than
// minVersion, comparing dotted-integer components (major.minor.patch — the
// shape space.yaml's min_binary_version is constrained to,
// schemas/manifest/v1/space.schema.json's pattern). A leading "v" is
// tolerated on either input (common release-tag convention) and missing
// trailing components default to 0. Either input failing to parse as
// dotted integers returns (false, ErrInvalidVersion) — the CC-085 guard
// treats an unparseable version as "cannot verify" and fails CLOSED
// (refuses the write) rather than silently permitting it.
func versionOlderThan(binaryVersion, minVersion string) (bool, error) {
	bv, err := parseVersion(binaryVersion)
	if err != nil {
		return false, err
	}
	mv, err := parseVersion(minVersion)
	if err != nil {
		return false, err
	}
	for i := range bv {
		if bv[i] != mv[i] {
			return bv[i] < mv[i], nil
		}
	}
	return false, nil
}

// parseVersion parses a "v"?major(.minor(.patch)?)? string into a
// fixed-length [3]int tuple, stdlib-only (no new semver dependency).
func parseVersion(s string) ([3]int, error) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" {
		return out, &Error{Op: "parseVersion", Input: s, Err: ErrInvalidVersion}
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return out, &Error{Op: "parseVersion", Input: s, Err: ErrInvalidVersion}
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, &Error{Op: "parseVersion", Input: s, Err: ErrInvalidVersion}
		}
		out[i] = n
	}
	return out, nil
}
