// Package version is the SSOT bare-version comparator: dotted
// major.minor.patch parsing and strict-older comparison, stdlib-only, with
// no dependency on any other a2ahub package. internal/space, internal/cli,
// and internal/release all consume OlderThan (spec 19 §7 anti-dup) instead
// of writing a third copy.
package version

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidVersion is returned when a version string cannot be parsed as
// dotted-integer major.minor.patch. Callers that guard a version-gated
// action (e.g. the CC-085 write funnel) treat this as "cannot verify" and
// fail CLOSED rather than silently permitting the action.
var ErrInvalidVersion = errors.New("version: invalid version string")

// OlderThan reports whether a is strictly older than b, comparing dotted
// major.minor.patch components (the shape space.yaml's min_binary_version
// is constrained to, schemas/manifest/v1/space.schema.json's pattern). A
// leading "v" is tolerated on either input (common release-tag convention)
// and missing trailing components default to 0. Either input failing to
// parse as dotted integers returns (false, ErrInvalidVersion) — fail
// CLOSED: an unparseable version is treated as "cannot verify", never as
// "not older".
func OlderThan(a, b string) (bool, error) {
	av, err := parseVersion(a)
	if err != nil {
		return false, err
	}
	bv, err := parseVersion(b)
	if err != nil {
		return false, err
	}
	for i := range av {
		if av[i] != bv[i] {
			return av[i] < bv[i], nil
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
		return out, ErrInvalidVersion
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return out, ErrInvalidVersion
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, ErrInvalidVersion
		}
		out[i] = n
	}
	return out, nil
}

// Canonical returns s in canonical major.minor.patch form, so two spellings
// of the same version compare equal as map keys.
//
// It exists because a version reaches this codebase by two different routes
// and only one of them normalises: `contract publish` writes its own parsed
// value back (`newVersion.String()`), while `deprecate` and `retire` record
// the operator's `--version` flag VERBATIM. So `publish 1.0.0` followed by
// `deprecate 01.0.0` records two strings for one version, and any policy that
// keys on the raw string sees two versions where there is one. POL-011 did
// exactly that and produced a phantom refusal naming the contract's own sole
// version — the authoritative-looking wrong answer this project treats as
// worse than the defect it reports.
func Canonical(s string) (string, error) {
	v, err := parseVersion(s)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]), nil
}
