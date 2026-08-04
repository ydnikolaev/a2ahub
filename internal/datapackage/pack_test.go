package datapackage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// --- test doubles ---

// fakeEntryChecker is pack_test.go's own EntryChecker double: pack.go must
// ask the SAME question checker.go describes (one CheckEntry call per
// dataset entry) and react to its answer without reimplementing any
// conformance logic itself, so a fake that returns a scripted Check per
// path is enough to prove pack.go's own control flow — the real checker
// (a later wave) is exercised by its own package's tests, not this one's.
type fakeEntryChecker struct {
	calledPaths []string
	checks      map[string]Check
	errs        map[string]error
}

func (f *fakeEntryChecker) CheckEntry(_ context.Context, req EntryCheckRequest) (Check, error) {
	f.calledPaths = append(f.calledPaths, req.Entry.RelPath)
	if err, ok := f.errs[req.Entry.RelPath]; ok {
		return Check{}, err
	}
	if check, ok := f.checks[req.Entry.RelPath]; ok {
		return check, nil
	}
	return NewCheck("chk-ok", req.Entry.RelPath, CheckPass, nil), nil
}

// --- request builder ---

// basePackRequest returns a PackRequest that, unmodified, packs source into
// a schema-valid first-attempt manifest. Every field a specific test cares
// about is overridden by that test; everything else stays a value already
// known to satisfy data-package/v1's own patterns (borrowed from the
// shipped fixtures this same package's manifest_test.go already proves
// schema-valid), so a test failure is never accidentally about an
// unrelated field.
func basePackRequest(t *testing.T, source string, entries map[string]EntryDeclaration, checker EntryChecker) PackRequest {
	t.Helper()
	return PackRequest{
		Source:         source,
		System:         "axon",
		Thread:         "thread:axon-20260721-k3f9",
		Contract:       "XC-axon-order-api@2.3.0#sha256:" + strings.Repeat("a", 64),
		Classification: "restricted",
		DataProfile:    DataProfileSynthetic,
		Format:         FormatJSON,
		Locator:        "checkout-core/deliveries/DP-test",
		Entries:        entries,
		Expires:        30 * 24 * time.Hour,
		Attempt:        1,
		Bounds:         generousBounds(),
		Checker:        checker,
		Provenance:     Provenance{OriginSystem: "axon-orders-db", ExtractedAt: "2026-08-04T11:59:30Z"},
		Now:            time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		Entropy:        bytes.NewReader([]byte{0, 0, 0, 0}),
	}
}

func assertValidatesAgainstSchema(t *testing.T, doc Document) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("json.Marshal(doc): %v", err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("json.Unmarshal(doc) for schema check: %v", err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	violations, err := corpus.ValidateDataPackage(instance)
	if err != nil {
		t.Fatalf("ValidateDataPackage: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("Pack produced a manifest that fails its own schema: %+v\nwire: %s", violations, raw)
	}
}

// snapshotTree reads every regular file under root (relative path -> exact
// bytes), so a test can prove Pack wrote nothing by comparing a before and
// an after snapshot of the one directory Pack ever touches.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
		return nil
	}); err != nil {
		t.Fatalf("snapshotTree(%s): %v", root, err)
	}
	return out
}

func assertTreeUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("tree under %s changed: before had %d files, after has %d", root, len(before), len(after))
	}
	for path, contents := range before {
		if after[path] != contents {
			t.Fatalf("tree under %s changed: %s differs (or is missing) after Pack", root, path)
		}
	}
}

// --- happy path ---

func TestPack_JSON_HappyPath_ProducesSchemaValidManifest(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "data/orders.json", []byte(`[{"id":1},{"id":2}]`))
	writeLocalFile(t, source, "README.md", []byte("hello"))

	entries := map[string]EntryDeclaration{
		"data/orders.json": {Role: RoleDataset, MediaType: "application/json", ConformsTo: "schema/order.schema.json"},
		"README.md":        {Role: RoleReadme, MediaType: "text/markdown"},
	}
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, entries, checker)

	doc, err := Pack(t.Context(), req)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if doc.Schema != "data-package/v1" {
		t.Errorf("Schema = %q, want data-package/v1", doc.Schema)
	}
	if !packageIDPattern.MatchString(doc.ID) {
		t.Errorf("ID = %q, does not match the DP- id shape", doc.ID)
	}
	if doc.Attempt != 1 || doc.Supersedes != "" {
		t.Errorf("Attempt/Supersedes = %d/%q, want 1/\"\" for a first attempt", doc.Attempt, doc.Supersedes)
	}
	if doc.RecordCount != 2 {
		t.Errorf("RecordCount = %d, want 2 (the orders.json array length)", doc.RecordCount)
	}
	if doc.AggregateDigest == "" {
		t.Error("AggregateDigest is empty")
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(doc.Entries))
	}
	if len(checker.calledPaths) != 1 || checker.calledPaths[0] != "data/orders.json" {
		t.Errorf("checker.calledPaths = %v, want exactly [data/orders.json] (README is not a dataset entry)", checker.calledPaths)
	}
	var ordersEntry, readmeEntry Entry
	for _, e := range doc.Entries {
		switch e.Path {
		case "data/orders.json":
			ordersEntry = e
		case "README.md":
			readmeEntry = e
		}
	}
	if ordersEntry.RecordCount == nil || *ordersEntry.RecordCount != 2 {
		t.Errorf("orders.json entry RecordCount = %v, want pointer to 2", ordersEntry.RecordCount)
	}
	if ordersEntry.ConformsTo != "schema/order.schema.json" {
		t.Errorf("orders.json entry ConformsTo = %q", ordersEntry.ConformsTo)
	}
	if readmeEntry.RecordCount != nil {
		t.Errorf("README.md entry RecordCount = %v, want nil (not a dataset entry)", readmeEntry.RecordCount)
	}
	if readmeEntry.ConformsTo != "" {
		t.Errorf("README.md entry ConformsTo = %q, want empty (non-dataset entries never carry one)", readmeEntry.ConformsTo)
	}

	assertValidatesAgainstSchema(t, doc)
}

func TestPack_NDJSON_HappyPath_SupersedingAttempt_ProducesSchemaValidManifest(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "data/orders.ndjson", []byte("{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n"))
	writeLocalFile(t, source, "data/index.json", []byte(`{"count":3}`))

	entries := map[string]EntryDeclaration{
		"data/orders.ndjson": {Role: RoleDataset, MediaType: "application/x-ndjson", ConformsTo: "schema/order.schema.json"},
		"data/index.json":    {Role: RoleIndex, MediaType: "application/json"},
	}
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, entries, checker)
	req.Format = FormatNDJSON
	req.DataProfile = DataProfileSanitized
	req.Attempt = 2
	req.Supersedes = &Supersedes{ID: "DP-axon-20260804-k3f9", Attempt: 1}

	doc, err := Pack(t.Context(), req)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if doc.Attempt != 2 || doc.Supersedes != "DP-axon-20260804-k3f9" {
		t.Errorf("Attempt/Supersedes = %d/%q, want 2/DP-axon-20260804-k3f9", doc.Attempt, doc.Supersedes)
	}
	if doc.RecordCount != 3 {
		t.Errorf("RecordCount = %d, want 3", doc.RecordCount)
	}
	// The package-level RecordCount must be DERIVED from the dataset
	// entries' own pointers, not a coincidentally-matching separate
	// computation: with exactly one dataset entry, the aggregate and that
	// entry's own pointer must agree, and the non-dataset (index) entry
	// must carry no record count at all.
	var ordersEntry, indexEntry Entry
	for _, e := range doc.Entries {
		switch e.Path {
		case "data/orders.ndjson":
			ordersEntry = e
		case "data/index.json":
			indexEntry = e
		}
	}
	if ordersEntry.RecordCount == nil || doc.RecordCount != *ordersEntry.RecordCount {
		t.Errorf("doc.RecordCount = %d, orders.ndjson entry RecordCount = %v, want them equal and non-nil", doc.RecordCount, ordersEntry.RecordCount)
	}
	if indexEntry.RecordCount != nil {
		t.Errorf("index.json entry RecordCount = %v, want nil (not a dataset entry)", indexEntry.RecordCount)
	}
	assertValidatesAgainstSchema(t, doc)
}

// --- AC-2: production profile refused before staging ---

func TestPack_RefusesProductionProfile_BeforeStaging(t *testing.T) {
	t.Parallel()
	// Source does not exist: if the profile check did not run first, the
	// walk would fail with a filesystem error instead, and this test would
	// not be able to tell the two apart. Pointing Source at a nonexistent
	// path turns "checked first" into a distinguishable error TYPE, not
	// merely an error being non-nil.
	source := filepath.Join(t.TempDir(), "does-not-exist")
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, nil, checker)
	req.DataProfile = DataProfileProduction

	_, err := Pack(t.Context(), req)
	if !errors.Is(err, ErrProductionProfile) {
		t.Fatalf("Pack(production) = %v, want errors.Is ErrProductionProfile", err)
	}
	if len(checker.calledPaths) != 0 {
		t.Errorf("checker was called %v, want it never invoked before the profile refusal", checker.calledPaths)
	}
}

// --- locator refusal ---

func TestPack_RefusesUnsafeLocator(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"absolute path":     "/var/lib/space/deliveries/DP-axon-20260804-k3f9",
		"credential shaped": "https://token:s3cr3t@space.internal/deliveries/DP-axon-20260804-k3f9",
	}
	for name, locator := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := t.TempDir()
			checker := &fakeEntryChecker{}
			req := basePackRequest(t, source, nil, checker)
			req.Locator = locator

			_, err := Pack(t.Context(), req)
			if !errors.Is(err, ErrUnsafeLocator) {
				t.Fatalf("Pack(locator=%q) = %v, want errors.Is ErrUnsafeLocator", locator, err)
			}
			if len(checker.calledPaths) != 0 {
				t.Errorf("checker was called %v, want it never invoked before the locator refusal", checker.calledPaths)
			}
		})
	}
}

// --- AC-14: attempt ceiling, both directions ---

func TestPack_AttemptCeiling_SetRefusesAttemptOverMax(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, nil, checker)
	req.MaxAttempts = 2
	req.Attempt = 3
	req.Supersedes = &Supersedes{ID: "DP-axon-20260804-k3f9", Attempt: 2}

	_, err := Pack(t.Context(), req)
	if !errors.Is(err, ErrAttemptCeiling) {
		t.Fatalf("Pack(attempt 3, max 2) = %v, want errors.Is ErrAttemptCeiling", err)
	}
	if !strings.Contains(err.Error(), "escalat") {
		t.Errorf("Pack(attempt 3, max 2) error = %q, want it to name escalation as the remaining action", err.Error())
	}
	if len(checker.calledPaths) != 0 {
		t.Errorf("checker was called %v, want it never invoked before the ceiling refusal", checker.calledPaths)
	}
}

func TestPack_AttemptCeiling_UnsetRefusesNothing(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "README.md", []byte("hello"))
	entries := map[string]EntryDeclaration{"README.md": {Role: RoleReadme, MediaType: "text/markdown"}}
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, entries, checker)
	req.MaxAttempts = 0 // unset — the shipped default
	req.Attempt = 99
	req.Supersedes = &Supersedes{ID: "DP-axon-20260804-k3f9", Attempt: 98}

	doc, err := Pack(t.Context(), req)
	if err != nil {
		t.Fatalf("Pack(attempt 99, max unset) = %v, want nil (unset refuses nothing)", err)
	}
	if doc.Attempt != 99 {
		t.Errorf("Attempt = %d, want 99", doc.Attempt)
	}
}

// --- AC-1: one bad file among many refuses the whole pack ---

func TestPack_OneBadFileAmongManyRefusesTheWholePack(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "a.ndjson", []byte("{\"id\":1}\n"))
	writeLocalFile(t, source, "b.ndjson", []byte("{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n"))
	writeLocalFile(t, source, "c.ndjson", []byte("{\"id\":1}\n"))

	entries := map[string]EntryDeclaration{
		"a.ndjson": {Role: RoleDataset, MediaType: "application/x-ndjson", ConformsTo: "schema/order.schema.json"},
		"b.ndjson": {Role: RoleDataset, MediaType: "application/x-ndjson", ConformsTo: "schema/order.schema.json"},
		"c.ndjson": {Role: RoleDataset, MediaType: "application/x-ndjson", ConformsTo: "schema/order.schema.json"},
	}
	badRecord := int64(3)
	checker := &fakeEntryChecker{
		checks: map[string]Check{
			"b.ndjson": NewCheck("chk-order-shape", "b.ndjson", CheckFail, []InstanceViolation{
				{InstancePointer: "/id", SchemaPointer: "/properties/id", Message: "id must be a string", Record: &badRecord},
			}),
		},
	}
	req := basePackRequest(t, source, entries, checker)
	req.Format = FormatNDJSON

	before := snapshotTree(t, source)
	_, err := Pack(t.Context(), req)
	if !errors.Is(err, ErrEntryNonConforming) {
		t.Fatalf("Pack(one bad file) = %v, want errors.Is ErrEntryNonConforming", err)
	}
	if !strings.Contains(err.Error(), "b.ndjson") {
		t.Errorf("error = %q, want it to name b.ndjson", err.Error())
	}
	if !strings.Contains(err.Error(), "record 3") {
		t.Errorf("error = %q, want it to name record 3", err.Error())
	}
	// The whole pack is refused at the first offending entry in sorted
	// order — c.ndjson (alphabetically after the bad file) is never even
	// checked.
	if got := checker.calledPaths; len(got) != 2 || got[0] != "a.ndjson" || got[1] != "b.ndjson" {
		t.Errorf("checker.calledPaths = %v, want exactly [a.ndjson b.ndjson] (c.ndjson never reached)", got)
	}
	assertTreeUnchanged(t, source, before)
}

// --- record-count bound (D-11 second row) ---

func TestPack_RecordCountBound_RefusesAtBoundPassesOneUnder(t *testing.T) {
	t.Parallel()
	buildReq := func(t *testing.T, maxRecords int) (PackRequest, *fakeEntryChecker) {
		source := t.TempDir()
		writeLocalFile(t, source, "orders.ndjson", []byte("{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n"))
		entries := map[string]EntryDeclaration{
			"orders.ndjson": {Role: RoleDataset, MediaType: "application/x-ndjson", ConformsTo: "schema/order.schema.json"},
		}
		checker := &fakeEntryChecker{}
		req := basePackRequest(t, source, entries, checker)
		req.Format = FormatNDJSON
		req.Bounds = Bounds{MaxEntryBytes: 1 << 20, MaxPackageBytes: 1 << 20, MaxEntries: 10, MaxRecords: maxRecords}
		return req, checker
	}

	t.Run("refuses one under the count", func(t *testing.T) {
		t.Parallel()
		req, checker := buildReq(t, 2) // 3 records declared, bound is 2
		_, err := Pack(t.Context(), req)
		if !errors.Is(err, ErrBoundExceeded) {
			t.Fatalf("Pack(3 records, bound 2) = %v, want errors.Is ErrBoundExceeded", err)
		}
		if len(checker.calledPaths) != 0 {
			t.Errorf("checker was called %v, want it never invoked before the record-count refusal", checker.calledPaths)
		}
	})

	t.Run("passes exactly at the count", func(t *testing.T) {
		t.Parallel()
		req, _ := buildReq(t, 3)
		doc, err := Pack(t.Context(), req)
		if err != nil {
			t.Fatalf("Pack(3 records, bound 3) = %v, want nil", err)
		}
		if doc.RecordCount != 3 {
			t.Errorf("RecordCount = %d, want 3", doc.RecordCount)
		}
	})
}

// --- media_type must match format on dataset entries (D-11 third row) ---

func TestPack_RefusesMediaTypeMismatchOnDatasetEntry(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "orders.json", []byte(`[{"id":1}]`))
	entries := map[string]EntryDeclaration{
		"orders.json": {Role: RoleDataset, MediaType: "text/csv", ConformsTo: "schema/order.schema.json"},
	}
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, entries, checker)
	req.Format = FormatJSON

	_, err := Pack(t.Context(), req)
	if !errors.Is(err, ErrEntryNonConforming) {
		t.Fatalf("Pack(media_type mismatch) = %v, want errors.Is ErrEntryNonConforming", err)
	}
	if !strings.Contains(err.Error(), "media_type") {
		t.Errorf("error = %q, want it to name media_type", err.Error())
	}
	if len(checker.calledPaths) != 1 {
		t.Errorf("checker.calledPaths = %v, want the checker to have run (conformance is checked before media_type, step 6 before step 7)", checker.calledPaths)
	}
}

// TestPack_ConformanceCheckedBeforeMediaType proves the ORDER, not merely
// that both refusals exist: an entry that fails both conformance and
// media_type must be refused for conformance (step 6), never for
// media_type (step 7) — "order matters and is the design" (spec 05a "What
// to do").
func TestPack_ConformanceCheckedBeforeMediaType(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "orders.json", []byte(`[{"id":1}]`))
	entries := map[string]EntryDeclaration{
		"orders.json": {Role: RoleDataset, MediaType: "text/csv", ConformsTo: "schema/order.schema.json"}, // wrong media_type too
	}
	checker := &fakeEntryChecker{
		checks: map[string]Check{
			"orders.json": NewCheck("chk-order-shape", "orders.json", CheckFail, []InstanceViolation{
				{InstancePointer: "/id", SchemaPointer: "/properties/id", Message: "id must be a string"},
			}),
		},
	}
	req := basePackRequest(t, source, entries, checker)
	req.Format = FormatJSON

	_, err := Pack(t.Context(), req)
	if !errors.Is(err, ErrEntryNonConforming) {
		t.Fatalf("Pack = %v, want errors.Is ErrEntryNonConforming", err)
	}
	if !strings.Contains(err.Error(), "chk-order-shape") {
		t.Errorf("error = %q, want the conformance check's own id (step 6 refused first)", err.Error())
	}
	if strings.Contains(err.Error(), "does not match format") {
		t.Errorf("error = %q, must not be the media_type message: step 7 never runs once step 6 has refused", err.Error())
	}
}

// --- entry declaration coverage ---

func TestPack_RefusesWalkedFileWithNoDeclaration(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "mystery.json", []byte(`{}`))
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, map[string]EntryDeclaration{}, checker)

	_, err := Pack(t.Context(), req)
	if err == nil {
		t.Fatal("Pack(undeclared file) = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "mystery.json") {
		t.Errorf("error = %q, want it to name mystery.json", err.Error())
	}
}

func TestPack_RefusesDeclarationWithNoWalkedFile(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	entries := map[string]EntryDeclaration{
		"ghost.json": {Role: RoleReadme, MediaType: "text/markdown"},
	}
	checker := &fakeEntryChecker{}
	req := basePackRequest(t, source, entries, checker)

	_, err := Pack(t.Context(), req)
	if !errors.Is(err, ErrEntryMissing) {
		t.Fatalf("Pack(declared but absent) = %v, want errors.Is ErrEntryMissing", err)
	}
}

// --- attempt/supersedes consistency (§T2.1) ---

func TestPack_AttemptSupersedesConsistency(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		attempt    int
		supersedes *Supersedes
		wantErr    bool
	}{
		{"first attempt, no supersedes: ok", 1, nil, false},
		{"attempt 2 with no supersedes: refused", 2, nil, true},
		{"attempt 1 with a supersedes: refused", 1, &Supersedes{ID: "DP-axon-20260804-k3f9", Attempt: 1}, true},
		{"attempt matches supersedes+1: ok", 2, &Supersedes{ID: "DP-axon-20260804-k3f9", Attempt: 1}, false},
		{"attempt does not match supersedes+1: refused", 3, &Supersedes{ID: "DP-axon-20260804-k3f9", Attempt: 1}, true},
		{"supersedes with invalid attempt: refused", 1, &Supersedes{ID: "DP-axon-20260804-k3f9", Attempt: 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := t.TempDir()
			writeLocalFile(t, source, "README.md", []byte("hello"))
			entries := map[string]EntryDeclaration{"README.md": {Role: RoleReadme, MediaType: "text/markdown"}}
			checker := &fakeEntryChecker{}
			req := basePackRequest(t, source, entries, checker)
			req.Attempt = tc.attempt
			req.Supersedes = tc.supersedes

			_, err := Pack(t.Context(), req)
			if tc.wantErr && err == nil {
				t.Fatal("Pack = nil, want a refusal")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Pack = %v, want nil", err)
			}
		})
	}
}

// --- CountRecords (exported so verify, a later wave, reuses the same rule) ---

func TestCountRecords_NDJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"three records, trailing newline", "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n", 3},
		{"no trailing newline", "{\"a\":1}\n{\"a\":2}", 2},
		{"blank line skipped", "{\"a\":1}\n\n{\"a\":2}\n", 2},
		{"crlf line endings", "{\"a\":1}\r\n{\"a\":2}\r\n", 2},
		{"empty file", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CountRecords(FormatNDJSON, []byte(tc.raw))
			if err != nil {
				t.Fatalf("CountRecords: %v", err)
			}
			if got != tc.want {
				t.Errorf("CountRecords(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestCountRecords_JSON(t *testing.T) {
	t.Parallel()
	if got, err := CountRecords(FormatJSON, []byte(`[{"a":1},{"a":2},{"a":3}]`)); err != nil || got != 3 {
		t.Errorf("CountRecords(array) = %d, %v, want 3, nil", got, err)
	}
	if got, err := CountRecords(FormatJSON, []byte(`{"a":1}`)); err != nil || got != 1 {
		t.Errorf("CountRecords(object) = %d, %v, want 1, nil", got, err)
	}
	if got, err := CountRecords(FormatJSON, []byte(`[]`)); err != nil || got != 0 {
		t.Errorf("CountRecords(empty array) = %d, %v, want 0, nil", got, err)
	}
	if _, err := CountRecords(FormatJSON, []byte(`not json`)); err == nil {
		t.Error("CountRecords(malformed json) = nil error, want an error")
	}
}

func TestMediaTypeForFormat(t *testing.T) {
	t.Parallel()
	if got, ok := MediaTypeForFormat(FormatJSON); !ok || got != "application/json" {
		t.Errorf("MediaTypeForFormat(json) = %q, %v, want application/json, true", got, ok)
	}
	if got, ok := MediaTypeForFormat(FormatNDJSON); !ok || got != "application/x-ndjson" {
		t.Errorf("MediaTypeForFormat(ndjson) = %q, %v, want application/x-ndjson, true", got, ok)
	}
	if _, ok := MediaTypeForFormat("bogus"); ok {
		t.Error("MediaTypeForFormat(bogus) = true, want false")
	}
}
