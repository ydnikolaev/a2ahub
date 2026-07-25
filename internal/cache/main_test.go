package cache

import (
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens this package's own git spawns against git's gc --auto
// grandchild racing a t.TempDir() cleanup's RemoveAll — this is the row
// that actually flaked: mirror.go's refresh path fires a detached
// goroutine that clones/fetches under test (observed 2026-07-24 in
// TestStatusline_NoHubSymbol: "unlinkat …/.git/objects: directory not
// empty"). See testkit/gitfixture/gitfixture.go's package doc.
func TestMain(m *testing.M) {
	os.Exit(gitfixture.RunTests(m))
}
