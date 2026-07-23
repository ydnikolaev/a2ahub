package notes

import "errors"

// Sentinel errors, one per failure class (P1 idiom: internal/artifact,
// mirrored by internal/space/errors.go and internal/schema/errors.go).
var (
	// ErrReleaseNotesInvalid is returned when a release-notes YAML file
	// fails structural parse.
	ErrReleaseNotesInvalid = errors.New("notes: release notes file is not valid yaml")

	// ErrCorpusLoad is returned when Load fails to read or parse the
	// embedded release-notes corpus (a build-time defect, never expected
	// at runtime against the shipped binary).
	ErrCorpusLoad = errors.New("notes: corpus failed to load")
)

// Error is the small typed error every exported operation in this package
// returns on failure. It always wraps one of the sentinels above so
// callers can use errors.Is/As; it never panics on bad input.
type Error struct {
	// Op names the failing operation (e.g. "ParseReleaseNotes", "Load").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel (see the vars above).
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "notes: " + e.Op + ": " + e.Err.Error()
	}
	return "notes: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
