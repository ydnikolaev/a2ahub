package host

// retry.go owns ONE classification of an HTTP attempt against GitHub, and the
// bounded schedule for retrying the attempts that deserve it.
//
// It lives in the PRODUCT, not in the live tier, and that placement is the
// point. This logic was written first in internal/livee2e, where it correctly
// treated a 5xx as UNKNOWN — never a failure. The product had no equivalent,
// so during GitHub's Pull Requests outage on 2026-07-24 every `a2a submit`
// died instantly with
//
//	host: OpenPR: github api request failed: status 500:
//
// an empty body and no hint. The push had landed; only the pull request had
// not. An agent reading that cannot tell a platform outage from a real
// refusal, and the correct next move — re-run, which is safe because the
// funnel is idempotent by head branch — was named nowhere.
//
// The tier being right about something the product is wrong about is the
// failure this file exists to make impossible: internal/livee2e now aliases
// these symbols rather than keeping its own copy, so the two cannot disagree
// and deleting anything here breaks the tier's build.

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Outcome classifies the result of one HTTP attempt against GitHub, so a
// caller can decide RETRY vs GIVE UP without re-deriving GitHub's status
// vocabulary at every call site.
//
// Outcome's zero value is its most pessimistic member — OutcomeUnknown. The
// polarity is the point: an Outcome nobody set means "we do not know what
// happened", which obliges the caller to re-read state, and re-reading when
// the answer was in fact known costs one request. The other orderings both
// fail open — a zero-valued OutcomeOK would let an unclassified attempt read
// as an accepted write, and a zero-valued OutcomeFatal would report a defect
// where there was only silence.
type Outcome int

const (
	// OutcomeUnknown — the request may or may not have reached GitHub, or
	// GitHub may have accepted it and failed only to say so (a transient
	// 5xx, a rate limit, or a transport-level error such as a timeout).
	// NEVER render this as a failure: re-read state before concluding
	// anything — the observed 504-on-OpenPR that had in fact landed, and
	// the 500-on-push that had not. It is the zero value, so this is also
	// what an unset Outcome means.
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

// String renders the outcome for logs and failure messages.
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
//     5xx AFTER the write landed (a 504 on OpenPR that had in fact created
//     the PR, and a 500 on git push that had not) — a 5xx never means "did
//     not happen," only "did not confirm."
//  4. 429 → OutcomeUnknown. A rate limit is definitionally retryable once the
//     window resets.
//  5. 403 → OutcomeFatal. GitHub also uses 403 for BOTH a true rate limit
//     ("secondary rate limit exceeded") and a hard permission refusal
//     ("Resource not accessible by personal access token" — observed
//     2026-07-24 against a pending fine-grained PAT), and status code alone
//     cannot tell them apart. Rather than guess, ClassifyStatus takes the
//     fail-closed reading (Fatal, do not burn the retry budget on a
//     permission problem retrying cannot fix). A caller that has the response
//     headers CAN distinguish the two — GitHub sets `Retry-After` or
//     `x-ratelimit-remaining: 0` on the rate-limit case — and should classify
//     that case itself before ever calling ClassifyStatus with a bare 403,
//     rather than this function guessing from a status code alone. That is
//     what classifyResponse (github.go) does.
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

// RetryTotalCap is the hard ceiling on the WHOLE retry window for one call,
// including a server-supplied Retry-After. It is what an operator budgets:
// past this, a command that looks hung is worse than a command that reports a
// transient failure and says re-running is safe.
//
// Sized from RetryDelay's own worst case (2s + 4s + 8s = 14s of sleeping) with
// room for the attempts themselves. A `Retry-After` longer than what remains
// is not honoured in full — the retry stops instead, because a well-behaved
// GitHub asking for two minutes and a CLI hanging for two minutes are
// different products.
const RetryTotalCap = 20 * time.Second

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

// retryAfterDelay reads GitHub's `Retry-After` header, which it sets on a
// genuine rate limit. Only the delta-seconds form is honoured; the HTTP-date
// form is ignored rather than parsed against a clock this package does not
// own, and an unparseable or non-positive value yields ok=false so the caller
// falls back to its own schedule.
//
// The value is NOT trusted beyond RetryTotalCap — see the caller. A header is
// a request, not an instruction to hang.
func retryAfterDelay(h http.Header) (time.Duration, bool) {
	raw := h.Get("Retry-After")
	if raw == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// rateLimited reports whether a 403 is in fact a rate limit rather than a
// permission refusal — the one case ClassifyStatus's doc comment says a
// caller holding the response headers must decide for itself instead of
// letting a bare status code guess.
//
// GitHub advertises the rate-limit case two ways, and either is enough:
// `Retry-After` on a secondary limit, or `x-ratelimit-remaining: 0` on the
// primary one. A 403 with neither is a permission problem, and retrying it
// would burn the budget on an answer that will not change.
func rateLimited(h http.Header) bool {
	if _, ok := retryAfterDelay(h); ok {
		return true
	}
	return h.Get("X-RateLimit-Remaining") == "0"
}

// ErrTransient marks a request that failed in a way that says nothing about
// whether the write landed: a 5xx, a rate limit, or a transport error, having
// survived the bounded retry above.
//
// It is wrapped INTO the error a caller receives so `errors.Is` resolves it,
// and the message deliberately spells out the three things an agent needs and
// previously had to guess: that the class is transient, that the write may or
// may not have landed, and that re-running is safe. The last is a property of
// this system, not a platitude — the write funnel is idempotent by head
// branch, so a re-run finds the pull request the failed attempt may have
// created rather than opening a second one. Verified during the 2026-07-24
// outage: the re-run produced no duplicate.
var ErrTransient = errors.New("host: github is failing transiently — the write may or may not have landed; " +
	"re-running is safe (the write funnel is idempotent by head branch, so a re-run adopts an " +
	"already-open pull request instead of opening a second one)")

// ErrRetriedWriteWasDuplicate is the specific, and initially confusing, shape
// a retry can produce: the first attempt DID land, GitHub failed only to say
// so, and the retry was then refused as a duplicate.
//
// Without this the caller would see a bare 422 "already exists" for a write it
// believes it never made. Naming it converts that into the most useful fact
// available: the write is IN, and a re-run will find it.
var ErrRetriedWriteWasDuplicate = errors.New("host: a retry after a transient failure was refused as a duplicate — " +
	"which means the FIRST attempt landed after all; re-run and the funnel will adopt it")
