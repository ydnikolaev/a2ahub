package host

import (
	"errors"
	"strings"
)

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

	// ErrMergeMethodUnavailable is returned before OpenPR, EnableAutoMerge,
	// or MergePR writes when the repository allows no merge method at all
	// (merge commit, squash, and rebase all disabled). There is no way to land
	// the PR, and guessing would be wrong.
	ErrMergeMethodUnavailable = errors.New("host: repository allows no merge method")
)

// PartialWriteStage is the last externally durable boundary proven by a host
// operation that later failed. It is host-owned so this package does not
// import the space orchestration layer.
type PartialWriteStage string

const (
	// PartialWriteStagePRCreated means the pull request was created before failure.
	PartialWriteStagePRCreated PartialWriteStage = "pr-created"
)

// PartialWriteOperation is a stable, machine-safe name for the unfinished
// provider operation. It never contains request or provider response data.
type PartialWriteOperation string

const (
	// PartialWriteOperationEnableAutoMerge identifies the auto-merge mutation.
	PartialWriteOperationEnableAutoMerge PartialWriteOperation = "enable-auto-merge"
)

// PartialWriteError reports a durable partial result plus the operation that
// did not complete. Error deliberately does not render Err: GraphQL response
// bodies can contain provider or untrusted data. The cause remains available
// to trusted Go callers through errors.Is/As and Unwrap.
type PartialWriteError struct {
	Stage     PartialWriteStage
	Operation PartialWriteOperation
	Err       error
}

func (e *PartialWriteError) Error() string {
	return packagePrefix + "partial write at " + string(e.Stage) + ": " + string(e.Operation) + " failed"
}

func (e *PartialWriteError) Unwrap() error { return e.Err }

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

// packagePrefix is what every sentinel in this file opens with, so that a
// sentinel printed on its own still says which package it came from.
const packagePrefix = "host: "

// Error renders as `host: <Op>[: <Input>]: <wrapped>`.
//
// The wrapped text's own leading "host: " is TRIMMED, because every sentinel in
// this file carries one and the result was `host: OpenPR: host: github api
// request failed: …` — the prefix twice, in the middle of the sentence a user
// actually reads. Cosmetic until 2026-07-26, when it turned up in the live
// matrix inside the one message P44 exists to make readable, which is what moved
// it from "tidy later" to worth doing.
//
// Trimmed HERE rather than dropped from the sentinels: a sentinel printed on its
// own (returned bare, or wrapped by another package's formatting) still needs to
// say where it came from. This composes the two without either duplicating or
// losing the attribution, and yields exactly the form spec 44 quotes from the
// real outage: `host: OpenPR: github api request failed: status 500:`.
//
// errors.Is/As are unaffected — only the rendered string changes.
func (e *Error) Error() string {
	inner := strings.TrimPrefix(e.Err.Error(), packagePrefix)
	if e.Input == "" {
		return packagePrefix + e.Op + ": " + inner
	}
	return packagePrefix + e.Op + ": " + e.Input + ": " + inner
}

func (e *Error) Unwrap() error { return e.Err }
