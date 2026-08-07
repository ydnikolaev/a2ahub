package cli

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestGateMarkerAgreesWithFold binds this package's own GateMarker column
// to fold.HumanGate, which is where the fact now lives.
//
// "approve/reject are G3-gated" was written down in three places before
// this: here, in internal/mcp's parallel verb table, and (by its absence)
// in internal/pendency, which shipped a verdict telling an agent to make a
// move CC-021 says the fold ignores and flags. Two silent copies is how the
// third one got it wrong. The registry moved to internal/fold — the layer
// all three already import — and this test is what keeps this copy honest
// rather than merely coincidentally correct.
//
// It does not assert WHICH verbs are gated; that claim belongs to fold and
// its own source citation. It asserts only that this table and that
// registry cannot drift apart.
//
// It lives in an INTERNAL test file because lifecycleVerbTable is
// unexported and this package's main test file is an external `cli_test`.
func TestGateMarkerAgreesWithFold(t *testing.T) {
	t.Parallel()

	gated := 0
	for _, spec := range lifecycleVerbTable {
		want := fold.HumanGate(spec.Transition) != ""
		if want {
			gated++
		}
		if spec.GateMarker != want {
			t.Errorf("verb %q (transition %q): GateMarker=%v, fold.HumanGate=%q — the two homes of one rule disagree",
				spec.Verb, spec.Transition, spec.GateMarker, fold.HumanGate(spec.Transition))
		}
	}
	if gated == 0 {
		t.Fatal("no verb in the table is human-gated according to fold — the registry lost its rows and a green result here would mean nothing")
	}
}
