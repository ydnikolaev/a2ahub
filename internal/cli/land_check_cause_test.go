package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// The host that runs a land check prefixes every error with "land check could
// not judge the write", so a check that says so itself prints it twice.
const landCheckHostPrefix = "could not judge"

func TestDecodeLandCheckRun_ReportEntryErrorIsTheCause(t *testing.T) {
	t.Parallel()
	const cause = "contract XC-axon-export: cannot read schema/**: file exceeds the bound"
	stdout, err := json.Marshal(ciReport{Mode: "v3-pr", Artifacts: []validateReport{
		{Path: "axon/provides/export/contract.md", Error: cause},
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, judged := decodeLandCheckRun(1, stdout, "")
	if judged == nil {
		t.Fatal("a report with an entry error and no violation must not pass")
	}
	if !strings.Contains(judged.Error(), cause) {
		t.Fatalf("error = %q, want it to carry the report's own cause %q", judged, cause)
	}
}

func TestLandCheckErrorsLeaveThePrefixToTheHost(t *testing.T) {
	t.Parallel()
	report, err := json.Marshal(ciReport{Mode: "v3-pr", Artifacts: []validateReport{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		exit   int
		stdout []byte
	}{
		{name: "no violation in the report", exit: 1, stdout: report},
		{name: "no readable report", exit: 1, stdout: []byte("{")},
		{name: "another exit", exit: 2, stdout: report},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, got := decodeLandCheckRun(tc.exit, tc.stdout, "boom")
			if got == nil || strings.Contains(got.Error(), landCheckHostPrefix) || !strings.Contains(got.Error(), "boom") {
				t.Fatalf("error = %v, want the bare cause carrying stderr", got)
			}
		})
	}

	t.Run("approval gates", func(t *testing.T) {
		t.Parallel()
		_, got := landGateApproval(context.Background(), host.LandCandidate{
			CheckoutDir: t.TempDir(), BaseSHA: strings.Repeat("a", 40), MergeSHA: strings.Repeat("b", 40),
		})
		if got == nil || strings.Contains(got.Error(), landCheckHostPrefix) {
			t.Fatalf("error = %v, want the bare cause", got)
		}
	})
}
