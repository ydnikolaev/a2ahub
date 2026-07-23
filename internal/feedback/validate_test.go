package feedback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// schemaFixturesRoot is 13a's own fixture tree — this package's own
// validation oracle (TDD): every schemas/feedback/v1/fixtures/invalid/*
// sidecar names the FB code Validate must produce for that fixture.
const schemaFixturesRoot = "../../schemas/feedback/v1/fixtures"

type expectSidecar struct {
	Code string `yaml:"code"`
}

func hasCode(report Report, code string) bool {
	for _, v := range report.Violations {
		if v.Code == code {
			return true
		}
	}
	return false
}

// TestValidate_ValidFixtures is the oracle's green half: every
// fixtures/valid/*.yaml must validate clean.
func TestValidate_ValidFixtures(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob(filepath.Join(schemaFixturesRoot, "valid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one valid fixture")
	}
	for _, f := range matches {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			report := Validate(raw, Options{})
			if !report.Valid {
				t.Fatalf("expected %s to validate clean, got violations: %+v", f, report.Violations)
			}
		})
	}
}

// TestValidate_InvalidFixtures is the oracle's red half (this brief's
// central TDD requirement): for every schemas/feedback/v1/fixtures/
// invalid/<x>.yaml, Validate must produce a Violation whose Code equals
// the sidecar's cited FB code.
func TestValidate_InvalidFixtures(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob(filepath.Join(schemaFixturesRoot, "invalid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var fixtures []string
	for _, m := range matches {
		if strings.HasSuffix(m, ".expect.yaml") {
			continue
		}
		fixtures = append(fixtures, m)
	}
	if len(fixtures) == 0 {
		t.Fatal("expected at least one invalid fixture")
	}
	for _, f := range fixtures {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			sidecarRaw, err := os.ReadFile(f + ".expect.yaml")
			if err != nil {
				t.Fatalf("read sidecar for %s: %v", f, err)
			}
			var sc expectSidecar
			if err := yaml.Unmarshal(sidecarRaw, &sc); err != nil {
				t.Fatalf("decode sidecar for %s: %v", f, err)
			}
			if sc.Code == "" {
				t.Fatalf("%s: sidecar has no code", f)
			}

			report := Validate(raw, Options{})
			if report.Valid {
				t.Fatalf("expected %s to be invalid (sidecar cites %s), got Valid", f, sc.Code)
			}
			if !hasCode(report, sc.Code) {
				t.Fatalf("%s: sidecar cites %s, but Validate produced %+v", f, sc.Code, report.Violations)
			}
		})
	}
}

// TestValidate_OversizePastCap checks the boundary explicitly (16 KiB
// cap, §T2) beyond the shipped oversize fixture.
func TestValidate_OversizePastCap(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(schemaFixturesRoot, "valid", "fb-20260701-1a2b3c.yaml"))
	if err != nil {
		t.Fatalf("read base valid fixture: %v", err)
	}
	padded := append(append([]byte{}, raw...), []byte(strings.Repeat("x", 17*1024))...)
	report := Validate(padded, Options{})
	if !hasCode(report, CodeOversize) {
		t.Fatalf("expected %s for a padded-oversize report, got %+v", CodeOversize, report.Violations)
	}
}

// TestValidate_CI_IntakeGuards covers FB-007/FB-008 — intake-only path
// guards with no schemas/feedback/v1/fixtures home (codes.yaml's own
// comment: "exercised by 13b/13c's own test corpus").
func TestValidate_CI_IntakeGuards(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(schemaFixturesRoot, "valid", "fb-20260701-1a2b3c.yaml"))
	if err != nil {
		t.Fatalf("read base valid fixture: %v", err)
	}

	t.Run("filename mismatch", func(t *testing.T) {
		t.Parallel()
		report := Validate(raw, Options{CI: true, Path: "feedback/inbox/wrong-name.yaml"})
		if !hasCode(report, CodeFilenameMismatch) {
			t.Fatalf("expected %s, got %+v", CodeFilenameMismatch, report.Violations)
		}
	})

	t.Run("path not under inbox", func(t *testing.T) {
		t.Parallel()
		report := Validate(raw, Options{CI: true, Path: "somewhere/else/fb-20260701-1a2b3c.yaml"})
		if !hasCode(report, CodePathNotUnderInbox) {
			t.Fatalf("expected %s, got %+v", CodePathNotUnderInbox, report.Violations)
		}
	})

	t.Run("happy intake path is clean", func(t *testing.T) {
		t.Parallel()
		report := Validate(raw, Options{CI: true, Path: "feedback/inbox/fb-20260701-1a2b3c.yaml"})
		if !report.Valid {
			t.Fatalf("expected a valid report on the happy intake path, got %+v", report.Violations)
		}
	})
}
