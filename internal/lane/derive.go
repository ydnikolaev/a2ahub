package lane

import (
	"fmt"
	"sort"
)

// Derive computes the lane for a changed-file set (spec §3.2/§3.3): every
// KindAlways declaration always runs (it claims no path, so it is never
// what a refusal is about); every KindScoped declaration whose Inputs
// intersect the diff runs, carrying which changed paths matched; a
// KindNever declaration is never auto-selected (D-8 — live-e2e). A changed
// path claimed by no KindScoped declaration is a Refusal in the spec's
// §3.3 wording, naming the path — never a fallback to the ceiling and
// never silence (J2).
//
// Derive is a pure function of (decls, changed): it carries no telemetry
// and leaves each SelectedPhase's Estimate at its zero value — the CLI
// attaches the real duration estimate from ReadTelemetry at print time,
// so a derivation used as a library is never coupled to a telemetry file
// on disk.
func Derive(decls []Declaration, changed []string) (Selection, []Refusal) {
	var sel Selection
	var refusals []Refusal

	for _, d := range decls {
		if d.Kind == KindAlways {
			sel.Phases = append(sel.Phases, SelectedPhase{Declaration: d})
		}
	}

	var scoped []Declaration
	for _, d := range decls {
		if d.Kind == KindScoped {
			scoped = append(scoped, d)
		}
	}

	// The lane is the UNION of every gate whose declared inputs intersect
	// the diff (spec §3.2) — a changed .go file legitimately selects
	// go-test, vet, golangci-lint AND logic-e2e at once, so every scoped
	// declaration is checked for every path, not just the first match.
	matched := make(map[string][]MatchedPath) // Phase -> matched changed paths + winning pattern
	for _, path := range changed {
		claimed := false
		for _, d := range scoped {
			if ok, pattern := MatchingInput(d.Inputs, path); ok {
				claimed = true
				matched[d.Phase] = append(matched[d.Phase], MatchedPath{Path: path, Pattern: pattern})
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

	for _, d := range scoped {
		if paths, ok := matched[d.Phase]; ok {
			sel.Phases = append(sel.Phases, SelectedPhase{Declaration: d, Matched: paths})
		}
	}

	sort.Slice(sel.Phases, func(i, j int) bool {
		return sel.Phases[i].Declaration.Phase < sel.Phases[j].Declaration.Phase
	})
	return sel, refusals
}
