package lane

import "fmt"

// Coverage checks that every path in universe is claimed by at least one
// KindScoped declaration's Inputs, a KindAlways declaration's Claims
// (D-10 — a gate that is universal for one reason and scoped for another,
// e.g. epic-drift/docs/status.md), or is explicitly listed in ungated
// (D-3). A KindAlways declaration with no Claims and every KindNever
// declaration still claim nothing — a universal gate running on every path
// does not mean every path has been JUDGED by something that reads it,
// which is the property Coverage checks.
//
// ungated is a list of GLOBS (scripts/lib/lane-ungated.txt's own documented
// grammar — Match's, the same one Inputs/Claims use), matched with Match,
// never exact string membership: D-6's adjudication is written root-shaped
// ("integrations/macos-notifier/**", 40 files, one line) and an exact-match
// set would silently match none of them.
func Coverage(decls []Declaration, universe, ungated []string) []Refusal {
	var claimers []Declaration // KindScoped (via Inputs) + KindAlways (via Claims, D-10)
	for _, d := range decls {
		switch {
		case d.Kind == KindScoped:
			claimers = append(claimers, d)
		case d.Kind == KindAlways && len(d.Claims) > 0:
			claimers = append(claimers, d)
		}
	}

	var refusals []Refusal
	for _, path := range universe {
		if MatchesAnyGlob(ungated, path) {
			continue
		}
		claimed := false
		for _, d := range claimers {
			inputs := d.Inputs
			if d.Kind == KindAlways {
				inputs = d.Claims
			}
			if MatchInputs(inputs, path) {
				claimed = true
				break
			}
		}
		if !claimed {
			refusals = append(refusals, Refusal{
				Subject: path,
				Problem: fmt.Sprintf("no gate claims %s", path),
				Fix:     "add it to a gate's inputs or declare it explicitly ungated",
			})
		}
	}
	return refusals
}
