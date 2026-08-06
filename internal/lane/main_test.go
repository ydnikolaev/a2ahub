package lane

import (
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain is the hook testkit/gitfixture's R2 hygiene gate requires of any
// package that spawns git from a test: HardenEnv disables git's background
// maintenance for every child process, including the ones Args cannot reach.
// This package's universe test builds a throwaway repo, so it qualifies.
func TestMain(m *testing.M) {
	os.Exit(gitfixture.RunTests(m))
}
