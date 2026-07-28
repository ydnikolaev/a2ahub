package cache

import (
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
