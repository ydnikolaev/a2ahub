package cache

import (
	"os"
	"path/filepath"
	"testing"
)

// writeParentCriteriaArtifact writes a minimal envelope-shaped artifact
// directly under dir/id.md carrying criteriaYAML verbatim as its own
// frontmatter tail — this file's own arrange helper for
// AcceptanceCriteriaCount/AcceptanceCriteriaIDs, mirroring
// internal/cli/adapters_test.go's own writeParentArtifact (that helper
// lives in a different package and cannot be imported here).
func writeParentCriteriaArtifact(t *testing.T, dir, id, criteriaYAML string) {
	t.Helper()
	raw := "---\nid: " + id + "\ntype: work_request\n" + criteriaYAML + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeResponseCriteriaArtifact writes a minimal response artifact
// carrying a `parent:` field — ParentOf's own read target, mirroring
// internal/cli/adapters_test.go's own writeResponseArtifact.
func writeResponseCriteriaArtifact(t *testing.T, dir, id, parentID string) {
	t.Helper()
	raw := "---\nid: " + id + "\ntype: response\nparent: " + parentID + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildParentCriteriaIndex is this file's own arrange step: build a real
// BuildArtifactIndex over dir, failing the test on a walk error — every
// test below calls AcceptanceCriteriaCount/AcceptanceCriteriaIDs/ParentOf
// against the SAME index a caller (cli.MirrorResolver, mcp.MirrorResolver)
// would have already built via ensureIndex.
func buildParentCriteriaIndex(t *testing.T, dir string) map[string]ArtifactIndexEntry {
	t.Helper()
	idx, _, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}
	return idx
}

// TestAcceptanceCriteriaCount is P5's own direct proof at the shared seat
// (spec 05 §6, "the shared seat" row): declared count, unresolvable
// parent, and an absent field degrading to not-ok rather than a bare zero
// — the same three cases internal/cli's own (now-retired) method-level
// test covered, moved here since the logic itself moved here.
func TestAcceptanceCriteriaCount(t *testing.T) {
	t.Parallel()

	t.Run("declared criteria count", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentCriteriaArtifact(t, dir, "XW-axon-20260820-pc01", `acceptance_criteria: ["a", "b", "c"]`)
		idx := buildParentCriteriaIndex(t, dir)

		count, ok := AcceptanceCriteriaCount(dir, idx, "XW-axon-20260820-pc01")
		if !ok {
			t.Fatal("AcceptanceCriteriaCount: expected ok=true")
		}
		if count != 3 {
			t.Errorf("AcceptanceCriteriaCount = %d, want 3", count)
		}
	})

	t.Run("unknown parent degrades to not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		idx := buildParentCriteriaIndex(t, dir)

		count, ok := AcceptanceCriteriaCount(dir, idx, "XW-axon-does-not-exist")
		if ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=false for an unresolvable parent, got count=%d", count)
		}
	})

	t.Run("absent acceptance_criteria degrades to not-ok, never a bare zero", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentCriteriaArtifact(t, dir, "XH-axon-20260820-pc02", "deliverables: []")
		idx := buildParentCriteriaIndex(t, dir)

		count, ok := AcceptanceCriteriaCount(dir, idx, "XH-axon-20260820-pc02")
		if ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=false for an absent field, got count=%d", count)
		}
	})

	t.Run("parent with zero declared criteria is a determinable zero, not not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentCriteriaArtifact(t, dir, "XW-axon-20260820-pc03", "acceptance_criteria: []")
		idx := buildParentCriteriaIndex(t, dir)

		count, ok := AcceptanceCriteriaCount(dir, idx, "XW-axon-20260820-pc03")
		if !ok {
			t.Fatal("AcceptanceCriteriaCount: expected ok=true for a present-but-empty acceptance_criteria[]")
		}
		if count != 0 {
			t.Errorf("AcceptanceCriteriaCount = %d, want 0", count)
		}
	})
}

// TestAcceptanceCriteriaIDs is the id-addressed half (spec 05 §6:
// "bare-string and {id,text} forms"): the {id,text} shape resolves an
// ordered id list; the bare-string shape (and every unresolvable case)
// degrades to not-ok, never a slice of empty strings.
func TestAcceptanceCriteriaIDs(t *testing.T) {
	t.Parallel()

	t.Run("id-addressed parent resolves its ordered id list", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentCriteriaArtifact(t, dir, "XW-axon-20260820-ci01",
			"acceptance_criteria:\n  - {id: first, text: \"first criterion\"}\n  - {id: second, text: \"second criterion\"}\n")
		idx := buildParentCriteriaIndex(t, dir)

		ids, ok := AcceptanceCriteriaIDs(dir, idx, "XW-axon-20260820-ci01")
		if !ok {
			t.Fatal("AcceptanceCriteriaIDs: expected ok=true for an id-addressed parent")
		}
		if len(ids) != 2 || ids[0] != "first" || ids[1] != "second" {
			t.Errorf("AcceptanceCriteriaIDs = %v, want [first second]", ids)
		}
	})

	t.Run("bare-string (ordinal) parent declares no ids", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentCriteriaArtifact(t, dir, "XW-axon-20260820-ci02", `acceptance_criteria: ["a", "b"]`)
		idx := buildParentCriteriaIndex(t, dir)

		ids, ok := AcceptanceCriteriaIDs(dir, idx, "XW-axon-20260820-ci02")
		if ok {
			t.Fatalf("AcceptanceCriteriaIDs: expected ok=false for an ordinal (bare-string) parent, got %v", ids)
		}
	})

	t.Run("unresolvable parent degrades to not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		idx := buildParentCriteriaIndex(t, dir)

		ids, ok := AcceptanceCriteriaIDs(dir, idx, "XW-axon-does-not-exist")
		if ok {
			t.Fatalf("AcceptanceCriteriaIDs: expected ok=false for an unresolvable parent, got %v", ids)
		}
	})

	t.Run("parent with zero declared criteria degrades to not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentCriteriaArtifact(t, dir, "XW-axon-20260820-ci03", "acceptance_criteria: []")
		idx := buildParentCriteriaIndex(t, dir)

		ids, ok := AcceptanceCriteriaIDs(dir, idx, "XW-axon-20260820-ci03")
		if ok {
			t.Fatalf("AcceptanceCriteriaIDs: expected ok=false for a zero-length declared list, got %v", ids)
		}
	})
}

// TestParentOf is ParentOf's own direct proof — the verify-side hop
// internal/validate/verdicts.go's checkVerdictIndexRange takes when a
// response's own parent is not directly criteria-bearing.
func TestParentOf(t *testing.T) {
	t.Parallel()

	t.Run("resolves the response's own parent", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeResponseCriteriaArtifact(t, dir, "XS-seomatrix-20260820-po01", "XW-axon-20260820-po01")
		idx := buildParentCriteriaIndex(t, dir)

		parentID, ok := ParentOf(dir, idx, "XS-seomatrix-20260820-po01")
		if !ok {
			t.Fatal("ParentOf: expected ok=true")
		}
		if parentID != "XW-axon-20260820-po01" {
			t.Errorf("ParentOf = %q, want XW-axon-20260820-po01", parentID)
		}
	})

	t.Run("unknown response degrades to not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		idx := buildParentCriteriaIndex(t, dir)

		parentID, ok := ParentOf(dir, idx, "XS-does-not-exist")
		if ok {
			t.Fatalf("ParentOf: expected ok=false for an unresolvable response, got parent=%q", parentID)
		}
	})

	t.Run("artifact with no parent degrades to not-ok, never an empty string treated as found", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentCriteriaArtifact(t, dir, "XW-axon-20260820-po02", `acceptance_criteria: ["a"]`) // a work_request carries no `parent`
		idx := buildParentCriteriaIndex(t, dir)

		parentID, ok := ParentOf(dir, idx, "XW-axon-20260820-po02")
		if ok {
			t.Fatalf("ParentOf: expected ok=false for an artifact with no parent, got parent=%q", parentID)
		}
	})
}
