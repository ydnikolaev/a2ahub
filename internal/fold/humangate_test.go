package fold

import "testing"

// TestHumanGateApproveReject pins the one gate this file claims to know
// about: `approve`/`reject` sit behind G3 (03-domain.md §3.7), and nothing
// else does.
func TestHumanGateApproveReject(t *testing.T) {
	t.Parallel()

	for _, transition := range []string{TApprove, TReject} {
		if got := HumanGate(transition); got != "G3" {
			t.Errorf("HumanGate(%q) = %q, want %q", transition, got, "G3")
		}
	}
}

// TestHumanGateDefaultsToUngated asserts the documented default (03-domain.md
// §3.7: "everything else agents do without humans") for a representative
// sample of ordinary transitions, plus an unknown name — a caller must never
// see a non-empty gate for a transition this file has not claimed.
func TestHumanGateDefaultsToUngated(t *testing.T) {
	t.Parallel()

	for _, transition := range []string{TNote, "submit", "acknowledge", "not-a-real-transition"} {
		if got := HumanGate(transition); got != "" {
			t.Errorf("HumanGate(%q) = %q, want \"\" (agents do this without a human)", transition, got)
		}
	}
}
