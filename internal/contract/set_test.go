package contract

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

func TestBuildCarriedSetV2ExactAndDeterministic(t *testing.T) {
	t.Parallel()
	descriptor := validDescriptor()
	raw := []byte("final descriptor bytes\n")
	candidates := validCandidates()

	set, issues := BuildCarriedSet(ProfileContractSetV2, raw, descriptor, candidates)
	assertNoIssues(t, issues)
	if set.Profile != ProfileContractSetV2 {
		t.Fatalf("Profile = %q", set.Profile)
	}
	wantPaths := []string{
		"artifacts/changelog.md",
		"fixtures/invalid/missing-id.json",
		"fixtures/valid/order.json",
		"schema/order.schema.json",
	}
	gotPaths := make([]string, len(set.Entries))
	for i := range set.Entries {
		gotPaths[i] = set.Entries[i].Path
	}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("sorted paths = %v, want %v", gotPaths, wantPaths)
	}
	if _, ok := set.PerFileDigest[DescriptorPath]; !ok {
		t.Fatal("contract.md missing from v2 digest")
	}
	if len(set.PerFileDigest) != len(candidates)+1 {
		t.Fatalf("per-file count = %d", len(set.PerFileDigest))
	}
	wantDigest := artifact.CombineDigestPairs(set.PerFileDigest)
	if set.AggregateDigest != wantDigest {
		t.Fatalf("digest = %s, want %s", set.AggregateDigest, wantDigest)
	}

	reversed := slices.Clone(candidates)
	slices.Reverse(reversed)
	reorderedDescriptor := descriptor
	reorderedDescriptor.Artifacts = slices.Clone(descriptor.Artifacts)
	slices.Reverse(reorderedDescriptor.Artifacts)
	reordered, reorderedIssues := BuildCarriedSet(ProfileContractSetV2, raw, reorderedDescriptor, reversed)
	assertNoIssues(t, reorderedIssues)
	if reordered.AggregateDigest != set.AggregateDigest || !maps.Equal(reordered.PerFileDigest, set.PerFileDigest) {
		t.Fatal("candidate or decoded-inventory enumeration order changed digest")
	}
}

func TestDescriptorRawOrderAndEveryCarriedMutationAffectV2Digest(t *testing.T) {
	t.Parallel()
	descriptor := validDescriptor()
	candidates := validCandidates()
	rawA := []byte("artifacts: [schema, valid, invalid, changelog]\n")
	rawB := []byte("artifacts: [changelog, invalid, valid, schema]\n")

	setA, issues := BuildCarriedSet(ProfileContractSetV2, rawA, descriptor, candidates)
	assertNoIssues(t, issues)
	setB, issues := BuildCarriedSet(ProfileContractSetV2, rawB, descriptor, candidates)
	assertNoIssues(t, issues)
	if setA.PerFileDigest[DescriptorPath] == setB.PerFileDigest[DescriptorPath] || setA.AggregateDigest == setB.AggregateDigest {
		t.Fatal("descriptor-only byte change did not change v2 integrity")
	}

	for i := range candidates {
		mutated := cloneCandidates(candidates)
		mutated[i].Raw = append(mutated[i].Raw, 'x')
		got, gotIssues := BuildCarriedSet(ProfileContractSetV2, rawA, descriptor, mutated)
		assertNoIssues(t, gotIssues)
		if got.AggregateDigest == setA.AggregateDigest {
			t.Fatalf("mutation of %s did not change aggregate", candidates[i].Path)
		}
	}

	normativeChanged := descriptor
	normativeChanged.Artifacts = slices.Clone(descriptor.Artifacts)
	normativeChanged.Artifacts[3].Normative = true
	setC, issues := BuildCarriedSet(ProfileContractSetV2, append(rawA, []byte("normative: true\n")...), normativeChanged, candidates)
	assertNoIssues(t, issues)
	if setC.AggregateDigest == setA.AggregateDigest {
		t.Fatal("normative-bit descriptor change did not change aggregate")
	}
}

func TestV2ExactSetFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		descriptor Descriptor
		candidates []CandidateFile
		want       IssueKind
	}{
		{name: "missing", descriptor: validDescriptor(), candidates: validCandidates()[:3], want: IssueMissingFile},
		{name: "undeclared", descriptor: validDescriptor(), candidates: append(validCandidates(), regular("artifacts/extra.txt", "extra")), want: IssueUndeclaredFile},
		{name: "duplicate declaration", descriptor: withExtraEntry(validDescriptor(), validDescriptor().Artifacts[0]), candidates: validCandidates(), want: IssueDuplicatePath},
		{name: "duplicate candidate", descriptor: validDescriptor(), candidates: append(validCandidates(), validCandidates()[0]), want: IssueDuplicatePath},
		{name: "declared case collision", descriptor: withExtraEntry(validDescriptor(), Entry{Path: "schema/ORDER.schema.json", Role: RoleSchema, Normative: true, MediaType: "application/json"}), candidates: validCandidates(), want: IssueCaseCollision},
		{name: "candidate case collision", descriptor: validDescriptor(), candidates: append(validCandidates(), regular("schema/ORDER.schema.json", "{}")), want: IssueCaseCollision},
		{name: "descriptor explicit alias", descriptor: withExtraEntry(validDescriptor(), Entry{Path: "contract.md", Role: RoleOther, Normative: true, MediaType: "text/plain"}), candidates: validCandidates(), want: IssueDescriptorAlias},
		{name: "descriptor candidate alias", descriptor: validDescriptor(), candidates: append(validCandidates(), regular("CONTRACT.md", "alias")), want: IssueDescriptorAlias},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), tc.descriptor, tc.candidates)
			if set.AggregateDigest != "" || len(set.PerFileDigest) != 0 {
				t.Fatal("invalid input produced a digest")
			}
			assertIssue(t, issues, tc.want)
		})
	}
}

func TestIssueProjectionIsPermutationInvariant(t *testing.T) {
	t.Parallel()
	candidates := append(validCandidates(),
		regular("artifacts/extra.txt", "extra"),
		CandidateFile{Path: "schema/link.json", Kind: CandidateSymlink, Raw: []byte("target")},
		regular("schema/ORDER.schema.json", "{}"),
	)
	_, forward := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), validDescriptor(), candidates)
	reversed := slices.Clone(candidates)
	slices.Reverse(reversed)
	_, backward := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), validDescriptor(), reversed)
	if !slices.Equal(forward, backward) {
		t.Fatalf("issue order/content changed with enumeration:\nforward=%+v\nbackward=%+v", forward, backward)
	}
}

func TestRoleRootConformsAndMediaMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		entry Entry
		want  IssueKind
	}{
		{name: "unknown role", entry: Entry{Path: "artifacts/x", Role: "extension", Normative: true, MediaType: "text/plain"}, want: IssueUnknownRole},
		{name: "schema root", entry: Entry{Path: "artifacts/x.json", Role: RoleSchema, Normative: true, MediaType: "application/json"}, want: IssueWrongRoot},
		{name: "valid root", entry: Entry{Path: "fixtures/x.json", Role: RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/order.schema.json"}, want: IssueWrongRoot},
		{name: "artifact root", entry: Entry{Path: "schema/errors.yaml", Role: RoleErrors, Normative: true, MediaType: "application/yaml"}, want: IssueWrongRoot},
		{name: "fixture target required", entry: Entry{Path: "fixtures/valid/x.json", Role: RoleValidFixture, Normative: true, MediaType: "application/json"}, want: IssueConformsRequired},
		{name: "nonfixture target forbidden", entry: Entry{Path: "artifacts/x", Role: RoleOther, Normative: true, MediaType: "text/plain", ConformsTo: "schema/order.schema.json"}, want: IssueConformsForbidden},
		{name: "uppercase media", entry: Entry{Path: "artifacts/x", Role: RoleOther, Normative: true, MediaType: "Text/Plain"}, want: IssueInvalidMediaType},
		{name: "parameter media", entry: Entry{Path: "artifacts/x", Role: RoleOther, Normative: true, MediaType: "text/plain; charset=utf-8"}, want: IssueInvalidMediaType},
		{name: "json schema media", entry: Entry{Path: "schema/x.yaml", Role: RoleSchema, Normative: true, MediaType: "application/yaml"}, want: IssueInvalidMediaType},
		{name: "json fixture media", entry: Entry{Path: "fixtures/valid/x.yaml", Role: RoleValidFixture, Normative: true, MediaType: "application/yaml", ConformsTo: "schema/order.schema.json"}, want: IssueInvalidMediaType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issues := validateEntryShape("json-schema-2020-12", tc.entry)
			assertIssue(t, issues, tc.want)
		})
	}

	unresolved := validDescriptor()
	unresolved.Artifacts[1].ConformsTo = "schema/missing.json"
	_, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), unresolved, validCandidates())
	assertIssue(t, issues, IssueConformsUnresolved)

	otherFormat := validDescriptor()
	otherFormat.SchemaFormat = "openapi-3.x"
	otherFormat.Artifacts[0].MediaType = "application/yaml"
	set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), otherFormat, validCandidates())
	assertNoIssues(t, issues)
	if set.AggregateDigest == "" {
		t.Fatal("non-JSON schema media should remain carried")
	}
}

func TestEveryClosedRoleIsAccepted(t *testing.T) {
	t.Parallel()
	descriptor := validDescriptor()
	candidates := validCandidates()
	artifactRoles := []Role{RoleErrors, RoleVocabulary, RoleLimits, RoleExample, RoleOther}
	for _, role := range artifactRoles {
		path := "artifacts/" + string(role) + ".txt"
		descriptor.Artifacts = append(descriptor.Artifacts, Entry{Path: path, Role: role, Normative: true, MediaType: "text/plain"})
		candidates = append(candidates, regular(path, string(role)))
	}
	set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), descriptor, candidates)
	assertNoIssues(t, issues)
	if len(set.Entries) != len(descriptor.Artifacts) {
		t.Fatalf("entries = %d, want %d", len(set.Entries), len(descriptor.Artifacts))
	}
}

func TestExportSourceAndAdvisorySeparation(t *testing.T) {
	t.Parallel()
	set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), validDescriptor(), validCandidates())
	assertNoIssues(t, issues)

	export, err := set.ExportSource()
	if err != nil {
		t.Fatal(err)
	}
	if export.Profile != ProfileExportSourceV1 {
		t.Fatalf("profile = %q", export.Profile)
	}
	if export.Digest == set.AggregateDigest {
		t.Fatal("export provenance digest equals descriptor-including publication digest")
	}
	if _, ok := export.PerFileDigest[DescriptorPath]; ok {
		t.Fatal("descriptor leaked into export-source-v1")
	}
	if _, ok := export.PerFileDigest["artifacts/changelog.md"]; ok {
		t.Fatal("artifact companion leaked into export-source-v1")
	}
	if len(export.PerFileDigest) != 3 {
		t.Fatalf("export per-file count = %d", len(export.PerFileDigest))
	}

	withAssertion := validDescriptor()
	withAssertion.GeneratedFrom = &GeneratedFrom{Tool: "exporter", SourceDigest: export.Digest}
	asserted, assertedIssues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor with assertion"), withAssertion, validCandidates())
	assertNoIssues(t, assertedIssues)
	assertNoIssues(t, asserted.VerifyGeneratedSource())
	asserted.Descriptor.GeneratedFrom.SourceDigest = artifact.Digest([]byte("wrong"))
	assertIssue(t, asserted.VerifyGeneratedSource(), IssueDigestMismatch)

	compatibility := set.CompatibilityEntries()
	if len(compatibility) != 3 {
		t.Fatalf("compatibility entries = %d, want 3", len(compatibility))
	}
	if _, ok := set.PerFileDigest["artifacts/changelog.md"]; !ok {
		t.Fatal("advisory companion missing from integrity")
	}
	if _, ok := set.Bytes["artifacts/changelog.md"]; !ok {
		t.Fatal("advisory companion missing from materializable bytes")
	}
}

func TestBuildClonesCandidateAndDescriptorInputs(t *testing.T) {
	t.Parallel()
	descriptor := validDescriptor()
	candidates := validCandidates()
	raw := []byte("descriptor")
	set, issues := BuildCarriedSet(ProfileContractSetV2, raw, descriptor, candidates)
	assertNoIssues(t, issues)
	wantDigest := set.AggregateDigest
	wantSchema := string(set.Bytes["schema/order.schema.json"])

	raw[0] = 'X'
	candidates[0].Raw[0] = 'X'
	descriptor.Artifacts[0].Path = "schema/mutated.json"
	if set.AggregateDigest != wantDigest || string(set.Bytes["schema/order.schema.json"]) != wantSchema || set.Descriptor.Artifacts[0].Path == descriptor.Artifacts[0].Path {
		t.Fatal("carried set retained mutable caller storage")
	}
}

func TestVerifyDigestRequiresFullLowercaseSHA256(t *testing.T) {
	t.Parallel()

	set, issues := BuildCarriedSet(ProfileContractSetV2, []byte("descriptor"), validDescriptor(), validCandidates())
	assertNoIssues(t, issues)
	assertNoIssues(t, set.VerifyDigest(set.AggregateDigest))

	for _, invalid := range []string{
		"",
		"sha256:",
		"sha256:ABCDEF",
		"md5:" + strings.Repeat("0", 64),
		"sha256:" + strings.Repeat("0", 63),
	} {
		t.Run(invalid, func(t *testing.T) {
			t.Parallel()
			assertIssue(t, set.VerifyDigest(invalid), IssueDigestMismatch)
		})
	}
}

func TestLegacyProfileGoldenAndScope(t *testing.T) {
	t.Parallel()
	candidates := []CandidateFile{
		regular("contract.md", "descriptor is excluded"),
		regular("artifacts/errors.yaml", "excluded"),
		regular("fixtures/invalid/b.json", `{"valid":false}`),
		regular("schema/a.json", `{"type":"object"}`),
		regular("fixtures/valid/a.json", `{"valid":true}`),
	}
	set, issues := BuildCarriedSet(ProfileContractTreeV1, []byte("ignored"), Descriptor{}, candidates)
	assertNoIssues(t, issues)
	const wantLegacyDigest = "sha256:29a8912a749e7efc39e3c5ae304f6190c89fbbf1f7934f3ccc51ce5eb2041307"
	if set.AggregateDigest != wantLegacyDigest {
		t.Fatalf("legacy digest = %q, want %q", set.AggregateDigest, wantLegacyDigest)
	}
	if _, ok := set.PerFileDigest[DescriptorPath]; ok {
		t.Fatal("legacy digest includes descriptor")
	}
	if _, ok := set.PerFileDigest["artifacts/errors.yaml"]; ok {
		t.Fatal("legacy digest includes artifacts root")
	}

	reversed := slices.Clone(candidates)
	slices.Reverse(reversed)
	again, issues := BuildCarriedSet(ProfileContractTreeV1, nil, Descriptor{}, reversed)
	assertNoIssues(t, issues)
	if again.AggregateDigest != set.AggregateDigest {
		t.Fatal("legacy digest depends on traversal order")
	}
}

func validDescriptor() Descriptor {
	return Descriptor{
		SchemaFormat: "json-schema-2020-12",
		Artifacts: []Entry{
			{Path: "schema/order.schema.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"},
			{Path: "fixtures/valid/order.json", Role: RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/order.schema.json"},
			{Path: "fixtures/invalid/missing-id.json", Role: RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/order.schema.json"},
			{Path: "artifacts/changelog.md", Role: RoleChangelog, Normative: false, MediaType: "text/markdown"},
		},
	}
}

func validCandidates() []CandidateFile {
	return []CandidateFile{
		regular("schema/order.schema.json", `{"type":"object"}`),
		regular("fixtures/valid/order.json", `{"id":"1"}`),
		regular("fixtures/invalid/missing-id.json", `{}`),
		regular("artifacts/changelog.md", "Advisory change notes.\n"),
	}
}

func regular(path, raw string) CandidateFile {
	return CandidateFile{Path: path, Kind: CandidateRegular, Raw: []byte(raw)}
}

func cloneCandidates(candidates []CandidateFile) []CandidateFile {
	clone := make([]CandidateFile, len(candidates))
	for i, candidate := range candidates {
		clone[i] = candidate
		clone[i].Raw = append([]byte(nil), candidate.Raw...)
	}
	return clone
}

func withExtraEntry(descriptor Descriptor, entry Entry) Descriptor {
	descriptor.Artifacts = append(slices.Clone(descriptor.Artifacts), entry)
	return descriptor
}

func assertNoIssues(t *testing.T, issues []Issue) {
	t.Helper()
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func assertIssue(t *testing.T, issues []Issue, want IssueKind) {
	t.Helper()
	for _, issue := range issues {
		if issue.Kind == want {
			return
		}
	}
	t.Fatalf("issues %+v do not contain %q", issues, want)
}
