package host

import "errors"

// Sentinel errors, one per failure class (P1 idiom: internal/artifact).
// Callers use errors.Is against these; a typed *Error carries the
// operation and offending input on top.
var (
	// ErrPushRejected is returned when the git host refuses a branch push
	// (revoked/expired credential, non-fast-forward, protected ref, etc.,
	// CC-061). No partial state is left behind — a rejected push never
	// updates the remote ref.
	ErrPushRejected = errors.New("host: push rejected")

	// ErrRequestFailed is returned when a GitHub REST/GraphQL call returns
	// a non-2xx status or a transport-level failure.
	ErrRequestFailed = errors.New("host: github api request failed")

	// ErrInvalidRequest is returned when a caller omits a required field
	// on a request value.
	ErrInvalidRequest = errors.New("host: invalid request")
)

// Error is the small typed error every exported operation in this package
// returns on failure. It always wraps one of the sentinels above so callers
// can use errors.Is/As; it never panics on bad input.
type Error struct {
	// Op names the failing operation (e.g. "PushBranch", "OpenPR").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel (see the vars above).
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "host: " + e.Op + ": " + e.Err.Error()
	}
	return "host: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
