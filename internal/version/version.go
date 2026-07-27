// Package version is the SSOT for reasoning about bare dotted
// major.minor.patch versions: parsing, strict-older comparison,
// canonicalisation, major extraction, and baseline selection. Stdlib-only,
// with no dependency on any other a2ahub package — which is what lets any
// layer consume it, including the ones that must stay pure.
//
// It exists so the rules live in ONE place rather than being re-derived per
// caller (spec 19 §7 anti-dup). Consumers are deliberately not enumerated
// here: the list only ever grows, and a stale enumeration reads as a limit
// on who may use this.
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

// Major extracts the major component from a dotted version string —
// P4 wave 5's Edge 1 (04-per-version-lifecycle.md §4): the retire gate
// compares a registered consumer's pinned major (consumes.yaml's own
// `major` field) against the major of the line being retired, and this is
// the one place that comparison's version-string side gets parsed, shared
// rather than re-derived per surface. Fails CLOSED (0, ErrInvalidVersion)
// on an unparseable v, matching OlderThan/Canonical's own contract.
func Major(v string) (int, error) {
	parsed, err := parseVersion(v)
	if err != nil {
		return 0, err
	}
	return parsed[0], nil
}

// Baseline returns max{v ∈ published : v < target} — the highest published
// version strictly older than target, not necessarily the highest of
// published overall. P4 wave 5's Edge 2 (04-per-version-lifecycle.md §4):
// publishing a maintenance 1.2 while 2.0 is already published must compare
// against 1.1, not 2.0 — "baseline = priorVersions[len-1]" picks the
// globally-highest published version regardless of line, which is only
// this rule's special case when target IS the highest-ever publish. found
// is false when no published version qualifies (target is the first-ever
// publish for its line, or every published version is >= target) — the
// caller's own "nothing to compare against" case, not an error.
//
// Every entry in published, and target itself, must already parse as a
// dotted version (OlderThan's own shape) — the first that does not fails
// CLOSED with ErrInvalidVersion, matching OlderThan/Canonical's own
// contract; a caller comparing against a baseline it cannot trust must
// stop, not guess.
func Baseline(published []string, target string) (baseline string, found bool, err error) {
	if _, terr := parseVersion(target); terr != nil {
		return "", false, terr
	}
	for _, p := range published {
		older, oerr := OlderThan(p, target)
		if oerr != nil {
			return "", false, oerr
		}
		if !older {
			continue
		}
		if !found {
			baseline, found = p, true
			continue
		}
		baselineOlder, berr := OlderThan(baseline, p)
		if berr != nil {
			return "", false, berr
		}
		if baselineOlder {
			baseline = p
		}
	}
	return baseline, found, nil
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
