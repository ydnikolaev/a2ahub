package livee2e

import (
	"fmt"
	"sort"
)

// tier.go implements spec 09 §5's own gate: "every row declares which tier
// can judge it, and the declaration is gated. A row that runs in the logic
// tier while depending on a provider behaviour will pass against a fake that
// decides the answer, and that is worse than not running it: the report
// reads complete."
//
// UNTAGGED on purpose, matching completeness.go's and familyset.go's own
// precedent: the RULE is pure, so it lives where the plain
// `go test ./internal/livee2e/` suite `make check` runs covers it — nothing
// here touches the network or the livee2e build tag, and a gate that only
// runs during a 40-minute live run is a gate nobody runs.
//
// Every check below returns a sorted []string of offenders rather than an
// error, so a failing report names every broken row at once instead of just
// the first — the same shape missingCoveredKinds/unknownDeclaredKinds
// (completeness.go) and missingCoveredFamilies/unknownDeclaredFamilies
// (familyset.go) already use, and each is unit-tested against a SYNTHETIC
// scenario slice so proving the gate reds does not require mutating the
// real catalogue.

// Tier declares which internal/livee2e tier may judge a scenario's own
// verdict.
type Tier string

const (
	// TierLogic scenarios may be judged by EITHER tier: the logic tier
	// proves them locally against a local space in seconds, and the live
	// tier proves them again for real — belt and suspenders, never a
	// second, different claim.
	TierLogic Tier = "logic"
	// TierProvider scenarios depend on a fact only GitHub can decide — the
	// gate's own conclusion, branch protection, enforce_admins, re-trigger
	// semantics, stale-ref serving, real workflow-run history, repo
	// settings, token-scope reporting, real latency — and may be judged
	// ONLY by the live tier. testkit/fakegithub deliberately does not grow
	// routes for these facts (plan D-6); its 404 is the fail-closed guard
	// if a provider-only row somehow reaches the logic tier.
	TierProvider Tier = "provider"
)

// scenarioRowKey renders a scenario's own identity for an offender line —
// "name" for an unbranched row, "name/branch" for a P7 operational-
// confidence row, matching resultScenarioKey's (report.go) own convention.
func scenarioRowKey(s Scenario) string {
	if s.Branch == "" {
		return s.Name
	}
	return s.Name + "/" + s.Branch
}

// scenarioDeclaresAssertion reports whether name is one of s's own
// Assertions. A local helper rather than the package's containsString
// (scenarios_operational_confidence_live.go) because that file carries the
// `livee2e` build tag and this one must stay untagged.
func scenarioDeclaresAssertion(s Scenario, name string) bool {
	for _, a := range s.Assertions {
		if a == name {
			return true
		}
	}
	return false
}

// undeclaredTierRows is D-3's first fail-closed rule: a row whose Tier is
// neither TierLogic nor TierProvider — including the zero value — is a gate
// failure, never a silent default of "logic".
func undeclaredTierRows(scenarios []Scenario) []string {
	var offenders []string
	for _, s := range scenarios {
		if s.Tier != TierLogic && s.Tier != TierProvider {
			offenders = append(offenders, scenarioRowKey(s))
		}
	}
	sort.Strings(offenders)
	return offenders
}

// providerAssertionsOnProviderRows is D-3's second rule: ProviderAssertions
// is only meaningful on an otherwise-TierLogic row (plan D-2's "progression
// is not judgement" carve-out). A TierProvider row's assertions are ALL
// provider, so naming a subset in ProviderAssertions is incoherent and must
// be refused rather than interpreted.
func providerAssertionsOnProviderRows(scenarios []Scenario) []string {
	var offenders []string
	for _, s := range scenarios {
		if len(s.ProviderAssertions) > 0 && s.Tier != TierLogic {
			offenders = append(offenders, scenarioRowKey(s))
		}
	}
	sort.Strings(offenders)
	return offenders
}

// undeclaredProviderAssertions is D-3's third rule: every ProviderAssertions
// entry must also appear in that row's own Assertions. A carve-out naming an
// assertion the row does not have is a typo that would silently exempt
// nothing.
func undeclaredProviderAssertions(scenarios []Scenario) []string {
	var offenders []string
	for _, s := range scenarios {
		for _, pa := range s.ProviderAssertions {
			if !scenarioDeclaresAssertion(s, pa) {
				offenders = append(offenders, fmt.Sprintf("%s: %q", scenarioRowKey(s), pa))
			}
		}
	}
	sort.Strings(offenders)
	return offenders
}

// duplicateProviderAssertions is D-3's fourth rule: ProviderAssertions may
// not repeat an entry within the same row.
func duplicateProviderAssertions(scenarios []Scenario) []string {
	var offenders []string
	for _, s := range scenarios {
		seen := make(map[string]bool, len(s.ProviderAssertions))
		for _, pa := range s.ProviderAssertions {
			if seen[pa] {
				offenders = append(offenders, fmt.Sprintf("%s: %q", scenarioRowKey(s), pa))
				continue
			}
			seen[pa] = true
		}
	}
	sort.Strings(offenders)
	return offenders
}

// JudgeableBy reports whether tier t may pass judgement on this scenario's
// own verdict. A TierLogic row may be judged by either tier; a TierProvider
// row (or one whose Tier is not yet validly declared) may be judged only by
// the provider tier — the fail-closed reading of "we do not know" is the
// strictest tier, never the most permissive one.
func (s Scenario) JudgeableBy(t Tier) bool {
	if s.Tier == TierLogic {
		return true
	}
	return t == TierProvider
}

// FilterJudgeableBy returns, in declaration order, the subset of scenarios
// tier t may judge.
func FilterJudgeableBy(scenarios []Scenario, t Tier) []Scenario {
	out := make([]Scenario, 0, len(scenarios))
	for _, s := range scenarios {
		if s.JudgeableBy(t) {
			out = append(out, s)
		}
	}
	return out
}
