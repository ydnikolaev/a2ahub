package validate

import (
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
)

func violationCodes(violations []Violation) []string {
	codes := make([]string, 0, len(violations))
	for _, violation := range violations {
		codes = append(codes, violation.Code)
	}
	return codes
}

// TestCheckGatesForHost is spec 04 §T1.3's `autonomous` check: POL-028 for
// `autonomous` where the host reviews writes, and nothing else. The vocabulary
// verdict (POL-027) is ValidateManifest's, so an out-of-vocabulary value here
// yields nothing at all.
func TestCheckGatesForHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		manifest    string
		hostReviews bool
		want        []string
	}{
		{name: "autonomous on a reviewed host", manifest: "space: demo\ngates: autonomous\n", hostReviews: true, want: []string{"POL-028"}},
		{name: "autonomous on a local host", manifest: "space: demo\ngates: autonomous\n", hostReviews: false},
		{name: "confirm on a reviewed host stays clean", manifest: "space: demo\ngates: confirm\n", hostReviews: true},
		{name: "default on a reviewed host stays clean", manifest: "space: demo\ngates: default\n", hostReviews: true},
		{name: "absent on a reviewed host stays clean", manifest: "space: demo\n", hostReviews: true},
		{name: "unknown word is not this check's finding", manifest: "space: demo\ngates: sometimes\n", hostReviews: true},
		{name: "mapping is not this check's finding", manifest: "space: demo\ngates: {G1: autonomous}\n", hostReviews: true},
		{name: "quoted autonomous is still autonomous", manifest: "space: demo\ngates: \"autonomous\"\n", hostReviews: true, want: []string{"POL-028"}},
		{name: "unparseable yaml is POL-002's finding", manifest: "space: [unterminated\n", hostReviews: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := violationCodes(CheckGatesForHost([]byte(tt.manifest), tt.hostReviews))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("CheckGatesForHost(%q, hostReviews=%v) codes = %v, want %v", tt.manifest, tt.hostReviews, got, tt.want)
			}
		})
	}
}

func TestCheckGatesForHostViolationShape(t *testing.T) {
	t.Parallel()

	violations := CheckGatesForHost([]byte("space: demo\ngates: autonomous\n"), true)
	if len(violations) != 1 {
		t.Fatalf("violations = %+v, want exactly one", violations)
	}
	got := violations[0]
	if got.Code != "POL-028" || got.Class != ClassPolicy || got.Severity != SeverityReject || got.Path != "gates" {
		t.Fatalf("violation = %+v, want POL-028 / policy / reject / path gates", got)
	}
}

// TestCheckGateApproval is spec 04 §T1.3's land check: one POL-029 per required
// gate missing from the approved set when the mode is confirm, nothing
// otherwise, and the verdict never depends on the order or the repeats in
// either list.
func TestCheckGateApproval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mode     contract.GatesMode
		required []string
		approved []string
		want     []string // gates named, in order
	}{
		{name: "confirm, nothing required", mode: contract.GatesModeConfirm},
		{name: "confirm, G1 unapproved", mode: contract.GatesModeConfirm, required: []string{"G1"}, want: []string{"G1"}},
		{name: "confirm, G1 approved", mode: contract.GatesModeConfirm, required: []string{"G1"}, approved: []string{"G1"}},
		{name: "confirm, G2 bypassed with an empty approved set", mode: contract.GatesModeConfirm, required: []string{"G2"}, want: []string{"G2"}},
		{name: "confirm, approval narrower than the requirement (CAS retry G1 became G2)", mode: contract.GatesModeConfirm, required: []string{"G2"}, approved: []string{"G1"}, want: []string{"G2"}},
		{name: "confirm, approval wider than the requirement", mode: contract.GatesModeConfirm, required: []string{"G1"}, approved: []string{"G1", "G4"}},
		{name: "confirm, one violation per missing gate in gate order", mode: contract.GatesModeConfirm, required: []string{"G4", "G1", "G3"}, approved: []string{"G3"}, want: []string{"G1", "G4"}},
		{name: "confirm, repeats collapse", mode: contract.GatesModeConfirm, required: []string{"G1", "G1"}, want: []string{"G1"}},
		{name: "autonomous never asks", mode: contract.GatesModeAutonomous, required: []string{"G1", "G2", "G3", "G4"}},
		{name: "review never asks (the host's review is the gate)", mode: contract.GatesModeReview, required: []string{"G1", "G2", "G3", "G4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			violations := CheckGateApproval(tt.mode, tt.required, tt.approved)
			var named []string
			for _, violation := range violations {
				if violation.Code != "POL-029" || violation.Class != ClassPolicy || violation.Severity != SeverityReject {
					t.Fatalf("violation = %+v, want POL-029 / policy / reject", violation)
				}
				named = append(named, strings.Join(violation.Subjects, ","))
				if want := strings.Join(violation.Subjects, ",") + " requires --approve in a confirm space"; violation.Message != want {
					t.Fatalf("message = %q, want %q", violation.Message, want)
				}
			}
			if !slices.Equal(named, tt.want) {
				t.Fatalf("CheckGateApproval(%q, %v, %v) named %v, want %v", tt.mode, tt.required, tt.approved, named, tt.want)
			}
		})
	}
}

// TestApprovableGatesCoverFoldsHumanGates keeps the approvable set honest
// against the one gate table fold owns: every transition fold.HumanGate gates
// must name a gate the trailer can carry.
func TestApprovableGatesCoverFoldsHumanGates(t *testing.T) {
	t.Parallel()

	for _, transition := range []string{fold.TApprove, fold.TReject} {
		gate := fold.HumanGate(transition)
		if gate == "" {
			t.Fatalf("fold.HumanGate(%q) = \"\", want a gate", transition)
		}
		if _, ok := contract.CanonicalApprovedGates([]string{gate}); !ok {
			t.Errorf("fold.HumanGate(%q) = %q, which the approval trailer cannot carry", transition, gate)
		}
	}
}

// TestCheckGatesForHostIgnoresAmbiguousForms pins that POL-028 reads through
// the shared decoder: a merge key or an alias supplying `autonomous` is not
// interpreted here (ValidateManifest refuses it as POL-027 on the same write),
// so the two readers never disagree about what the bytes say.
func TestCheckGatesForHostIgnoresAmbiguousForms(t *testing.T) {
	t.Parallel()

	for _, manifest := range []string{
		"space: demo\nbase: &b {gates: autonomous}\n<<: *b\n",
		"space: demo\ng: &g autonomous\ngates: *g\n",
	} {
		if got := CheckGatesForHost([]byte(manifest), true); len(got) != 0 {
			t.Fatalf("CheckGatesForHost(%q, true) = %+v, want none: the form is ambiguous and POL-027's", manifest, got)
		}
	}
}
