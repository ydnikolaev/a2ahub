package cache

import (
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// contractVersionWindow renders a folded contract's per-version states as an
// ordered slice — the ROLLING WINDOW a reader needs and neither
// ContractInfo.Version (the newest published) nor ContractInfo.State (the
// projection over all of them) can express.
//
// SEMVER ascending, not lexical. fold.Result.VersionNumbers() sorts
// lexically and says so in its own doc comment: ADR-001 allows that package
// exactly one non-stdlib import, so ordering versions correctly is not its
// job, and it deliberately promises determinism only. This is the layer
// where it becomes someone's job — internal/cache may import
// internal/version, the repo's SSOT comparator, so the ordering happens
// here, once, rather than in every renderer.
//
// A version string internal/version cannot parse sorts LAST, among its own
// kind lexically. Unparseable data must not silently claim to be the oldest
// version, which is the position a zero-value comparison would hand it.
//
// nil for every contract with no recorded versions — the shape every history
// had before P4 — which is what keeps `a2a contracts --json` byte-identical
// for them (ContractInfo.Versions is omitempty).
func contractVersionWindow(r fold.Result) []ContractVersion {
	if len(r.Versions) == 0 {
		return nil
	}
	out := make([]ContractVersion, 0, len(r.Versions))
	for v, s := range r.Versions {
		out = append(out, ContractVersion{Version: v, State: string(s)})
	}
	sort.Slice(out, func(i, j int) bool {
		_, iErr := version.Canonical(out[i].Version)
		_, jErr := version.Canonical(out[j].Version)
		switch {
		case iErr != nil && jErr != nil:
			return out[i].Version < out[j].Version
		case (iErr != nil) != (jErr != nil):
			return jErr != nil
		}
		if older, err := version.OlderThan(out[i].Version, out[j].Version); err == nil && older {
			return true
		}
		if older, err := version.OlderThan(out[j].Version, out[i].Version); err == nil && older {
			return false
		}
		// Two spellings of one version ("1.0" and "1.0.0"): neither is
		// older, and sort.Slice is not stable, so without this the order
		// would depend on the pivot.
		return out[i].Version < out[j].Version
	})
	return out
}
