package host

import (
	"context"
	"os"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens this package's own git spawns (github.go pushes to the
// remote under test) against git's gc --auto grandchild racing a
// t.TempDir() cleanup's RemoveAll — see
// testkit/gitfixture/gitfixture.go's package doc for the flake this
// prevents.
//
// It also severs the machine's real GitHub CLI login for every test in this
// package. FetchAvatar now authenticates its login-resolution call, so without
// this the existing avatar tests — which point at an httptest server — would
// still shell out to the operator's `gh` and hold their live token; a failing
// assertion that formats a credential is how one reached a log file already.
// Severed by DEFAULT because an opt-out cannot be forgotten by the next test
// while an opt-in silently can. A test that wants the authenticated branch
// assigns lookupGitHubCLIToken itself and restores it.
func TestMain(m *testing.M) {
	lookupGitHubCLIToken = func(context.Context) (string, bool) { return "", false }
	os.Exit(gitfixture.RunTests(m))
}
