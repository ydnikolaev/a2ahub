package datapackage

import (
	"context"
	"fmt"
	"time"
)

// Clock returns the current instant. Verify takes it as a parameter rather
// than reading the wall clock itself, for two reasons that are really one:
// StartedAt, FinishedAt and the duration derived from them are the ONLY
// three values L-4 permits to differ between two runs over the same package
// and contract (spec 05a §T2.5), and a test proving that needs to control —
// or mask — exactly those values and nothing else.
type Clock func() time.Time

// VerifyRequest is data verify's core question, assembled once per run: is
// this manifest's own aggregate correct for these delivered bytes, does
// every dataset entry conform to the contract that was actually pinned, and
// what does the consumer itself measure while answering that (spec 05a §T1,
// §T2.3, §T2.6, plan D-2)?
type VerifyRequest struct {
	// ID is the report's own verification-report/v1 id (VR-...). Taken as
	// an input, never minted inside Verify: MintReportID draws random
	// entropy, and two runs over the same package must be diffable with
	// only the time window masked (L-4) — a freshly minted id on every call
	// would leak that entropy into the diff. Minting is the caller's
	// concern, the same way NewReport (report.go) already takes id rather
	// than minting one.
	ID string

	// Manifest is the package's own data-package/v1 document, already
	// decoded and schema-valid. Manifest.Contract is the pinned contract
	// ref this run is judged against (L-2) — see ContractRef below for why
	// it is not simply trusted without a check of its own.
	Manifest Document

	// Files is every byte actually delivered, addressed by
	// LocalFile.RelPath. VerifyEntrySet re-proves each declared entry's
	// digest against these bytes before any conformance check runs (L-3) —
	// a verdict about bytes that were never confirmed to be the delivered
	// bytes is worthless.
	Files []LocalFile

	// Checker answers one dataset entry's conformance question. The caller
	// constructs it once, against the contract's digest-verified schemas
	// (ContractRef below), and Verify calls it exactly once per dataset
	// entry — never once per entry regardless of role, and never rebuilt or
	// replaced mid-run (spec 05a §T2.6, plan D-2: resolving and
	// digest-verifying the contract's carried set is a once-per-run cost,
	// not a once-per-entry one). Verify itself has no way to enforce that
	// Checker's own construction obeyed that rule — the carried set is not
	// a parameter this interface exposes — so what this file proves is the
	// half it owns: exactly one CheckEntry call per dataset entry, none for
	// index or readme entries, through one Checker instance for the whole
	// run.
	Checker EntryChecker

	// ContractRef is the contract version and digest the caller actually
	// resolved and digest-verified locally, and the ref Checker was built
	// against. Verify refuses if it disagrees with Manifest.Contract:
	// Checker's schemas came from ContractRef, so a mismatch means the
	// checks about to run were not run against what the manifest pins — L-2
	// living at the caller boundary where Checker is actually constructed,
	// where this package cannot otherwise see it. On success the report's
	// own Contract field is set from Manifest.Contract, never from this
	// parameter directly, so "what the manifest pinned" stays the one
	// source of truth for the wire value even though both had to agree to
	// proceed at all.
	ContractRef string

	// Consumer is the authenticated system identity running this verify —
	// becomes the report's own "consumer" field verbatim.
	Consumer string

	// Clock supplies StartedAt and FinishedAt. Called exactly twice: once
	// before any entry is checked, once after the last one — DurationMS is
	// derived from exactly those two calls and nothing else reads the wall
	// clock anywhere in this file (L-4).
	Clock Clock

	// ContractSupersededBy is L-2's own observation: set by the caller when
	// a newer version of the same contract exists at verify time. It is
	// recorded in Observed and changes nothing else — the verdict is
	// unaffected by it, by construction, because nothing in this file reads
	// it before Result is derived.
	ContractSupersededBy string
}

// Verify runs data verify's core: VerifyEntrySet first (L-3), then one
// conformance check per dataset entry through Checker, then the consumer's
// own record recount compared against the producer's claim, assembled into
// one verification-report/v1 via NewReport — which is the only place Result
// is ever derived, never authored here (spec 05a §T2.3, §6.2).
//
// DEVIATION, recorded because it closes an open question rather than
// following a stated instruction: the brief lists "the contract version
// actually used" as an input distinct from the manifest, but the manifest
// already carries its own pinned Contract field, and L-2 requires the
// report to always reflect exactly that field. Dropping the caller's own
// resolved ref entirely would have been the simpler reading, but it would
// also have discarded a real safety property: the caller resolved and
// digest-verified ContractRef's schemas OUTSIDE this package (this package
// cannot import internal/contract — plan D-3) before building Checker, so a
// caller bug that resolves a different version than the manifest pins would
// otherwise run Checker's conformance checks against schemas the manifest
// never agreed to, and Verify would have no way to notice. ContractRef is
// therefore kept as an input and checked for equality against
// Manifest.Contract before anything else runs; only Manifest.Contract is
// ever written to the report. The refusal wraps ErrContractRefMismatch,
// added to the package vocabulary once this reading was accepted.
func Verify(ctx context.Context, req VerifyRequest) (Report, error) {
	if req.ContractRef != req.Manifest.Contract {
		return Report{}, fmt.Errorf(
			"datapackage: Verify: %w: resolved %q, manifest pins %q",
			ErrContractRefMismatch, req.ContractRef, req.Manifest.Contract,
		)
	}
	if req.Checker == nil {
		return Report{}, fmt.Errorf("datapackage: Verify: an EntryChecker is required")
	}
	if req.Clock == nil {
		return Report{}, fmt.Errorf("datapackage: Verify: a clock is required")
	}

	startedAt := req.Clock()

	// L-3: every declared entry's bytes are re-proven against its own
	// declared digest, in sorted-path order, and the aggregate below is
	// RECOMPUTED from those verified entries — never trusted from the
	// manifest's own claim. checks[] is built from verifiedSet.Entries
	// (this exact slice, sorted by VerifyEntrySet), never from
	// req.Manifest.Entries directly: that is what keeps checks[] and the
	// package's aggregate_digest independent of whatever order the caller
	// happened to supply entries or files in (L-4).
	verifiedSet, err := VerifyEntrySet(req.Manifest.Entries, req.Files)
	if err != nil {
		return Report{}, fmt.Errorf("datapackage: Verify: %w", err)
	}

	bytesByPath := make(map[string][]byte, len(req.Files))
	for _, f := range req.Files {
		bytesByPath[f.RelPath] = f.Bytes
	}

	checks := make([]Check, 0, len(verifiedSet.Entries))
	var recordTotal int64
	for _, entry := range verifiedSet.Entries {
		if entry.Role != RoleDataset || entry.ConformsTo == "" {
			// Only a dataset entry carries conforms_to (§T2.2); an index or
			// readme entry has no contract schema to run and no record
			// count to recount.
			continue
		}
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}

		raw := bytesByPath[entry.Path]

		check, err := req.Checker.CheckEntry(ctx, EntryCheckRequest{
			ConformsTo: entry.ConformsTo,
			Format:     req.Manifest.Format,
			Entry:      LocalFile{RelPath: entry.Path, Bytes: raw},
		})
		if err != nil {
			// A Go-level failure from the checker (a malformed request, a
			// schema that could not even be reached) is not the same thing
			// as a conformance outcome of "error" — that outcome is
			// CheckError, a Check the checker deliberately returns with a
			// nil error, and is what ResultError is derived from
			// (report.go's deriveResult). This path means the question was
			// never actually asked, so Verify itself fails and produces no
			// report, rather than manufacturing a Check to paper over it.
			return Report{}, fmt.Errorf("datapackage: Verify: CheckEntry(%q): %w", entry.Path, err)
		}
		checks = append(checks, check)

		// The consumer's own recount (spec 05a §T2.3 "observed": "the
		// consumer's own numbers, never the producer's claim copied"),
		// through the package's single counting rule in records.go — pack
		// claims with the same function the consumer recounts with, so a
		// delivery can never be reported as mismatching itself.
		//
		// A counting error means the bytes are not the shape the format
		// declares. It is not papered over with a fabricated count, which
		// would then flow into the report as the consumer's own
		// measurement: the count is left at zero and the CheckEntry above
		// has already recorded the malformed bytes as the violation they
		// are. Nothing here needs to name the fault a second time.
		counted, countErr := CountRecords(req.Manifest.Format, raw)
		if countErr != nil {
			counted = 0
		}
		recordTotal += counted

		var claimed int64
		if entry.RecordCount != nil {
			claimed = *entry.RecordCount
		}
		if claimed != counted {
			// DEVIATION: Observed (report.go, schema
			// $defs/observed) is additionalProperties:false with no field
			// for the producer's per-entry claim, so checks[] is the only
			// place in this wire shape a disagreement between the
			// producer's declared record_count and the consumer's own
			// recount CAN be recorded at all. It is written as a Fail — a
			// finding, not a Go error and not CheckError — because "record
			// both where they disagree; a disagreement is a finding, not
			// an error" (this brief) describes exactly this case, and a
			// bare Go error here would refuse the whole verify rather than
			// naming what disagreed and letting the producer's next
			// attempt be aimed at it.
			checks = append(checks, NewCheck(
				"record-count:"+entry.Path,
				entry.Path,
				CheckFail,
				[]InstanceViolation{{
					Message: fmt.Sprintf(
						"declared record_count %d disagrees with the consumer's own recount %d",
						claimed, counted,
					),
				}},
			))
		}
	}

	var sizeTotal int64
	for _, entry := range verifiedSet.Entries {
		sizeTotal += entry.SizeBytes
	}

	finishedAt := req.Clock()
	durationMS := finishedAt.Sub(startedAt).Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}

	observed := Observed{
		RecordCount:          recordTotal,
		SizeBytes:            sizeTotal,
		DurationMS:           durationMS,
		ContractSupersededBy: req.ContractSupersededBy,
	}

	pkg := PackageRef{ID: req.Manifest.ID, AggregateDigest: verifiedSet.AggregateDigest}

	report, err := NewReport(
		req.ID,
		req.Manifest.Thread,
		pkg,
		req.Manifest.Contract,
		req.Consumer,
		checks,
		observed,
		startedAt.UTC().Format(time.RFC3339),
		finishedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return Report{}, fmt.Errorf(
			"datapackage: Verify: manifest %q declares no dataset entries to check, nothing to verify: %w",
			req.Manifest.ID, err,
		)
	}
	return report, nil
}

// VerifyPass is the one exported way to drive verify-pass: it returns nil
// only if r's own Checks derive to ResultPass, and ErrVerdictNotPass
// otherwise. Spec 05a's own boundary requirement: "driving verify-pass
// while result != pass must be impossible... expose the decision as a
// function the caller cannot bypass rather than as a convention the caller
// is asked to follow."
//
// It re-derives from r.Checks rather than trusting r.Result(), which
// matters for exactly one case: Report.UnmarshalJSON (report.go)
// deliberately preserves a declared "result" verbatim for a hand-authored
// or forged document, precisely so the schema's own result-derivation
// conditional can catch it on the wire (report.go's own doc comment). A
// guard that read r.Result() here would trust that same forged value and
// authorize exactly the forced pass spec 05a AC-5 names as refused. Because
// deriveResult only ever sees r.Checks — never r.Result() — this holds for
// every Report this package's own API can construct AND for one decoded
// from arbitrary bytes, which NewReport alone does not cover.
func VerifyPass(r Report) error {
	if deriveResult(r.Checks) != ResultPass {
		return fmt.Errorf("datapackage: VerifyPass: %w", ErrVerdictNotPass)
	}
	return nil
}
