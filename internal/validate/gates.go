package validate

import (
	"fmt"
	"slices"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// This file is the pure half of the gates-and-flow policy (local-space-2026-10
// spec 04): the vocabulary check for the manifest's `gates` and `flow` keys,
// the host check for `autonomous`, and the land check's approval rule. Nothing
// here reads a host, a repository or a commit: the caller supplies every fact as
// a value, so internal/validate imports neither internal/space nor
// internal/host.
//
// The vocabulary itself (contract.ParseGatesSetting and friends) is defined in
// internal/contract, the one package both this package and internal/space may
// import, so the write-side check and the read-side effective mode cannot
// disagree about which words are legal.

// manifestPolicyKeys decodes the two policy keys out of raw space.yaml bytes.
// ok is false when raw is not YAML or not a mapping: POL-002 belongs to
// ValidateManifest and these checks stay silent about a document they cannot
// read.
func manifestPolicyKeys(raw []byte) (gates, flow any, ok bool) {
	instance, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		return nil, nil, false
	}
	return policyKeysOf(instance)
}

func policyKeysOf(instance any) (gates, flow any, ok bool) {
	document, isMapping := instance.(map[string]any)
	if !isMapping {
		return nil, nil, false
	}
	return document["gates"], document["flow"], true
}

// checkGatesFlowVocabulary is POL-027: `gates` or `flow` carries a value
// outside its vocabulary, or a value that is not a string. An absent key, or an
// explicit null, is the key's default and is clean.
//
// It runs from ValidateManifest only, never from checkManifestPolicy: the
// latter runs on every pull request through ValidateManifestPolicy, so a rule
// placed there would red an unrelated PR over an old odd value. Judged at the
// write that introduces the value, the way spec 04 §T1.1 puts it.
func checkGatesFlowVocabulary(instance any) []Violation {
	gates, flow, ok := policyKeysOf(instance)
	if !ok {
		return nil
	}
	var violations []Violation
	if _, legal := contract.ParseGatesSetting(gates); !legal {
		violations = append(violations, outOfVocabularyViolation("gates", gates, "default, confirm or autonomous"))
	}
	if _, legal := contract.ParseFlowSetting(flow); !legal {
		violations = append(violations, outOfVocabularyViolation("flow", flow, "full or express"))
	}
	return violations
}

func outOfVocabularyViolation(key string, value any, legal string) Violation {
	got := "a value that is not a string"
	if word, isString := value.(string); isString {
		got = fmt.Sprintf("%q", word)
	}
	return Violation{
		Code:     "POL-027",
		Class:    ClassPolicy,
		Path:     key,
		Message:  fmt.Sprintf("%s is %s; the legal values are %s", key, got, legal),
		Severity: SeverityReject,
	}
}

// CheckGatesForHost is POL-028: `gates: autonomous` on a host that reviews
// writes. It returns that and nothing else. The vocabulary verdict is
// (*Engine).ValidateManifest's, so an out-of-vocabulary value, a manifest that
// is not YAML, and a `gates` of any other shape all yield no violation here.
//
// hostReviews is the host's declared Capabilities().ReviewsWrites, supplied by
// the caller; this function never learns a host's name. The three callers
// (spec 04 §T1.3) run it where ValidateManifest runs, on a manifest-changing
// write, and never from ValidateManifestPolicy: `validate --ci` passes true, the
// local land check passes false, and `space set` / `space init --local` pass the
// capability of the host they are about to write to.
// CheckGatesForHost is part of the public package API.
func CheckGatesForHost(raw []byte, hostReviews bool) []Violation {
	if !hostReviews {
		return nil
	}
	gates, _, ok := manifestPolicyKeys(raw)
	if !ok {
		return nil
	}
	setting, legal := contract.ParseGatesSetting(gates)
	if !legal || setting != contract.GatesSettingAutonomous {
		return nil
	}
	return []Violation{{
		Code:     "POL-028",
		Class:    ClassPolicy,
		Path:     "gates",
		Message:  "gates: autonomous is refused on a host that reviews writes; its pull-request review is the gate there",
		Severity: SeverityReject,
	}}
}

// CheckGateApproval is the land check's approval rule (spec 04 §T1.3): it
// returns one POL-029 per required gate that the approved set lacks when mode is
// contract.GatesModeConfirm, and nothing in any other mode.
//
// mode is the EFFECTIVE mode (contract.EffectiveGatesMode) read from the TIP
// manifest the write lands on, never from the candidate's own, so a write that
// flips `confirm` to `autonomous` is judged under `confirm`. required is what
// the caller recomputed against the tip: G1 and G2 through
// contract.PublicationGateFor, G3 through fold.HumanGate, G4 when the write
// changes space.yaml. approved is the gate ids the write carries in its
// A2A-Approved-Gates trailer. An approved gate that was not required is
// ignored, and an approval narrower than the requirement (a retry whose G1
// became a G2 after a rival published first) leaves the missing gate named.
//
// Each violation names its gate in Subjects, in gate order, one per gate however
// often required repeats it. G5 is not a gate this function can be given: it is
// never approvable.
// CheckGateApproval is part of the public package API.
func CheckGateApproval(mode contract.GatesMode, required, approved []string) []Violation {
	if mode != contract.GatesModeConfirm {
		return nil
	}
	missing := make([]string, 0, len(required))
	for _, gate := range required {
		if !slices.Contains(approved, gate) && !slices.Contains(missing, gate) {
			missing = append(missing, gate)
		}
	}
	slices.Sort(missing)
	var violations []Violation
	for _, gate := range missing {
		violations = append(violations, Violation{
			Code:     "POL-029",
			Class:    ClassPolicy,
			Path:     "",
			Message:  gate + " requires --approve in a confirm space",
			Severity: SeverityReject,
			Subjects: []string{gate},
		})
	}
	return violations
}
