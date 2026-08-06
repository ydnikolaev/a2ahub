package lane

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEstimateForOddSample(t *testing.T) {
	e := EstimateFor([]int64{100, 300, 200})
	if e.MedianMS != 200 || e.Samples != 3 {
		t.Fatalf("got %+v", e)
	}
	if !e.Ok() {
		t.Fatalf("Ok() = false, want true for n=3 (MinSamples)")
	}
}

func TestEstimateForEvenSampleMeanOfTwoMiddle(t *testing.T) {
	e := EstimateFor([]int64{100, 200, 300, 400})
	// central values 200, 300 -> mean 250
	if e.MedianMS != 250 || e.Samples != 4 {
		t.Fatalf("got %+v", e)
	}
}

func TestEstimateForEvenSampleRoundsHalfUp(t *testing.T) {
	e := EstimateFor([]int64{100, 201})
	// mean of 100 and 201 is 150.5 -> rounds to 151
	if e.MedianMS != 151 {
		t.Fatalf("got %+v", e)
	}
}

func TestEstimateForBelowMinSamples(t *testing.T) {
	e := EstimateFor([]int64{100, 200})
	if e.Ok() {
		t.Fatalf("Ok() = true for n=2, want false (MinSamples=%d)", MinSamples)
	}
	if got := e.String(); got != "no estimate (n=2)" {
		t.Fatalf("String() = %q, want %q", got, "no estimate (n=2)")
	}
}

func TestEstimateForZeroSamples(t *testing.T) {
	e := EstimateFor(nil)
	if e.Ok() {
		t.Fatalf("Ok() = true for n=0")
	}
	if got := e.String(); got != "no estimate (n=0)" {
		t.Fatalf("String() = %q", got)
	}
}

func TestReadTelemetryMissingFileIsNoEstimateNotError(t *testing.T) {
	root := t.TempDir()
	byGate := ReadTelemetry(root)
	if len(byGate) != 0 {
		t.Fatalf("got %v, want empty map for a missing telemetry file", byGate)
	}
}

func TestReadTelemetryParsesAndGroupsByGate(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".a2a", "cache", "verify")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"gate":"readme-lint","verdict":"pass","duration_ms":120,"mode":"full","at":"2026-08-06T00:00:00Z"}
{"gate":"readme-lint","verdict":"pass","duration_ms":140,"mode":"full","at":"2026-08-06T00:01:00Z"}
{"gate":"go-test","verdict":"fail","duration_ms":9000,"mode":"full","at":"2026-08-06T00:02:00Z"}

not-json-at-all
{"gate":"","verdict":"pass","duration_ms":1,"mode":"full","at":"x"}
`
	if err := os.WriteFile(filepath.Join(dir, "telemetry.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	byGate := ReadTelemetry(root)
	if got := byGate["readme-lint"]; len(got) != 2 || got[0] != 120 || got[1] != 140 {
		t.Fatalf("readme-lint samples = %v", got)
	}
	if got := byGate["go-test"]; len(got) != 1 || got[0] != 9000 {
		t.Fatalf("go-test samples = %v", got)
	}
	if _, ok := byGate[""]; ok {
		t.Fatalf("empty-gate record should have been skipped")
	}
}
