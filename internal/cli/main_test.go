package cli

import (
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens this package's own git spawns (contract_git.go,
// cmd_contract.go, cmd_validate_ci.go all invoke git under test) against
// git's gc --auto grandchild racing a t.TempDir() cleanup's RemoveAll —
// see testkit/gitfixture/gitfixture.go's package doc for the flake this
// prevents. internal/cli's tests also live in package cli_test (same
// directory, same test binary) — Go allows exactly one TestMain per test
// binary regardless of which of the two packages declares it.
func TestMain(m *testing.M) {
	os.Exit(gitfixture.RunTests(m))
}
