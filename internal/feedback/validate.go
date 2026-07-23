package feedback

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	iSchema "github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/schemas"
	"gopkg.in/yaml.v3"
)

// maxFeedbackBytes is §T2's whole-file size cap (16 KiB), enforced at
// runtime (JSON Schema has no serialized-byte-size keyword).
const maxFeedbackBytes = 16 * 1024

// feedbackSchemaResourceKey is this package's own synthetic, never-
// dereferenced jsonschema/v6 resource-identity key — mirrors internal/
// schema's own resourceURLPrefix convention, kept as this package's own
// copy (I1: feedback compiles its own schema instance, it never runs
// internal/schema's envelope Corpus).
const feedbackSchemaResourceKey = "https://schemas.a2ahub.internal/feedback/v1/feedback.schema.json"

var (
	feedbackSchemaOnce sync.Once
	feedbackSchema     *jsonschema.Schema
	feedbackSchemaErr  error
)

// loadFeedbackSchema compiles the embedded feedback.schema.json exactly
// once (a pure, side-effect-free embedded-resource compile — no
// filesystem/network variability, so a package-level memoized compile is
// safe and avoids recompiling on every Validate call).
func loadFeedbackSchema() (*jsonschema.Schema, error) {
	feedbackSchemaOnce.Do(func() {
		raw, err := schemas.FS.ReadFile("feedback/v1/feedback.schema.json")
		if err != nil {
			feedbackSchemaErr = fmt.Errorf("feedback: loadFeedbackSchema: %w", err)
			return
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			feedbackSchemaErr = fmt.Errorf("feedback: loadFeedbackSchema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(feedbackSchemaResourceKey, doc); err != nil {
			feedbackSchemaErr = fmt.Errorf("feedback: loadFeedbackSchema: %w", err)
			return
		}
		sch, err := c.Compile(feedbackSchemaResourceKey)
		if err != nil {
			feedbackSchemaErr = fmt.Errorf("feedback: loadFeedbackSchema: %w", err)
			return
		}
		feedbackSchema = sch
	})
	return feedbackSchema, feedbackSchemaErr
}

// Options carries Validate's runtime knobs (§T1): CI additionally enforces
// the intake path constraints (FB-007/FB-008) against Path.
type Options struct {
	CI   bool
	Path string
}

type feedbackEvidenceProbe struct {
	Steps    []string `yaml:"steps"`
	Expected string   `yaml:"expected"`
	Actual   string   `yaml:"actual"`
}

type feedbackProbe struct {
	ID       string                 `yaml:"id"`
	Kind     string                 `yaml:"kind"`
	Status   string                 `yaml:"status"`
	Checks   map[string]bool        `yaml:"checks"`
	Evidence *feedbackEvidenceProbe `yaml:"evidence"`
}

// requiredChecks are the five §2.1 honesty gates feedback.schema.json's
// `checks` object requires structurally; validate additionally requires
// every one to be literally true (I5).
var requiredChecks = []string{
	"docs_consulted", "grounded_in_real_work", "not_space_specific",
	"no_sensitive_content", "duplicates_checked",
}

// Validate runs every §T1/§T2 gate over raw (a feedback report's whole
// file content) and returns feedback's own Report (§11 A1): schema
// structure (FB-001, or FB-002 for the bug-evidence conditional), the
// checks-all-true honesty gate (FB-003), the size cap (FB-004), status ==
// "new" (FB-005), a secret-scan re-map (FB-006, §11 A3), and — only when
// opts.CI — the intake filename/path guards (FB-007/FB-008).
func Validate(raw []byte, opts Options) Report {
	var violations []Violation

	sch, err := loadFeedbackSchema()
	if err != nil {
		// The embedded schema itself failed to load/compile — this is a
		// binary-build defect, not a fixture-driven code, but Validate must
		// still degrade to a reported violation rather than panic.
		return Report{Valid: false, Violations: []Violation{{Code: CodeSchemaStructural, Message: err.Error()}}}
	}

	var probe feedbackProbe
	probeErr := yaml.Unmarshal(raw, &probe)

	instance, decodeErr := iSchema.DecodeYAMLInstance(raw)
	switch {
	case decodeErr != nil:
		violations = append(violations, Violation{Code: CodeSchemaStructural, Message: decodeErr.Error()})
	default:
		if verr := sch.Validate(instance); verr != nil {
			code := CodeSchemaStructural
			if probeErr == nil && probe.Kind == "bug" && evidenceIncomplete(probe.Evidence) {
				code = CodeMissingBugEvidence
			}
			violations = append(violations, Violation{Code: code, Message: verr.Error()})
		}
	}

	if probeErr == nil {
		if !allChecksTrue(probe.Checks) {
			violations = append(violations, Violation{Code: CodeChecksGateFalse, Field: "checks", Message: "one or more §2.1 honesty gates is false"})
		}
		if probe.Status != "new" {
			violations = append(violations, Violation{Code: CodeStatusNotNew, Field: "status", Message: fmt.Sprintf("status %q is not new", probe.Status)})
		}
	}

	if len(raw) > maxFeedbackBytes {
		violations = append(violations, Violation{Code: CodeOversize, Message: fmt.Sprintf("report is %d bytes, exceeds the %d byte cap", len(raw), maxFeedbackBytes)})
	}

	// §11 A3: ScanSecrets returns validate.Violation{Code,Class,Path,
	// Message,CCRef,Severity} — re-mapped into feedback's own Violation
	// shape (Field carries the scanner's Path, which is always "" for a
	// whole-body scan; see this phase's Deviations report).
	for _, v := range validate.ScanSecrets(raw) {
		violations = append(violations, Violation{Code: CodeSecretDetected, Field: v.Path, Message: v.Message})
	}

	if opts.CI {
		id := probe.ID
		base := filepath.Base(opts.Path)
		if id == "" || base != id+".yaml" {
			violations = append(violations, Violation{Code: CodeFilenameMismatch, Field: "path", Message: fmt.Sprintf("filename %q does not match id %q", base, id)})
		}
		if !strings.HasPrefix(filepath.ToSlash(opts.Path), "feedback/inbox/") {
			violations = append(violations, Violation{Code: CodePathNotUnderInbox, Field: "path", Message: fmt.Sprintf("path %q is not under feedback/inbox/", opts.Path)})
		}
	}

	return Report{Valid: len(violations) == 0, Violations: violations}
}

func evidenceIncomplete(e *feedbackEvidenceProbe) bool {
	if e == nil {
		return true
	}
	return len(e.Steps) == 0 || e.Expected == "" || e.Actual == ""
}

func allChecksTrue(m map[string]bool) bool {
	for _, k := range requiredChecks {
		if !m[k] {
			return false
		}
	}
	return true
}
