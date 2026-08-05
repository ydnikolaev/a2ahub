package livee2e

// Verdict is a scenario's outcome. It is a CLOSED enum whose ZERO VALUE is
// VerdictNotRun — fail-closed by construction rather than by discipline
// (spec 36 AC-962.2, §T3 "a run that cannot reach GitHub reports 'not run',
// never 'pass'").
//
// The zero value matters more than it looks: a scenario that panicked, was
// never dispatched, or whose struct was built and abandoned carries the
// verdict of a scenario that did not run. There is no code path that has to
// remember to set it. A `bool passed` field would have defaulted the same
// cases to "did not pass", which reads as a failure and would train us to
// ignore the report; VerdictNotRun says the truth, which is that we do not
// know.
type Verdict int

const (
	// VerdictNotRun is the zero value: the scenario did not execute. Either
	// the tier was not configured, preflight refused, or an earlier scenario
	// aborted the run. Never rendered as a pass and never as a failure.
	VerdictNotRun Verdict = iota
	// VerdictPass — the scenario ran and observed what it expected.
	VerdictPass
	// VerdictFail — the scenario ran and observed something else. This is
	// the only verdict that asserts a defect.
	VerdictFail
	// VerdictTimedOut — the scenario ran but a bounded wait expired before
	// GitHub reached a terminal state (Actions latency, rate limiting).
	// Deliberately distinct from VerdictFail: "we did not wait long enough"
	// and "the product is wrong" are different reports, and collapsing them
	// is how a flaky tier gets ignored.
	VerdictTimedOut
	// VerdictRefused — a guard declined to run the scenario (preflight,
	// org mismatch, an admin participant). The scenario proved nothing, and
	// saying so is the point: a refusal reported as a skip would be
	// indistinguishable from a pass in a summary line.
	VerdictRefused
	// VerdictUnverified is reserved for the two optional P7 provider branches.
	// It is evidence of a named provider limitation, never a pass.
	VerdictUnverified
	// VerdictSkippedProvider marks a TierProvider row (or, in a future wave,
	// a TierLogic row's own ProviderAssertions carve-out) that the LOGIC
	// tier declined to judge, because the assertion is GitHub's own to
	// decide and a fake would have invented the answer (spec 09 §5, plan
	// D-2). Never a pass — IsPass() stays false — and it is what lets the
	// logic tier report a complete, honest run without ever claiming to
	// have judged what only the live tier may. Appended after
	// VerdictUnverified so no existing verdict's int value shifts: report
	// tokens are compared by golden fixtures.
	VerdictSkippedProvider
)

// String renders the verdict for the report. Values are stable report tokens
// — golden fixtures compare against them, so renaming one is a fixture
// change, not a cosmetic edit.
func (v Verdict) String() string {
	switch v {
	case VerdictNotRun:
		return "not-run"
	case VerdictPass:
		return "pass"
	case VerdictFail:
		return "fail"
	case VerdictTimedOut:
		return "timed-out"
	case VerdictRefused:
		return "refused"
	case VerdictUnverified:
		return "unverified"
	case VerdictSkippedProvider:
		return "skipped-provider"
	default:
		// An out-of-range Verdict is a programming error, and the
		// fail-closed reading of "I do not recognise this" is NOT "pass".
		return "not-run"
	}
}

// IsPass reports whether the verdict may be counted as a success. Every
// caller that summarises a run must ask through this method rather than
// comparing to VerdictFail, so that adding a verdict later cannot silently
// widen what counts as green.
func (v Verdict) IsPass() bool { return v == VerdictPass }
