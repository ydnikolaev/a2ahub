package contract

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

const (
	ConformanceJSONSchema202012         = "json-schema-2020-12"
	ConformanceSchemaScopeSelfContained = "self-contained"
	MaxConformanceViolations            = 256
	MaxConformanceDetailBytes           = 512
)

type ConformanceMode string

const (
	ConformanceModePayload ConformanceMode = "payload"
	ConformanceModeSuite   ConformanceMode = "suite"
)

type ConformanceOutcome string

const (
	ConformanceConformant           ConformanceOutcome = "conformant"
	ConformanceNonconformant        ConformanceOutcome = "nonconformant"
	ConformanceSuiteConsistent      ConformanceOutcome = "suite-consistent"
	ConformanceSuiteInconsistent    ConformanceOutcome = "suite-inconsistent"
	ConformanceUnsupported          ConformanceOutcome = "unsupported"
	ConformanceIntegrityFailed      ConformanceOutcome = "integrity-failed"
	ConformanceInvalidInput         ConformanceOutcome = "invalid-input"
	ConformanceUnsupportedReference ConformanceOutcome = "unsupported-reference"
)

type ConformanceExpectation string

const (
	ConformanceExpectedConformant    ConformanceExpectation = "conformant"
	ConformanceExpectedNonconformant ConformanceExpectation = "nonconformant"
)

type ConformanceActual string

const (
	ConformanceActualConformant    ConformanceActual = "conformant"
	ConformanceActualNonconformant ConformanceActual = "nonconformant"
	ConformanceActualInvalidJSON   ConformanceActual = "invalid-json"
	ConformanceActualInvalidSchema ConformanceActual = "invalid-schema"
	ConformanceActualUnevaluated   ConformanceActual = "unevaluated"
)

type ConformanceMappingSource string

const (
	ConformanceMappingManifest ConformanceMappingSource = "manifest"
	ConformanceMappingLegacy   ConformanceMappingSource = "legacy"
)

// ConformanceInput contains already-resolved immutable bytes. The pure core
// performs no filesystem, Git, schema-loader or network I/O.
type ConformanceInput struct {
	ContractID      string
	Version         string
	Commit          string
	SchemaFormat    string
	PublishedDigest string
	Set             CarriedSet
	Mode            ConformanceMode
	SchemaPath      string
	PayloadPath     string
	Payload         []byte
}

type ConformanceCaseResult struct {
	Path       string                 `json:"path"`
	Schema     string                 `json:"schema"`
	Expected   ConformanceExpectation `json:"expected"`
	Actual     ConformanceActual      `json:"actual"`
	Violations []InstanceViolation    `json:"violations"`
}

type ConformanceResult struct {
	Ref             string                   `json:"ref"`
	Commit          string                   `json:"commit"`
	DigestProfile   DigestProfile            `json:"digest_profile"`
	AggregateDigest string                   `json:"aggregate_digest"`
	SchemaFormat    string                   `json:"schema_format"`
	SchemaScope     string                   `json:"schema_scope"`
	Mode            ConformanceMode          `json:"mode"`
	MappingSource   ConformanceMappingSource `json:"mapping_source,omitempty"`
	Supported       bool                     `json:"supported"`
	Passed          bool                     `json:"passed"`
	Outcome         ConformanceOutcome       `json:"outcome"`
	Message         string                   `json:"message,omitempty"`
	Results         []ConformanceCaseResult  `json:"results"`
	Truncated       bool                     `json:"truncated"`
	TotalResults    int                      `json:"total_results"`
	TotalViolations int                      `json:"total_violations"`
}

// CheckConformance runs payload or self-suite semantics through P5's
// library-agnostic InstanceChecker seam. A checker must use Passed=false with
// at least one structured violation for an evaluated nonconforming instance;
// Passed=false with no violations means compilation/evaluation did not occur.
// Keeping that distinction prevents an uncompilable schema from satisfying an
// invalid-fixture expectation.
func CheckConformance(input ConformanceInput, checker InstanceChecker) ConformanceResult {
	result := ConformanceResult{
		Ref: input.ContractID + "@" + input.Version, Commit: input.Commit,
		DigestProfile: input.Set.Profile, AggregateDigest: input.PublishedDigest,
		SchemaFormat: input.SchemaFormat, SchemaScope: ConformanceSchemaScopeSelfContained,
		Mode: input.Mode, Results: []ConformanceCaseResult{},
	}
	if !validConformanceReference(input) {
		result.Outcome, result.Message = ConformanceInvalidInput, "contract reference, commit, format or mode is invalid"
		return result
	}
	set, ok := verifiedConformanceSet(input)
	if !ok {
		result.Outcome, result.Message = ConformanceIntegrityFailed, "carried-set bytes do not match the published digest"
		return result
	}
	if input.SchemaFormat != ConformanceJSONSchema202012 {
		result.Outcome, result.Message = ConformanceUnsupported, "no offline conformance adapter exists for the declared format"
		return result
	}
	result.Supported = true
	if checker == nil {
		result.Outcome, result.Message = ConformanceInvalidInput, "instance checker is required"
		return result
	}

	schemas := conformanceSchemas(set)
	if len(schemas) == 0 {
		result.Outcome, result.Message = ConformanceInvalidInput, "contract carries no schema entry"
		return result
	}

	switch input.Mode {
	case ConformanceModePayload:
		return checkConformancePayload(result, input, set, schemas, checker)
	case ConformanceModeSuite:
		return checkConformanceSuite(result, input, set, schemas, checker)
	default:
		result.Outcome = ConformanceInvalidInput
		return result
	}
}

func validConformanceReference(input ConformanceInput) bool {
	parsed, err := artifact.ParseID(input.ContractID)
	if err != nil || parsed.Prefix != "XC" || parsed.Class != artifact.ClassStanding {
		return false
	}
	canonical, err := version.Canonical(input.Version)
	if err != nil || canonical != input.Version || !validConformanceOID(input.Commit) || input.Commit != strings.ToLower(input.Commit) {
		return false
	}
	return input.SchemaFormat != "" && (input.Mode == ConformanceModePayload || input.Mode == ConformanceModeSuite)
}

func validConformanceOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func verifiedConformanceSet(input ConformanceInput) (CarriedSet, bool) {
	if input.PublishedDigest == "" || input.Set.Profile != ProfileContractSetV2 && input.Set.Profile != ProfileContractTreeV1 ||
		len(input.Set.Entries) > MaxExplicitFiles {
		return CarriedSet{}, false
	}
	wantBytes := len(input.Set.Entries)
	if input.Set.Profile == ProfileContractSetV2 {
		wantBytes++
		if input.Set.Descriptor.SchemaFormat != input.SchemaFormat {
			return CarriedSet{}, false
		}
	}
	if len(input.Set.Bytes) != wantBytes {
		return CarriedSet{}, false
	}
	candidates := make([]CandidateFile, 0, len(input.Set.Entries))
	for _, entry := range input.Set.Entries {
		raw, exists := input.Set.Bytes[entry.Path]
		if !exists {
			return CarriedSet{}, false
		}
		candidates = append(candidates, CandidateFile{Path: entry.Path, Kind: CandidateRegular, Raw: raw})
	}
	var rebuilt CarriedSet
	var issues []Issue
	if input.Set.Profile == ProfileContractSetV2 {
		descriptorRaw, exists := input.Set.Bytes[DescriptorPath]
		if !exists {
			return CarriedSet{}, false
		}
		rebuilt, issues = BuildCarriedSet(input.Set.Profile, descriptorRaw, input.Set.Descriptor, candidates)
	} else {
		rebuilt, issues = BuildCarriedSet(input.Set.Profile, nil, Descriptor{}, candidates)
	}
	if len(issues) != 0 || rebuilt.AggregateDigest != input.Set.AggregateDigest || rebuilt.AggregateDigest != input.PublishedDigest {
		return CarriedSet{}, false
	}
	return rebuilt, true
}

func conformanceSchemas(set CarriedSet) []string {
	paths := make([]string, 0)
	for _, entry := range set.Entries {
		if set.Profile == ProfileContractSetV2 {
			if entry.Role == RoleSchema {
				paths = append(paths, entry.Path)
			}
		} else if strings.HasPrefix(entry.Path, "schema/") {
			paths = append(paths, entry.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

func inspectSelfContainedSchema(raw []byte) (malformed bool, unsupported bool) {
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return true, false
	}
	var walk func(any)
	walk = func(value any) {
		if unsupported {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := typed[key]
				switch key {
				case "$ref", "$dynamicRef", "$recursiveRef", "$id":
					if reference, ok := child.(string); ok && reference != "" && !strings.HasPrefix(reference, "#") {
						unsupported = true
						return
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(document)
	return false, unsupported
}

func checkConformancePayload(
	result ConformanceResult,
	input ConformanceInput,
	set CarriedSet,
	schemas []string,
	checker InstanceChecker,
) ConformanceResult {
	if !cleanPortablePath(input.PayloadPath) || len(input.Payload) > MaxFileBytes {
		result.Outcome, result.Message = ConformanceInvalidInput, "payload path or size is invalid"
		return result
	}
	schemaPath := input.SchemaPath
	if schemaPath == "" {
		if len(schemas) != 1 {
			result.Outcome, result.Message = ConformanceInvalidInput, "payload schema is ambiguous"
			return result
		}
		schemaPath = schemas[0]
	} else if !containsConformancePath(schemas, schemaPath) {
		result.Outcome, result.Message = ConformanceInvalidInput, "payload schema is not declared"
		return result
	}
	malformed, unsupported := inspectSelfContainedSchema(set.Bytes[schemaPath])
	if unsupported {
		result.Outcome = ConformanceUnsupportedReference
		result.Message = "schema contains a non-fragment reference or document identifier"
		result.Results = []ConformanceCaseResult{{
			Path: schemaPath, Schema: schemaPath, Actual: ConformanceActualUnevaluated,
			Violations: []InstanceViolation{},
		}}
		result.TotalResults = 1
		return result
	}
	item := ConformanceCaseResult{
		Path: input.PayloadPath, Schema: schemaPath, Expected: ConformanceExpectedConformant,
		Violations: []InstanceViolation{},
	}
	if malformed {
		item.Actual = ConformanceActualInvalidSchema
		result.Results = []ConformanceCaseResult{item}
		result.TotalResults, result.Outcome = 1, ConformanceNonconformant
		result.Message = "selected schema is not valid JSON"
		return result
	}
	if !json.Valid(input.Payload) {
		item.Actual = ConformanceActualInvalidJSON
		result.Results = []ConformanceCaseResult{item}
		result.TotalResults, result.Outcome = 1, ConformanceNonconformant
		return result
	}
	checked := checker.CheckInstance(InstanceCheckInput{
		SchemaPath: schemaPath, Schema: append([]byte(nil), set.Bytes[schemaPath]...),
		InstancePath: input.PayloadPath, Instance: append([]byte(nil), input.Payload...),
	})
	item.Actual, item.Violations, result.TotalViolations, result.Truncated = normalizeInstanceResult(checked, MaxConformanceViolations)
	result.Results, result.TotalResults = []ConformanceCaseResult{item}, 1
	if item.Actual == ConformanceActualConformant {
		result.Passed, result.Outcome = true, ConformanceConformant
	} else {
		result.Outcome = ConformanceNonconformant
	}
	return result
}

func checkConformanceSuite(
	result ConformanceResult,
	input ConformanceInput,
	set CarriedSet,
	schemas []string,
	checker InstanceChecker,
) ConformanceResult {
	malformedSchemas := make(map[string]struct{})
	for _, schemaPath := range schemas {
		malformed, unsupported := inspectSelfContainedSchema(set.Bytes[schemaPath])
		if unsupported {
			result.Outcome = ConformanceUnsupportedReference
			result.Message = "schema contains a non-fragment reference or document identifier"
			result.Results = []ConformanceCaseResult{{
				Path: schemaPath, Schema: schemaPath, Actual: ConformanceActualUnevaluated,
				Violations: []InstanceViolation{},
			}}
			result.TotalResults = 1
			return result
		}
		if malformed {
			malformedSchemas[schemaPath] = struct{}{}
		}
	}
	cases, mappingOK := conformanceSuiteCases(set, schemas)
	result.MappingSource = mappingSourceFor(set.Profile)
	result.TotalResults = len(cases)
	if !mappingOK || len(cases) == 0 {
		result.Outcome, result.Message = ConformanceSuiteInconsistent, "fixture-to-schema mapping is incomplete or ambiguous"
		for _, fixture := range cases {
			result.Results = append(result.Results, ConformanceCaseResult{
				Path: fixture.path, Schema: fixture.schema, Expected: fixture.expected,
				Actual: ConformanceActualUnevaluated, Violations: []InstanceViolation{},
			})
		}
		return result
	}

	consistent := true
	remainingViolations := MaxConformanceViolations
	for _, fixture := range cases {
		item := ConformanceCaseResult{
			Path: fixture.path, Schema: fixture.schema, Expected: fixture.expected,
			Violations: []InstanceViolation{},
		}
		raw := set.Bytes[fixture.path]
		if _, malformed := malformedSchemas[fixture.schema]; malformed {
			item.Actual = ConformanceActualInvalidSchema
		} else if !json.Valid(raw) {
			item.Actual = ConformanceActualInvalidJSON
		} else {
			checked := checker.CheckInstance(InstanceCheckInput{
				SchemaPath: fixture.schema, Schema: append([]byte(nil), set.Bytes[fixture.schema]...),
				InstancePath: fixture.path, Instance: append([]byte(nil), raw...),
			})
			var total int
			var truncated bool
			item.Actual, item.Violations, total, truncated = normalizeInstanceResult(checked, remainingViolations)
			result.TotalViolations += total
			remainingViolations -= len(item.Violations)
			result.Truncated = result.Truncated || truncated
		}
		if item.Actual == ConformanceActualInvalidJSON || item.Actual == ConformanceActualInvalidSchema ||
			item.Expected == ConformanceExpectedConformant && item.Actual != ConformanceActualConformant ||
			item.Expected == ConformanceExpectedNonconformant && item.Actual != ConformanceActualNonconformant {
			consistent = false
		}
		result.Results = append(result.Results, item)
	}
	for schemaPath := range malformedSchemas {
		used := false
		for _, fixture := range cases {
			if fixture.schema == schemaPath {
				used = true
				break
			}
		}
		if !used {
			result.Results = append(result.Results, ConformanceCaseResult{
				Path: schemaPath, Schema: schemaPath, Actual: ConformanceActualInvalidSchema,
				Violations: []InstanceViolation{},
			})
			result.TotalResults++
		}
		consistent = false
	}
	sort.Slice(result.Results, func(i, j int) bool { return result.Results[i].Path < result.Results[j].Path })
	if consistent {
		result.Passed, result.Outcome = true, ConformanceSuiteConsistent
	} else {
		result.Outcome = ConformanceSuiteInconsistent
	}
	return result
}

type conformanceSuiteCase struct {
	path     string
	schema   string
	expected ConformanceExpectation
}

func conformanceSuiteCases(set CarriedSet, schemas []string) ([]conformanceSuiteCase, bool) {
	cases := make([]conformanceSuiteCase, 0)
	if set.Profile == ProfileContractSetV2 {
		for _, entry := range set.Entries {
			expected := ConformanceExpectation("")
			switch entry.Role {
			case RoleValidFixture:
				expected = ConformanceExpectedConformant
			case RoleInvalidFixture:
				expected = ConformanceExpectedNonconformant
			default:
				continue
			}
			cases = append(cases, conformanceSuiteCase{path: entry.Path, schema: entry.ConformsTo, expected: expected})
		}
		sort.Slice(cases, func(i, j int) bool { return cases[i].path < cases[j].path })
		return cases, true
	}

	fixturePaths := make([]string, 0)
	expectedByPath := make(map[string]ConformanceExpectation)
	for _, entry := range set.Entries {
		switch {
		case strings.HasPrefix(entry.Path, "fixtures/valid/"):
			fixturePaths = append(fixturePaths, entry.Path)
			expectedByPath[entry.Path] = ConformanceExpectedConformant
		case strings.HasPrefix(entry.Path, "fixtures/invalid/"):
			fixturePaths = append(fixturePaths, entry.Path)
			expectedByPath[entry.Path] = ConformanceExpectedNonconformant
		}
	}
	sort.Strings(fixturePaths)
	mapping, ok := legacyConformanceMapping(fixturePaths, schemas)
	for _, fixturePath := range fixturePaths {
		cases = append(cases, conformanceSuiteCase{
			path: fixturePath, schema: mapping[fixturePath], expected: expectedByPath[fixturePath],
		})
	}
	return cases, ok
}

func legacyConformanceMapping(fixtures, schemas []string) (map[string]string, bool) {
	mapping := make(map[string]string, len(fixtures))
	unmatched := make([]string, 0)
	for _, fixture := range fixtures {
		want := strings.TrimSuffix(path.Base(fixture), ".json") + ".schema.json"
		hits := make([]string, 0, 1)
		for _, schemaPath := range schemas {
			if path.Base(schemaPath) == want {
				hits = append(hits, schemaPath)
			}
		}
		if len(hits) > 1 {
			return mapping, false
		}
		if len(hits) == 1 {
			mapping[fixture] = hits[0]
		} else {
			unmatched = append(unmatched, fixture)
		}
	}
	if len(unmatched) == 0 {
		return mapping, true
	}
	if len(mapping) == 0 && len(schemas) == 1 {
		for _, fixture := range fixtures {
			mapping[fixture] = schemas[0]
		}
		return mapping, true
	}
	return mapping, false
}

func mappingSourceFor(profile DigestProfile) ConformanceMappingSource {
	if profile == ProfileContractTreeV1 {
		return ConformanceMappingLegacy
	}
	return ConformanceMappingManifest
}

func normalizeInstanceResult(result InstanceCheckResult, limit int) (ConformanceActual, []InstanceViolation, int, bool) {
	if limit < 0 {
		limit = 0
	}
	total := len(result.Violations)
	violations := make([]InstanceViolation, 0, min(total, limit))
	for _, violation := range result.Violations {
		bounded := InstanceViolation{
			InstancePointer: boundedConformanceText(violation.InstancePointer),
			SchemaPointer:   boundedConformanceText(violation.SchemaPointer),
			Message:         boundedConformanceText(violation.Message),
		}
		index := sort.Search(len(violations), func(index int) bool {
			return !conformanceViolationLess(violations[index], bounded)
		})
		if index >= limit {
			continue
		}
		violations = append(violations, InstanceViolation{})
		copy(violations[index+1:], violations[index:])
		violations[index] = bounded
		if len(violations) > limit {
			violations = violations[:limit]
		}
	}
	truncated := total > limit
	switch {
	case result.Passed && total == 0:
		return ConformanceActualConformant, violations, total, truncated
	case !result.Passed && total != 0:
		return ConformanceActualNonconformant, violations, total, truncated
	default:
		return ConformanceActualUnevaluated, violations, total, truncated
	}
}

func conformanceViolationLess(left, right InstanceViolation) bool {
	if left.InstancePointer != right.InstancePointer {
		return left.InstancePointer < right.InstancePointer
	}
	if left.SchemaPointer != right.SchemaPointer {
		return left.SchemaPointer < right.SchemaPointer
	}
	return left.Message < right.Message
}

func boundedConformanceText(value string) string {
	if len(value) > MaxConformanceDetailBytes {
		value = value[:MaxConformanceDetailBytes]
	}
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	value = strings.ToValidUTF8(value, "�")
	if len(value) > MaxConformanceDetailBytes {
		value = value[:MaxConformanceDetailBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func containsConformancePath(paths []string, want string) bool {
	index, found := sort.Find(len(paths), func(index int) int { return strings.Compare(want, paths[index]) })
	return found && index < len(paths)
}
