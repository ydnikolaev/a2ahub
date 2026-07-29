package validate

import (
	"slices"
	"testing"
)

func TestValidateManifestPolicyAuthorityMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manifest  string
		wantPaths []string
	}{
		{
			name: "valid active and historical participants",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
  - {system: legacy, section: legacy/, owners: [alice], status: left}
`,
		},
		{
			name: "duplicate systems and sections",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
  - {system: axon, section: axon/, owners: [bob], status: active}
`,
			wantPaths: []string{"participants.1.section", "participants.1.system"},
		},
		{
			name: "unclean nested section",
			manifest: `
space: demo
participants:
  - {system: axon, section: teams/axon/, owners: [alice], status: active}
`,
			wantPaths: []string{"participants.0.section"},
		},
		{
			name: "active participant without owner",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [], status: active}
`,
			wantPaths: []string{"participants.0.owners"},
		},
		{
			name: "one active login cannot own two systems",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
  - {system: matrix, section: matrix/, owners: [alice], status: active}
`,
			wantPaths: []string{"participants.1.owners.0"},
		},
		{
			name: "empty authority fields",
			manifest: `
space: demo
participants:
  - {system: " ", section: ./, owners: [" "], status: active}
`,
			wantPaths: []string{"participants.0.owners.0", "participants.0.section", "participants.0.system"},
		},
	}

	engine := mustEngine(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := engine.ValidateManifestPolicy([]byte(test.manifest))
			if err != nil {
				t.Fatalf("ValidateManifestPolicy: %v", err)
			}
			var gotPaths []string
			for _, violation := range result.Violations {
				if violation.Code != "REF-013" || violation.Class != ClassReferential || violation.Severity != SeverityReject {
					t.Fatalf("violation = %+v, want rejecting REF-013 referential violation", violation)
				}
				gotPaths = append(gotPaths, violation.Path)
			}
			slices.Sort(gotPaths)
			if !slices.Equal(gotPaths, test.wantPaths) {
				t.Fatalf("paths = %v, want %v", gotPaths, test.wantPaths)
			}
			if result.Valid != (len(test.wantPaths) == 0) {
				t.Fatalf("Valid = %v, want %v", result.Valid, len(test.wantPaths) == 0)
			}
		})
	}
}
