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
			if p.Optional {
				break // opt-in phase, never declared: not a lane phase, not a defect.
			}
			refusals = append(refusals, Refusal{
				Subject: p.Name,
				Problem: fmt.Sprintf("phase %q has no lane-inputs declaration", p.Name),
				Fix:     "add a lane-inputs block above " + p.Source + "\n" + UndeclaredPhaseHelp,
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

// UndeclaredPhaseHelp is printed with every "phase has no lane-inputs
// declaration" refusal.
//
// It exists because the alternative is a second copy of the convention living
// in a rule file somebody has to know to read — which is the exact failure
// this phase was built to remove. A new gate is authored through
// `/mate-validator new`, a shared skill that has never heard of lane-inputs;
// the only surface guaranteed to reach its author is the refusal they get the
// first time they run `make check`. So the refusal teaches, and there is
// nothing to keep in step.
const UndeclaredPhaseHelp = `
  A validation phase must say which repo paths can change its verdict.
  One question decides the shape:

    Does your verdict depend on WHICH FILES changed?

    yes -> list the paths. As narrow as the gate really reads, no narrower:
           over-declaring costs seconds, under-declaring is a false green.

             # lane-inputs:
             #   internal/foo/**/*.go
             #   !internal/foo/**/*_test.go

    no  -> it is universal. Say WHY, naming the read that makes it so.
           A universal gate runs in every lane and CLAIMS NO PATH — if it is
           universal for one reason but really does read specific files, add
           lane-claims so those paths are covered without selecting it.

             # lane-inputs: ALWAYS
             # lane-reason: reads git ls-files over the whole tracked set (line 114)
             # lane-claims:
             #   docs/status.md

    never selectable at all (credentials, network, hours) ->

             # lane-inputs: NEVER
             # lane-reason: needs two credentials and a real GitHub space

  Put the block directly above the thing it describes: the gate script's
  header, the verify.sh run_phase line, or the Makefile recipe. In Go it is
  the same block with // in a package's doc.go, and gofmt will re-indent it.

  If the gate reads a path built from a variable or through a sourced helper,
  the extractor cannot resolve it — say so once, specifically:

             # lane-reads-opaque: the tracker path is built as "$FEATURES_DIR"/*/*/tracker.yaml

  Full reasoning: .claude/rules/check-convention.md, section "Adding a gate".`
