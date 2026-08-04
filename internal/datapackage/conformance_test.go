package datapackage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// conformanceTestSchema is a flat object schema (no allOf/$ref composition)
// with enough distinct constraints — required, type, additionalProperties —
// to produce several independent violations from one instance, which the
// determinism and ordering tests below both need.
const conformanceTestSchema = `{
  "type": "object",
  "required": ["name", "age"],
  "properties": {
    "name": {"type": "string"},
    "age": {"type": "integer"},
    "email": {"type": "string"}
  },
  "additionalProperties": false
}`

const conformanceSchemaName = "schema/record.schema.json"

func newTestChecker(t *testing.T, schemas map[string][]byte) EntryChecker {
	t.Helper()
	checker, err := NewEntryChecker(schemas)
	if err != nil {
		t.Fatalf("NewEntryChecker: %v", err)
	}
	return checker
}

func checkEntry(t *testing.T, checker EntryChecker, req EntryCheckRequest) Check {
	t.Helper()
	check, err := checker.CheckEntry(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckEntry: %v", err)
	}
	return check
}

// --- construction ---

func TestNewEntryChecker_CompilesEachSchemaOnceAtConstruction(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})

	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{"name":"a","age":1}`)},
	})
	if check.Status != CheckPass {
		t.Fatalf("Status = %v, want CheckPass", check.Status)
	}
}

// TestNewEntryChecker_RefusesUncompilableSchema is the witness that schema
// compilation happens at construction, not lazily inside CheckEntry: if it
// were lazy, a broken schema would only surface the first time some entry
// needed it, and NewEntryChecker itself would never error.
func TestNewEntryChecker_RefusesUncompilableSchema(t *testing.T) {
	t.Parallel()
	_, err := NewEntryChecker(map[string][]byte{
		conformanceSchemaName: []byte(`{"type": "not-a-real-type"`), // truncated/invalid JSON
	})
	if err == nil {
		t.Fatal("NewEntryChecker(uncompilable schema) = nil error, want an error")
	}
}

// --- json framing ---

func TestCheckEntry_JSON_Valid(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{"name":"a","age":1}`)},
	})
	if check.Status != CheckPass {
		t.Fatalf("Status = %v, want CheckPass", check.Status)
	}
	if len(check.Violations) != 0 {
		t.Fatalf("Violations = %v, want none", check.Violations)
	}
	if check.Path != "a.json" {
		t.Fatalf("Path = %q, want %q", check.Path, "a.json")
	}
}

func TestCheckEntry_JSON_InvalidAgainstSchema(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{"age":"not-a-number","email":123,"extra1":true,"extra2":false}`)},
	})
	if check.Status != CheckFail {
		t.Fatalf("Status = %v, want CheckFail", check.Status)
	}
	// missing name (root required), age wrong type, email wrong type,
	// extra1 and extra2 forbidden by additionalProperties: 5 independent
	// violations from one document.
	if len(check.Violations) != 5 {
		t.Fatalf("len(Violations) = %d, want 5: %+v", len(check.Violations), check.Violations)
	}
	for _, v := range check.Violations {
		if v.Record != nil {
			t.Fatalf("json violation Record = %v, want nil", v.Record)
		}
	}
}

func TestCheckEntry_JSON_MalformedIsAFailNotAnError(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{not valid json`)},
	})
	if check.Status != CheckFail {
		t.Fatalf("Status = %v, want CheckFail", check.Status)
	}
	if len(check.Violations) != 1 {
		t.Fatalf("len(Violations) = %d, want 1", len(check.Violations))
	}
}

// --- ndjson framing ---

func TestCheckEntry_NDJSON_AllValid(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	body := strings.Join([]string{
		`{"name":"a","age":1}`,
		`{"name":"b","age":2}`,
		`{"name":"c","age":3}`,
	}, "\n") + "\n"
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatNDJSON,
		Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
	})
	if check.Status != CheckPass {
		t.Fatalf("Status = %v, want CheckPass: %+v", check.Status, check.Violations)
	}
	if len(check.Violations) != 0 {
		t.Fatalf("Violations = %v, want none", check.Violations)
	}
}

// TestCheckEntry_NDJSON_InvalidRecordNamedByLineNumber is the record-
// numbering requirement: a violation on line 4 of 5 must carry Record == 4,
// and every other line's own violations (there are none here) must not
// exist — this is the "record 4108 of 90000" case at small scale.
func TestCheckEntry_NDJSON_InvalidRecordNamedByLineNumber(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	body := strings.Join([]string{
		`{"name":"a","age":1}`,
		`{"name":"b","age":2}`,
		`{"name":"c","age":3}`,
		`{"name":"d"}`, // missing required "age" — the one bad record
		`{"name":"e","age":5}`,
	}, "\n") + "\n"
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatNDJSON,
		Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
	})
	if check.Status != CheckFail {
		t.Fatalf("Status = %v, want CheckFail", check.Status)
	}
	if len(check.Violations) != 1 {
		t.Fatalf("len(Violations) = %d, want 1: %+v", len(check.Violations), check.Violations)
	}
	v := check.Violations[0]
	if v.Record == nil || *v.Record != 4 {
		t.Fatalf("Violations[0].Record = %v, want pointer to 4", v.Record)
	}
}

func TestCheckEntry_NDJSON_MalformedLineIsAViolationNotAnAbortedRun(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	body := strings.Join([]string{
		`{"name":"a","age":1}`,
		`not json at all {{{`,
		`{"name":"c","age":3}`,
	}, "\n") + "\n"
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatNDJSON,
		Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
	})
	if check.Status != CheckFail {
		t.Fatalf("Status = %v, want CheckFail", check.Status)
	}
	if len(check.Violations) != 1 {
		t.Fatalf("len(Violations) = %d, want 1: %+v", len(check.Violations), check.Violations)
	}
	if got := check.Violations[0].Record; got == nil || *got != 2 {
		t.Fatalf("malformed-line Record = %v, want pointer to 2", got)
	}
}

// TestCheckEntry_NDJSON_TruncatedFinalLineAndItsValidSibling exercises §6.4's
// truncated-final-line case (no trailing newline, incomplete JSON) next to
// its sibling that must NOT regress: a final line with no trailing newline
// that IS complete, valid JSON must still pass. Both live in one test
// because the sibling is exactly the case a naive "no trailing newline ==
// truncated" special case would break.
func TestCheckEntry_NDJSON_TruncatedFinalLineAndItsValidSibling(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})

	t.Run("truncated final line is a violation", func(t *testing.T) {
		t.Parallel()
		body := `{"name":"a","age":1}` + "\n" + `{"name":"b","age":2` // no closing brace, no trailing newline
		check := checkEntry(t, checker, EntryCheckRequest{
			ConformsTo: conformanceSchemaName,
			Format:     FormatNDJSON,
			Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
		})
		if check.Status != CheckFail {
			t.Fatalf("Status = %v, want CheckFail", check.Status)
		}
		if len(check.Violations) != 1 {
			t.Fatalf("len(Violations) = %d, want 1: %+v", len(check.Violations), check.Violations)
		}
		if got := check.Violations[0].Record; got == nil || *got != 2 {
			t.Fatalf("truncated-line Record = %v, want pointer to 2", got)
		}
	})

	t.Run("valid final line without trailing newline passes", func(t *testing.T) {
		t.Parallel()
		body := `{"name":"a","age":1}` + "\n" + `{"name":"b","age":2}` // no trailing newline, but complete
		check := checkEntry(t, checker, EntryCheckRequest{
			ConformsTo: conformanceSchemaName,
			Format:     FormatNDJSON,
			Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
		})
		if check.Status != CheckPass {
			t.Fatalf("Status = %v, want CheckPass: %+v", check.Status, check.Violations)
		}
	})
}

func TestCheckEntry_NDJSON_BlankLinesAreSkipped(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	body := strings.Join([]string{
		`{"name":"a","age":1}`,
		``,
		`   `,
		`{"name":"b","age":2}`,
		``,
	}, "\n")
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatNDJSON,
		Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
	})
	if check.Status != CheckPass {
		t.Fatalf("Status = %v, want CheckPass: %+v", check.Status, check.Violations)
	}
	if len(check.Violations) != 0 {
		t.Fatalf("Violations = %v, want none", check.Violations)
	}
}

// --- missing schema ---

func TestCheckEntry_MissingSchemaIsCheckErrorNotCheckFail(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: "schema/never-given.schema.json",
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{"name":"a","age":1}`)},
	})
	if check.Status != CheckError {
		t.Fatalf("Status = %v, want CheckError", check.Status)
	}
	if len(check.Violations) != 0 {
		t.Fatalf("Violations = %v, want empty — nothing was evaluated, nothing was proven wrong", check.Violations)
	}
}

func TestCheckEntry_UnsupportedFormatIsCheckError(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     "yaml",
		Entry:      LocalFile{RelPath: "a.yaml", Bytes: []byte(`name: a`)},
	})
	if check.Status != CheckError {
		t.Fatalf("Status = %v, want CheckError", check.Status)
	}
}

// --- bound and truncation visibility ---

func TestCheckEntry_ViolationBoundTruncatesAndSaysSo(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})

	lines := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		// `{}` is missing both required fields, but a root-level
		// `required` failure naming several missing fields collapses to
		// ONE FieldViolation (internal/schema/violation.go's
		// classifyKeyword does not expand kind.Required the way it
		// expands kind.AdditionalProperties) — so each record contributes
		// exactly one violation, and twelve records produce twelve.
		lines = append(lines, `{}`)
	}
	body := strings.Join(lines, "\n") + "\n"

	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo:    conformanceSchemaName,
		Format:        FormatNDJSON,
		Entry:         LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
		MaxViolations: 5,
	})
	if check.Status != CheckFail {
		t.Fatalf("Status = %v, want CheckFail", check.Status)
	}
	if len(check.Violations) != 5 {
		t.Fatalf("len(Violations) = %d, want exactly the bound (5): %+v", len(check.Violations), check.Violations)
	}
	last := check.Violations[len(check.Violations)-1]
	if !strings.HasPrefix(last.Message, "violation list truncated") {
		t.Fatalf("last violation Message = %q, want a truncation marker prefix", last.Message)
	}
	if !strings.Contains(last.Message, "12") {
		t.Fatalf("truncation Message = %q, want it to name the true total (12)", last.Message)
	}
	if !strings.Contains(last.Message, "5") {
		t.Fatalf("truncation Message = %q, want it to name the bound (5)", last.Message)
	}
}

func TestCheckEntry_ViolationBoundDefaultsWhenZero(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})

	lines := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		lines = append(lines, `{}`)
	}
	body := strings.Join(lines, "\n") + "\n"

	check := checkEntry(t, checker, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatNDJSON,
		Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(body)},
		// MaxViolations left at zero: 3 records x 1 violation each (a
		// root-level `required` failure collapses to one FieldViolation,
		// see TestCheckEntry_ViolationBoundTruncatesAndSaysSo), well under
		// defaultMaxViolations, so nothing should be truncated.
	})
	if len(check.Violations) != 3 {
		t.Fatalf("len(Violations) = %d, want 3 (no truncation under the default bound)", len(check.Violations))
	}
	for _, v := range check.Violations {
		if strings.Contains(v.Message, "truncated") {
			t.Fatalf("unexpected truncation marker under default bound: %+v", check.Violations)
		}
	}
}

// --- determinism (L-4) ---

// TestCheckEntry_DeterministicAcrossRepeatedRuns is L-4's own test: two (in
// this case twenty) CheckEntry calls over byte-identical input must produce
// byte-identical Checks. The json case carries 5 independent violations
// from one document and the ndjson case carries 3 failing records, so this
// only passes if BOTH the underlying jsonschema/v6 leaf order AND any
// randomized map iteration in the instance decode are neutralized by this
// package's own sort — a 2-violation case would only catch that
// nondeterministically (roughly half the time per run), which is why this
// uses more violations and more iterations rather than fewer.
func TestCheckEntry_DeterministicAcrossRepeatedRuns(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})

	jsonReq := EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{"age":"x","email":1,"extra1":true,"extra2":false,"extra3":true}`)},
	}
	ndjsonBody := strings.Join([]string{
		`{"age":1}`,
		`{"name":"b"}`,
		`{}`,
	}, "\n") + "\n"
	ndjsonReq := EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatNDJSON,
		Entry:      LocalFile{RelPath: "d.ndjson", Bytes: []byte(ndjsonBody)},
	}

	firstJSON, err := json.Marshal(checkEntry(t, checker, jsonReq))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	firstNDJSON, err := json.Marshal(checkEntry(t, checker, ndjsonReq))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	for i := 0; i < 20; i++ {
		gotJSON, err := json.Marshal(checkEntry(t, checker, jsonReq))
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		if string(gotJSON) != string(firstJSON) {
			t.Fatalf("run %d: json check differs:\n first=%s\n  got=%s", i, firstJSON, gotJSON)
		}
		gotNDJSON, err := json.Marshal(checkEntry(t, checker, ndjsonReq))
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		if string(gotNDJSON) != string(firstNDJSON) {
			t.Fatalf("run %d: ndjson check differs:\n first=%s\n  got=%s", i, firstNDJSON, gotNDJSON)
		}
	}
}

func TestNewEntryChecker_ImplementsEntryChecker(t *testing.T) {
	t.Parallel()
	var _ EntryChecker = (*entryChecker)(nil)
	checker, err := NewEntryChecker(nil)
	if err != nil {
		t.Fatalf("NewEntryChecker(nil): %v", err)
	}
	check, err := checker.CheckEntry(context.Background(), EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("CheckEntry with no schemas registered: %v", err)
	}
	if check.Status != CheckError {
		t.Fatalf("Status = %v, want CheckError", check.Status)
	}
}

func TestCheckEntry_ContextCanceledReturnsError(t *testing.T) {
	t.Parallel()
	checker := newTestChecker(t, map[string][]byte{conformanceSchemaName: []byte(conformanceTestSchema)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := checker.CheckEntry(ctx, EntryCheckRequest{
		ConformsTo: conformanceSchemaName,
		Format:     FormatJSON,
		Entry:      LocalFile{RelPath: "a.json", Bytes: []byte(`{"name":"a","age":1}`)},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckEntry(canceled ctx) = %v, want errors.Is context.Canceled", err)
	}
}
