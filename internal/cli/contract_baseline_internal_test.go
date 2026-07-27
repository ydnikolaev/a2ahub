package cli

import "testing"

// TestContractSelectBaseline is P4 wave 5's Edge 2 (04-per-version-
// lifecycle.md §4), asserted directly against the bridge rather than only
// through a full `contract publish` run: epic AC-8's own scenario —
// publishing 1.2 while 2.0 is already published compares against 1.1, not
// 2.0, the globally-highest published version. This file is `package cli`
// because contractSelectBaseline is unexported (ADR-001: it mirrors
// internal/mcp's own copy, never a shared symbol across the two surfaces —
// decision 8's DECISION lives in internal/version.Baseline instead, which
// both bridges call).
func TestContractSelectBaseline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		priorVersions []contractSemver
		newVersion    contractSemver
		wantBaseline  contractSemver
		wantFound     bool
	}{
		{
			name:          "first-ever publish has no baseline",
			priorVersions: nil,
			newVersion:    contractSemver{1, 0, 0},
			wantFound:     false,
		},
		{
			name:          "ordinary sequential publish picks the immediate prior",
			priorVersions: []contractSemver{{1, 0, 0}},
			newVersion:    contractSemver{2, 0, 0},
			wantBaseline:  contractSemver{1, 0, 0},
			wantFound:     true,
		},
		{
			name:          "AC-8: maintenance 1.2 while 2.0 is published compares against 1.1, not 2.0",
			priorVersions: []contractSemver{{1, 0, 0}, {1, 1, 0}, {2, 0, 0}},
			newVersion:    contractSemver{1, 2, 0},
			wantBaseline:  contractSemver{1, 1, 0},
			wantFound:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := contractSelectBaseline(tc.priorVersions, tc.newVersion)
			if found != tc.wantFound {
				t.Fatalf("contractSelectBaseline(%v, %v) found = %v, want %v", tc.priorVersions, tc.newVersion, found, tc.wantFound)
			}
			if found && got != tc.wantBaseline {
				t.Fatalf("contractSelectBaseline(%v, %v) = %v, want %v", tc.priorVersions, tc.newVersion, got, tc.wantBaseline)
			}
		})
	}
}
