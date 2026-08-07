//go:build livee2e

package livee2e

import "testing"

// TestPRVisibilityIntervalIsChargedToTheSeamThatHasAWindow guards the pricing
// of the retry loop that dominated this tier's cost.
//
// The 5s interval exists for ONE reason, stated where it is declared: a real
// GitHub POST /pulls and the LIST /pulls that follows are not an atomic
// visibility boundary. A local seam — testkit/fakegithub over loopback, from
// in-process state — has no such window, so every second spent waiting there
// buys a guarantee already given for free.
//
// It was not free. A broken lookup burned the full 18 × 5s per call and pushed
// the logic tier from ~5 minutes past a 20-minute timeout; short of that, every
// legitimately-retried lookup in the offline tier still paid the live host's
// tax. Without this gate the cheap interval is one plausible-looking edit away
// from being reverted, and the cost would come back as "the matrix got slow"
// with nothing pointing here.
//
// It reads the SEAM rather than the tier deliberately. harness.Tier's own doc
// forbids scenario bodies branching on the tier, and the reason generalizes:
// the tier governs what a run may ASSERT, while "does this host have a
// visibility window" is a fact about the host.
func TestPRVisibilityIntervalIsChargedToTheSeamThatHasAWindow(t *testing.T) {
	t.Parallel()

	live := NewLiveHostSeam("some-org", "some-repo")
	if !live.IsRealGitHub() {
		t.Fatal("NewLiveHostSeam did not produce a real-GitHub seam — this test would then " +
			"assert the live pricing against a seam that is not live, and prove nothing")
	}
	if got := prVisibilityInterval(live); got != prVisibilityPollInterval {
		t.Fatalf("live seam interval = %v, want %v — a real GitHub run must keep the "+
			"consistency window the 2026-07-29 false failure bought", got, prVisibilityPollInterval)
	}

	local := NewLocalHostSeam("http://127.0.0.1:0", t.TempDir())
	if local.IsRealGitHub() {
		t.Fatal("NewLocalHostSeam produced a seam that claims to be real GitHub")
	}
	if got := prVisibilityInterval(local); got != prVisibilityPollIntervalLocal {
		t.Fatalf("local seam interval = %v, want %v — the offline tier must not pay for an "+
			"eventual-consistency window its host does not have", got, prVisibilityPollIntervalLocal)
	}

	if prVisibilityPollIntervalLocal >= prVisibilityPollInterval {
		t.Fatalf("the local interval (%v) is not cheaper than the live one (%v); the whole "+
			"point of the split is gone", prVisibilityPollIntervalLocal, prVisibilityPollInterval)
	}
	if prVisibilityPollIntervalLocal <= 0 {
		t.Fatal("a zero local interval turns the retry loop into a spin — the loop's shape is " +
			"what is being reused, only its price should change")
	}
}
