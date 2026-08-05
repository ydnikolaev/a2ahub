package release

import (
	"context"
	"os"
	"testing"
)

// TestMain severs this package's tests from the machine's real GitHub CLI
// login, for every test, by default.
//
// This is not hygiene, it is a leak fix. ResolveToken gained a `gh auth token`
// fallback, and the existing TestResolveToken_EnvOrder — written when the
// function was env-only — asserts that "nothing set" resolves to "". It does
// not stub anything, so on a developer machine it resolved the operator's REAL
// token, failed the comparison, and printed that live credential verbatim into
// the test output via %q. A failing assertion became a credential disclosure
// into whatever collects test logs.
//
// The lesson generalises past this one test: any test that exercises a
// resolver which can reach the machine will, on failure, print what it found.
// So the default is severed here rather than in each test — an opt-out cannot
// be forgotten by the next test, while an opt-in silently would be.
//
// A test that wants the fallback branch assigns lookupGitHubCLIToken itself
// and restores it (see resolvetoken_test.go), which is explicit and local.
func TestMain(m *testing.M) {
	lookupGitHubCLIToken = func(context.Context) (string, bool) { return "", false }
	os.Exit(m.Run())
}
