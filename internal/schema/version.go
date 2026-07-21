package schema

import (
	"regexp"
	"strconv"
)

// currentEnvelopeVersion is N in "envelope/v<N>" — the newest envelope
// schema version this build of the corpus ships (§5.4). Bumping it is a
// schemas/** + registry change (P2-owned); this constant mirrors what the
// embedded corpus actually contains — it is NOT itself the SSOT (the
// SSOT is the set of files under schemas/envelope/v<N>/).
const currentEnvelopeVersion = 1

// currentEventVersion / currentManifestVersion / currentConsumesVersion:
// each family versions independently in principle (§5.1), but v1-min only
// ships v1 of each. Documented separately from currentEnvelopeVersion so a
// future per-family version bump doesn't require touching unrelated
// families' seam logic.
const (
	currentEventVersion    = 1
	currentManifestVersion = 1
	currentConsumesVersion = 1
)

// versionSuffix matches a trailing "v<N>" version token, either bare
// ("v1") or family-prefixed ("envelope/v1", "event/v1", ...) — both
// shapes appear across this package's callers (an artifact's own
// `schema:` field is family-prefixed; a caller that has already split off
// the family passes the bare form).
var versionSuffix = regexp.MustCompile(`(?:^|/)v([0-9]+)$`)

// ParseVersion parses the "v<N>" suffix of a schema field value (e.g.
// "envelope/v1" -> 1, or bare "v1" -> 1). It returns ok=false for anything
// that doesn't match the "v" + digits shape.
func ParseVersion(schemaField string) (n int, ok bool) {
	m := versionSuffix.FindStringSubmatch(schemaField)
	if m == nil {
		return 0, false
	}
	parsed, err := strconv.Atoi(m[1])
	if err != nil || parsed < 1 {
		return 0, false
	}
	return parsed, true
}

// acceptedWindow returns [min, current] — the one-cycle overlap window
// (§5.4 last bullet: "the binary understands envelope version N and
// N-1") for a family whose newest shipped version is current. When
// current == 1 (v1-min: no prior version ever existed), the window
// collapses to just 1 — there is no "v0" to accept.
func acceptedWindow(current int) (min, max int) {
	min = current - 1
	if min < 1 {
		min = 1
	}
	return min, current
}

// AcceptsEnvelopeVersion reports whether v (parsed from an artifact's
// `schema: envelope/v<N>` field) is inside the one-cycle overlap window
// this build of the binary understands. CC-005: a version OUTSIDE this
// window (in practice, only "newer than known" is reachable in the wild —
// an older-than-N-1 binary would have already been retired) must be
// refused: read-only + loud warning, never a silent write (§5.4, §7.3).
// This function only answers the pure yes/no question; surfacing the
// CC-005 refusal + warning is internal/validate's job.
func AcceptsEnvelopeVersion(v int) bool {
	min, max := acceptedWindow(currentEnvelopeVersion)
	return v >= min && v <= max
}

// AcceptsEventVersion / AcceptsManifestVersion / AcceptsConsumesVersion:
// the same one-cycle-overlap seam for the other three product-schema
// families (§5.1 lists each independently; only envelope currently has a
// documented AC/CC hook, but the seam is designed for all four so a
// future per-family bump doesn't require new plumbing).
func AcceptsEventVersion(v int) bool {
	min, max := acceptedWindow(currentEventVersion)
	return v >= min && v <= max
}

// AcceptsManifestVersion reports whether v is inside the one-cycle overlap window for manifest versions.
func AcceptsManifestVersion(v int) bool {
	min, max := acceptedWindow(currentManifestVersion)
	return v >= min && v <= max
}

// AcceptsConsumesVersion reports whether v is inside the one-cycle overlap window for consumes versions.
func AcceptsConsumesVersion(v int) bool {
	min, max := acceptedWindow(currentConsumesVersion)
	return v >= min && v <= max
}
