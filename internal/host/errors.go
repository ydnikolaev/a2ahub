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

	// ErrPushForbidden REFINES ErrPushRejected: the remote refused the push
	// because the credential may not write to that repository (a
	// non-collaborator, a revoked scope), as opposed to a non-fast-forward
	// or a protected ref. Every ErrPushForbidden error also satisfies
	// errors.Is(err, ErrPushRejected) — callers that only care that the
	// push failed keep working unchanged; the fork fallback (P28) is the
	// one caller that needs the distinction, and classifying git's stderr
	// vocabulary belongs here, not in internal/space.
	ErrPushForbidden = errors.New("host: push forbidden (no write access)")

	// ErrForkUnavailable is returned by the optional Forker capability when
	// the credential holder's fork can neither be found nor created — the
	// caller must fall back to the manual fork+PR path, never to silence.
	ErrForkUnavailable = errors.New("host: fork unavailable")

	// ErrRequestFailed is returned when a GitHub REST/GraphQL call returns
	// a non-2xx status or a transport-level failure.
	ErrRequestFailed = errors.New("host: github api request failed")

	// ErrGraphQLFailed is returned when a GraphQL call answers HTTP 200 —
	// which GraphQL does even for a failed operation — while reporting the
	// failure in the response body's `errors` array. A separate sentinel
	// from ErrRequestFailed because the transport SUCCEEDED; only the
	// operation did not, and a caller may want to recognise a specific
	// refusal (see IsAutoMergeAlreadyClean).
	ErrGraphQLFailed = errors.New("host: github graphql operation failed")

	// ErrInvalidRequest is returned when a caller omits a required field
	// on a request value.
	ErrInvalidRequest = errors.New("host: invalid request")

	// ErrPRNotMergeable is returned when GitHub answers HTTP 405 to a direct
	// merge attempt (Merger.MergePR): the PR cannot be merged right now. A
	// distinct sentinel from ErrPushRejected/ErrRequestFailed because a
	// caller (the funnel's guard) must be able to tell "the merge attempt
	// itself was refused" apart from every other request failure.
	ErrPRNotMergeable = errors.New("host: pull request not mergeable")

	// ErrPRHeadMoved is returned when GitHub answers HTTP 409 to a direct
	// merge attempt: the PR's head moved since the caller last read it, so
	// the commit that would be merged is not the one the caller verified
	// green. A caller MUST NOT report this outcome as a landed write.
	ErrPRHeadMoved = errors.New("host: pull request head moved")

	// ErrMergeMethodUnavailable is returned by Merger.MergePR when the
	// repository allows no merge method at all (merge commit, squash, and
	// rebase all disabled) — there is no way to land the PR, and guessing
	// would be wrong, so this is a typed, actionable error rather than a
	// best-effort attempt.
	ErrMergeMethodUnavailable = errors.New("host: repository allows no merge method")
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
