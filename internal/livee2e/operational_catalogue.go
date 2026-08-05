package livee2e

import "errors"

// FamilyOperationalConfidence groups the operational-confidence live scenarios.
const FamilyOperationalConfidence = "operational-confidence"

// OperationalConfidenceCatalogue is the exact P7 §6.2 release catalogue.
// IDs, branches and assertion tokens are wire values shared with evidence.go;
// spelling drift would make a runtime result impossible to validate.
//
// LE-OC-03 is TierLogic WITH a ProviderAssertions carve-out (spec 09 §5):
// its other assertions are hermetic, but "sse-revision-only" needs two
// intervening REAL merges inside its three-minute read deadline, which a fake
// that merges instantly cannot exercise. Left unconsulted that assertion does
// not merely fail locally — it PASSES, because the condition it guards cannot
// occur here, which is worse.
//
// LE-OC-05 was declared the same way and it was WRONG, corrected here after
// the first full local matrix run rather than reasoned about again. Its
// SECOND assertion, "visibility-higher-classification", asserts that `a2a
// doctor` emits "public repository carries higher-classified material" — a
// WARN doctor can only reach once it has DETERMINED the repository is public.
// Locally it correctly reports visibility unverified instead, so both of this
// row's assertions rest on the same provider fact and the row is
// TierProvider outright. A carve-out naming only one of them would have left
// the row failing every local run over the other.
//
// LE-OC-04's provider-setting branch and LE-OC-08's unsupported-provider
// branch are TierProvider for the simpler reason: a single assertion that is
// entirely GitHub's own to report.
func OperationalConfidenceCatalogue() []Scenario {
	return []Scenario{
		withTier(operationalScenario("LE-OC-01", "baseline", false, "receipt", "provenance", "fresh-fold", "no-mismatch-flag"), TierLogic),
		withTier(operationalScenario("LE-OC-02", "baseline", false, "announcement-v2", "durable-checkpoints", "cross-clone-projection", "local-heartbeat-local-only"), TierLogic),
		withTier(operationalScenario("LE-OC-03", "baseline", false, "snapshot-revision-equal", "operational-rows-equal", "sse-revision-only", "conditional-snapshot"), TierLogic, "sse-revision-only"),
		withTier(operationalScenario("LE-OC-04", "provider-setting", true, "same-pr-retry"), TierProvider),
		withTier(operationalScenario("LE-OC-05", "baseline", false, "visibility-public", "visibility-higher-classification"), TierProvider),
		withTier(operationalScenario("LE-OC-06", "baseline", false, "preflight-write-free", "contract-v2", "event-v2", "publication-intent", "contract-set-v2", "plan-publish-equal"), TierLogic),
		withTier(operationalScenario("LE-OC-07", "baseline", false, "historical-bytes-equal", "historical-digest-equal", "idempotent-rerun", "divergent-refusal"), TierLogic),
		withTier(operationalScenario("LE-OC-08", "baseline", false, "self-suite", "known-valid-pass", "known-invalid-stable-locations"), TierLogic),
		withTier(operationalScenario("LE-OC-08", "unsupported-provider", true, "unsupported-reported"), TierProvider),
	}
}

func operationalScenario(id, branch string, optional bool, assertions ...string) Scenario {
	return Scenario{
		Name: id, Branch: branch, Optional: optional,
		Systems: []string{SystemA}, Surfaces: cliOnly(),
		Family:     FamilyOperationalConfidence,
		Assertions: append([]string(nil), assertions...),
	}
}

// withTier returns a copy of s carrying the given Tier and, on a TierLogic
// row, the given ProviderAssertions carve-out (LE-OC-03, LE-OC-05). Kept
// separate from operationalScenario itself so that helper's own signature —
// which operational_runtime_test.go calls directly — stays exactly as it
// was.
func withTier(s Scenario, tier Tier, providerAssertions ...string) Scenario {
	s.Tier = tier
	s.ProviderAssertions = append([]string(nil), providerAssertions...)
	return s
}

func operationalRequireVerdict(verdict Verdict, err error) (Verdict, error) {
	if verdict == VerdictNotRun {
		return VerdictFail, errors.Join(err, errors.New("operational scenario body omitted its verdict"))
	}
	return verdict, err
}
