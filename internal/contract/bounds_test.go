package contract

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestPortablePathAdversarialCorpus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path  string
		valid bool
	}{
		{path: "schema/order.schema.json", valid: true},
		{path: "artifacts/errors_v2-1.yaml", valid: true},
		{path: ""},
		{path: "."},
		{path: "/schema/a"},
		{path: "./schema/a"},
		{path: "schema//a"},
		{path: "schema/a/.."},
		{path: "schema/../a"},
		{path: "../schema/a"},
		{path: `schema\a`},
		{path: "C:/schema/a"},
		{path: "c:relative"},
		{path: "schema/a\x00b"},
		{path: "schema/café.json"},
		{path: "schema/a b.json"},
		{path: "schema/a%20b.json"},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.path), func(t *testing.T) {
			t.Parallel()
			if got := cleanPortablePath(tc.path); got != tc.valid {
				t.Fatalf("cleanPortablePath(%q) = %v, want %v", tc.path, got, tc.valid)
			}
		})
	}
}

func TestRejectedPathDiagnosticsDoNotLeakAbsoluteInput(t *testing.T) {
	t.Parallel()
	const absolute = "/Users/operator/private-space/schema/secret.json"
	_, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), validDescriptor(), append(validCandidates(), regular(absolute, "secret")))
	assertIssue(t, issues, IssueInvalidPath)
	for _, issue := range issues {
		if strings.Contains(issue.Path, "/Users/") || strings.Contains(issue.Error(), "/Users/") {
			t.Fatalf("unsafe rejected path leaked into diagnostic: %v", issue)
		}
	}
}

func TestCandidateModeAndTextFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		kind CandidateKind
		raw  []byte
		want IssueKind
	}{
		{name: "symlink", kind: CandidateSymlink, raw: []byte("target"), want: IssueUnsafeMode},
		{name: "submodule", kind: CandidateSubmodule, raw: []byte("commit"), want: IssueUnsafeMode},
		{name: "other mode", kind: CandidateOther, raw: []byte("data"), want: IssueUnsafeMode},
		{name: "unspecified mode", raw: []byte("data"), want: IssueUnsafeMode},
		{name: "invalid UTF-8", kind: CandidateRegular, raw: []byte{0xff}, want: IssueInvalidText},
		{name: "NUL", kind: CandidateRegular, raw: []byte{'a', 0, 'b'}, want: IssueInvalidText},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candidates := validCandidates()
			candidates[0].Kind = tc.kind
			candidates[0].Raw = tc.raw
			set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), validDescriptor(), candidates)
			if set.AggregateDigest != "" {
				t.Fatal("invalid candidate produced digest")
			}
			assertIssue(t, issues, tc.want)
		})
	}
}

func TestPerFileSizeBoundaries(t *testing.T) {
	t.Parallel()
	for _, size := range []int{MaxFileBytes - 1, MaxFileBytes, MaxFileBytes + 1} {
		t.Run(fmt.Sprintf("candidate-%d", size), func(t *testing.T) {
			t.Parallel()
			candidates := validCandidates()
			candidates[0].Raw = bytes.Repeat([]byte{'x'}, size)
			set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), validDescriptor(), candidates)
			if size <= MaxFileBytes {
				assertNoIssues(t, issues)
				if set.AggregateDigest == "" {
					t.Fatal("valid boundary produced no digest")
				}
				return
			}
			assertIssue(t, issues, IssueFileTooLarge)
		})
	}

	for _, size := range []int{MaxFileBytes - 1, MaxFileBytes, MaxFileBytes + 1} {
		t.Run(fmt.Sprintf("descriptor-%d", size), func(t *testing.T) {
			t.Parallel()
			raw := bytes.Repeat([]byte{'d'}, size)
			_, issues := BuildCarriedSet(ProfileContractSetV2, raw, validDescriptor(), validCandidates())
			if size <= MaxFileBytes {
				assertNoIssues(t, issues)
				return
			}
			assertIssue(t, issues, IssueFileTooLarge)
		})
	}
}

func TestExplicitCountBoundaries(t *testing.T) {
	t.Parallel()
	for _, count := range []int{MaxExplicitFiles - 1, MaxExplicitFiles, MaxExplicitFiles + 1} {
		t.Run(fmt.Sprintf("count-%d", count), func(t *testing.T) {
			t.Parallel()
			descriptor, candidates := descriptorWithCount(count)
			set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), descriptor, candidates)
			if count <= MaxExplicitFiles {
				assertNoIssues(t, issues)
				if len(set.Entries) != count {
					t.Fatalf("entries = %d, want %d", len(set.Entries), count)
				}
				return
			}
			assertIssue(t, issues, IssueTooManyEntries)
		})
	}

	_, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), Descriptor{SchemaFormat: "json-schema-2020-12"}, nil)
	assertIssue(t, issues, IssueTooFewEntries)
	assertIssue(t, issues, IssueRequiredRoleAbsent)
}

func TestLegacyCountBoundRefusesOversizedHistoricalTree(t *testing.T) {
	t.Parallel()

	candidates := make([]CandidateFile, 0, MaxExplicitFiles+1)
	for i := 0; i <= MaxExplicitFiles; i++ {
		candidates = append(candidates, CandidateFile{
			Path: fmt.Sprintf("schema/legacy-%03d.json", i), Kind: CandidateRegular, Raw: []byte("{}"),
		})
	}
	set, issues := BuildCarriedSet(ProfileContractTreeV1, nil, Descriptor{}, candidates)
	if set.AggregateDigest != "" {
		t.Fatal("oversized legacy tree produced a digest")
	}
	assertIssue(t, issues, IssueTooManyEntries)
}

func TestAggregateSizeBoundaries(t *testing.T) {
	t.Parallel()
	lengthsAtN := make([]int, 16)
	for i := 0; i < 15; i++ {
		lengthsAtN[i] = MaxFileBytes
	}
	lengthsAtN[15] = MaxFileBytes - 1 // plus one descriptor byte = exactly 16 MiB

	lengthsBelow := append([]int(nil), lengthsAtN...)
	lengthsBelow[15]--
	lengthsAbove := append([]int(nil), lengthsAtN...)
	lengthsAbove[15]++

	tests := []struct {
		name    string
		lengths []int
		wantErr bool
	}{
		{name: "N-1", lengths: lengthsBelow},
		{name: "N", lengths: lengthsAtN},
		{name: "N+1", lengths: lengthsAbove, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			descriptor, candidates := descriptorWithLengths(tc.lengths)
			set, issues := BuildCarriedSet(ProfileContractSetV2, []byte{'d'}, descriptor, candidates)
			if tc.wantErr {
				assertIssue(t, issues, IssueAggregateTooLarge)
				if set.AggregateDigest != "" {
					t.Fatal("oversized aggregate produced digest")
				}
				return
			}
			assertNoIssues(t, issues)
			if set.AggregateDigest == "" {
				t.Fatal("valid aggregate boundary produced no digest")
			}
		})
	}
}

func descriptorWithCount(count int) (Descriptor, []CandidateFile) {
	entries := make([]Entry, 0, count)
	candidates := make([]CandidateFile, 0, count)
	for i := 0; i < count; i++ {
		entry, candidate := boundaryEntry(i, 1)
		entries = append(entries, entry)
		candidates = append(candidates, candidate)
	}
	return Descriptor{SchemaFormat: "json-schema-2020-12", Artifacts: entries}, candidates
}

func descriptorWithLengths(lengths []int) (Descriptor, []CandidateFile) {
	entries := make([]Entry, 0, len(lengths))
	candidates := make([]CandidateFile, 0, len(lengths))
	for i, length := range lengths {
		entry, candidate := boundaryEntry(i, length)
		entries = append(entries, entry)
		candidates = append(candidates, candidate)
	}
	return Descriptor{SchemaFormat: "json-schema-2020-12", Artifacts: entries}, candidates
}

func boundaryEntry(index, size int) (Entry, CandidateFile) {
	switch index {
	case 0:
		path := "schema/main.json"
		return Entry{Path: path, Role: RoleSchema, Normative: true, MediaType: "application/json"}, CandidateFile{Path: path, Kind: CandidateRegular, Raw: bytes.Repeat([]byte{'x'}, size)}
	case 1:
		path := "fixtures/valid/main.json"
		return Entry{Path: path, Role: RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/main.json"}, CandidateFile{Path: path, Kind: CandidateRegular, Raw: bytes.Repeat([]byte{'x'}, size)}
	case 2:
		path := "fixtures/invalid/main.json"
		return Entry{Path: path, Role: RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/main.json"}, CandidateFile{Path: path, Kind: CandidateRegular, Raw: bytes.Repeat([]byte{'x'}, size)}
	default:
		path := fmt.Sprintf("artifacts/example-%03d.txt", index)
		return Entry{Path: path, Role: RoleExample, Normative: false, MediaType: "text/plain"}, CandidateFile{Path: path, Kind: CandidateRegular, Raw: bytes.Repeat([]byte{'x'}, size)}
	}
}
