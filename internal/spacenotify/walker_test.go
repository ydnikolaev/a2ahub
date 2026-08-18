package spacenotify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackageNeverWalksTheTree is AC10's own package-level half: this
// package must call internal/cache.BuildNotifyIndex — the ONE fold — and
// never walk the space tree itself (spec 03 §5: "if the reviewer finds
// two assemblies in the tree after this phase, the phase failed
// regardless of its tests").
func TestPackageNeverWalksTheTree(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(raw)
		for _, forbidden := range []string{"filepath.WalkDir", "filepath.Walk(", "os.ReadDir", "ioutil.ReadDir"} {
			if strings.Contains(src, forbidden) {
				t.Fatalf("%s contains %q — internal/spacenotify must never walk the tree itself (AC10)", e.Name(), forbidden)
			}
		}
	}
}
