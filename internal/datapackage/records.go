package datapackage

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CountRecords is the ONE record-counting rule, for both formats and both
// sides of the loop.
//
// It lives in its own file, owned by neither pack nor verify, because the two
// of them briefly had a copy each and the copies disagreed: one returned an
// error on malformed JSON, the other silently returned 1. That disagreement is
// not cosmetic. `record_count` is the producer's claim in the manifest and the
// consumer's own recount in the report, and a verdict compares them — so two
// rules mean a delivery this package produced could be reported as mismatching
// itself, with the consumer's number being the wrong one.
//
//   - ndjson: the number of non-blank lines. A trailing newline adds no
//     record; a blank line anywhere, including mid-file, is skipped rather
//     than counted. This is pure framing — counting does not check that any
//     line is well-formed JSON, which is the EntryChecker's separate job.
//   - json: a top-level array counts as its length; any other well-formed
//     top-level value counts as exactly one record.
//
// Malformed JSON is an error rather than a guess. Counting cannot proceed
// without knowing which of the two shapes the document is, and a fabricated 1
// would flow into a report as the consumer's own measurement.
//
// The json rule is not stated in spec 05a; it is chosen here, recorded in the
// phase plan, and lives in one place so a later correction is one edit.
func CountRecords(format string, raw []byte) (int64, error) {
	switch format {
	case FormatNDJSON:
		var count int64
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(strings.TrimRight(line, "\r")) == "" {
				continue
			}
			count++
		}
		return count, nil
	case FormatJSON:
		var array []json.RawMessage
		if err := json.Unmarshal(raw, &array); err == nil {
			return int64(len(array)), nil
		}
		var single json.RawMessage
		if err := json.Unmarshal(raw, &single); err != nil {
			return 0, fmt.Errorf("datapackage: CountRecords: not valid json: %w", err)
		}
		return 1, nil
	default:
		return 0, fmt.Errorf("datapackage: CountRecords: unknown format %q", format)
	}
}
