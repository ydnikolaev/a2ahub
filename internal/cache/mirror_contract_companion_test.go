package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMirrorFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A contract's companion is data its descriptor inventories, so a Markdown one
// has no frontmatter by design and is not a skipped artifact.
func TestWalkArtifacts_ContractCompanionIsNotASkippedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, rel := range []string{
		"axon/provides/export/artifacts/CHANGELOG.md",
		"axon/provides/export/schema/notes.md",
		"axon/provides/export/fixtures/valid/case.md",
		"axon/provides/export/fixtures/invalid/case.md",
	} {
		writeMirrorFile(t, dir, rel, "# prose, no frontmatter\n")
	}

	artifacts, skips, err := walkArtifacts(dir)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want none: a companion must not be folded in as an envelope", artifacts)
	}
	if len(skips) != 0 {
		t.Fatalf("skips = %+v, want none: a companion must be skipped silently", skips)
	}
}

// The exemption stops at the contract's own companion directories: the
// descriptor, an unknown directory beside them and an artifact elsewhere in the
// section are still envelopes, and a malformed one still reports.
func TestWalkArtifacts_MalformedArtifactBesideContractCompanionsStillReported(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMirrorFile(t, dir, "axon/provides/export/artifacts/CHANGELOG.md", "# prose\n")
	want := []string{
		"axon/exchanges/XQ-axon-bad.md",
		"axon/provides/export/contract.md",
		"axon/provides/export/elsewhere/notes.md",
	}
	for _, rel := range want {
		writeMirrorFile(t, dir, rel, "not frontmatter shaped at all\n")
	}

	_, skips, err := walkArtifacts(dir)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}
	if len(skips) != len(want) {
		t.Fatalf("skips = %+v, want exactly %v", skips, want)
	}
	for i, rel := range want {
		if skips[i].Path != rel || skips[i].Reason != SkipReasonNotFrontmatterShaped {
			t.Fatalf("skips[%d] = %+v, want %s reported as not frontmatter shaped", i, skips[i], rel)
		}
	}
}
