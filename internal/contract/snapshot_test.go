package contract

import (
	"maps"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

func TestValidateCarriedSetDelegatesToCanonicalBuilder(t *testing.T) {
	t.Parallel()

	descriptor := validDescriptor()
	snapshot := validSnapshot(t, descriptor)
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
			descriptor := validDescriptor()
			snapshot := validSnapshot(t, descriptor)
			snapshot.Descriptor.Kind = tc.kind
			set, issues := ValidateCarriedSet(ProfileContractSetV2, descriptor, snapshot)
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
			descriptor := validDescriptor()
			snapshot := validSnapshot(t, descriptor)
			snapshot.Descriptor.Path = tc.path
			set, issues := ValidateCarriedSet(ProfileContractSetV2, descriptor, snapshot)
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
			descriptor := validDescriptor()
			snapshot := validSnapshot(t, descriptor)
			tc.mutate(&snapshot)
			set, issues := ValidateCarriedSet(ProfileContractSetV2, descriptor, snapshot)
			if set.AggregateDigest != "" {
				t.Fatal("invalid snapshot produced a digest")
			}
			assertIssue(t, issues, tc.wantKind)
		})
	}
}

func TestValidateCarriedSetBindsRawDescriptorToTypedProjection(t *testing.T) {
	t.Parallel()

	t.Run("caller projection disagrees", func(t *testing.T) {
		t.Parallel()
		rawDescriptor := validDescriptor()
		snapshot := validSnapshot(t, rawDescriptor)
		callerDescriptor := cloneDescriptor(rawDescriptor)
		callerDescriptor.Artifacts[0].Path = "schema/different.schema.json"

		set, issues := ValidateCarriedSet(ProfileContractSetV2, callerDescriptor, snapshot)
		if set.AggregateDigest != "" {
			t.Fatal("descriptor projection mismatch produced a digest")
		}
		assertIssue(t, issues, IssueDescriptorMismatch)
	})

	t.Run("raw descriptor is not parseable", func(t *testing.T) {
		t.Parallel()
		descriptor := validDescriptor()
		snapshot := validSnapshot(t, descriptor)
		snapshot.Descriptor.Raw = []byte("not frontmatter")

		set, issues := ValidateCarriedSet(ProfileContractSetV2, descriptor, snapshot)
		if set.AggregateDigest != "" {
			t.Fatal("unparseable descriptor produced a digest")
		}
		assertIssue(t, issues, IssueDescriptorMismatch)
	})
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

func validSnapshot(t *testing.T, descriptor Descriptor) CandidateSnapshot {
	t.Helper()
	raw, err := yaml.Marshal(descriptor)
	if err != nil {
		t.Fatalf("marshal descriptor projection: %v", err)
	}
	return CandidateSnapshot{
		Descriptor: CandidateFile{
			Path: DescriptorPath,
			Kind: CandidateRegular,
			Raw:  artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: raw, Body: []byte("# Contract\n")}),
		},
		Files: validCandidates(),
	}
}
