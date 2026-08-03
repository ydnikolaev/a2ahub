package contract

import (
	"maps"
	"strings"
	"testing"
)

func TestValidateCarriedSetDelegatesToCanonicalBuilder(t *testing.T) {
	t.Parallel()

	descriptor := validDescriptor()
	snapshot := CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: []byte("final descriptor bytes\n")},
		Files:      validCandidates(),
	}
	want, wantIssues := BuildCarriedSet(ProfileContractSetV2, snapshot.Descriptor.Raw, descriptor, snapshot.Files)
	assertNoIssues(t, wantIssues)

	got, gotIssues := ValidateCarriedSet(ProfileContractSetV2, descriptor, snapshot)
	assertNoIssues(t, gotIssues)
	if got.AggregateDigest != want.AggregateDigest || !maps.Equal(got.PerFileDigest, want.PerFileDigest) {
		t.Fatalf("snapshot validator diverged from canonical builder: got %s, want %s", got.AggregateDigest, want.AggregateDigest)
	}
}

func TestValidateCarriedSetRefusesUnsafeDescriptorLeaf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind CandidateKind
	}{
		{name: "symlink", kind: CandidateSymlink},
		{name: "submodule", kind: CandidateSubmodule},
		{name: "other", kind: CandidateOther},
		{name: "unspecified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set, issues := ValidateCarriedSet(ProfileContractSetV2, validDescriptor(), CandidateSnapshot{
				Descriptor: CandidateFile{Path: DescriptorPath, Kind: tc.kind, Raw: []byte("descriptor")},
				Files:      validCandidates(),
			})
			if set.AggregateDigest != "" || len(set.Bytes) != 0 {
				t.Fatal("unsafe descriptor leaf produced a carried set")
			}
			assertIssue(t, issues, IssueUnsafeMode)
		})
	}
}

func TestValidateCarriedSetRequiresExactSafeDescriptorPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want IssueKind
	}{
		{name: "case alias", path: "CONTRACT.md", want: IssueDescriptorAlias},
		{name: "different clean leaf", path: "artifacts/contract.md", want: IssueDescriptorAlias},
		{name: "absolute", path: "/private/contract.md", want: IssueInvalidPath},
		{name: "traversal", path: "../contract.md", want: IssueInvalidPath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set, issues := ValidateCarriedSet(ProfileContractSetV2, validDescriptor(), CandidateSnapshot{
				Descriptor: CandidateFile{Path: tc.path, Kind: CandidateRegular, Raw: []byte("descriptor")},
				Files:      validCandidates(),
			})
			if set.AggregateDigest != "" {
				t.Fatal("wrong descriptor path produced a digest")
			}
			assertIssue(t, issues, tc.want)
			for _, issue := range issues {
				if strings.Contains(issue.Error(), "/private/") {
					t.Fatalf("unsafe descriptor path leaked into diagnostic: %v", issue)
				}
			}
		})
	}
}

func TestValidateCarriedSetPreservesExactnessAndTextRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*CandidateSnapshot)
		wantKind IssueKind
	}{
		{
			name: "missing declared file",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Files = snapshot.Files[:len(snapshot.Files)-1]
			},
			wantKind: IssueMissingFile,
		},
		{
			name: "undeclared discovered file",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Files = append(snapshot.Files, regular("artifacts/extra.txt", "extra"))
			},
			wantKind: IssueUndeclaredFile,
		},
		{
			name: "case collision",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Files = append(snapshot.Files, regular("schema/ORDER.schema.json", "{}"))
			},
			wantKind: IssueCaseCollision,
		},
		{
			name: "binary descriptor",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Descriptor.Raw = []byte{'a', 0, 'b'}
			},
			wantKind: IssueInvalidText,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snapshot := CandidateSnapshot{
				Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: []byte("descriptor")},
				Files:      validCandidates(),
			}
			tc.mutate(&snapshot)
			set, issues := ValidateCarriedSet(ProfileContractSetV2, validDescriptor(), snapshot)
			if set.AggregateDigest != "" {
				t.Fatal("invalid snapshot produced a digest")
			}
			assertIssue(t, issues, tc.wantKind)
		})
	}
}

func TestValidateCarriedSetLegacyIgnoresExcludedDescriptor(t *testing.T) {
	t.Parallel()

	files := []CandidateFile{
		regular("schema/a.json", `{}`),
		regular("fixtures/valid/a.json", `{}`),
	}
	want, wantIssues := BuildCarriedSet(ProfileContractTreeV1, nil, Descriptor{}, files)
	assertNoIssues(t, wantIssues)

	got, issues := ValidateCarriedSet(ProfileContractTreeV1, Descriptor{}, CandidateSnapshot{
		Descriptor: CandidateFile{Path: "/ignored", Kind: CandidateSymlink, Raw: []byte{0}},
		Files:      files,
	})
	assertNoIssues(t, issues)
	if got.AggregateDigest != want.AggregateDigest {
		t.Fatalf("legacy digest = %s, want %s", got.AggregateDigest, want.AggregateDigest)
	}
}
