package livee2e

import (
	"errors"
	"time"
)

// Outcome classifies the result of one HTTP attempt against GitHub, so a
// caller can decide RETRY vs GIVE UP without re-deriving GitHub's status
// vocabulary at every call site (spec 36 §T6-d).
//
// Like Verdict (verdict.go), Outcome's zero value is its most pessimistic
// member — here OutcomeUnknown. The polarity is the point: an Outcome nobody
// set means "we do not know what happened", which obliges the caller to
// re-read state, and re-reading when the answer was in fact known costs one
// request. The other orderings both fail open — a zero-valued OutcomeOK would
// let an unclassified attempt read as an accepted write, and a zero-valued
// OutcomeFatal would report a defect where there was only silence. This
// package's fail-closed property is structural rather than disciplinary
// (doc.go), and one enum with the opposite polarity would be the exception
// that makes the rule unreadable.
type Outcome int

const (
	// OutcomeUnknown — the request may or may not have reached GitHub, or
	// GitHub may have accepted it and failed only to say so (a transient
	// 5xx, a rate limit, or a transport-level error such as a timeout).
	// NEVER render this as a failure: re-read state before concluding
	// anything (spec 36 §T6-d — the observed 504-on-OpenPR that had in fact
	// landed, and the 500-on-push that had not). It is the zero value, so
	// this is also what an unset Outcome means.
	OutcomeUnknown Outcome = iota
	// OutcomeOK — the request completed and GitHub accepted it (2xx). Safe
	// to treat the write as landed.
	OutcomeOK
	// OutcomeFatal — GitHub answered and refused the request in a way a
	// retry cannot fix (a 4xx that is not a rate limit): bad auth, no
	// permission, not found, validation failure. Retrying wastes the
	// caller's retry budget on an answer that will not change.
	OutcomeFatal
)

// String renders the outcome for logs and failure messages — the same
// "stable report tokens" convention as Verdict.String (verdict.go).
//
// An out-of-range value renders as "unknown" rather than as its number: the
// same pessimistic default as the zero value, so a token that reached a log
// or a report row can never overstate what was observed.
func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeUnknown:
		return "unknown"
	case OutcomeFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// ErrUnknownOutcome is returned by a retrying caller when every attempt
// classified as OutcomeUnknown and the retry budget (MaxAttempts) is
// exhausted. It is NOT a failure verdict. A caller that receives it must
// RE-READ state (look the resource up by its deterministic identity — e.g.
// `a2a submit`'s idempotent-retry path looks a PR up by its head branch
// rather than trusting the response it never got) before concluding
// anything. Render the scenario as VerdictTimedOut, never VerdictFail: "we
// could not confirm" and "the product is wrong" are different reports, and
// this sentinel exists so a caller cannot collapse them by accident.
var ErrUnknownOutcome = errors.New("livee2e: retries exhausted on an unknown outcome — re-read state before concluding")

// ClassifyStatus maps one HTTP attempt's outcome to Outcome.
//
// Precedence, in order:
//
//  1. transportErr != nil → OutcomeUnknown. A transport-level failure (a
//     dropped connection, a client-side timeout) means the request may still
//     have reached GitHub; the status code, if any, is not trustworthy
//     alongside a transport error, so it is not inspected. This is
//     deliberately blanket: no error-type special-casing.
//  2. 2xx (200-299) → OutcomeOK.
//  3. 5xx (500-599) → OutcomeUnknown. GitHub can fail mid-write and answer
//     5xx AFTER the write landed (spec 36 §T6-d: a 504 on OpenPR that had in
//     fact created the PR, and a 500 on git push that had not) — a 5xx
//     never means "did not happen," only "did not confirm."
//  4. 429 → OutcomeUnknown. A rate limit is definitionally retryable once the
//     window resets.
//  5. 403 → OutcomeFatal. GitHub also uses 403 for BOTH a true rate limit
//     ("secondary rate limit exceeded") and a hard permission refusal
//     ("Resource not accessible by personal access token" — observed
//     2026-07-24 against a pending fine-grained PAT, spec 36 §9.5.3), and
//     status code alone cannot tell them apart. Rather than guess,
//     ClassifyStatus takes the fail-closed reading (Fatal, do not burn the
//     retry budget on a permission problem retrying cannot fix). A caller
//     that has the response headers CAN distinguish the two — GitHub sets
//     `Retry-After` or `x-ratelimit-remaining: 0` on the rate-limit case —
//     and should classify that case as OutcomeUnknown itself before ever
//     calling ClassifyStatus with a bare 403, rather than this function
//     guessing from a status code alone.
//  6. Every other 4xx → OutcomeFatal. Validation failures, not-found, bad
//     auth: none of these change on retry.
//  7. Anything else (0, 1xx, 3xx, or out of range) → OutcomeFatal. The
//     default is NEVER OutcomeOK — an unrecognised status is not evidence of
//     success.
func ClassifyStatus(status int, transportErr error) Outcome {
	if transportErr != nil {
		return OutcomeUnknown
	}
	switch {
	case status >= 200 && status <= 299:
		return OutcomeOK
	case status >= 500 && status <= 599:
		return OutcomeUnknown
	case status == 429:
		return OutcomeUnknown
	case status == 403:
		return OutcomeFatal
	case status >= 400 && status <= 499:
		return OutcomeFatal
	default:
		return OutcomeFatal
	}
}

// MaxAttempts bounds the retry budget for an OutcomeUnknown result. Four
// attempts means three delays (RetryDelay(1), RetryDelay(2), RetryDelay(3))
// between them — see RetryDelay's worst-case wall time.
const MaxAttempts = 4

// retryBase is the first retry's delay; retryCap is the hard ceiling no
// delay may exceed, regardless of attempt.
const (
	retryBase = 2 * time.Second
	retryCap  = 8 * time.Second
)

// RetryDelay returns the delay to wait AFTER attempt N has failed, before
// attempt N+1 — so RetryDelay(1) is the wait after the FIRST attempt, never
// a wait before it. attempt <= 0 is clamped to 1 (treated as "after the
// first attempt") rather than panicking or returning zero: a caller
// mis-indexing by one gets the smallest real delay, not a hot loop.
//
// The schedule is a fixed, deterministic bounded exponential — no jitter, no
// rand: a flaky-timing test is worse than a fixed schedule an operator can
// read.
//
//	attempt   delay
//	1         2s
//	2         4s
//	3         8s   (== retryCap; every later attempt also returns 8s)
//	4+        8s
//
// With MaxAttempts = 4 (attempts 1-4, three delays between them: after 1,
// after 2, after 3), the worst-case wall time a caller retrying to
// exhaustion spends sleeping is 2s + 4s + 8s = 14s. That is the number an
// operator budgets, not MaxAttempts alone.
func RetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	// retryBase << (attempt-1), computed via multiplication rather than a
	// bit shift so a very large attempt cannot overflow into a negative
	// Duration (which time.Sleep would treat as "return immediately" — a
	// silent hot loop, exactly the failure mode a hard cap exists to
	// prevent). Clamping BEFORE multiplying keeps every intermediate value
	// bounded.
	shift := attempt - 1
	if shift > 32 {
		shift = 32
	}
	delay := retryBase
	for i := 0; i < shift; i++ {
		if delay >= retryCap {
			return retryCap
		}
		delay *= 2
	}
	if delay > retryCap {
		return retryCap
	}
	return delay
}
