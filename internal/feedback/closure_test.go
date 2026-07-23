package feedback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestFBClosure mirrors internal/validate/registry_test.go's own
// TestRegistryClosure shape for the feedback-local FB-### set (spec §11
// A2 revised): every code in codes.yaml is (a) produced by some Validate
// path this test drives, AND (b) cited by >=1 fixture *.expect.yaml
// sidecar — EXCEPT FB-007/FB-008, the intake-only path guards codes.yaml
// itself documents as having no schemas/feedback/v1/fixtures home (they
// are covered by TestValidate_CI_IntakeGuards's own inline --ci inputs
// instead, per this brief's explicit carve-out).
func TestFBClosure(t *testing.T) {
	t.Parallel()

	table, err := LoadCodes()
	if err != nil {
		t.Fatalf("LoadCodes: %v", err)
	}

	produced := map[string]bool{}
	record := func(report Report) {
		for _, v := range report.Violations {
			if v.Code == "" {
				t.Errorf("violation %+v has an empty code", v)
				continue
			}
			if !table.Has(v.Code) {
				t.Errorf("violation carries unknown FB code %q: %+v", v.Code, v)
				continue
			}
			produced[v.Code] = true
		}
	}

	// FB-001..FB-006: drive every shipped invalid fixture.
	matches, err := filepath.Glob(filepath.Join(schemaFixturesRoot, "invalid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range matches {
		if strings.HasSuffix(f, ".expect.yaml") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		record(Validate(raw, Options{}))
	}

	// FB-007/FB-008: intake-only, no fixture home — driven inline.
	validRaw, err := os.ReadFile(filepath.Join(schemaFixturesRoot, "valid", "fb-20260701-1a2b3c.yaml"))
	if err != nil {
		t.Fatalf("read base valid fixture: %v", err)
	}
	record(Validate(validRaw, Options{CI: true, Path: "feedback/inbox/wrong-name.yaml"}))
	record(Validate(validRaw, Options{CI: true, Path: "elsewhere/fb-20260701-1a2b3c.yaml"}))

	for _, code := range table.Codes() {
		if !produced[code] {
			t.Errorf("FB code %q is never produced by any exercised path in this test", code)
		}
	}

	// Citation half: every code EXCEPT FB-007/FB-008 must be cited by >=1
	// fixture sidecar (codes.yaml's own documented exemption for the two
	// intake-only guards).
	cited := map[string]bool{}
	sidecars, err := filepath.Glob(filepath.Join(schemaFixturesRoot, "invalid", "*.expect.yaml"))
	if err != nil {
		t.Fatalf("glob sidecars: %v", err)
	}
	if len(sidecars) == 0 {
		t.Fatal("expected at least one *.expect.yaml sidecar")
	}
	for _, s := range sidecars {
		raw, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("read sidecar %s: %v", s, err)
		}
		var sc expectSidecar
		if err := yaml.Unmarshal(raw, &sc); err != nil {
			t.Fatalf("decode sidecar %s: %v", s, err)
		}
		cited[sc.Code] = true
	}

	exemptFromCitation := map[string]bool{CodeFilenameMismatch: true, CodePathNotUnderInbox: true}
	for _, code := range table.Codes() {
		if exemptFromCitation[code] {
			continue
		}
		if !cited[code] {
			t.Errorf("FB code %q is never cited by any fixture sidecar", code)
		}
	}
}
