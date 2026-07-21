package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// cursorSnapshot is the per-system read cursor's on-disk shape (format is
// this package's own — machine-local, gitignored, fully rebuildable,
// D-001): the folded state of every known item as of the last `a2a
// inbox` run. "New" (OP-207) is an item absent from Items; outbox's
// "state changed since read cursor" (OP-208) compares the current state
// against Items[id].
type cursorSnapshot struct {
	AdvancedAt time.Time         `json:"advanced_at"`
	Items      map[string]string `json:"items"`
}

// loadCursor reads path; a missing, unreadable, or schema-mismatched
// file is treated as "never read" (empty snapshot) rather than an error
// — the cursor is disposable cache state (D-001), not a durable record.
func loadCursor(path string) (cursorSnapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return cursorSnapshot{Items: map[string]string{}}, nil
	}
	var c cursorSnapshot
	if err := json.Unmarshal(raw, &c); err != nil {
		return cursorSnapshot{Items: map[string]string{}}, nil
	}
	if c.Items == nil {
		c.Items = map[string]string{}
	}
	return c, nil
}

// saveCursor writes c to path, creating parent directories as needed.
func saveCursor(path string, c cursorSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
