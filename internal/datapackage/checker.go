package datapackage

import "context"

// EntryChecker runs one delivered entry's bytes against the contract schema
// its manifest entry names.
//
// It exists as an interface for one reason: pack and verify must ask the
// same question and get the same answer. Pack asks it before anything is
// staged so a producer learns the payload is wrong while it is still cheap
// to fix; verify asks it against the bytes that actually arrived. If those
// two grew separate implementations, a package could pass at pack and fail
// at verify — or worse, the reverse — and neither side could tell which
// answer was the true one.
//
// The implementation compiles each distinct schema once and differs only in
// framing between the formats: a json entry is one document, an ndjson entry
// is a document per line, checked against the same compiled schema. It is
// not a second validator (spec 05a §T2.6, plan D-2).
type EntryChecker interface {
	CheckEntry(ctx context.Context, req EntryCheckRequest) (Check, error)
}

// EntryCheckRequest is one entry's conformance question.
type EntryCheckRequest struct {
	// ConformsTo names the contract schema entry to check against — the
	// manifest entry's own conforms_to value.
	ConformsTo string

	// Format is FormatJSON or FormatNDJSON. It selects framing only; the
	// compiled schema and the validation engine are identical either way.
	Format string

	// Entry is the file being checked: its package-root-relative path and
	// its exact bytes.
	Entry LocalFile

	// MaxViolations bounds how many violations one check may report. A
	// malformed ndjson dataset can fail on every one of a hundred thousand
	// records, and a report that lists all of them is unreadable and
	// unbounded on the wire. Zero means the implementation's own default.
	MaxViolations int
}
