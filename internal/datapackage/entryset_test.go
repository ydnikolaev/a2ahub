package datapackage

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildEntrySet_SortsByPathRegardlessOfInputOrder(t *testing.T) {
	t.Parallel()

	orderA := []LocalFile{
		{RelPath: "c.json", Bytes: []byte(`"c"`)},
		{RelPath: "a.json", Bytes: []byte(`"a"`)},
		{RelPath: "b.json", Bytes: []byte(`"b"`)},
	}
	orderB := []LocalFile{
		{RelPath: "b.json", Bytes: []byte(`"b"`)},
		{RelPath: "a.json", Bytes: []byte(`"a"`)},
		{RelPath: "c.json", Bytes: []byte(`"c"`)},
	}

	setA, err := BuildEntrySet(orderA)
	if err != nil {
		t.Fatalf("BuildEntrySet(orderA): %v", err)
	}
	setB, err := BuildEntrySet(orderB)
	if err != nil {
		t.Fatalf("BuildEntrySet(orderB): %v", err)
	}

	wantPaths := []string{"a.json", "b.json", "c.json"}
	for i, want := range wantPaths {
		if setA.Entries[i].Path != want {
			t.Fatalf("setA.Entries[%d].Path = %q, want %q", i, setA.Entries[i].Path, want)
		}
		if setB.Entries[i].Path != want {
			t.Fatalf("setB.Entries[%d].Path = %q, want %q", i, setB.Entries[i].Path, want)
		}
	}
	if setA.AggregateDigest != setB.AggregateDigest {
		t.Fatalf("AggregateDigest differs by input order: %s vs %s", setA.AggregateDigest, setB.AggregateDigest)
	}
	for path, digest := range setA.PerFileDigest {
		if setB.PerFileDigest[path] != digest {
			t.Fatalf("PerFileDigest[%q] differs by input order: %s vs %s", path, digest, setB.PerFileDigest[path])
		}
	}
}

func TestBuildEntrySet_DuplicatePathRefused(t *testing.T) {
	t.Parallel()
	files := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`"a"`)},
		{RelPath: "a.json", Bytes: []byte(`"a-again"`)},
	}
	_, err := BuildEntrySet(files)
	if !errors.Is(err, ErrDuplicateEntryPath) {
		t.Fatalf("BuildEntrySet(duplicate path) = %v, want errors.Is ErrDuplicateEntryPath", err)
	}
}

func TestVerifyEntrySet_AcceptsUntamperedBytes(t *testing.T) {
	t.Parallel()
	files := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		{RelPath: "b.json", Bytes: []byte(`{"b":2}`)},
	}
	built, err := BuildEntrySet(files)
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}

	verified, err := VerifyEntrySet(built.Entries, files)
	if err != nil {
		t.Fatalf("VerifyEntrySet(untampered): %v", err)
	}
	if verified.AggregateDigest != built.AggregateDigest {
		t.Fatalf("VerifyEntrySet aggregate = %s, want %s", verified.AggregateDigest, built.AggregateDigest)
	}
}

func TestVerifyEntrySet_MissingDeclaredFileRefused(t *testing.T) {
	t.Parallel()
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte(`{"a":1}`)}}
	built, err := BuildEntrySet(files)
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}

	_, err = VerifyEntrySet(built.Entries, nil)
	if !errors.Is(err, ErrEntryMissing) {
		t.Fatalf("VerifyEntrySet(no delivered bytes) = %v, want errors.Is ErrEntryMissing", err)
	}
}

// TestVerifyEntrySet_TamperedEntryRefusedBeforeAggregate is spec 05a's L-3
// adversarial case (AC-11, §6.2, §6.4): "a package whose aggregate digest
// matches while one entry does not is refused at that entry, before
// assembly."
//
// The attack this simulates: an adversary (or a bug) tampers b.json's
// bytes on disk, then recomputes an aggregate over the CURRENT (tampered)
// bytes of every file — the aggregate a naive "hash everything, compare
// one number" checker would accept, because it happens to match what that
// checker would independently compute from the same tampered bytes it is
// about to trust. VerifyEntrySet must still refuse, and must refuse citing
// the ONE entry whose bytes contradict its own DECLARED digest — because
// this function never consults or is even given any externally supplied
// aggregate to be fooled by in the first place; it only ever compares a
// declared per-entry digest to freshly computed bytes.
func TestVerifyEntrySet_TamperedEntryRefusedBeforeAggregate(t *testing.T) {
	t.Parallel()
	originalFiles := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		{RelPath: "b.json", Bytes: []byte(`{"b":2}`)},
	}
	built, err := BuildEntrySet(originalFiles)
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}
	declared := built.Entries // carries b.json's ORIGINAL declared digest

	tamperedFiles := []LocalFile{
		{RelPath: "a.json", Bytes: []byte(`{"a":1}`)},
		// Same length as the original `{"b":2}` (7 bytes) so this test
		// exercises the DIGEST comparison specifically, not the separate
		// size comparison VerifyEntrySet also makes — a same-length,
		// different-content tamper is exactly the case a byte-count check
		// alone cannot catch.
		{RelPath: "b.json", Bytes: []byte(`{"b":9}`)},
	}

	// The naive aggregate-first check this test guards against: an
	// aggregate computed fresh over the CURRENT (tampered) bytes. If a
	// checker compared only this number to some externally supplied
	// "expected" aggregate that also happened to be updated to match, it
	// would accept the tampered package. VerifyEntrySet is never even
	// handed such a value — confirmed by its signature, which takes no
	// aggregate parameter at all — so there is no path by which this
	// number could short-circuit the per-entry check below.
	naiveTamperedSet, err := BuildEntrySet(tamperedFiles)
	if err != nil {
		t.Fatalf("BuildEntrySet(tampered): %v", err)
	}
	if naiveTamperedSet.AggregateDigest == built.AggregateDigest {
		t.Fatal("test setup bug: tampering must change the naive aggregate, or this test proves nothing")
	}

	_, err = VerifyEntrySet(declared, tamperedFiles)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("VerifyEntrySet(tampered b.json) = %v, want errors.Is ErrDigestMismatch", err)
	}
	if got := err.Error(); !strings.Contains(got, "b.json") {
		t.Fatalf("VerifyEntrySet(tampered b.json) error = %q, want it to name b.json", got)
	}
}
