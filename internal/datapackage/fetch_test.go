package datapackage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- fixtures ---

// fetchFixtureBase is a fixed instant so every test below is wall-clock
// independent, per the brief: the clock is injected as Fetch's own now
// parameter, never read from time.Now inside fetch.go.
var fetchFixtureBase = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func fetchTestBounds() Bounds {
	return Bounds{MaxEntryBytes: 1 << 20, MaxPackageBytes: 1 << 30, MaxEntries: 1000, MaxRecords: 1000}
}

// buildValidPackage builds a self-consistent Document + delivered []LocalFile
// pair: entries[] and aggregate_digest are derived from files via
// BuildEntrySet (never hand-authored, per the brief), expires_at is one hour
// after fetchFixtureBase, and every other required manifest field carries a
// plausible placeholder value Fetch itself does not inspect.
func buildValidPackage(t *testing.T, files []LocalFile) (Document, []LocalFile) {
	t.Helper()
	built, err := BuildEntrySet(files)
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}
	doc := Document{
		Schema:          "data-package/v1",
		ID:              "DP-a-20260804-ab12",
		Thread:          "thread-1",
		Contract:        "XC-a-1@1.0.0#deadbeef",
		Mode:            ModeSnapshot,
		Classification:  "internal",
		DataProfile:     DataProfileSynthetic,
		Format:          FormatJSON,
		TransportDriver: TransportDriverSpaceGit,
		Locator:         "packages/attempt-1",
		Entries:         built.Entries,
		AggregateDigest: built.AggregateDigest,
		SizeBytes:       sumLocalFileBytes(files),
		RecordCount:     0,
		CreatedAt:       fetchFixtureBase.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:       fetchFixtureBase.Add(time.Hour).Format(time.RFC3339),
		Attempt:         1,
		Provenance:      Provenance{OriginSystem: "system-a", ExtractedAt: fetchFixtureBase.Add(-time.Hour).Format(time.RFC3339)},
	}
	return doc, files
}

func sumLocalFileBytes(files []LocalFile) int64 {
	var total int64
	for _, f := range files {
		total += int64(len(f.Bytes))
	}
	return total
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%s): %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// --- happy path ---

func TestFetch_HappyPath_InstallsIntoCleanDestination(t *testing.T) {
	t.Parallel()
	files := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		{RelPath: "sub/b.json", Bytes: []byte(`{"b":2}`)},
	}
	doc, delivered := buildValidPackage(t, files)

	parent := t.TempDir()
	destination := filepath.Join(parent, "pkg") // must not exist yet, see TestFetch trap note below

	outcome, verified, err := Fetch(t.Context(), doc, delivered, destination, fetchTestBounds(), fetchFixtureBase)
	if err != nil {
		t.Fatalf("Fetch = %v", err)
	}
	if outcome != Installed {
		t.Fatalf("outcome = %q, want %q", outcome, Installed)
	}
	if verified.AggregateDigest != doc.AggregateDigest {
		t.Fatalf("verified.AggregateDigest = %s, want %s", verified.AggregateDigest, doc.AggregateDigest)
	}
	got, err := os.ReadFile(filepath.Join(destination, "a.json"))
	if err != nil {
		t.Fatalf("read installed a.json: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("installed a.json = %q, want %q", got, `{"a":1}`)
	}
}

func TestFetch_ByteIdenticalDestinationAccepted(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, delivered := buildValidPackage(t, files)
	destination := filepath.Join(t.TempDir(), "pkg")
	bounds := fetchTestBounds()

	if _, _, err := Fetch(t.Context(), doc, delivered, destination, bounds, fetchFixtureBase); err != nil {
		t.Fatalf("first Fetch = %v", err)
	}

	// A re-fetch that finds exactly what it would have written is a
	// success (plan D-5): the same manifest and the same delivered bytes,
	// fetched a second time into the same destination.
	outcome, _, err := Fetch(t.Context(), doc, delivered, destination, bounds, fetchFixtureBase)
	if err != nil {
		t.Fatalf("second Fetch = %v", err)
	}
	if outcome != AlreadyPresent {
		t.Fatalf("second Fetch outcome = %q, want %q", outcome, AlreadyPresent)
	}
}

// --- AC-4: tampered digest refused, before any write, destination untouched ---

func TestFetch_TamperedDigestRefused_DestinationUntouched(t *testing.T) {
	t.Parallel()
	originalFiles := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		{RelPath: "b.json", Bytes: []byte(`{"b":2}`)},
	}
	doc, _ := buildValidPackage(t, originalFiles)

	// Flip one byte of the delivered bytes (the mirror) without touching
	// the manifest's own declared digest: this is the "declared digest !=
	// computed digest" refusal of §T2.4, exercised on the DELIVERED side.
	tamperedFiles := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		{RelPath: "b.json", Bytes: []byte(`{"b":9}`)}, // same length, different byte
	}

	parent := t.TempDir()
	destination := filepath.Join(parent, "pkg")
	before := readDirNames(t, parent)

	_, _, err := Fetch(t.Context(), doc, tamperedFiles, destination, fetchTestBounds(), fetchFixtureBase)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Fetch(tampered) = %v, want errors.Is ErrDigestMismatch", err)
	}

	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination %s exists after a refused fetch", destination)
	}
	// Compare the PARENT directory's listing, not merely the leaf's
	// absence: a stray ".a2a-datapackage-install-<hex>" temporary sibling
	// left behind by a write that started before the refusal would still
	// pass a bare os.Stat(destination) check.
	after := readDirNames(t, parent)
	if len(before) != len(after) {
		t.Fatalf("parent directory changed after a refused fetch: before %v, after %v", before, after)
	}
}

// --- destination already holds different bytes (space integration §6.3 shape) ---

func TestFetch_DivergentDestinationRefused_LeftUnchanged(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, delivered := buildValidPackage(t, files)

	destination := filepath.Join(t.TempDir(), "pkg")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "a.json"), []byte(`{"a":999}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := Fetch(t.Context(), doc, delivered, destination, fetchTestBounds(), fetchFixtureBase)
	if !errors.Is(err, ErrDestinationDiverged) {
		t.Fatalf("Fetch(divergent destination) = %v, want errors.Is ErrDestinationDiverged", err)
	}
	got, readErr := os.ReadFile(filepath.Join(destination, "a.json"))
	if readErr != nil {
		t.Fatalf("read destination after refused fetch: %v", readErr)
	}
	if string(got) != `{"a":999}` {
		t.Fatalf("destination content changed after a refused fetch: got %q", got)
	}
}

// --- expiry ---

func TestFetch_ExpiredRefused(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, delivered := buildValidPackage(t, files)
	doc.ExpiresAt = fetchFixtureBase.Add(-time.Minute).Format(time.RFC3339) // in the past, otherwise valid

	destination := filepath.Join(t.TempDir(), "pkg")
	_, _, err := Fetch(t.Context(), doc, delivered, destination, fetchTestBounds(), fetchFixtureBase)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Fetch(expired) = %v, want errors.Is ErrExpired", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatal("destination exists after a refused (expired) fetch")
	}
}

func TestFetch_NotYetExpiredAtExactBoundaryAccepted(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, delivered := buildValidPackage(t, files)
	// now == expires_at exactly: "after this instant a fetch is refused"
	// (spec 05a §T2.1) is read at face value — the instant itself is not
	// yet "after". Recorded as a closed question in this brief's deviation
	// report, not left implicit.
	now := fetchFixtureBase.Add(time.Hour)
	doc.ExpiresAt = now.Format(time.RFC3339)

	destination := filepath.Join(t.TempDir(), "pkg")
	if _, _, err := Fetch(t.Context(), doc, delivered, destination, fetchTestBounds(), now); err != nil {
		t.Fatalf("Fetch(now == expires_at) = %v, want acceptance", err)
	}
}

func TestFetch_MalformedExpiresAtIsNotClaimedExpired(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, delivered := buildValidPackage(t, files)
	doc.ExpiresAt = "not-a-timestamp"

	destination := filepath.Join(t.TempDir(), "pkg")
	_, _, err := Fetch(t.Context(), doc, delivered, destination, fetchTestBounds(), fetchFixtureBase)
	if err == nil {
		t.Fatal("Fetch(malformed expires_at) = nil, want an error")
	}
	if errors.Is(err, ErrExpired) {
		t.Fatalf("Fetch(malformed expires_at) = %v, want it NOT to claim ErrExpired (unreadable is not the same claim as past)", err)
	}
}

// --- bounds re-checked at fetch, not trusted from pack ---

func TestFetch_BoundsReCheckedAgainstDeliveredBytes(t *testing.T) {
	t.Parallel()
	// The manifest's own declared entry is small and compliant; what is
	// actually delivered is oversized. A consumer that trusted the
	// manifest's claimed size would never catch this — the whole point of
	// re-checking bounds against the DELIVERED bytes at fetch.
	smallOriginal := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, _ := buildValidPackage(t, smallOriginal)

	bounds := Bounds{MaxEntryBytes: 16, MaxPackageBytes: 1 << 20, MaxEntries: 10, MaxRecords: 10}
	oversized := []LocalFile{{RelPath: "a.json", Bytes: []byte(strings.Repeat("x", 64))}}

	destination := filepath.Join(t.TempDir(), "pkg")
	_, _, err := Fetch(t.Context(), doc, oversized, destination, bounds, fetchFixtureBase)
	if !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("Fetch(oversized delivery) = %v, want errors.Is ErrBoundExceeded", err)
	}
}

// --- AC-11: entry-level refusal, not an aggregate-level one ---

// TestFetch_TamperedEntryRefusedAtEntry_NotAggregate is spec 05a's L-3
// adversarial case, at the Fetch boundary rather than VerifyEntrySet's own
// (entryset_test.go already proves the primitive; this proves Fetch wires
// it without adding a shortcut of its own). The manifest's declared
// aggregate_digest is deliberately set to what a naive "hash everything
// fresh, compare one number" checker would compute over the TAMPERED bytes
// — i.e. it "matches" the delivered content at the aggregate level — so the
// only thing that can catch the tamper is the per-entry check, and it must
// fire before Fetch's own aggregate comparison (step 4) is ever reached.
func TestFetch_TamperedEntryRefusedAtEntry_NotAggregate(t *testing.T) {
	t.Parallel()
	originalFiles := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		{RelPath: "b.json", Bytes: []byte(`{"b":2}`)},
	}
	doc, _ := buildValidPackage(t, originalFiles) // doc.Entries carries b.json's ORIGINAL declared digest

	tamperedFiles := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		{RelPath: "b.json", Bytes: []byte(`{"b":9}`)}, // same length, different content
	}
	naiveTampered, err := BuildEntrySet(tamperedFiles)
	if err != nil {
		t.Fatalf("BuildEntrySet(tampered): %v", err)
	}
	if naiveTampered.AggregateDigest == doc.AggregateDigest {
		t.Fatal("test setup bug: tampering must change the naive aggregate, or this test proves nothing")
	}
	doc.AggregateDigest = naiveTampered.AggregateDigest // the aggregate now "matches" the tampered bytes

	destination := filepath.Join(t.TempDir(), "pkg")
	_, _, fetchErr := Fetch(t.Context(), doc, tamperedFiles, destination, fetchTestBounds(), fetchFixtureBase)
	if !errors.Is(fetchErr, ErrDigestMismatch) {
		t.Fatalf("Fetch(tampered entry, matching aggregate) = %v, want errors.Is ErrDigestMismatch", fetchErr)
	}
	got := fetchErr.Error()
	if !strings.Contains(got, "b.json") {
		t.Fatalf("Fetch error = %q, want it to name the tampered entry b.json (entry-level refusal)", got)
	}
	if strings.Contains(got, "manifest aggregate_digest") {
		t.Fatalf("Fetch error = %q, this is Fetch's own aggregate-level message — the per-entry check must fire first", got)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatal("destination exists after a refused (tampered-entry) fetch")
	}
}

// TestFetch_InconsistentManifestAggregateRefused is Fetch's own step 4: a
// manifest whose declared aggregate_digest disagrees with entries that each
// individually verify fine (a defect in the manifest itself, distinct from
// AC-11's per-entry tamper) is still refused, just one step later.
func TestFetch_InconsistentManifestAggregateRefused(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, delivered := buildValidPackage(t, files)
	doc.AggregateDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

	destination := filepath.Join(t.TempDir(), "pkg")
	_, _, err := Fetch(t.Context(), doc, delivered, destination, fetchTestBounds(), fetchFixtureBase)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Fetch(inconsistent manifest aggregate) = %v, want errors.Is ErrDigestMismatch", err)
	}
	if !strings.Contains(err.Error(), "manifest aggregate_digest") {
		t.Fatalf("Fetch error = %q, want it to name the manifest aggregate_digest mismatch", err.Error())
	}
}

// --- ordering: nothing is written before verification completes ---

func TestFetch_ContextCanceledBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	doc, delivered := buildValidPackage(t, files)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	destination := filepath.Join(t.TempDir(), "pkg")
	_, _, err := Fetch(ctx, doc, delivered, destination, fetchTestBounds(), fetchFixtureBase)
	if err == nil {
		t.Fatal("Fetch(canceled context) = nil, want an error")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatal("destination exists after a canceled fetch")
	}
}
