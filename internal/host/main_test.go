package host

import (
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens this package's own git spawns (github.go pushes to the
// remote under test) against git's gc --auto grandchild racing a
// t.TempDir() cleanup's RemoveAll — see
// testkit/gitfixture/gitfixture.go's package doc for the flake this
// prevents.
func TestMain(m *testing.M) {
	os.Exit(gitfixture.RunTests(m))
}
