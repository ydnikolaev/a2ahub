package livee2e

// verdictmap.go holds the error->verdict mapping and the sentinels it reads.
//
// UNTAGGED on purpose. Every scenario file in this package sits behind
// `//go:build livee2e`, where `make check` cannot see it — and this mapping is
// exactly the kind of logic that must not hide there. On 2026-07-26 a red
// required check was reported as a TIMEOUT, which this tier's own rule reads
// as "we did not wait long enough" rather than "the product is wrong", and a
// real production blocker went unnamed for a whole run because of it. A
// classification that decides whether a finding is a finding belongs where a
// test can reach it on every commit.

import (
	"context"
	"errors"
)

// ErrCheckWaitTimedOut is WaitForRequiredCheck's sentinel. Not part of the
// pinned cross-agent API; a coordination point named in the wave report —
// wave 3-2's scenario families map it to VerdictTimedOut, never to a fail
// (spec 36 §T4: a bounded wait expiring is "we did not wait long enough",
// not "the product is wrong").
var ErrCheckWaitTimedOut = errors.New("livee2e: WaitForRequiredCheck: bounded wait expired before the check reached a terminal state")

// ErrRequiredCheckFailed is the answer a merge-wait must give when the
// required check has CONCLUDED FAILURE.
//
// It exists because on 2026-07-26 the matrix reported
// contract-integrity-registered-consumer as TIMED-OUT when the truth was a
// red gate: the scenario polled for `merged`, the required check had already
// failed, and no amount of waiting could ever change that. The row read as
// impatience or Actions latency — which is what a timeout MEANS here, by
// this file's own rule — so it took reading the pull request by hand to find
// the actual defect underneath, which was a real production blocker.
//
// A timeout that can hide a failure is the same disease as a green run over
// red CI: the verdict is not wrong so much as unable to distinguish. Distinct
// sentinel, so verdictForError maps it to VerdictFail and the row says what
// happened.
var ErrRequiredCheckFailed = errors.New("livee2e: the required check concluded FAILURE — no wait can turn that into a merge")

// ErrNoPRForBranch is returned when a submit reported success but no pull
// request exists for its deterministic branch. Distinct from a submit error:
// it is the §T6-d shape where the push landed and the PR did not, and a
// caller must render it as unknown rather than as a product defect.
var ErrNoPRForBranch = errors.New("livee2e: submit left no pull request on its branch")

// verdictForError maps an error to the honest verdict, so four scenario
// families cannot each decide differently what a timeout or an unresolved
// 5xx means. ok is false when err is nil (the caller decides pass/fail from
// what it observed) or when the error is a genuine product failure.
//
// The mapped classes are the ones spec 36 §T4 and §T6-d say must never render
// as VerdictFail: a bounded wait expiring is "we did not wait long enough", an
// exhausted 5xx retry is "we do not know what happened", and a push that
// landed without its PR appearing is the observed 504-on-OpenPR shape.
// Reporting any of them as a defect is how a tier trains its readers to
// ignore red.
//
// ErrNoPRForBranch and the context classes were missing from the first
// version, and one of the four scenario families found it: it needed the
// mapping, could not edit this file, and wrote a local extension with a
// comment saying this helper's doc claimed a coverage its switch did not have.
// It was right, so the coverage moved here rather than the other three
// families each growing their own copy — which is the whole reason this
// function exists.
//
// ErrNoBranchMatch (branchmatch.go) joins the same class as ErrNoPRForBranch:
// "the composite branch this write should have opened is not visible yet" is
// the identical §T6-d shape one level up, for the one verb whose branch a
// caller cannot look up by exact HeadRef equality. ErrAmbiguousBranchMatch is
// deliberately NOT here — an ambiguous match is an anomaly the row must
// report as a FAILURE naming the candidates, never as "we did not wait long
// enough" (brief: "a live tier that guesses is worse than one that fails").
func verdictForError(err error) (Verdict, bool) {
	switch {
	case err == nil:
		return VerdictNotRun, false
	// A concluded failure is decisive, so it must fall through to the caller's
	// own VerdictFail — never into the timed-out bucket below, which exists
	// for "we did not wait long enough".
	case errors.Is(err, ErrRequiredCheckFailed):
		return 0, false
	case errors.Is(err, ErrCheckWaitTimedOut), errors.Is(err, ErrUnknownOutcome), errors.Is(err, ErrNoPRForBranch), errors.Is(err, ErrNoBranchMatch):
		return VerdictTimedOut, true
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		// The whole-run ceiling expiring says nothing about the product.
		return VerdictTimedOut, true
	default:
		return VerdictNotRun, false
	}
}
