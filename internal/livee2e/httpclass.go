package livee2e

// httpclass.go used to OWN this tier's classification of an HTTP attempt
// against GitHub. It now imports the product's, and holds only the one piece
// of vocabulary that is genuinely this tier's own (ErrUnknownOutcome, which is
// about how a REPORT ROW is rendered).
//
// The classification was written here first, and it was right: a 5xx is
// UNKNOWN, never a failure. The product had no equivalent, so during GitHub's
// Pull Requests outage on 2026-07-24 every `a2a submit` died on a bare
// `status 500:` while this tier, one package over, already knew that meant
// "did not confirm" rather than "did not happen".
//
// A tier that is right about something the product is wrong about is worse
// than useless — it certifies the product against a rule the product does not
// follow. So internal/host owns it (AC-1041.1, spec 44 §5: "this is the SSOT
// to MOVE, not to copy"), and these aliases are what makes the two unable to
// disagree: delete or change ClassifyStatus, Outcome, MaxAttempts or
// RetryDelay over there and this package stops compiling. That is the teeth,
// and it is structural rather than a convention somebody has to remember.

import (
	"errors"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// Outcome is host.Outcome — this tier keeps no classification of its own. See
// there for the vocabulary and for why its zero value is the pessimistic one.
type Outcome = host.Outcome

const (
	// OutcomeUnknown — the request may or may not have reached GitHub. Never
	// render it as a failure; re-read state first. It is the zero value, so
	// this is also what an unset Outcome means.
	OutcomeUnknown = host.OutcomeUnknown
	// OutcomeOK — GitHub accepted it (2xx); the write landed.
	OutcomeOK = host.OutcomeOK
	// OutcomeFatal — refused in a way a retry cannot fix.
	OutcomeFatal = host.OutcomeFatal
)

// MaxAttempts and the retry schedule are the product's too — so a scenario
// here waits exactly as long as a user's own `a2a submit` would, and a change
// to the product's patience cannot leave this tier asserting against the old
// one.
const MaxAttempts = host.MaxAttempts

// ClassifyStatus maps one HTTP attempt's outcome to Outcome (host.ClassifyStatus).
func ClassifyStatus(status int, transportErr error) Outcome {
	return host.ClassifyStatus(status, transportErr)
}

// RetryDelay returns the delay to wait AFTER attempt N has failed
// (host.RetryDelay).
func RetryDelay(attempt int) time.Duration {
	return host.RetryDelay(attempt)
}

// ErrUnknownOutcome stays HERE, because it is about this tier's reporting and
// not about HTTP. It is returned by a retrying caller when every attempt
// classified as OutcomeUnknown and the retry budget was exhausted.
//
// It is NOT a failure verdict. A caller that receives it must RE-READ state
// (look the resource up by its deterministic identity — e.g. a PR by its head
// branch rather than trusting a response it never got) before concluding
// anything. Render the scenario as VerdictTimedOut, never VerdictFail: "we
// could not confirm" and "the product is wrong" are different reports, and
// this sentinel exists so a caller cannot collapse them by accident.
//
// The product's own counterpart is host.ErrTransient, which says the same
// thing to a USER — including that re-running is safe, because the write
// funnel is idempotent by head branch.
var ErrUnknownOutcome = errors.New("livee2e: retries exhausted on an unknown outcome — re-read state before concluding")
