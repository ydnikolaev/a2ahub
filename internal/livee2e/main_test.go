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
	code := gitfixture.RunTests(m)
	// The shared logic binary lives in a process-level dir no harness owns.
	_ = sharedLogicBinary.Close() // reason: best-effort temp cleanup at exit
	os.Exit(code)
}
