package datapackage

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// defaultMaxViolations is EntryCheckRequest.MaxViolations's zero-value
// default. 256 is not an arbitrary round number: it is
// verification-report/v1's own checks[].violations maxItems hard cap
// (schemas/verification-report/v1/verification-report.schema.json). A
// caller that never sets MaxViolations therefore always gets a Check whose
// Violations already fits the wire schema without any further capping
// downstream. A caller that explicitly asks for more is honoured exactly —
// this constructor does not clamp an explicit request back down to 256; if
// the resulting Check later fails to marshal under the wire schema's own
// maxItems, that is the report assembler's bound to enforce, not this
// package's to override (see the deviations note in the brief response).
const defaultMaxViolations = 256

// entryChecker is the production EntryChecker: every distinct conforms_to
// schema handed to NewEntryChecker is compiled exactly once, at
// construction, and CheckEntry only ever looks the compiled schema up by
// name — it never compiles anything itself (spec 05a §T2.6, plan D-2).
type entryChecker struct {
	schemas map[string]*extSchema
}

// extSchema is a local alias for schema.ExternalSchema, purely to keep this
// file's own signatures shorter; it is not a second type.
type extSchema = schema.ExternalSchema

var _ EntryChecker = (*entryChecker)(nil)

// NewEntryChecker compiles every schema in schemas — keyed by the
// conforms_to value it answers for — through internal/schema's external
// compile path (the same instance internal/validate's
// ContractInstanceAdapter uses), exactly once each, and returns an
// EntryChecker that only ever runs the already-compiled schemas.
//
// schemas' bytes must already be digest-verified against the contract's own
// carried set by the caller: resolving and verifying the contract needs the
// space mirror, which this package must not import (spec 05a §5, plan D-3),
// so that is the caller's job. This constructor's only job is compiling and
// running.
//
// A schema that fails to compile is a construction-time error: NewEntryChecker
// returns before any entry is ever checked, rather than letting a bad schema
// surface lazily and inconsistently the first time some particular entry
// happens to need it.
func NewEntryChecker(schemas map[string][]byte) (EntryChecker, error) {
	// Compile in sorted name order. Map iteration order is not itself part
	// of any output this function returns (the returned lookup table is a
	// map, looked up by exact key, never ranged over for output), but a
	// stable compile order keeps "which schema failed first" reproducible
	// across runs when more than one is broken.
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	compiled := make(map[string]*extSchema, len(schemas))
	for _, name := range names {
		cs, err := schema.CompileExternal(name, schemas[name])
		if err != nil {
			return nil, fmt.Errorf("datapackage: NewEntryChecker: compiling schema %q: %w", name, err)
		}
		compiled[name] = cs
	}
	return &entryChecker{schemas: compiled}, nil
}

// CheckEntry frames req.Entry by req.Format and validates it against the
// compiled schema req.ConformsTo names, returning a Check whose Violations
// is deterministically ordered (L-4) and bounded by req.MaxViolations (or
// defaultMaxViolations when zero).
func (c *entryChecker) CheckEntry(ctx context.Context, req EntryCheckRequest) (Check, error) {
	if err := ctx.Err(); err != nil {
		return Check{}, err
	}

	id := "conformance:" + req.Entry.RelPath
	limit := req.MaxViolations
	if limit <= 0 {
		limit = defaultMaxViolations
	}

	compiled, ok := c.schemas[req.ConformsTo]
	if !ok {
		// Nothing was proven wrong: the check could not run at all,
		// because the schema its own manifest names was never given to
		// this checker. This is CheckError, never CheckFail, and it is
		// this package's precedent (checker.go's own doc comment: "error
		// beats fail beats pass") that error and fail must never be
		// conflated. Violations stays empty deliberately: a populated
		// entry here would assert something was proven wrong about the
		// bytes, and nothing was — it was never evaluated. Path and
		// ConformsTo (visible on the manifest entry the caller already
		// has) are what names the offending value; this Check need not
		// duplicate that.
		return NewCheck(id, req.Entry.RelPath, CheckError, nil), nil
	}

	var kept []InstanceViolation
	var total int
	switch req.Format {
	case FormatJSON:
		kept, total = checkJSONEntry(compiled, req.Entry.Bytes, limit)
	case FormatNDJSON:
		kept, total = checkNDJSONEntry(compiled, req.Entry.Bytes, limit)
	default:
		// Same precedence as the missing-schema case above: an
		// unrecognized format value means this check never ran, not that
		// it ran and failed.
		return NewCheck(id, req.Entry.RelPath, CheckError, nil), nil
	}

	violations := finalizeViolations(kept, total, limit)
	status := CheckPass
	if total > 0 {
		status = CheckFail
	}
	return NewCheck(id, req.Entry.RelPath, status, violations), nil
}

// checkJSONEntry validates raw as one whole document (FormatJSON framing).
// A json entry is held fully resident regardless — bounds.go's own
// documented tradeoff — so there is no streaming concern here: every
// violation is collected, sorted for determinism, and then bounded.
func checkJSONEntry(compiled *extSchema, raw []byte, limit int) (kept []InstanceViolation, total int) {
	fvs, err := compiled.ValidateInstanceViolations(raw)
	if err != nil {
		// raw did not even decode as JSON. This is a violation the
		// producer can act on ("this entry is not JSON"), not a Go error
		// that abandons the whole check — the same non-abandoning
		// treatment §6.4/T2.6 requires for a malformed ndjson line, held
		// consistently for the json framing too.
		return []InstanceViolation{{Message: "entry is not valid JSON"}}, 1
	}
	violations := convertViolations(fvs, nil)
	sortViolations(violations)
	total = len(violations)
	if total > limit {
		return violations[:limit], total
	}
	return violations, total
}

// checkNDJSONEntry validates raw record by record (FormatNDJSON framing),
// one document per line, against the same compiled schema checkJSONEntry
// uses for the json framing — the only difference between the two formats
// is this function's own framing, never a second validator.
//
// Streaming discipline: raw is scanned line by line via bufio.Scanner, never
// split into an in-memory slice of lines first — a dataset entry is bounded
// well above a contract file (bounds.go), and this is the reason that bound
// can stay generous. kept never grows past limit real violations regardless
// of how many records fail — a dataset failing on every one of a hundred
// thousand records still holds only `limit` violations in memory at once —
// while total keeps counting every violation actually found, across every
// record, so the eventual truncation message names an honest total.
func checkNDJSONEntry(compiled *extSchema, raw []byte, limit int) (kept []InstanceViolation, total int) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// A record may legitimately be larger than bufio.Scanner's 64 KiB
	// default token size; raw is already fully resident (LocalFile reads
	// once, doc.go), so sizing the scan buffer to the whole entry costs
	// nothing additional and never truncates a legitimate long record.
	scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)

	var lineNo int64
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			// Blank/whitespace-only lines are skipped: not a record, not
			// a violation, not counted. A stray separator between real
			// records is not itself data the producer needs to fix, and
			// treating it as a malformed record would flag noise as if it
			// were a defect. Documented here because the spec leaves the
			// choice to the implementer (brief: "decide and document").
			continue
		}

		record := lineNo
		var lineViolations []InstanceViolation
		fvs, err := compiled.ValidateInstanceViolations(line)
		switch {
		case err != nil:
			// Covers both a plain malformed line and a truncated final
			// line with no trailing newline and incomplete JSON (§6.4):
			// bufio.Scanner's default split function already returns the
			// last, newline-less token as its own line, so an incomplete
			// final record reaches this exact path — a named violation,
			// not a silent skip and not an abandoned check.
			lineViolations = []InstanceViolation{{Message: "record is not valid JSON", Record: &record}}
		case len(fvs) > 0:
			lineViolations = convertViolations(fvs, &record)
			sortViolations(lineViolations)
		}

		total += len(lineViolations)
		if room := limit - len(kept); room > 0 {
			if room >= len(lineViolations) {
				kept = append(kept, lineViolations...)
			} else {
				kept = append(kept, lineViolations[:room]...)
			}
		}
	}
	// scanner.Err() is deliberately not surfaced as a hard error: the only
	// two conditions bufio.Scanner can report are an underlying Read
	// failure (impossible here — bytes.Reader never fails a Read) and a
	// token exceeding the configured buffer, which cannot happen either
	// because the buffer above is sized to the whole entry. There is no
	// reachable path where scanner.Err() is non-nil.
	return kept, total
}

// finalizeViolations turns the bounded-during-collection kept slice (at
// most limit real violations, sorted, one entry format's or the other's)
// into the Check's final Violations: unchanged if nothing was truncated
// (total <= limit), or kept's first limit-1 entries plus one synthetic
// truncation-marker violation naming exactly how much was cut, so the final
// slice is never longer than limit and a truncated report never reads as
// "only these things were wrong" (brief: "a silently truncated list reads
// as 'only three things were wrong'").
func finalizeViolations(kept []InstanceViolation, total, limit int) []InstanceViolation {
	if total <= limit {
		return kept
	}
	shown := limit - 1
	if shown < 0 {
		shown = 0
	}
	out := append([]InstanceViolation(nil), kept[:shown]...)
	out = append(out, InstanceViolation{
		Message: fmt.Sprintf("violation list truncated: showing %d of %d violations (bound %d)", shown, total, limit),
	})
	return out
}

// convertViolations maps internal/schema's library-agnostic FieldViolation
// into this package's own InstanceViolation shape (report.go's own doc
// comment: mirrors internal/contract's field names and RFC 6901 pointer
// semantics without importing internal/contract). record is shared by every
// violation on the same line/document — never mutated afterward, so sharing
// the pointer across them is safe.
func convertViolations(fvs []schema.FieldViolation, record *int64) []InstanceViolation {
	out := make([]InstanceViolation, 0, len(fvs))
	for _, fv := range fvs {
		out = append(out, InstanceViolation{
			InstancePointer: fieldPathPointer(fv.Path),
			SchemaPointer:   fv.SchemaPointer,
			Message:         fmt.Sprintf("JSON Schema keyword %s rejected the instance", fv.Keyword),
			Record:          record,
		})
	}
	return out
}

// fieldPathPointer converts a dot-joined FieldViolation.Path into an RFC
// 6901 JSON pointer, the same conversion internal/validate's
// ContractInstanceAdapter (contract_adapters.go's fieldPathPointer) applies
// at its own boundary — reimplemented here rather than imported because
// internal/datapackage must not import internal/contract or
// internal/validate (this package is core), and this is a small pure
// string transform, not a second validator or a second digest.
func fieldPathPointer(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	if strings.HasPrefix(field, "/") {
		return field
	}
	parts := strings.Split(field, ".")
	for i := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "~", "~0"), "/", "~1")
	}
	return "/" + strings.Join(parts, "/")
}

// sortViolations imposes a total, deterministic order (L-4) regardless of
// the order the underlying jsonschema/v6 walk produced — that walk can
// itself be influenced by Go's randomized map iteration when the instance
// decodes to an object, so relying on it would make two runs over identical
// bytes produce different checks[] byte-for-byte. The primary key is
// Record (nil sorts as 0, so a json entry's single implicit "record" is
// stable relative to itself), then InstancePointer, then SchemaPointer,
// then Message, per the brief: "by record, then by instance pointer."
func sortViolations(violations []InstanceViolation) {
	sort.Slice(violations, func(i, j int) bool {
		return violationLess(violations[i], violations[j])
	})
}

func violationLess(a, b InstanceViolation) bool {
	ar, br := recordOrZero(a.Record), recordOrZero(b.Record)
	if ar != br {
		return ar < br
	}
	if a.InstancePointer != b.InstancePointer {
		return a.InstancePointer < b.InstancePointer
	}
	if a.SchemaPointer != b.SchemaPointer {
		return a.SchemaPointer < b.SchemaPointer
	}
	return a.Message < b.Message
}

func recordOrZero(r *int64) int64 {
	if r == nil {
		return 0
	}
	return *r
}
