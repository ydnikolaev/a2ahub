package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"
)

// TestExportSourceV1_ReproducibleFromShippedTextAlone is P7 §8 row 2
// (computed-not-listed-2026-08): an implementation written from
// schemas/templates/v2/contract.md's own generated_from comment block
// alone — the shipped bytes a third party actually receives, per that
// file's own §5 elements (separator bytes, sort order, combining hash,
// output encoding, per-file value encoding) — reproduces the SAME digest
// this package's own production combine (CombineDigestPairs + Digest)
// computes.
//
// This is the test's own fixture-agreement check, not the external
// implementer's independence: ExportSource() (internal/contract) is not
// used here — internal/contract imports internal/artifact, so reaching it
// from this package's own test would either need an external
// `artifact_test` package or accept the added indirection for no extra
// proof value. internal/contract/set.go:211,274,299,580 already show
// CombineDigestPairs/Digest ARE the algorithm export-source-v1 runs (both
// call sites are artifact.CombineDigestPairs / artifact.Digest directly,
// no separate combine implementation in internal/contract) — so comparing
// against them here is comparing against the real production path, one
// import hop closer than ExportSource() would be. What this test
// falsifies is unchanged either way: that reimplementFromShippedText,
// built from nothing but the documented steps, produces byte-identical
// output to the shipped implementation.
func TestExportSourceV1_ReproducibleFromShippedTextAlone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		files map[string]string // repo-relative path -> raw file content
	}{
		{
			name:  "empty file set",
			files: map[string]string{},
		},
		{
			name: "single-file set",
			files: map[string]string{
				"schema/ingest.schema.json": `{"type":"object"}`,
			},
		},
		{
			name: "multi-file set, a path byte that affects sort order",
			files: map[string]string{
				"fixtures/valid/ingest.json":   `{"a":1}`,
				"fixtures/invalid/ingest.json": `{"a":"x"}`,
				"schema/ingest.schema.json":    `{"type":"object"}`,
				// "-" (0x2d) sorts before "/" (0x2f) in byte-lexicographic
				// order, so this path must land BEFORE "schema/..." above
				// despite both starting "schema".
				"schema-notes.txt": `not a declared artifact, just a sort probe`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Production algorithm: this package's own CombineDigestPairs
			// over per-file values from Digest — the fixture this test's
			// own comparison target.
			perFile := make(map[string]string, len(tc.files))
			for path, content := range tc.files {
				perFile[path] = Digest([]byte(content))
			}
			want := CombineDigestPairs(perFile)

			// An implementation written from contract.md's shipped text
			// ALONE: no call into this package's own combine or digest
			// functions, no access to docs/, no oracle run.
			got := reimplementFromShippedText(tc.files)

			if got != want {
				t.Fatalf("reimplementFromShippedText() = %q, want %q (production CombineDigestPairs/Digest)", got, want)
			}
		})
	}
}

// reimplementFromShippedText is an INDEPENDENT reimplementation of
// export-source-v1's combine, written from nothing but
// schemas/templates/v2/contract.md's own generated_from comment block
// (this phase's own addition — the five unstated elements):
//
//  1. per-file value: "sha256:" + lowercase hex of SHA-256(raw bytes)
//  2. pair list: (contract-root-relative path, per-file value)
//  3. sort order: Go byte-lexicographic ascending on the path
//  4. combine: path-bytes + 0x00 + per-file-value-bytes + '\n', in sorted
//     order, into one SHA-256 hash
//  5. output encoding: "sha256:" + lowercase hex of the combined sum
//
// Deliberately does not call artifact.Digest or artifact.CombineDigestPairs
// — that would make this test compare the production implementation
// against itself, which proves nothing about the SHIPPED TEXT'S own
// sufficiency (this test's own falsification target, per the phase spec's
// §8 row 2 note distinguishing the test's fixture use of the production
// algorithm from the external implementer's independence from it).
func reimplementFromShippedText(files map[string]string) string {
	type pair struct {
		path  string
		value string
	}
	pairs := make([]pair, 0, len(files))
	for path, content := range files {
		sum := sha256.Sum256([]byte(content))
		pairs = append(pairs, pair{path: path, value: "sha256:" + hex.EncodeToString(sum[:])})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].path < pairs[j].path })

	h := sha256.New()
	for _, p := range pairs {
		h.Write([]byte(p.path))
		h.Write([]byte{0x00})
		h.Write([]byte(p.value))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
