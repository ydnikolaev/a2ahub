package cache

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildArtifactIndex_IndexesArtifactPathThreadDigest proves
// BuildArtifactIndex's own contract for the resolver-facing shape (spec
// agent-ops-2026-07/specs/01-resolver-one-home.md §2): id -> {path,
// thread, digest}, sourced from the SAME walk/decode chain buildIndex's
// own fold path uses.
func TestBuildArtifactIndex_IndexesArtifactPathThreadDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	relPath := "axon/exchanges/XR-axon-country-vocabulary.md"
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nid: XR-axon-country-vocabulary\ntype: requirement\nthread: thread:axon-20260729-c7q2\n---\nbody\n"
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	idx, skipped, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %+v, want none for a clean tree", skipped)
	}
	entry, ok := idx["XR-axon-country-vocabulary"]
	if !ok {
		t.Fatal("index does not carry the indexed artifact's id")
	}
	if entry.Path != relPath {
		t.Fatalf("Path = %q, want %q", entry.Path, relPath)
	}
	if entry.Thread != "thread:axon-20260729-c7q2" {
		t.Fatalf("Thread = %q, want thread:axon-20260729-c7q2", entry.Thread)
	}
	if entry.Digest == "" {
		t.Fatal("Digest = \"\", want a non-empty walk-time digest")
	}
}

// TestBuildArtifactIndex_OneUndecodableFileNeverBlindsTheOthers is AC-1.1's
// proof at the cache layer: a duplicated `thread:` key (the exact
// malformed shape filed 2026-07-26, SkipReasonUndecodableYAML) two
// directories away from a real artifact must never blind the walk to that
// real artifact, and must be reported, not merely dropped.
func TestBuildArtifactIndex_OneUndecodableFileNeverBlindsTheOthers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	goodPath := filepath.Join(dir, "axon", "exchanges", "XQ-axon-20260721-good1.md")
	if err := os.MkdirAll(filepath.Dir(goodPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(goodPath, []byte("---\nid: XQ-axon-20260721-good1\ntype: question\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}

	badRelPath := "beta/exchanges/XW-beta-20260721-bad.md"
	badPath := filepath.Join(dir, filepath.FromSlash(badRelPath))
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("---\nid: XW-beta-20260721-bad\nthread: thread:beta:one\nthread: thread:beta:two\n---\nbad body\n"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	idx, skipped, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}
	if _, ok := idx["XQ-axon-20260721-good1"]; !ok {
		t.Fatal("the good artifact was not indexed — one bad file elsewhere must never blind the walk to it")
	}
	if len(skipped) != 1 || skipped[0].Path != badRelPath || skipped[0].Reason != SkipReasonUndecodableYAML {
		t.Fatalf("skipped = %+v, want exactly one entry naming %q/%s", skipped, badRelPath, SkipReasonUndecodableYAML)
	}
}

// TestBuildArtifactIndex_ExcludesVendoredAndGit proves the lead decision
// recorded in plan.md's "Lead decisions taken before dispatch" §1 and
// spec §11's Amendments entry: vendored/ and .git are excluded from the
// unified resolver walk, matching walkArtifacts' own long-standing
// exclusion for the read model. An artifact placed under either directory
// must never resolve — a resolver index reaching further than the read
// model would produce a ref `validate` accepts but `a2a show` cannot
// display.
func TestBuildArtifactIndex_ExcludesVendoredAndGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	for _, sub := range []string{"vendored", ".git"} {
		p := filepath.Join(dir, sub, "exchanges", "XQ-axon-20260721-excluded.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("---\nid: XQ-axon-20260721-excluded\ntype: question\n---\nbody\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	idx, _, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}
	if _, ok := idx["XQ-axon-20260721-excluded"]; ok {
		t.Fatal("an artifact under vendored/ or .git resolved — both must be excluded from the resolver index (spec §11 amendment)")
	}
}
