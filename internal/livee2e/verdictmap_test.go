package livee2e

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestVerdictForErrorDistinguishesARedCheckFromImpatience is the regression
// for a classification that hid a production blocker for a whole run.
//
// On 2026-07-26 `contract-integrity-registered-consumer` reported TIMED-OUT.
// The truth was a red required check: the scenario polled for `merged`, the
// gate had already concluded failure, and no amount of waiting could change
// that. This tier's own rule says a timeout means "we did not wait long
// enough" — never "the product is wrong" — so the row read as Actions
// latency, and it took reading the pull request by hand to find the actual
// defect underneath (the validation gate blaming the wrong author).
//
// The two must therefore never share a bucket, and this is where that is
// enforced.
func TestVerdictForErrorDistinguishesARedCheckFromImpatience(t *testing.T) {
	t.Parallel()

	// A concluded failure is DECISIVE: not a timeout, so the caller renders
	// its own VerdictFail.
	if v, isTimeout := verdictForError(fmt.Errorf("wrapped: %w", ErrRequiredCheckFailed)); isTimeout {
		t.Fatalf("a red required check was classified as a timeout (verdict %v) — that reads as "+
			"impatience and is exactly how a real blocker went unnamed for a run", v)
	}

	// A bounded wait expiring genuinely IS a timeout, and must stay one:
	// calling every slow run a product failure is the opposite mistake and
	// just as corrosive.
	if _, isTimeout := verdictForError(fmt.Errorf("wrapped: %w", ErrCheckWaitTimedOut)); !isTimeout {
		t.Fatal("a bounded wait expiring must still classify as a timeout — treating slowness as a " +
			"product failure teaches people to ignore red rows")
	}
	for _, err := range []error{ErrUnknownOutcome, ErrNoPRForBranch, ErrNoBranchMatch,
		context.DeadlineExceeded, context.Canceled} {
		if _, isTimeout := verdictForError(fmt.Errorf("wrapped: %w", err)); !isTimeout {
			t.Errorf("%v stopped classifying as a timeout", err)
		}
	}

	// A nil error is not a verdict at all.
	if _, isTimeout := verdictForError(nil); isTimeout {
		t.Error("a nil error classified as a timeout")
	}

	// An unrecognised error is the caller's own failure to render, not a
	// timeout — otherwise any new error class would silently become
	// "we were impatient".
	if _, isTimeout := verdictForError(errors.New("something nobody mapped")); isTimeout {
		t.Error("an unmapped error classified as a timeout — a new error class must not default to " +
			"the bucket that means 'not the product's fault'")
	}
}
