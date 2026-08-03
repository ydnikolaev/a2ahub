package livee2e

import "errors"

// FamilyOperationalConfidence groups the operational-confidence live scenarios.
const FamilyOperationalConfidence = "operational-confidence"

// OperationalConfidenceCatalogue is the exact P7 §6.2 release catalogue.
// IDs, branches and assertion tokens are wire values shared with evidence.go;
// spelling drift would make a runtime result impossible to validate.
func OperationalConfidenceCatalogue() []Scenario {
	return []Scenario{
		operationalScenario("LE-OC-01", "baseline", false, "receipt", "provenance", "fresh-fold", "no-mismatch-flag"),
		operationalScenario("LE-OC-02", "baseline", false, "announcement-v2", "durable-checkpoints", "cross-clone-projection", "local-heartbeat-local-only"),
		operationalScenario("LE-OC-03", "baseline", false, "snapshot-revision-equal", "operational-rows-equal", "sse-revision-only", "conditional-snapshot"),
		operationalScenario("LE-OC-04", "provider-setting", true, "same-pr-retry"),
		operationalScenario("LE-OC-05", "baseline", false, "visibility-public", "visibility-higher-classification"),
		operationalScenario("LE-OC-06", "baseline", false, "preflight-write-free", "contract-v2", "event-v2", "publication-intent", "contract-set-v2", "plan-publish-equal"),
		operationalScenario("LE-OC-07", "baseline", false, "historical-bytes-equal", "historical-digest-equal", "idempotent-rerun", "divergent-refusal"),
		operationalScenario("LE-OC-08", "baseline", false, "self-suite", "known-valid-pass", "known-invalid-stable-locations"),
		operationalScenario("LE-OC-08", "unsupported-provider", true, "unsupported-reported"),
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

func operationalRequireVerdict(verdict Verdict, err error) (Verdict, error) {
	if verdict == VerdictNotRun {
		return VerdictFail, errors.Join(err, errors.New("operational scenario body omitted its verdict"))
	}
	return verdict, err
}
