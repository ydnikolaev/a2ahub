package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMarker_WriteReadRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := PendingMarker{ArtifactID: "XW-axon-1", Branch: "a2a/axon/XW-axon-1", PRNumber: 1, PRURL: "https://example/1", CommitSHA: "abc123", State: "pending-merge", MarkedAt: time.Now()}
	if err := WriteMarker(dir, "sp1", m); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	got, err := ReadMarkers(dir, "sp1")
	if err != nil {
		t.Fatalf("ReadMarkers: %v", err)
	}
	if len(got) != 1 || got[0].ArtifactID != "XW-axon-1" {
		t.Fatalf("got %+v", got)
	}

	if err := RemoveSpaceMarkers(dir, "sp1"); err != nil {
		t.Fatalf("RemoveSpaceMarkers: %v", err)
	}
	got, err = ReadMarkers(dir, "sp1")
	if err != nil {
		t.Fatalf("ReadMarkers after remove: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no markers after RemoveSpaceMarkers, got %+v", got)
	}
}

func TestMarker_ReadMissingSpaceIsEmpty(t *testing.T) {
	t.Parallel()
	got, err := ReadMarkers(t.TempDir(), "never-connected")
	if err != nil {
		t.Fatalf("ReadMarkers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestMarker_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := PendingMarker{ArtifactID: "../../outside"}

	if err := WriteMarker(dir, "sp1", m); err == nil || !strings.Contains(err.Error(), "invalid pending marker artifact id") {
		t.Fatalf("WriteMarker traversal error = %v, want invalid artifact id", err)
	}
	if _, err := ReadMarker(dir, "../outside", "XW-axon-1"); err == nil || !strings.Contains(err.Error(), "invalid pending marker space id") {
		t.Fatalf("ReadMarker traversal error = %v, want invalid space id", err)
	}
	if err := RemoveMarker(dir, "sp1", "../outside"); err == nil || !strings.Contains(err.Error(), "invalid pending marker artifact id") {
		t.Fatalf("RemoveMarker traversal error = %v, want invalid artifact id", err)
	}
}

func TestReconcilePendingMarkers_RemovesOnlyArtifactsNowInCanonicalMirror(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	mirrorDir := t.TempDir()
	const spaceID = "sp1"
	const firstID = "XW-axon-20260802-first"
	const secondID = "XS-axon-20260802-second"
	const combinedID = firstID + "+" + secondID

	for _, artifactID := range []string{firstID, secondID, combinedID} {
		if err := WriteMarker(cacheDir, spaceID, PendingMarker{ArtifactID: artifactID, State: "pending-merge"}); err != nil {
			t.Fatalf("WriteMarker(%s): %v", artifactID, err)
		}
	}
	writeCanonicalArtifact := func(artifactID string) {
		t.Helper()
		path := filepath.Join(mirrorDir, "axon", "exchanges", artifactID+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		raw := []byte("---\nid: " + artifactID + "\ntype: work_request\nfrom: axon\nto: [seomatrix]\n---\nbody\n")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", artifactID, err)
		}
	}

	// Current combined writes have one marker per artifact; historical writes
	// may also have one A+B marker. Only the direct marker already visible on
	// canonical may clear, while A+B waits for both artifacts.
	writeCanonicalArtifact(firstID)
	if err := ReconcilePendingMarkers(cacheDir, spaceID, mirrorDir); err != nil {
		t.Fatalf("ReconcilePendingMarkers(first): %v", err)
	}
	if _, err := ReadMarker(cacheDir, spaceID, firstID); !os.IsNotExist(err) {
		t.Fatalf("first marker still exists after canonical artifact appeared: %v", err)
	}
	if _, err := ReadMarker(cacheDir, spaceID, secondID); err != nil {
		t.Fatalf("second marker removed before its artifact appeared: %v", err)
	}
	if _, err := ReadMarker(cacheDir, spaceID, combinedID); err != nil {
		t.Fatalf("combined marker removed before every carried artifact appeared: %v", err)
	}

	writeCanonicalArtifact(secondID)
	if err := ReconcilePendingMarkers(cacheDir, spaceID, mirrorDir); err != nil {
		t.Fatalf("ReconcilePendingMarkers(second): %v", err)
	}
	if _, err := ReadMarker(cacheDir, spaceID, secondID); !os.IsNotExist(err) {
		t.Fatalf("second marker still exists after canonical artifact appeared: %v", err)
	}
	if _, err := ReadMarker(cacheDir, spaceID, combinedID); !os.IsNotExist(err) {
		t.Fatalf("combined marker still exists after every carried artifact appeared: %v", err)
	}
	if err := ReconcilePendingMarkers(cacheDir, spaceID, mirrorDir); err != nil {
		t.Fatalf("idempotent ReconcilePendingMarkers: %v", err)
	}
}
