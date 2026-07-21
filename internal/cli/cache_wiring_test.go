package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestCacheBackedPendingMarker_WritesMarkerFile(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	marker := cli.NewCacheBackedPendingMarker(cacheDir)

	if err := marker.MarkPending(context.Background(), "sp1", "XW-axon-1", space.WriteResult{
		Branch: "a2a/axon/XW-axon-1", PRNumber: 7, PRURL: "https://example/7", CommitSHA: "abc", State: space.WriteStatePendingMerge,
	}); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(cacheDir, "pending", "sp1", "XW-axon-1.json"))
	if err != nil {
		t.Fatalf("expected a marker file on disk: %v", err)
	}
	var decoded struct {
		ArtifactID string `json:"artifact_id"`
		PRNumber   int    `json:"pr_number"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.ArtifactID != "XW-axon-1" || decoded.PRNumber != 7 {
		t.Fatalf("got %+v", decoded)
	}
}

func TestCacheBackedPendingMarker_SyncRefreshIsNoop(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	marker := cli.NewCacheBackedPendingMarker(cacheDir)

	// cmd_sync's own calling convention: spaceID set, artifactID empty,
	// zero WriteResult.
	if err := marker.MarkPending(context.Background(), "sp1", "", space.WriteResult{}); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(cacheDir, "pending"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("expected no marker written for the bare refresh call, got %v", entries)
	}
}

func TestCacheBackedCacheRemover_RemovesSpaceMarkers(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	marker := cli.NewCacheBackedPendingMarker(cacheDir)
	if err := marker.MarkPending(context.Background(), "sp1", "XW-axon-1", space.WriteResult{State: space.WriteStatePendingMerge}); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}

	remover := cli.NewCacheBackedCacheRemover(cacheDir)
	if err := remover.RemoveSpace(context.Background(), "sp1"); err != nil {
		t.Fatalf("RemoveSpace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "pending", "sp1")); !os.IsNotExist(err) {
		t.Fatalf("expected sp1's pending dir to be gone, stat err = %v", err)
	}
}
