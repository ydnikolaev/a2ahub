package datapackage

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// --- test fixtures shared by this file only ---

// entryMeta is the contract-conformance knowledge BuildEntrySet cannot
// derive from bytes alone (entryset.go's own doc comment: Role, MediaType,
// ConformsTo and RecordCount are the caller's to fill in). Tests in this
// file build entries the same way a real pack would: digest/size from
// BuildEntrySet, everything else layered on afterward.
type entryMeta struct {
	role        string
	mediaType   string
	conformsTo  string
	recordCount *int64
}

func int64Ptr(v int64) *int64 { return &v }

func buildManifestEntries(t *testing.T, files []LocalFile, meta map[string]entryMeta) []Entry {
	t.Helper()
	set, err := BuildEntrySet(files)
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}
	entries := make([]Entry, len(set.Entries))
	for i, e := range set.Entries {
		m := meta[e.Path]
		e.Role = m.role
		e.MediaType = m.mediaType
		e.ConformsTo = m.conformsTo
		e.RecordCount = m.recordCount
		entries[i] = e
	}
	return entries
}

// testHexA/testHexB are 64-char hex strings distinct from report_test.go's
// repeatHex("a") only in name, so both files can build fixtures
// independently without a shared constant.
var (
	testHexA        = repeatHex("a")
	testHexB        = repeatHex("b")
	testContractRef = "XC-axon-order-api@2.3.0#sha256:" + testHexA
)

func testManifest(id, thread, contract, format string, entries []Entry) Document {
	return Document{
		Schema:          "data-package/v1",
		ID:              id,
		Thread:          thread,
		Contract:        contract,
		Mode:            ModeSnapshot,
		Classification:  "restricted",
		DataProfile:     DataProfileSynthetic,
		Format:          format,
		TransportDriver: TransportDriverSpaceGit,
		Locator:         "checkout-core/deliveries/" + id,
		Entries:         entries,
		AggregateDigest: "sha256:" + testHexA,
		SizeBytes:       0,
		RecordCount:     0,
		CreatedAt:       "2026-08-04T12:00:00Z",
		ExpiresAt:       "2026-09-04T12:00:00Z",
		Attempt:         1,
		Provenance:      Provenance{OriginSystem: "axon-orders-db", ExtractedAt: "2026-08-04T11:59:30Z"},
	}
}

// fakeChecker is EntryChecker's test double: it records every call it
// receives (so a test can assert exactly which entries were checked, and
// how many times) and answers through respond, defaulting to an
// always-pass verdict.
type fakeChecker struct {
	calls   []EntryCheckRequest
	respond func(EntryCheckRequest) (Check, error)
}

func (f *fakeChecker) CheckEntry(_ context.Context, req EntryCheckRequest) (Check, error) {
	f.calls = append(f.calls, req)
	if f.respond != nil {
		return f.respond(req)
	}
	return NewCheck("CHK-"+req.Entry.RelPath, req.Entry.RelPath, CheckPass, nil), nil
}

// sequenceClock returns times in order, holding on the last one if called
// more times than supplied — Verify calls a Clock exactly twice.
func sequenceClock(times ...time.Time) Clock {
	i := 0
	return func() time.Time {
		if i >= len(times) {
			return times[len(times)-1]
		}
		t := times[i]
		i++
		return t
	}
}

func assertReportValidatesAgainstSchema(t *testing.T, report Report) {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal(report): %v", err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("json.Unmarshal(report): %v", err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	violations, err := corpus.ValidateVerificationReport(instance)
	if err != nil {
		t.Fatalf("ValidateVerificationReport: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("report failed its own schema: %+v\nreport: %s", violations, raw)
	}
}

// --- happy path ---

func TestVerify_AllPass_DerivesResultPassAndValidatesAgainstSchema(t *testing.T) {
	t.Parallel()

	files := []LocalFile{
		{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1},{"id":2},{"id":3}]`)},
		{RelPath: "README.md", Bytes: []byte("hello")},
	}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(3)},
		"README.md":        {role: RoleReadme, mediaType: "text/markdown"},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	checker := &fakeChecker{}
	clock := sequenceClock(
		time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 13, 0, 1, 0, time.UTC),
	)

	report, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       clock,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Result() != ResultPass {
		t.Fatalf("Result() = %q, want %q", report.Result(), ResultPass)
	}
	if len(checker.calls) != 1 {
		t.Fatalf("checker calls = %d, want 1 (readme must not be checked)", len(checker.calls))
	}
	if checker.calls[0].Entry.RelPath != "data/orders.json" {
		t.Fatalf("checker called on %q, want data/orders.json", checker.calls[0].Entry.RelPath)
	}
	if checker.calls[0].ConformsTo != "schema/order.schema.json" || checker.calls[0].Format != FormatJSON {
		t.Fatalf("checker request = %+v, want conforms_to/format threaded through from the manifest", checker.calls[0])
	}
	if report.Observed.RecordCount != 3 {
		t.Fatalf("Observed.RecordCount = %d, want 3 (consumer's own recount)", report.Observed.RecordCount)
	}
	wantSize := int64(len(files[0].Bytes) + len(files[1].Bytes))
	if report.Observed.SizeBytes != wantSize {
		t.Fatalf("Observed.SizeBytes = %d, want %d", report.Observed.SizeBytes, wantSize)
	}
	if report.Package.ID != manifest.ID {
		t.Fatalf("Package.ID = %q, want %q", report.Package.ID, manifest.ID)
	}
	built, err := BuildEntrySet(files)
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}
	if report.Package.AggregateDigest != built.AggregateDigest {
		t.Fatalf("Package.AggregateDigest = %q, want the RECOMPUTED aggregate %q, not the manifest's own claim", report.Package.AggregateDigest, built.AggregateDigest)
	}
	if report.Contract != manifest.Contract {
		t.Fatalf("Contract = %q, want the manifest-pinned %q", report.Contract, manifest.Contract)
	}
	if err := VerifyPass(report); err != nil {
		t.Fatalf("VerifyPass(passing report) = %v, want nil", err)
	}
	assertReportValidatesAgainstSchema(t, report)
}

func TestVerify_ChecksOnlyDatasetEntries(t *testing.T) {
	t.Parallel()
	files := []LocalFile{
		{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)},
		{RelPath: "data/index.json", Bytes: []byte(`{"count":1}`)},
		{RelPath: "README.md", Bytes: []byte("hello")},
	}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
		"data/index.json":  {role: RoleIndex, mediaType: "application/json"},
		"README.md":        {role: RoleReadme, mediaType: "text/markdown"},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	checker := &fakeChecker{}
	_, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(checker.calls) != 1 {
		t.Fatalf("checker calls = %d, want exactly 1 (only the dataset entry)", len(checker.calls))
	}
}

// --- AC-5: failing conformance derives fail, and a forced pass is refused ---

func TestVerify_FailingCheck_DerivesResultFail_AndVerifyPassRefuses(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)}}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	checker := &fakeChecker{respond: func(req EntryCheckRequest) (Check, error) {
		return NewCheck("CHK-1", req.Entry.RelPath, CheckFail, []InstanceViolation{{Message: "id must be a string"}}), nil
	}}

	report, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Result() != ResultFail {
		t.Fatalf("Result() = %q, want %q", report.Result(), ResultFail)
	}
	if err := VerifyPass(report); !errors.Is(err, ErrVerdictNotPass) {
		t.Fatalf("VerifyPass(failing report) = %v, want errors.Is ErrVerdictNotPass", err)
	}
	assertReportValidatesAgainstSchema(t, report)
}

// TestVerifyPass_RefusesAForgedPassEvenThoughUnmarshalPreservesIt is AC-5's
// "a forced pass is refused" for the one input VerifyPass cannot trust by
// construction from NewReport alone: a report decoded from bytes that were
// never produced by NewReport at all (hand-authored, or corrupted), where
// Report.UnmarshalJSON deliberately preserves the declared "result"
// verbatim (report.go's own doc comment) rather than recomputing it.
// VerifyPass must still refuse, because it re-derives from Checks, not
// Result().
func TestVerifyPass_RefusesAForgedPassEvenThoughUnmarshalPreservesIt(t *testing.T) {
	t.Parallel()
	forged := []byte(`{
		"schema": "verification-report/v1",
		"id": "VR-seomatrix-20260804-q7w2",
		"thread": "thread:axon-20260721-k3f9",
		"package": {"id": "DP-axon-20260804-k3f9", "aggregate_digest": "sha256:` + testHexB + `"},
		"contract": "` + testContractRef + `",
		"consumer": "seomatrix",
		"checks": [{"id": "CHK-1", "path": "a.json", "status": "fail", "violations": [{"instance_pointer": "", "schema_pointer": "", "message": "bad"}]}],
		"observed": {"record_count": 1, "size_bytes": 10, "duration_ms": 5},
		"result": "pass",
		"started_at": "2026-08-04T13:00:00Z",
		"finished_at": "2026-08-04T13:00:01Z"
	}`)
	var report Report
	if err := json.Unmarshal(forged, &report); err != nil {
		t.Fatalf("json.Unmarshal(forged): %v", err)
	}
	if report.Result() != ResultPass {
		t.Fatalf("test setup bug: forged Result() = %q, want %q so this test actually exercises the guard", report.Result(), ResultPass)
	}
	if err := VerifyPass(report); !errors.Is(err, ErrVerdictNotPass) {
		t.Fatalf("VerifyPass(forged pass over a failing check) = %v, want errors.Is ErrVerdictNotPass", err)
	}
}

// --- record-count disagreement is a finding, not an error ---

func TestVerify_RecordCountDisagreement_RecordsFindingNotError(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1},{"id":2},{"id":3}]`)}}
	meta := map[string]entryMeta{
		// producer claims 5, the bytes actually hold 3.
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(5)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	checker := &fakeChecker{}
	report, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(report.Checks) != 2 {
		t.Fatalf("Checks = %+v, want 2 (conformance pass + record-count finding)", report.Checks)
	}
	var found bool
	for _, c := range report.Checks {
		if c.ID == "record-count:data/orders.json" {
			found = true
			if c.Status != CheckFail {
				t.Fatalf("record-count check status = %q, want %q", c.Status, CheckFail)
			}
			if len(c.Violations) != 1 {
				t.Fatalf("record-count check violations = %+v, want exactly 1", c.Violations)
			}
		}
	}
	if !found {
		t.Fatal("no record-count check found in Checks")
	}
	if report.Observed.RecordCount != 3 {
		t.Fatalf("Observed.RecordCount = %d, want the consumer's own recount 3, not the producer's claim 5", report.Observed.RecordCount)
	}
	if report.Result() != ResultFail {
		t.Fatalf("Result() = %q, want %q (a disagreement is a finding, so the overall verdict fails without a Go error)", report.Result(), ResultFail)
	}
	assertReportValidatesAgainstSchema(t, report)
}

// --- ndjson framing: records are counted by non-empty line ---

func TestVerify_NDJSON_RecordCountByLines(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.ndjson", Bytes: []byte("{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n")}}
	meta := map[string]entryMeta{
		"data/orders.ndjson": {role: RoleDataset, mediaType: "application/x-ndjson", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(3)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260805-m7q2", "thread:axon-20260721-k3f9", testContractRef, FormatNDJSON, entries)

	checker := &fakeChecker{}
	report, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260805-q7w3",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(report.Checks) != 1 {
		t.Fatalf("Checks = %+v, want exactly 1 (record counts agree, no finding)", report.Checks)
	}
	if report.Observed.RecordCount != 3 {
		t.Fatalf("Observed.RecordCount = %d, want 3", report.Observed.RecordCount)
	}
	if checker.calls[0].Format != FormatNDJSON {
		t.Fatalf("checker request Format = %q, want %q", checker.calls[0].Format, FormatNDJSON)
	}
}

// TestVerify_JSONObjectEntry_CountsAsOneRecord covers countRecords'
// fallback branch: a json dataset entry whose top-level value is an object
// (not an array) counts as exactly one record — see countRecords' own doc
// comment for the convention.
func TestVerify_JSONObjectEntry_CountsAsOneRecord(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/summary.json", Bytes: []byte(`{"total":42}`)}}
	meta := map[string]entryMeta{
		"data/summary.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/summary.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	checker := &fakeChecker{}
	report, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Observed.RecordCount != 1 {
		t.Fatalf("Observed.RecordCount = %d, want 1 for a non-array json document", report.Observed.RecordCount)
	}
	if len(report.Checks) != 1 {
		t.Fatalf("Checks = %+v, want exactly 1 (declared record_count agrees)", report.Checks)
	}
}

// --- boundary validation: Checker and Clock are required inputs ---

func TestVerify_RequiresChecker(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)}}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	_, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if err == nil {
		t.Fatal("Verify(nil Checker) = nil error, want a refusal")
	}
}

func TestVerify_RequiresClock(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)}}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	_, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     &fakeChecker{},
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
	})
	if err == nil {
		t.Fatal("Verify(nil Clock) = nil error, want a refusal")
	}
}

// --- AC-10 / L-4: determinism ---

func TestVerify_Determinism_ByteIdenticalAcrossTwoRuns(t *testing.T) {
	t.Parallel()
	files := []LocalFile{
		{RelPath: "data/a.json", Bytes: []byte(`[{"id":1}]`)},
		{RelPath: "data/b.json", Bytes: []byte(`[{"id":2},{"id":3}]`)},
		{RelPath: "README.md", Bytes: []byte("hello")},
	}
	meta := map[string]entryMeta{
		"data/a.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
		"data/b.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(2)},
		"README.md":   {role: RoleReadme, mediaType: "text/markdown"},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	// Permute BOTH the manifest's own entries[] and the delivered files
	// between the two runs — L-4 must hold regardless of either order.
	permutedEntries := []Entry{entries[2], entries[0], entries[1]}
	permutedManifest := manifest
	permutedManifest.Entries = permutedEntries
	permutedFiles := []LocalFile{files[2], files[1], files[0]}

	run := func(manifest Document, files []LocalFile, clock Clock) Report {
		checker := &fakeChecker{}
		report, err := Verify(context.Background(), VerifyRequest{
			ID:          "VR-seomatrix-20260804-q7w2",
			Manifest:    manifest,
			Files:       files,
			Checker:     checker,
			ContractRef: manifest.Contract,
			Consumer:    "seomatrix",
			Clock:       clock,
		})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		return report
	}

	reportA := run(manifest, files, sequenceClock(
		time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 13, 0, 1, 0, time.UTC),
	))
	reportB := run(permutedManifest, permutedFiles, sequenceClock(
		time.Date(2026, 8, 4, 15, 30, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 15, 30, 9, 0, time.UTC),
	))

	maskedA := maskTimeWindow(t, reportA)
	maskedB := maskTimeWindow(t, reportB)
	if !reflect.DeepEqual(maskedA, maskedB) {
		t.Fatalf("reports differ beyond the time window:\nA: %+v\nB: %+v", maskedA, maskedB)
	}
}

// maskTimeWindow marshals report and returns it decoded with started_at,
// finished_at and observed.duration_ms removed — the only three values L-4
// permits to differ between two runs.
func maskTimeWindow(t *testing.T, report Report) map[string]any {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	delete(decoded, "started_at")
	delete(decoded, "finished_at")
	if observed, ok := decoded["observed"].(map[string]any); ok {
		delete(observed, "duration_ms")
	}
	return decoded
}

// --- L-2: verification always uses the manifest-pinned version ---

func TestVerify_ContractRefMismatch_RefusesBeforeAnyCheckRuns(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)}}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	checker := &fakeChecker{}
	_, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: "XC-axon-order-api@9.9.9#sha256:" + repeatHex("f"), // NOT what the manifest pins ("latest"-style drift)
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if !errors.Is(err, ErrContractRefMismatch) {
		t.Fatalf("Verify(mismatched ContractRef) = %v, want errors.Is ErrContractRefMismatch", err)
	}
	if len(checker.calls) != 0 {
		t.Fatalf("checker was called %d times before the pinned-version mismatch was refused, want 0", len(checker.calls))
	}
}

// --- AC-13 / L-2: a superseded contract observation leaves the verdict unchanged ---

func TestVerify_SupersededObservation_LeavesVerdictUnchanged(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)}}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	superseded := "XC-axon-order-api@2.4.0#sha256:" + testHexB
	checker := &fakeChecker{}
	report, err := Verify(context.Background(), VerifyRequest{
		ID:                   "VR-seomatrix-20260804-q7w2",
		Manifest:             manifest,
		Files:                files,
		Checker:              checker,
		ContractRef:          manifest.Contract,
		Consumer:             "seomatrix",
		Clock:                sequenceClock(time.Now(), time.Now()),
		ContractSupersededBy: superseded,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.Result() != ResultPass {
		t.Fatalf("Result() = %q, want %q — a superseded contract observation must not change the verdict", report.Result(), ResultPass)
	}
	if report.Observed.ContractSupersededBy != superseded {
		t.Fatalf("Observed.ContractSupersededBy = %q, want %q", report.Observed.ContractSupersededBy, superseded)
	}
	if report.Contract != manifest.Contract {
		t.Fatalf("Contract = %q, want the ORIGINAL pinned %q, not the superseded observation", report.Contract, manifest.Contract)
	}
	assertReportValidatesAgainstSchema(t, report)
}

// --- checker Go-level errors propagate; no partial report ---

func TestVerify_CheckerError_PropagatesAndProducesNoReport(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)}}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	boom := errors.New("schema unreachable")
	checker := &fakeChecker{respond: func(EntryCheckRequest) (Check, error) { return Check{}, boom }}
	report, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Verify(checker error) = %v, want errors.Is boom", err)
	}
	if !reflect.DeepEqual(report, Report{}) {
		t.Fatalf("Verify(checker error) returned a non-zero report %+v, want none", report)
	}
}

// --- no dataset entries: explicit refusal, not a silent empty report ---

func TestVerify_NoDatasetEntries_RefusesWithExplicitMessage(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "README.md", Bytes: []byte("hello")}}
	meta := map[string]entryMeta{"README.md": {role: RoleReadme, mediaType: "text/markdown"}}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	checker := &fakeChecker{}
	_, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       files,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if err == nil {
		t.Fatal("Verify(no dataset entries) = nil error, want a refusal")
	}
}

// --- L-3 stays reachable through Verify, not just VerifyEntrySet directly ---

func TestVerify_TamperedEntry_RefusedBeforeAnyCheckRuns(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":1}]`)}}
	meta := map[string]entryMeta{
		"data/orders.json": {role: RoleDataset, mediaType: "application/json", conformsTo: "schema/order.schema.json", recordCount: int64Ptr(1)},
	}
	entries := buildManifestEntries(t, files, meta)
	manifest := testManifest("DP-axon-20260804-k3f9", "thread:axon-20260721-k3f9", testContractRef, FormatJSON, entries)

	tamperedFiles := []LocalFile{{RelPath: "data/orders.json", Bytes: []byte(`[{"id":9}]`)}}
	checker := &fakeChecker{}
	_, err := Verify(context.Background(), VerifyRequest{
		ID:          "VR-seomatrix-20260804-q7w2",
		Manifest:    manifest,
		Files:       tamperedFiles,
		Checker:     checker,
		ContractRef: manifest.Contract,
		Consumer:    "seomatrix",
		Clock:       sequenceClock(time.Now(), time.Now()),
	})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Verify(tampered bytes) = %v, want errors.Is ErrDigestMismatch", err)
	}
	if len(checker.calls) != 0 {
		t.Fatalf("checker was called %d times over unverified bytes, want 0", len(checker.calls))
	}
}
