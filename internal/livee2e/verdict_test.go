package livee2e

import "testing"

// The zero value IS the contract (AC-962.2): a scenario struct that was built
// and never executed must read as "not run", with no code path responsible
// for remembering to set it.
func TestZeroVerdictIsNotRun(t *testing.T) {
	t.Parallel()
	var v Verdict
	if v != VerdictNotRun {
		t.Fatalf("zero Verdict is %v, want VerdictNotRun", v)
	}
	if v.String() != "not-run" {
		t.Fatalf("zero Verdict renders %q, want %q", v.String(), "not-run")
	}
	if v.IsPass() {
		t.Fatal("zero Verdict counts as a pass — the tier could report green having run nothing")
	}
}

// Only one verdict may be counted as green. Asserted exhaustively so that
// adding a verdict later without revisiting IsPass shows up here.
func TestOnlyPassCountsAsGreen(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		v    Verdict
		want string
		pass bool
	}{
		{VerdictNotRun, "not-run", false},
		{VerdictPass, "pass", true},
		{VerdictFail, "fail", false},
		{VerdictTimedOut, "timed-out", false},
		{VerdictRefused, "refused", false},
		{VerdictSkippedProvider, "skipped-provider", false},
	} {
		if got := tc.v.String(); got != tc.want {
			t.Errorf("Verdict(%d).String() = %q, want %q", tc.v, got, tc.want)
		}
		if got := tc.v.IsPass(); got != tc.pass {
			t.Errorf("Verdict(%q).IsPass() = %v, want %v", tc.want, got, tc.pass)
		}
	}
}

// TestSkippedProviderIsNeverAPass is spec 09 §5's own guard on the verdict
// itself: a logic run that could not judge a provider row must never render
// as green. Appended after VerdictUnverified deliberately — see
// VerdictSkippedProvider's own doc comment — so this test also pins that no
// EARLIER verdict's int value shifted.
func TestSkippedProviderIsNeverAPass(t *testing.T) {
	t.Parallel()
	if VerdictSkippedProvider != 6 {
		t.Fatalf("VerdictSkippedProvider = %d, want 6 (appended after VerdictUnverified=5 without shifting any existing verdict)", VerdictSkippedProvider)
	}
	if got := VerdictSkippedProvider.String(); got != "skipped-provider" {
		t.Fatalf("VerdictSkippedProvider.String() = %q, want %q", got, "skipped-provider")
	}
	if VerdictSkippedProvider.IsPass() {
		t.Fatal("VerdictSkippedProvider counts as a pass — a logic tier that could not judge a provider row would report green")
	}
}

// An unrecognised value is a programming error, and the fail-closed reading
// of "I do not know what this is" is never "pass".
func TestUnknownVerdictDegradesToNotRun(t *testing.T) {
	t.Parallel()
	v := Verdict(99)
	if v.String() != "not-run" {
		t.Errorf("out-of-range Verdict renders %q, want %q", v.String(), "not-run")
	}
	if v.IsPass() {
		t.Error("out-of-range Verdict counts as a pass")
	}
}
