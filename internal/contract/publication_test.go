package contract

import (
	"strings"
	"testing"
)

func TestResolveDigestProfileHistoryMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		schema   string
		declared string
		want     DigestProfile
		issue    IssueKind
	}{
		{name: "v1 implicit legacy", schema: "event/v1", want: ProfileContractTreeV1},
		{name: "v1 field forbidden", schema: "event/v1", declared: string(ProfileContractTreeV1), issue: IssueProfileForbidden},
		{name: "v2 legacy registered", schema: "event/v2", declared: string(ProfileContractTreeV1), want: ProfileContractTreeV1},
		{name: "v2 declared set", schema: "event/v2", declared: string(ProfileContractSetV2), want: ProfileContractSetV2},
		{name: "v2 absent", schema: "event/v2", issue: IssueProfileRequired},
		{name: "v2 unknown", schema: "event/v2", declared: "current", issue: IssueUnsupportedProfile},
		{name: "unknown event", schema: "event/v3", issue: IssueInvalidEventSchema},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, issues := ResolveDigestProfile(tc.schema, tc.declared)
			if tc.issue != "" {
				assertIssue(t, issues, tc.issue)
				return
			}
			assertNoIssues(t, issues)
			if got != tc.want {
				t.Fatalf("profile = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPublicationIntentClosedValues(t *testing.T) {
	t.Parallel()
	hex := strings.Repeat("a", 64)
	valid := PublicationIntent{
		IntentKey:             "op-v1-" + hex,
		CandidateIntentDigest: "sha256:" + hex,
		VersionSelector:       "auto:minor",
		OperationKey:          "op-v1-" + hex,
	}
	assertNoIssues(t, ValidatePublicationIntent(valid))

	selectors := []string{"auto:patch", "auto:minor", "auto:major", "explicit:0.19.0", "explicit:12.0.3"}
	for _, selector := range selectors {
		candidate := valid
		candidate.VersionSelector = selector
		assertNoIssues(t, ValidatePublicationIntent(candidate))
	}

	invalid := []PublicationIntent{
		{},
		{IntentKey: "op-v1-short", CandidateIntentDigest: valid.CandidateIntentDigest, VersionSelector: valid.VersionSelector, OperationKey: valid.OperationKey},
		{IntentKey: valid.IntentKey, CandidateIntentDigest: "sha256:ABC", VersionSelector: valid.VersionSelector, OperationKey: valid.OperationKey},
		{IntentKey: valid.IntentKey, CandidateIntentDigest: valid.CandidateIntentDigest, VersionSelector: "auto:breaking", OperationKey: valid.OperationKey},
		{IntentKey: valid.IntentKey, CandidateIntentDigest: valid.CandidateIntentDigest, VersionSelector: "explicit:01.2.3", OperationKey: valid.OperationKey},
		{IntentKey: valid.IntentKey, CandidateIntentDigest: valid.CandidateIntentDigest, VersionSelector: valid.VersionSelector, OperationKey: "different"},
	}
	for i, candidate := range invalid {
		if issues := ValidatePublicationIntent(candidate); len(issues) == 0 {
			t.Fatalf("invalid case %d accepted", i)
		}
	}
}
