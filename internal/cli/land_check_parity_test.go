package cli

// Audit F2: the gate the publication planner records (and the CLI and MCP then
// write into A2A-Approved-Gates) must be the gate the local land check
// requires, or an honest `contract publish --approve` is refused with POL-029.
// Both sides are driven for real here: contract.PlanPublication over the
// published versions, and landRequiredGates over a tip whose history published
// the same versions.

import (
	"context"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// compatibleChecker reports every comparison compatible, so the planner's gate
// is decided by the baseline and the versions alone, the same inputs the land
// check has.
type compatibleChecker struct{}

func (compatibleChecker) CheckCompatibility(contract.CompatibilityCheckInput) contract.CompatibilityResult {
	return contract.CompatibilityResult{Verdict: contract.CompatibilityCompatible}
}

// landParitySnapshot is the fixture contract at version as the planner reads
// it: the same bytes landContractFiles commits, relative to the contract root.
func landParitySnapshot(t *testing.T, version string) contract.CandidateSnapshot {
	t.Helper()
	root := path.Dir(ciContractDescriptorPath)
	var snapshot contract.CandidateSnapshot
	for rel, content := range landContractFiles(version) {
		file := contract.CandidateFile{Path: strings.TrimPrefix(rel, root+"/"), Kind: contract.CandidateRegular, Raw: []byte(content)}
		if file.Path == contract.DescriptorPath {
			snapshot.Descriptor = file
			continue
		}
		snapshot.Files = append(snapshot.Files, file)
	}
	return snapshot
}

func landParityPlannerGate(t *testing.T, published []string, candidateVersion, target string) contract.PublicationGate {
	t.Helper()
	intent, issues := contract.BuildCandidateIntent(landParitySnapshot(t, candidateVersion))
	if len(issues) != 0 {
		t.Fatalf("BuildCandidateIntent: %+v", issues)
	}
	var history []contract.PublishedContract
	for _, v := range published {
		snapshot := landParitySnapshot(t, v)
		descriptor, err := contract.ParseDescriptor(snapshot.Descriptor.Raw)
		if err != nil {
			t.Fatalf("ParseDescriptor %s: %v", v, err)
		}
		set, issues := contract.ValidateCarriedSet(contract.ProfileContractTreeV1, descriptor, snapshot)
		if len(issues) != 0 {
			t.Fatalf("ValidateCarriedSet %s: %+v", v, issues)
		}
		history = append(history, contract.PublishedContract{Version: v, DescriptorRaw: snapshot.Descriptor.Raw, Set: set})
	}
	plan, issues := contract.PlanPublication(contract.PublicationInput{
		System: "axon", ContractID: "XC-axon-widget", Selector: "explicit:" + target,
		AuthoringFloor: "0.1.0", Candidate: intent, Published: history, AllowEmptyBump: true,
		CandidateSource: contract.CandidateSource{Kind: contract.CandidateSourceMirror, Location: "tree:widget", Fingerprint: "sha256:" + strings.Repeat("1", 64)},
		ContractRoot:    path.Dir(ciContractDescriptorPath),
	}, compatibleChecker{})
	if len(issues) != 0 {
		t.Fatalf("PlanPublication: %+v", issues)
	}
	return plan.Gate
}

func TestLandCheck_GateParity_PlannerAndLandAgree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// published is the publication history at the tip; tipDescriptor is
		// the descriptor's version there, the candidate the publish reads.
		published     []string
		tipDescriptor string
		target        string
		want          contract.PublicationGate
	}{
		{name: "first publish over a submitted candidate", tipDescriptor: "0.0.0", target: "1.0.0", want: contract.GateG1},
		{name: "minor", published: []string{"1.0.0"}, tipDescriptor: "1.0.0", target: "1.1.0", want: contract.GateNone},
		{name: "major", published: []string{"1.0.0"}, tipDescriptor: "1.0.0", target: "2.0.0", want: contract.GateG2},
		{name: "minor after a reset to 0.0.0", published: []string{"1.0.0"}, tipDescriptor: "0.0.0", target: "1.1.0", want: contract.GateNone},
		{name: "major after a reset to 0.0.0", published: []string{"1.0.0"}, tipDescriptor: "0.0.0", target: "2.0.0", want: contract.GateG2},
		{name: "maintenance on an older line", published: []string{"1.0.0", "2.0.0"}, tipDescriptor: "2.0.0", target: "1.1.0", want: contract.GateNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			planned := landParityPlannerGate(t, tt.published, tt.tipDescriptor, tt.target)
			if planned != tt.want {
				t.Fatalf("planner gate = %q, want %q", planned, tt.want)
			}
			c := landGateCandidate(t, landTip(tt.tipDescriptor, tt.published...), landContractFiles(tt.target), "publish\n")
			required, err := landRequiredGates(context.Background(), c)
			if err != nil {
				t.Fatalf("landRequiredGates: %v", err)
			}
			var want []string
			if planned != contract.GateNone {
				want = []string{string(planned)}
			}
			if !reflect.DeepEqual(required, want) {
				t.Fatalf("land check requires %v, the planner records %q", required, planned)
			}
		})
	}
}
