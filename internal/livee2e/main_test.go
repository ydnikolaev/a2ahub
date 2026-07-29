package livee2e

import (
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens MainContains' git process against git's gc --auto
// grandchild racing temporary-directory cleanup. The live-tagged adapter is
// production code from this package's perspective even though make check
// never executes the real GitHub matrix.
func TestMain(m *testing.M) {
	os.Exit(gitfixture.RunTests(m))
}
