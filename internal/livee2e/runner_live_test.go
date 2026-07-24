//go:build livee2e

package livee2e

import (
	"fmt"
	"os"
	"testing"
)

// EnvReportPath optionally names a file the rendered report is also written
// to. Unset means stdout only — no default path, so a run can never quietly
// drop an artifact into the working tree.
const EnvReportPath = "A2A_LIVE_E2E_REPORT"

// TestLiveMatrix is the live tier's ONLY entry point, reachable solely
// through `make live-e2e` (which supplies -tags=livee2e). It exists in the
// tagged build so that `go test ./...` — what `make check` runs — cannot
// compile it, let alone execute it (AC-962.1).
//
// It is fail-closed end to end (AC-962.2): the matrix is declared before
// anything is read, so every cell already carries VerdictNotRun; a
// configuration failure aborts with the reason and the report exits non-zero
// having reported nothing as passed. There is no path through this function
// that reaches a zero exit without a passing cell.
func TestLiveMatrix(t *testing.T) {
	run := NewRunFor(os.Getenv(EnvOrg), DefaultRepo, Catalogue())

	cfg, err := LoadConfig(os.Getenv)
	switch {
	case err != nil:
		run.Abort(err.Error())
	default:
		// Wave 3 replaces this with provisioning + the scenario drivers.
		// Until then the honest outcome is that nothing ran — reported as
		// such, and non-zero, rather than as an empty success.
		_ = cfg
		run.Abort("live scenarios are not implemented yet (spec 36 wave 3); the matrix was declared and nothing was run")
	}

	report := run.Report()
	rendered := report.Render()
	fmt.Fprint(os.Stdout, "\n"+rendered)

	if path := os.Getenv(EnvReportPath); path != "" {
		if writeErr := os.WriteFile(path, []byte(rendered), 0o644); writeErr != nil {
			t.Errorf("write report to %s: %v", path, writeErr)
		}
	}

	if code := report.ExitCode(); code != 0 {
		t.Fatalf("live-e2e: %d cell(s) declared, exit code %d — see the report above", len(report.Results), code)
	}
}
