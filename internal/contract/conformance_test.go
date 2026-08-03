package contract

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCheckConformancePayloadSelectsSchemaAndReturnsStableViolations(t *testing.T) {
	t.Parallel()

	set := conformanceV2Set(t, Descriptor{
		SchemaFormat: "json-schema-2020-12",
		Artifacts:    []Entry{{Path: "schema/order.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"}},
	}, map[string]string{"schema/order.json": `{"type":"object"}`})
	checker := &recordingInstanceChecker{result: func(input InstanceCheckInput) InstanceCheckResult {
		return InstanceCheckResult{Violations: []InstanceViolation{
			{InstancePointer: "/z", SchemaPointer: "/required", Message: "z missing"},
			{InstancePointer: "/a", SchemaPointer: "/type", Message: "wrong type"},
		}}
	}}
	input := validConformanceInput(set, ConformanceModePayload)
	input.PayloadPath = "payloads/order.json"
	input.Payload = []byte(`{"id":1}`)

	result := CheckConformance(input, checker)
	if !result.Supported || result.Passed || result.Outcome != ConformanceNonconformant ||
		result.SchemaScope != ConformanceSchemaScopeSelfContained || result.Mode != ConformanceModePayload ||
		result.Ref != input.ContractID+"@"+input.Version || result.Commit != input.Commit ||
		result.DigestProfile != set.Profile || result.SchemaFormat != input.SchemaFormat || len(result.Results) != 1 {
		t.Fatalf("result = %+v", result)
	}
	item := result.Results[0]
	if item.Path != input.PayloadPath || item.Schema != "schema/order.json" ||
		item.Expected != ConformanceExpectedConformant || item.Actual != ConformanceActualNonconformant {
		t.Fatalf("payload result = %+v", item)
	}
	wantPointers := []string{"/a", "/z"}
	gotPointers := []string{item.Violations[0].InstancePointer, item.Violations[1].InstancePointer}
	if !slices.Equal(gotPointers, wantPointers) || checker.calls != 1 || checker.inputs[0].SchemaPath != "schema/order.json" {
		t.Fatalf("violations/calls = %v / %d inputs=%+v", gotPointers, checker.calls, checker.inputs)
	}
	if result.Results == nil || item.Violations == nil {
		t.Fatal("result arrays must be non-nil")
	}
}

func TestCheckConformancePayloadRequiresUniqueOrExplicitDeclaredSchema(t *testing.T) {
	t.Parallel()

	descriptor := Descriptor{SchemaFormat: "json-schema-2020-12", Artifacts: []Entry{
		{Path: "schema/a.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"},
		{Path: "schema/b.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"},
	}}
	set := conformanceV2Set(t, descriptor, map[string]string{
		"schema/a.json": `{"$ref":"https://example.invalid/not-selected.json"}`,
		"schema/b.json": `{}`,
	})
	checker := &recordingInstanceChecker{result: func(InstanceCheckInput) InstanceCheckResult { return InstanceCheckResult{Passed: true} }}
	input := validConformanceInput(set, ConformanceModePayload)
	input.PayloadPath, input.Payload = "payload.json", []byte(`{}`)

	result := CheckConformance(input, checker)
	if result.Outcome != ConformanceInvalidInput || checker.calls != 0 {
		t.Fatalf("ambiguous schema = %+v calls=%d", result, checker.calls)
	}
	input.SchemaPath = "schema/b.json"
	result = CheckConformance(input, checker)
	if result.Outcome != ConformanceConformant || !result.Passed || checker.calls != 1 || checker.inputs[0].SchemaPath != input.SchemaPath {
		t.Fatalf("explicit schema = %+v calls=%d inputs=%+v", result, checker.calls, checker.inputs)
	}
	input.SchemaPath = "schema/not-declared.json"
	result = CheckConformance(input, checker)
	if result.Outcome != ConformanceInvalidInput || checker.calls != 1 {
		t.Fatalf("undeclared schema = %+v calls=%d", result, checker.calls)
	}
}

func TestCheckConformanceRejectsUnsupportedReferencesBeforeChecker(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		schema string
		want   ConformanceOutcome
		calls  int
	}{
		{name: "local defs", schema: `{"$defs":{"id":{"type":"string"}},"$ref":"#/$defs/id"}`, want: ConformanceConformant, calls: 1},
		{name: "relative ref", schema: `{"$ref":"other.json"}`, want: ConformanceUnsupportedReference},
		{name: "relative fragment", schema: `{"$ref":"other.json#/$defs/id"}`, want: ConformanceUnsupportedReference},
		{name: "file ref", schema: `{"$ref":"file:///etc/passwd"}`, want: ConformanceUnsupportedReference},
		{name: "network ref", schema: `{"$ref":"https://example.invalid/schema.json"}`, want: ConformanceUnsupportedReference},
		{name: "dynamic network ref", schema: `{"$dynamicRef":"https://example.invalid/schema.json#node"}`, want: ConformanceUnsupportedReference},
		{name: "cross document id", schema: `{"$id":"https://example.invalid/schema.json","type":"object"}`, want: ConformanceUnsupportedReference},
		{name: "relative id", schema: `{"$id":"schema.json","type":"object"}`, want: ConformanceUnsupportedReference},
		{name: "local anchor", schema: `{"$defs":{"node":{"$anchor":"node","type":"object"}},"$ref":"#node"}`, want: ConformanceConformant, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			set := conformanceV2Set(t, Descriptor{
				SchemaFormat: "json-schema-2020-12",
				Artifacts:    []Entry{{Path: "schema/main.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"}},
			}, map[string]string{"schema/main.json": test.schema})
			checker := &recordingInstanceChecker{result: func(InstanceCheckInput) InstanceCheckResult { return InstanceCheckResult{Passed: true} }}
			input := validConformanceInput(set, ConformanceModePayload)
			input.PayloadPath, input.Payload = "payload.json", []byte(`{}`)

			result := CheckConformance(input, checker)
			if result.Outcome != test.want || checker.calls != test.calls {
				t.Fatalf("result = %+v checker calls=%d, want outcome=%s calls=%d", result, checker.calls, test.want, test.calls)
			}
		})
	}
}

func TestCheckConformanceSuiteExecutesEveryV2FixtureIncludingAdvisory(t *testing.T) {
	t.Parallel()

	descriptor := Descriptor{SchemaFormat: "json-schema-2020-12", Artifacts: []Entry{
		{Path: "schema/a.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"},
		{Path: "schema/b.json", Role: RoleSchema, Normative: true, MediaType: "application/json"},
		{Path: "fixtures/valid/z.json", Role: RoleValidFixture, Normative: false, MediaType: "application/json", ConformsTo: "schema/b.json"},
		{Path: "fixtures/invalid/a.json", Role: RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/a.json"},
		{Path: "fixtures/valid/m.json", Role: RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/a.json"},
	}}
	set := conformanceV2Set(t, descriptor, map[string]string{
		"schema/a.json": `{}`, "schema/b.json": `{}`,
		"fixtures/valid/z.json": `{"z":1}`, "fixtures/invalid/a.json": `{"bad":true}`, "fixtures/valid/m.json": `{"m":1}`,
	})
	checker := &recordingInstanceChecker{result: func(input InstanceCheckInput) InstanceCheckResult {
		if strings.Contains(input.InstancePath, "/invalid/") {
			return InstanceCheckResult{Violations: []InstanceViolation{{InstancePointer: "/bad", SchemaPointer: "/false", Message: "forbidden"}}}
		}
		return InstanceCheckResult{Passed: true, Violations: []InstanceViolation{}}
	}}

	result := CheckConformance(validConformanceInput(set, ConformanceModeSuite), checker)
	if result.Outcome != ConformanceSuiteConsistent || !result.Passed || result.MappingSource != ConformanceMappingManifest ||
		checker.calls != 3 || result.TotalResults != 3 || len(result.Results) != 3 {
		t.Fatalf("suite = %+v calls=%d", result, checker.calls)
	}
	wantPaths := []string{"fixtures/invalid/a.json", "fixtures/valid/m.json", "fixtures/valid/z.json"}
	gotPaths := []string{result.Results[0].Path, result.Results[1].Path, result.Results[2].Path}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("result order = %v, want %v", gotPaths, wantPaths)
	}
	if result.Results[0].Expected != ConformanceExpectedNonconformant || result.Results[0].Actual != ConformanceActualNonconformant ||
		result.Results[2].Schema != "schema/b.json" {
		t.Fatalf("suite mappings = %+v", result.Results)
	}
}

func TestCheckConformanceSuiteFailsEveryContradictionAndEvaluationFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		schemaRaw  string
		validRaw   string
		invalidRaw string
		check      func(InstanceCheckInput) InstanceCheckResult
		wantActual ConformanceActual
	}{
		{name: "valid fixture rejected", validRaw: `{}`, invalidRaw: `{}`, check: func(input InstanceCheckInput) InstanceCheckResult {
			if strings.Contains(input.InstancePath, "/valid/") {
				return InstanceCheckResult{Violations: []InstanceViolation{{Message: "rejected"}}}
			}
			return InstanceCheckResult{Violations: []InstanceViolation{{Message: "expected invalid"}}}
		}, wantActual: ConformanceActualNonconformant},
		{name: "invalid fixture accepted", validRaw: `{}`, invalidRaw: `{}`, check: func(InstanceCheckInput) InstanceCheckResult {
			return InstanceCheckResult{Passed: true}
		}, wantActual: ConformanceActualConformant},
		{name: "fixture malformed", validRaw: `{`, invalidRaw: `{}`, check: func(input InstanceCheckInput) InstanceCheckResult {
			return InstanceCheckResult{Passed: true}
		}, wantActual: ConformanceActualInvalidJSON},
		{name: "schema compile unevaluated", validRaw: `{}`, invalidRaw: `{}`, check: func(InstanceCheckInput) InstanceCheckResult {
			return InstanceCheckResult{}
		}, wantActual: ConformanceActualUnevaluated},
		{name: "schema malformed", schemaRaw: `{`, validRaw: `{}`, invalidRaw: `{}`, check: func(InstanceCheckInput) InstanceCheckResult {
			return InstanceCheckResult{Passed: true}
		}, wantActual: ConformanceActualInvalidSchema},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			descriptor := Descriptor{SchemaFormat: "json-schema-2020-12", Artifacts: []Entry{
				{Path: "schema/main.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"},
				{Path: "fixtures/valid/ok.json", Role: RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/main.json"},
				{Path: "fixtures/invalid/bad.json", Role: RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/main.json"},
			}}
			schemaRaw := test.schemaRaw
			if schemaRaw == "" {
				schemaRaw = `{}`
			}
			set := conformanceV2Set(t, descriptor, map[string]string{
				"schema/main.json": schemaRaw, "fixtures/valid/ok.json": test.validRaw, "fixtures/invalid/bad.json": test.invalidRaw,
			})
			checker := &recordingInstanceChecker{result: test.check}
			result := CheckConformance(validConformanceInput(set, ConformanceModeSuite), checker)
			if result.Outcome != ConformanceSuiteInconsistent || result.Passed {
				t.Fatalf("suite = %+v", result)
			}
			found := false
			for _, item := range result.Results {
				if item.Actual == test.wantActual {
					found = true
				}
			}
			if !found {
				t.Fatalf("want actual %s in %+v", test.wantActual, result.Results)
			}
		})
	}
}

func TestCheckConformanceLegacyMappingAndUnsupportedFormats(t *testing.T) {
	t.Parallel()

	legacy, issues := BuildCarriedSet(ProfileContractTreeV1, nil, Descriptor{}, []CandidateFile{
		{Path: "schema/order.schema.json", Kind: CandidateRegular, Raw: []byte(`{}`)},
		{Path: "fixtures/valid/order.json", Kind: CandidateRegular, Raw: []byte(`{}`)},
		{Path: "fixtures/invalid/order.json", Kind: CandidateRegular, Raw: []byte(`{}`)},
	})
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	checker := &recordingInstanceChecker{result: func(input InstanceCheckInput) InstanceCheckResult {
		if strings.Contains(input.InstancePath, "/invalid/") {
			return InstanceCheckResult{Violations: []InstanceViolation{{Message: "expected"}}}
		}
		return InstanceCheckResult{Passed: true}
	}}
	input := validConformanceInput(legacy, ConformanceModeSuite)
	result := CheckConformance(input, checker)
	if result.Outcome != ConformanceSuiteConsistent || result.MappingSource != ConformanceMappingLegacy || checker.calls != 2 {
		t.Fatalf("legacy suite = %+v calls=%d", result, checker.calls)
	}
	for _, item := range result.Results {
		if item.Schema != "schema/order.schema.json" {
			t.Fatalf("legacy mapping = %+v", result.Results)
		}
	}

	for _, format := range []string{"openapi-3.x", "proto3", "other"} {
		unsupported := input
		unsupported.SchemaFormat = format
		before := checker.calls
		result := CheckConformance(unsupported, checker)
		if result.Outcome != ConformanceUnsupported || result.Supported || result.Passed || checker.calls != before || result.Results == nil {
			t.Fatalf("format %s = %+v calls=%d", format, result, checker.calls)
		}
	}
}

func TestCheckConformanceBoundsAndInputIntegrityRefuseBeforeChecker(t *testing.T) {
	t.Parallel()

	set := conformanceV2Set(t, Descriptor{
		SchemaFormat: "json-schema-2020-12",
		Artifacts:    []Entry{{Path: "schema/main.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"}},
	}, map[string]string{"schema/main.json": `{}`})
	checker := &recordingInstanceChecker{result: func(InstanceCheckInput) InstanceCheckResult {
		violations := make([]InstanceViolation, MaxConformanceViolations+10)
		for index := range violations {
			violations[index] = InstanceViolation{InstancePointer: "/same", SchemaPointer: "/same", Message: strings.Repeat("x", MaxConformanceDetailBytes+10)}
		}
		return InstanceCheckResult{Violations: violations}
	}}
	input := validConformanceInput(set, ConformanceModePayload)
	input.PayloadPath, input.Payload = "payload.json", []byte(`{}`)
	result := CheckConformance(input, checker)
	if !result.Truncated || result.TotalViolations != MaxConformanceViolations+10 || len(result.Results[0].Violations) != MaxConformanceViolations ||
		len(result.Results[0].Violations[0].Message) > MaxConformanceDetailBytes {
		t.Fatalf("bounded result = %+v", result)
	}

	overBound := input
	overBound.Payload = []byte(strings.Repeat("x", MaxFileBytes+1))
	before := checker.calls
	result = CheckConformance(overBound, checker)
	if result.Outcome != ConformanceInvalidInput || checker.calls != before {
		t.Fatalf("over-bound payload = %+v calls=%d", result, checker.calls)
	}

	corrupt := input
	corrupt.Set.Bytes["schema/main.json"] = []byte(`{"changed":true}`)
	result = CheckConformance(corrupt, checker)
	if result.Outcome != ConformanceIntegrityFailed || checker.calls != before {
		t.Fatalf("corrupt set = %+v calls=%d", result, checker.calls)
	}
}

type recordingInstanceChecker struct {
	result func(InstanceCheckInput) InstanceCheckResult
	calls  int
	inputs []InstanceCheckInput
}

func (c *recordingInstanceChecker) CheckInstance(input InstanceCheckInput) InstanceCheckResult {
	c.calls++
	input.Schema = slices.Clone(input.Schema)
	input.Instance = slices.Clone(input.Instance)
	c.inputs = append(c.inputs, input)
	return c.result(input)
}

func validConformanceInput(set CarriedSet, mode ConformanceMode) ConformanceInput {
	return ConformanceInput{
		ContractID: "XC-axon-orders", Version: "1.2.3", Commit: strings.Repeat("a", 40),
		SchemaFormat: "json-schema-2020-12", PublishedDigest: set.AggregateDigest, Set: set, Mode: mode,
	}
}

func conformanceV2Set(t *testing.T, descriptor Descriptor, raw map[string]string) CarriedSet {
	t.Helper()
	descriptor.Artifacts = slices.Clone(descriptor.Artifacts)
	hasValid, hasInvalid, schemaPath := false, false, ""
	for _, entry := range descriptor.Artifacts {
		switch entry.Role {
		case RoleSchema:
			if schemaPath == "" {
				schemaPath = entry.Path
			}
		case RoleValidFixture:
			hasValid = true
		case RoleInvalidFixture:
			hasInvalid = true
		}
	}
	if !hasValid {
		const fixture = "fixtures/valid/default.json"
		descriptor.Artifacts = append(descriptor.Artifacts, Entry{Path: fixture, Role: RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: schemaPath})
		raw[fixture] = `{}`
	}
	if !hasInvalid {
		const fixture = "fixtures/invalid/default.json"
		descriptor.Artifacts = append(descriptor.Artifacts, Entry{Path: fixture, Role: RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: schemaPath})
		raw[fixture] = `{}`
	}
	descriptorRaw := []byte("descriptor bytes")
	candidates := make([]CandidateFile, 0, len(descriptor.Artifacts))
	for _, entry := range descriptor.Artifacts {
		candidates = append(candidates, CandidateFile{Path: entry.Path, Kind: CandidateRegular, Raw: []byte(raw[entry.Path])})
	}
	set, issues := BuildCarriedSet(ProfileContractSetV2, descriptorRaw, descriptor, candidates)
	if len(issues) != 0 {
		t.Fatalf("BuildCarriedSet: %+v", issues)
	}
	return set
}

func TestCheckConformanceIsDeterministicUnderDetachedResultMutation(t *testing.T) {
	t.Parallel()

	set := conformanceV2Set(t, Descriptor{
		SchemaFormat: "json-schema-2020-12",
		Artifacts:    []Entry{{Path: "schema/main.json", Role: RoleSchema, Normative: true, MediaType: "application/schema+json"}},
	}, map[string]string{"schema/main.json": `{}`})
	input := validConformanceInput(set, ConformanceModePayload)
	input.PayloadPath, input.Payload = "payload.json", []byte(`{}`)
	checker := func() *recordingInstanceChecker {
		return &recordingInstanceChecker{result: func(InstanceCheckInput) InstanceCheckResult {
			return InstanceCheckResult{Violations: []InstanceViolation{{InstancePointer: "/b", Message: "b"}, {InstancePointer: "/a", Message: "a"}}}
		}}
	}
	left, right := CheckConformance(input, checker()), CheckConformance(input, checker())
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("deterministic results differ:\nleft=%+v\nright=%+v", left, right)
	}
	left.Results[0].Violations[0].Message = "mutated"
	again := CheckConformance(input, checker())
	if again.Results[0].Violations[0].Message == "mutated" {
		t.Fatal("caller result mutation affected a later evaluation")
	}
}
