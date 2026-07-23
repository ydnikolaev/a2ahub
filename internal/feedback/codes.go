package feedback

import (
	"fmt"
	"sort"

	"github.com/ydnikolaev/a2ahub/schemas"
	"gopkg.in/yaml.v3"
)

// The FB-### code constants (schemas/feedback/v1/codes.yaml, spec §11 A2
// revised) — feedback-local, closed, append-only. This is NOT the shared
// envelope registry (schemas/errors/v1/registry.yaml); feedback is not an
// envelope (I1) and owns this separate set with its own closure test
// (closure_test.go).
const (
	// CodeSchemaStructural (FB-001) is a feedback.schema.json structural
	// violation: bad id pattern, extra/unknown property, missing required
	// field, or an out-of-enum value.
	CodeSchemaStructural = "FB-001"
	// CodeMissingBugEvidence (FB-002) is kind:bug with evidence (or one of
	// its steps/expected/actual subfields) absent.
	CodeMissingBugEvidence = "FB-002"
	// CodeChecksGateFalse (FB-003) is one or more checks.* gates false.
	CodeChecksGateFalse = "FB-003"
	// CodeOversize (FB-004) is a whole-file size over the 16 KiB cap.
	CodeOversize = "FB-004"
	// CodeStatusNotNew (FB-005) is status != "new" at submit/intake time.
	CodeStatusNotNew = "FB-005"
	// CodeSecretDetected (FB-006) is a validate.ScanSecrets hit (§11 A3).
	CodeSecretDetected = "FB-006"
	// CodeFilenameMismatch (FB-007) is an intake file whose basename !=
	// "<id>.yaml" (--ci intake-only guard).
	CodeFilenameMismatch = "FB-007"
	// CodePathNotUnderInbox (FB-008) is an intake file not located at
	// feedback/inbox/<id>.yaml (--ci intake-only guard).
	CodePathNotUnderInbox = "FB-008"
)

// CodeEntry is one schemas/feedback/v1/codes.yaml row.
type CodeEntry struct {
	Code  string `yaml:"code"`
	Title string `yaml:"title"`
	When  string `yaml:"when"`
}

type codesDoc struct {
	Entries []CodeEntry `yaml:"entries"`
}

// CodeTable is the loaded schemas/feedback/v1/codes.yaml registry — the
// closure test's own ground truth (every FB-### code this package can
// emit must appear here; every row here must be emitted at least once).
type CodeTable struct {
	entries []CodeEntry
	byCode  map[string]CodeEntry
}

// LoadCodes reads and parses the embedded schemas/feedback/v1/codes.yaml
// (schemas.FS, §11 A7's embed directive).
func LoadCodes() (*CodeTable, error) {
	const op = "LoadCodes"
	raw, err := schemas.FS.ReadFile("feedback/v1/codes.yaml")
	if err != nil {
		return nil, fmt.Errorf("feedback: %s: %w", op, err)
	}
	var doc codesDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("feedback: %s: %w", op, err)
	}
	byCode := make(map[string]CodeEntry, len(doc.Entries))
	for _, e := range doc.Entries {
		byCode[e.Code] = e
	}
	return &CodeTable{entries: doc.Entries, byCode: byCode}, nil
}

// Has reports whether code is a known FB-### row.
func (t *CodeTable) Has(code string) bool {
	_, ok := t.byCode[code]
	return ok
}

// Codes returns every known FB-### code, sorted.
func (t *CodeTable) Codes() []string {
	out := make([]string, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, e.Code)
	}
	sort.Strings(out)
	return out
}
