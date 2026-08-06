package lane

import (
	"fmt"
	"strings"
)

// derivedPhasePrefix marks a Go package's derived scoped-test phase
// (D-2's exemption): valid whenever the package directory it names exists,
// which Load already established by finding the doc.go there — so it
// never needs to appear in the fixed corpus list.
const derivedPhasePrefix = "go-test-scoped:./"

// Reconcile checks both directions (the check_hashes idiom,
// scripts/check-operational-confidence.sh:97-105):
//   - every corpus phase has exactly one declaration naming it (zero is a
//     missing declaration; more than one is an ambiguous, duplicated one —
//     Load's own validateAll already catches this for a real scanned tree,
//     but Reconcile is also called directly with hand-built slices, e.g.
//     the declaration gate's --teeth fixtures, so it checks again here);
//   - every declaration names a phase that actually exists in the corpus
//     (or is a derived go-test-scoped:./pkg/... declaration, exempt).
func Reconcile(decls []Declaration, corpus []Phase) []Refusal {
	byPhase := map[string][]Declaration{}
	for _, d := range decls {
		byPhase[d.Phase] = append(byPhase[d.Phase], d)
	}
	corpusNames := map[string]Phase{}
	for _, p := range corpus {
		corpusNames[p.Name] = p
	}

	var refusals []Refusal
	for _, p := range corpus {
		switch len(byPhase[p.Name]) {
		case 0:
			refusals = append(refusals, Refusal{
				Subject: p.Name,
				Problem: fmt.Sprintf("phase %q has no lane-inputs declaration", p.Name),
				Fix:     "add a lane-inputs block above " + p.Source,
			})
		case 1:
			// exactly one — reconciled.
		default:
			refusals = append(refusals, Refusal{
				Subject: p.Name,
				Problem: fmt.Sprintf("phase %q has %d lane-inputs declarations, want exactly one", p.Name, len(byPhase[p.Name])),
				Fix:     "remove the duplicate declaration",
			})
		}
	}
	for _, d := range decls {
		if strings.HasPrefix(d.Phase, derivedPhasePrefix) {
			continue
		}
		if _, ok := corpusNames[d.Phase]; !ok {
			refusals = append(refusals, Refusal{
				Subject: d.Source,
				Problem: fmt.Sprintf("declares phase %q which does not exist in the corpus", d.Phase),
				Fix:     "remove the stale declaration or fix the phase name",
			})
		}
	}
	return refusals
}
