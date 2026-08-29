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
// path claimed by no KindScoped declaration, or claimed only by a claim
// P1's claimVerdict cannot back, is a Refusal in the spec's §3.3 wording,
// naming the path — never a fallback to the ceiling and never silence (J2).
//
// P1's backing predicate changes ONLY what counts as JUDGED (whether a
// refusal fires), never what gets SELECTED: an over-declared, unbacked glob
// still selects its gate — internal/lane/reconcile.go:87's standing
// position on selection is untouched — so `matched` (and therefore
// sel.Phases) is populated exactly as it always was, while the separate
// `judged` bookkeeping below is what claimVerdict's backed return gates.
//
// Derive is a pure function of (decls, changed, backing): it carries no
// telemetry and leaves each SelectedPhase's Estimate at its zero value —
// the CLI attaches the real duration estimate from ReadTelemetry at print
// time, so a derivation used as a library is never coupled to a telemetry
// file on disk. backing may be nil (every claim reports unbacked, the same
// honest default Coverage documents).
func Derive(decls []Declaration, changed []string, backing map[string]PhaseBacking) (Selection, []Refusal) {
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
		judged := false
		for _, d := range scoped {
			claims, backed, pattern := claimVerdict(d, path, backing)
			if !claims {
				continue
			}
			// Selection is UNCONDITIONAL on claims alone — see the func
			// comment above.
			matched[d.Phase] = append(matched[d.Phase], MatchedPath{Path: path, Pattern: pattern})
			if backed {
				judged = true
			}
		}
		// A KindAlways gate's lane-claims paths satisfy the claim without
		// selecting anything — the gate is already in sel.Phases above,
		// unconditionally. Coverage() honours these; so must Derive, or the
		// user-facing lane refuses a path --verify accepts, which is a
		// contradiction the operator has no way to resolve.
		if !judged {
			judged = alwaysClaimBacked(decls, path, backing)
		}
		if !judged {
			refusals = append(refusals, unclaimedOrUnbackedRefusal(decls, scoped, path, backing))
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

// alwaysClaimBacked reports whether any KindAlways declaration's lane-claims
// list (D-10) covers path AND that claim is backed (claimVerdict). Those
// paths count as claimed but select nothing — the gate that claims them is
// universal and already in the lane. Coverage() applies the same rule via
// the same claimVerdict predicate; Derive has to agree with it, because a
// path that `--verify` accepts and `--derive` refuses is a contradiction
// with no operator-visible resolution.
func alwaysClaimBacked(decls []Declaration, path string, backing map[string]PhaseBacking) bool {
	for _, d := range decls {
		if d.Kind != KindAlways || len(d.Claims) == 0 {
			continue
		}
		if _, backed, _ := claimVerdict(d, path, backing); backed {
			return true
		}
	}
	return false
}

// unclaimedOrUnbackedRefusal builds the refusal for a changed path no
// declaration judged: the generic "no gate claims" shape when nothing
// claims it at all (scoped or ALWAYS-with-Claims), or coverage.go's own
// unbackedRefusal — naming the phase, the glob, and both legal fixes — when
// one or more declarations DO claim it but none backs the claim.
func unclaimedOrUnbackedRefusal(decls, scoped []Declaration, path string, backing map[string]PhaseBacking) Refusal {
	for _, d := range scoped {
		if claims, _, pattern := claimVerdict(d, path, backing); claims {
			return unbackedRefusal(path, d, pattern)
		}
	}
	for _, d := range decls {
		if d.Kind != KindAlways || len(d.Claims) == 0 {
			continue
		}
		if claims, _, pattern := claimVerdict(d, path, backing); claims {
			return unbackedRefusal(path, d, pattern)
		}
	}
	return Refusal{
		Subject: path,
		Problem: fmt.Sprintf("no gate claims %s", path),
		Fix:     "add it to a gate's inputs or declare it explicitly ungated",
	}
}
