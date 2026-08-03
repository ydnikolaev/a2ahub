package contract

import "regexp"

var (
	operationKeyPattern    = regexp.MustCompile(`^op-v1-[a-f0-9]{64}$`)
	sha256DigestPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	versionSelectorPattern = regexp.MustCompile(`^(?:auto:(?:patch|minor|major)|explicit:(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$`)
)

// ResolveDigestProfile preserves immutable event/profile interpretation.
// Event v1 has no profile field and always means contract-tree-v1. Event v2
// requires one of the registered profile names. Authoritative-floor policy
// deciding whether a new first-party v2 publish must use contract-set-v2 is
// intentionally owned by internal/validate.
func ResolveDigestProfile(eventSchema string, declared string) (DigestProfile, []Issue) {
	switch eventSchema {
	case "event/v1":
		if declared != "" {
			return "", []Issue{{Kind: IssueProfileForbidden, Detail: "event/v1 cannot declare digest_profile"}}
		}
		return ProfileContractTreeV1, nil
	case "event/v2":
		if declared == "" {
			return "", []Issue{{Kind: IssueProfileRequired, Detail: "event/v2 contract publish requires digest_profile"}}
		}
		profile := DigestProfile(declared)
		if profile != ProfileContractTreeV1 && profile != ProfileContractSetV2 {
			return "", []Issue{{Kind: IssueUnsupportedProfile, Detail: "event/v2 digest_profile is not registered"}}
		}
		return profile, nil
	default:
		return "", []Issue{{Kind: IssueInvalidEventSchema, Detail: "unsupported event schema family/version"}}
	}
}

// ValidatePublicationIntent validates only P5's closed wire values. P6 owns
// canonical key derivation and cross-checks against candidate bytes, subject,
// selector, and resolved published version.
func ValidatePublicationIntent(intent PublicationIntent) []Issue {
	issues := make([]Issue, 0, 4)
	if !operationKeyPattern.MatchString(intent.IntentKey) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "publication.intent_key", Detail: "must be op-v1- followed by 64 lowercase hexadecimal characters"})
	}
	if !sha256DigestPattern.MatchString(intent.CandidateIntentDigest) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "publication.candidate_intent_digest", Detail: "must be a full lowercase sha256 digest"})
	}
	if !versionSelectorPattern.MatchString(intent.VersionSelector) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "publication.version_selector", Detail: "must be auto:patch|minor|major or explicit:<canonical-semver>"})
	}
	if !operationKeyPattern.MatchString(intent.OperationKey) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "publication.operation_key", Detail: "must be op-v1- followed by 64 lowercase hexadecimal characters"})
	}
	sortIssues(issues)
	return issues
}
