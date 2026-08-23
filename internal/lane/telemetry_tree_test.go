package lane

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadTelemetryToleratesRecordsWithoutTree pins the compatibility half of
// release-cost-2026-08 P2's schema delta. The telemetry stream is a ring
// buffer: at any moment it holds records written by the verify.sh that was
// current when each ran, so a line predating the `tree` field is not an edge
// case, it is the majority of the file for a while after the change ships.
//
// The assertion is deliberately about the ESTIMATES rather than about the
// field: a reader that choked on an old line would blind `make lane`'s
// measured medians for every phase, which is how a compatibility break in a
// stream like this actually gets noticed — as a lane that stopped saying what
// anything costs.
func TestReadTelemetryToleratesRecordsWithoutTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(TelemetryPath)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lines := "" +
		`{"gate":"logic-e2e","verdict":"pass","duration_ms":100,"mode":"full","at":"2026-08-20T00:00:00Z"}` + "\n" +
		`{"gate":"logic-e2e","verdict":"pass","duration_ms":300,"mode":"full","at":"2026-08-21T00:00:00Z"}` + "\n" +
		`{"gate":"logic-e2e","verdict":"pass","duration_ms":200,"mode":"full","at":"2026-08-23T00:00:00Z","tree":"abc123"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, TelemetryPath), []byte(lines), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := ReadTelemetry(root)
	samples, ok := got["logic-e2e"]
	if !ok {
		t.Fatalf("ReadTelemetry dropped the gate entirely: %v", got)
	}
	if len(samples) != 3 {
		t.Fatalf("ReadTelemetry kept %d of 3 records; a line without \"tree\" must still parse: %v", len(samples), samples)
	}
	if est := EstimateFor(samples); est.MedianMS != 200 {
		t.Fatalf("median = %d, want 200 — the mixed old/new stream must estimate as one set", est.MedianMS)
	}
}
