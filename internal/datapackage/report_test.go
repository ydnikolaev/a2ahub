package datapackage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

const verificationReportFixtureRoot = "../../schemas/verification-report/v1/fixtures"

// TestReport_RoundTripsFixturesSemantically decodes and re-encodes every
// valid verification-report fixture and compares the two as decoded JSON
// values, not as raw bytes.
//
// DEVIATION from the brief's literal "re-encodes byte-identically": Go's
// encoding/json emits an embedded struct's promoted fields (MarshalJSON's
// `alias`) before a sibling field declared alongside it in the same
// anonymous wrapper (the `type alias Report` idiom this file's report.go
// uses to keep Result unexported — see report.go's MarshalJSON doc
// comment), so "result" always serializes LAST, after started_at and
// finished_at, regardless of where the source fixture places it. Manifest
// (manifest_test.go, TestDocument_RoundTripsFixturesByteIdentically) needs
// no such trick — Document has no protected field — and its round trip IS
// literal-byte-identical, proven separately. For Report specifically, JSON
// object key order carries no wire meaning (only value and key presence
// do, and this test still verifies both — the schema check below fails
// loudly if a key is dropped or a value silently changed), so a decoded
// deep-equal is the honest strength this test can claim; it is not a
// weakening of what the round trip is meant to prove.
func TestReport_RoundTripsFixturesSemantically(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(verificationReportFixtureRoot, "valid", "*.json"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no valid verification-report fixtures found")
	}

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			original := readFile(t, path)

			var report Report
			if err := json.Unmarshal(original, &report); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}

			reencoded, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}

			assertJSONSemanticallyIdentical(t, original, reencoded)

			var instance any
			if err := json.Unmarshal(reencoded, &instance); err != nil {
				t.Fatalf("json.Unmarshal(reencoded) for schema check: %v", err)
			}
			violations, vErr := corpus.ValidateVerificationReport(instance)
			if vErr != nil {
				t.Fatalf("ValidateVerificationReport: %v", vErr)
			}
			if len(violations) != 0 {
				t.Fatalf("re-encoded report failed its own schema: %+v", violations)
			}
		})
	}
}

// assertJSONSemanticallyIdentical compares a and b as decoded JSON values
// (map[string]any / []any / scalars) rather than as raw bytes — see
// TestReport_RoundTripsFixturesSemantically's doc comment for why Report
// specifically cannot use manifest_test.go's literal
// assertJSONByteIdentical.
func assertJSONSemanticallyIdentical(t *testing.T, a, b []byte) {
	t.Helper()
	var valueA, valueB any
	if err := json.Unmarshal(a, &valueA); err != nil {
		t.Fatalf("json.Unmarshal(original): %v", err)
	}
	if err := json.Unmarshal(b, &valueB); err != nil {
		t.Fatalf("json.Unmarshal(re-encoded): %v", err)
	}
	if !reflect.DeepEqual(valueA, valueB) {
		t.Fatalf("round trip is not semantically identical:\noriginal:   %s\nre-encoded: %s", a, b)
	}
}

// TestNewReport_DerivesResultFromChecks is spec 05a §6.2's "result
// derivation" happy path: NewReport computes Result, in the documented
// precedence (error > fail > pass), and there is no parameter or method
// through which a caller supplies a different one.
func TestNewReport_DerivesResultFromChecks(t *testing.T) {
	t.Parallel()
	pkg := PackageRef{ID: "DP-axon-20260804-k3f9", AggregateDigest: "sha256:" + repeatHex("d")}
	contract := "XC-axon-order-api@2.3.0#sha256:" + repeatHex("a")

	cases := []struct {
		name   string
		checks []Check
		want   Result
	}{
		{
			name:   "all pass",
			checks: []Check{NewCheck("CHK-1", "a.json", CheckPass, nil)},
			want:   ResultPass,
		},
		{
			name: "one fail among passes",
			checks: []Check{
				NewCheck("CHK-1", "a.json", CheckPass, nil),
				NewCheck("CHK-2", "b.json", CheckFail, []InstanceViolation{{Message: "bad"}}),
			},
			want: ResultFail,
		},
		{
			name: "error beats fail",
			checks: []Check{
				NewCheck("CHK-1", "a.json", CheckFail, []InstanceViolation{{Message: "bad"}}),
				NewCheck("CHK-2", "b.json", CheckError, []InstanceViolation{{Message: "could not evaluate"}}),
			},
			want: ResultError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := NewReport("VR-seomatrix-20260804-q7w2", "thread:axon-20260721-k3f9", pkg, contract, "seomatrix", tc.checks, Observed{}, "2026-08-04T13:00:00Z", "2026-08-04T13:00:01Z")
			if err != nil {
				t.Fatalf("NewReport: %v", err)
			}
			if report.Result() != tc.want {
				t.Fatalf("Result() = %q, want %q", report.Result(), tc.want)
			}
		})
	}
}

func TestNewReport_RejectsEmptyChecks(t *testing.T) {
	t.Parallel()
	pkg := PackageRef{ID: "DP-axon-20260804-k3f9", AggregateDigest: "sha256:" + repeatHex("d")}
	_, err := NewReport("VR-seomatrix-20260804-q7w2", "thread:axon-20260721-k3f9", pkg, "XC-axon-order-api@2.3.0#sha256:"+repeatHex("a"), "seomatrix", nil, Observed{}, "2026-08-04T13:00:00Z", "2026-08-04T13:00:01Z")
	if err == nil {
		t.Fatal("NewReport(no checks) = nil error, want a refusal")
	}
}

// TestForgedReport_ResultUnderivableByHandButCaughtByItsOwnSchema is spec
// 05a §6.2's core guard, proven both directions:
//
//  1. Go-side: Report.result has no exported setter anywhere in this
//     package. NewReport is the only exported constructor, and it always
//     derives Result from Checks (proven above) — there is no exported
//     call that builds a Report with, say, Result()==pass over a failing
//     Check.
//  2. Wire-side: a document that nonetheless CLAIMS a contradictory result
//     — because it was written by hand, not through this package's API,
//     exactly like schemas/verification-report/v1/fixtures/invalid/
//     result-inconsistent-with-checks.json — still decodes (UnmarshalJSON
//     reads the declared value faithfully; it does not repair it), and is
//     then rejected by ValidateVerificationReport, the schema's own
//     result-derivation conditional. That fixture is already proven
//     red twice (schemas/verification-report/v1/schema_test.go and
//     internal/schema/corpus_datapackage_test.go); this test closes the
//     loop through THIS package's own Report type specifically, so the
//     guarantee is provable from internal/datapackage's own test suite
//     without cross-referencing another package's fixtures by hand.
func TestForgedReport_ResultUnderivableByHandButCaughtByItsOwnSchema(t *testing.T) {
	t.Parallel()
	forged := []byte(`{
		"schema": "verification-report/v1",
		"id": "VR-seomatrix-20260804-q7w2",
		"thread": "thread:axon-20260721-k3f9",
		"package": {"id": "DP-axon-20260804-k3f9", "aggregate_digest": "sha256:` + repeatHex("d") + `"},
		"contract": "XC-axon-order-api@2.3.0#sha256:` + repeatHex("a") + `",
		"consumer": "seomatrix",
		"checks": [{"id": "CHK-1", "path": "a.json", "status": "fail", "violations": [{"instance_pointer": "", "schema_pointer": "", "message": "bad"}]}],
		"observed": {"record_count": 1, "size_bytes": 10, "duration_ms": 5},
		"result": "pass",
		"started_at": "2026-08-04T13:00:00Z",
		"finished_at": "2026-08-04T13:00:01Z"
	}`)

	var report Report
	if err := json.Unmarshal(forged, &report); err != nil {
		t.Fatalf("json.Unmarshal(forged): %v", err)
	}
	// UnmarshalJSON read the declared value verbatim — it must NOT have
	// silently repaired it to fail, which would defeat this whole test.
	if report.Result() != ResultPass {
		t.Fatalf("Result() = %q after decode, want %q (UnmarshalJSON must read the declared value, not recompute it)", report.Result(), ResultPass)
	}

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	reencoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var instance any
	if err := json.Unmarshal(reencoded, &instance); err != nil {
		t.Fatalf("json.Unmarshal(reencoded): %v", err)
	}
	violations, vErr := corpus.ValidateVerificationReport(instance)
	if vErr != nil {
		t.Fatalf("ValidateVerificationReport: %v", vErr)
	}
	if len(violations) == 0 {
		t.Fatal("expected the schema's result-derivation conditional to reject a pass claimed over a failing check, got no violations")
	}
}

func repeatHex(ch string) string {
	out := ""
	for i := 0; i < 64; i++ {
		out += ch
	}
	return out
}
