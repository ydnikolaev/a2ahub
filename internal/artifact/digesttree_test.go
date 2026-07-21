package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDigestTreeFile writes content at root/rel, creating parent
// directories as needed — this test's own minimal fixture writer (no
// exported helper for this exists in this package).
func writeDigestTreeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeDigestTreeFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writeDigestTreeFile: write %s: %v", full, err)
	}
}

// TestDigestTreeFS is MED-5's own test for the moved multi-file digest
// tree helper (spec 08 §5.7/D-029): §5.7's exact algorithm, the
// schema/**+fixtures/** subtree scoping (contract.md excluded by not being
// under either), and an absent subtree treated as empty (not fatal).
func TestDigestTreeFS(t *testing.T) {
	t.Parallel()

	t.Run("combines schema and fixtures, excludes contract.md", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeDigestTreeFile(t, root, "contract.md", "---\nid: XC-axon-widget\n---\nbody\n")
		writeDigestTreeFile(t, root, "schema/main.schema.json", `{"type":"object"}`)
		writeDigestTreeFile(t, root, "fixtures/valid/ok.json", `{}`)

		digest, perFile, err := DigestTreeFS(root, []string{"schema", "fixtures"})
		if err != nil {
			t.Fatalf("DigestTreeFS: unexpected error: %v", err)
		}
		if len(perFile) != 2 {
			t.Fatalf("perFile = %+v, want exactly 2 entries (contract.md excluded)", perFile)
		}
		if _, ok := perFile["schema/main.schema.json"]; !ok {
			t.Fatalf("expected schema/main.schema.json in perFile, got %+v", perFile)
		}
		if _, ok := perFile["fixtures/valid/ok.json"]; !ok {
			t.Fatalf("expected fixtures/valid/ok.json in perFile, got %+v", perFile)
		}
		if want := CombineDigestPairs(perFile); digest != want {
			t.Fatalf("digest = %q, want CombineDigestPairs(perFile) = %q", digest, want)
		}
	})

	t.Run("absent subtree is empty, not fatal", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeDigestTreeFile(t, root, "schema/main.schema.json", `{"type":"object"}`)
		// fixtures/ deliberately never created.

		digest, perFile, err := DigestTreeFS(root, []string{"schema", "fixtures"})
		if err != nil {
			t.Fatalf("DigestTreeFS: unexpected error: %v", err)
		}
		if len(perFile) != 1 {
			t.Fatalf("perFile = %+v, want exactly 1 entry (absent fixtures/ skipped)", perFile)
		}
		if digest == "" {
			t.Fatal("expected a non-empty combined digest even with one absent subtree")
		}
	})

	t.Run("deterministic across two independent calls", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeDigestTreeFile(t, root, "schema/main.schema.json", `{"type":"object"}`)
		writeDigestTreeFile(t, root, "fixtures/valid/ok.json", `{}`)

		digest1, _, err := DigestTreeFS(root, []string{"schema", "fixtures"})
		if err != nil {
			t.Fatalf("DigestTreeFS (1st call): unexpected error: %v", err)
		}
		digest2, _, err := DigestTreeFS(root, []string{"schema", "fixtures"})
		if err != nil {
			t.Fatalf("DigestTreeFS (2nd call): unexpected error: %v", err)
		}
		if digest1 != digest2 {
			t.Fatalf("digest1 = %q, digest2 = %q; expected identical digests for identical content", digest1, digest2)
		}
	})

	t.Run("changed leaf content changes the combined digest", func(t *testing.T) {
		t.Parallel()
		rootA := t.TempDir()
		writeDigestTreeFile(t, rootA, "schema/main.schema.json", `{"type":"object"}`)
		rootB := t.TempDir()
		writeDigestTreeFile(t, rootB, "schema/main.schema.json", `{"type":"object","properties":{"x":{}}}`)

		digestA, _, err := DigestTreeFS(rootA, []string{"schema", "fixtures"})
		if err != nil {
			t.Fatalf("DigestTreeFS(rootA): unexpected error: %v", err)
		}
		digestB, _, err := DigestTreeFS(rootB, []string{"schema", "fixtures"})
		if err != nil {
			t.Fatalf("DigestTreeFS(rootB): unexpected error: %v", err)
		}
		if digestA == digestB {
			t.Fatalf("expected different digests for different leaf content, got the same: %q", digestA)
		}
	})
}

// TestCombineDigestPairs is §5.7's exact algorithm in isolation: "SHA-256
// over the sorted list of (contract-root-relative-path, sha256(file-
// bytes)) pairs" — path ORDER in the input map must not affect the
// combined digest (map iteration order is randomized; only the SORTED
// order may matter).
func TestCombineDigestPairs(t *testing.T) {
	t.Parallel()

	perFile := map[string]string{
		"schema/b.json": "sha256:bbb",
		"schema/a.json": "sha256:aaa",
	}
	got1 := CombineDigestPairs(perFile)
	got2 := CombineDigestPairs(perFile)
	if got1 != got2 {
		t.Fatalf("CombineDigestPairs is not deterministic across calls: %q vs %q", got1, got2)
	}
	if got1 == "" {
		t.Fatal("expected a non-empty combined digest")
	}

	empty := CombineDigestPairs(map[string]string{})
	if empty == "" {
		t.Fatal("expected a non-empty combined digest even for an empty perFile map")
	}
}
