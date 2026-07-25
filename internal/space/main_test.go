package space

import (
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens this package's own git spawns (mirror.go's
// CloneOrFetch invokes git while running under test) against git's
// gc --auto grandchild racing a t.TempDir() cleanup's RemoveAll — the
// flake gitfixture.HardenEnv exists to prevent (see
// testkit/gitfixture/gitfixture.go's package doc).
func TestMain(m *testing.M) {
	os.Exit(gitfixture.RunTests(m))
}
