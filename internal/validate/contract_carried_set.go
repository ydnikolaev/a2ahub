package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// ContractCandidateMode states whether the supplied snapshot is a proposed
// write or immutable history. It is explicit because the current space floor
// governs new writes, while an old committed publication must retain the
// profile that was in force when it was written.
type ContractCandidateMode string

const (
	ContractCandidateProposed   ContractCandidateMode = "proposed"
	ContractCandidateHistorical ContractCandidateMode = "historical"
)

// ContractCarriedSetInput is the detached, pure V2/V3 boundary for one
// contract publication. The caller owns filesystem/Git collection and must
// supply exact candidate bytes and modes in Snapshot. In particular,
// ProducedBy is intentionally absent: producer attribution is authored by the
// producer and can never select a weaker profile than the authoritative space
// floor.
type ContractCarriedSetInput struct {
	InvocationPoint       InvocationPoint
	Mode                  ContractCandidateMode
	SpaceMinBinaryVersion string
	ContractID            string
	ContractSchema        string
	EventSchema           string
	DigestProfile         string
	ExpectedDigest        string
	Descriptor            contract.Descriptor
	Snapshot              contract.CandidateSnapshot
}

// ValidateContractCarriedSet applies one carried-set/profile policy at V2 and
// V3. It delegates all inventory, path, role, media, exactness, text and bound
// rules to contract.ValidateCarriedSet; this adapter only supplies rollout
// context, maps domain issues to registry-backed violations and runs the
// package's canonical secret scanner over the successfully resolved exact set.
//
// A non-empty CarriedSet is returned only with a valid Result. Invalid content
// is represented as violations. An error means the trusted invocation context
// itself is unreadable (currently an invalid authoritative space floor).
func ValidateContractCarriedSet(in ContractCarriedSetInput) (contract.CarriedSet, Result, error) {
	const op = "ValidateContractCarriedSet"

	contextViolations := validateContractContext(in)
	if in.Mode == ContractCandidateProposed {
		older, err := version.OlderThan(in.SpaceMinBinaryVersion, version.OperationalConfidenceFloor)
		if err != nil {
			return contract.CarriedSet{}, Result{}, &Error{Op: op, Input: in.SpaceMinBinaryVersion, Err: err}
		}
		if !older && strings.HasPrefix(in.ContractID, "XC-") {
			contextViolations = append(contextViolations, checkContractFloorProfile(in)...)
		}
	}

	profile, profileIssues := contract.ResolveDigestProfile(in.EventSchema, in.DigestProfile)
	contextViolations = append(contextViolations, mapContractIssues(in.ContractID, profileIssues)...)
	contextViolations = append(contextViolations, checkContractSchemaProfile(in.ContractSchema, in.EventSchema, profile)...)

	var set contract.CarriedSet
	var issues []contract.Issue
	if profile != "" {
		set, issues = contract.ValidateCarriedSet(profile, in.Descriptor, in.Snapshot)
		contextViolations = append(contextViolations, mapContractIssues(in.ContractID, issues)...)
	}
	if len(issues) == 0 && profile != "" {
		contextViolations = append(contextViolations, mapContractIssues(in.ContractID, set.VerifyGeneratedSource())...)
		contextViolations = append(contextViolations, mapContractIssues(in.ContractID, set.VerifyDigest(in.ExpectedDigest))...)
		contextViolations = append(contextViolations, scanCarriedSetSecrets(set)...)
	}

	result := newResult(in.InvocationPoint, in.ContractID, contextViolations)
	if !result.Valid {
		return contract.CarriedSet{}, result, nil
	}
	return set, result, nil
}

func validateContractContext(in ContractCarriedSetInput) []Violation {
	var violations []Violation
	if in.InvocationPoint != V2 && in.InvocationPoint != V3 {
		violations = append(violations, contractPolicyViolation(
			"invocation_point",
			fmt.Sprintf("contract carried-set validation requires V2 or V3 context, got %q", in.InvocationPoint),
		))
	}
	if in.Mode != ContractCandidateProposed && in.Mode != ContractCandidateHistorical {
		violations = append(violations, contractPolicyViolation(
			"mode",
			fmt.Sprintf("contract carried-set validation mode must be proposed or historical, got %q", in.Mode),
		))
	}
	return violations
}

func checkContractFloorProfile(in ContractCarriedSetInput) []Violation {
	var violations []Violation
	if in.ContractSchema != "envelope/v2" {
		violations = append(violations, contractPolicyViolation(
			"schema",
			fmt.Sprintf("new or changed first-party contract at authoritative floor %s must use envelope/v2", in.SpaceMinBinaryVersion),
		))
	}
	if in.EventSchema != "event/v2" {
		violations = append(violations, contractPolicyViolation(
			"event.schema",
			fmt.Sprintf("new or changed first-party contract at authoritative floor %s must publish with event/v2", in.SpaceMinBinaryVersion),
		))
	}
	if in.DigestProfile != string(contract.ProfileContractSetV2) {
		violations = append(violations, contractPolicyViolation(
			"event.digest_profile",
			fmt.Sprintf("new or changed first-party contract at authoritative floor %s must use %s", in.SpaceMinBinaryVersion, contract.ProfileContractSetV2),
		))
	}
	return violations
}

func checkContractSchemaProfile(contractSchema, eventSchema string, profile contract.DigestProfile) []Violation {
	switch contractSchema {
	case "envelope/v1":
		if profile == contract.ProfileContractSetV2 {
			return []Violation{contractPolicyViolation(
				"event.digest_profile",
				"envelope/v1 cannot be published with contract-set-v2",
			)}
		}
	case "envelope/v2":
		if eventSchema != "event/v2" || profile != contract.ProfileContractSetV2 {
			return []Violation{contractPolicyViolation(
				"event.digest_profile",
				"envelope/v2 requires event/v2 with contract-set-v2",
			)}
		}
	default:
		return []Violation{contractPolicyViolation(
			"schema",
			fmt.Sprintf("unsupported contract schema family/version %q", contractSchema),
		)}
	}
	return nil
}

func mapContractIssues(contractID string, issues []contract.Issue) []Violation {
	if len(issues) == 0 {
		return nil
	}

	violations := make([]Violation, 0, len(issues))
	missingRoles := make([]string, 0, 3)
	for _, issue := range issues {
		if issue.Kind == contract.IssueRequiredRoleAbsent {
			missingRoles = append(missingRoles, issue.Detail)
		}
	}
	for _, issue := range issues {
		if issue.Kind == contract.IssueRequiredRoleAbsent {
			continue
		}
		// An empty artifacts array is the same missing executable baseline
		// already owned by POL-009, not a second POL-013 diagnosis.
		if issue.Kind == contract.IssueTooFewEntries && len(missingRoles) != 0 {
			continue
		}
		violations = append(violations, contractIssueViolation(issue))
	}
	if len(missingRoles) != 0 {
		sort.Strings(missingRoles)
		violations = append(violations, Violation{
			Code:     "POL-009",
			Class:    ClassPolicy,
			Path:     "artifacts",
			Message:  fmt.Sprintf("contract %s lacks the required executable baseline: %s", contractID, strings.Join(missingRoles, "; ")),
			Severity: SeverityReject,
		})
	}
	return violations
}

func contractIssueViolation(issue contract.Issue) Violation {
	if contractIssueIsReferential(issue.Kind) {
		return Violation{
			Code:     "REF-014",
			Class:    ClassReferential,
			Path:     issue.Path,
			Message:  issue.Detail,
			Severity: SeverityReject,
		}
	}
	return contractPolicyViolation(issue.Path, issue.Detail)
}

func contractIssueIsReferential(kind contract.IssueKind) bool {
	switch kind {
	case contract.IssueDescriptorMismatch, contract.IssueMissingFile, contract.IssueUnsafeMode, contract.IssueDigestMismatch:
		return true
	default:
		return false
	}
}

func contractPolicyViolation(path, message string) Violation {
	return Violation{
		Code:     "POL-013",
		Class:    ClassPolicy,
		Path:     path,
		Message:  message,
		Severity: SeverityReject,
	}
}

func scanCarriedSetSecrets(set contract.CarriedSet) []Violation {
	paths := make([]string, 0, len(set.Bytes))
	for path := range set.Bytes {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var violations []Violation
	for _, path := range paths {
		for _, violation := range scanForSecrets(set.Bytes[path]) {
			violation.Path = path
			violations = append(violations, violation)
		}
	}
	return violations
}
