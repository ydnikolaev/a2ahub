package cache

import (
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
