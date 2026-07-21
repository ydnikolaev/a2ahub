package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// PendingMarker is the on-disk shape internal/cli/cache_wiring.go's real
// PendingMarker adapter writes on every successful submit (§7.2 OP-205's
// "local cache marks pending-merge" step) — the pending-merge overlay
// this package reads back and folds into Item.PendingMerge. Format is
// this package's own (machine-local, gitignored, rebuildable, D-001):
// losing it only means a submitted-but-unmerged item stops showing the
// overlay flag until the PR merges and its event lands on `main`.
type PendingMarker struct {
	ArtifactID string    `json:"artifact_id"`
	Branch     string    `json:"branch"`
	PRNumber   int       `json:"pr_number"`
	PRURL      string    `json:"pr_url"`
	CommitSHA  string    `json:"commit_sha"`
	State      string    `json:"state"`
	MarkedAt   time.Time `json:"marked_at"`
}

func markerPath(cacheDir, spaceID, artifactID string) string {
	return filepath.Join(cacheDir, "pending", spaceID, artifactID+".json")
}

// WriteMarker persists m for (spaceID, m.ArtifactID) under cacheDir.
func WriteMarker(cacheDir, spaceID string, m PendingMarker) error {
	path := markerPath(cacheDir, spaceID, m.ArtifactID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// ReadMarkers lists every pending marker recorded for spaceID under
// cacheDir. A never-created "pending" directory is not an error (nothing
// pending yet).
func ReadMarkers(cacheDir, spaceID string) ([]PendingMarker, error) {
	dir := filepath.Join(cacheDir, "pending", spaceID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PendingMarker
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		var m PendingMarker
		if jerr := json.Unmarshal(raw, &m); jerr != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// RemoveSpaceMarkers deletes every pending marker recorded for spaceID —
// the cache-side half of `a2a disconnect`'s "remove ... cache for that
// space" step (§7.2 OP-202); the cursor's own item-state entries for
// that space's items are left as harmless orphans (self-correcting: a
// disconnected space's items simply stop appearing in any future index,
// D-001 rebuildability).
func RemoveSpaceMarkers(cacheDir, spaceID string) error {
	return os.RemoveAll(filepath.Join(cacheDir, "pending", spaceID))
}

func markerSet(markers []PendingMarker) map[string]bool {
	out := make(map[string]bool, len(markers))
	for _, m := range markers {
		if m.ArtifactID != "" {
			out[m.ArtifactID] = true
		}
	}
	return out
}
