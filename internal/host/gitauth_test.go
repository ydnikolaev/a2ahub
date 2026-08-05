package host

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGitAuthArgsEmptyCredentialIsTokenless(t *testing.T) {
	t.Parallel()

	if got := GitAuthArgs(Credential{}); got != nil {
		t.Fatalf("GitAuthArgs(empty) = %q, want nil — a public repo must stay reachable with no token", got)
	}
}

func TestGitAuthArgsCarriesTheTokenOnlyInAPerInvocationOverride(t *testing.T) {
	t.Parallel()

	const token = "ghp_example_token_value"
	got := GitAuthArgs(Credential{Token: token})

	if len(got) != 2 {
		t.Fatalf("GitAuthArgs = %q, want exactly 2 args", got)
	}
	if got[0] != "-c" {
		t.Fatalf("GitAuthArgs[0] = %q, want %q", got[0], "-c")
	}
	const prefix = "http.extraheader=AUTHORIZATION: basic "
	if !strings.HasPrefix(got[1], prefix) {
		t.Fatalf("GitAuthArgs[1] = %q, want prefix %q", got[1], prefix)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got[1], prefix))
	if err != nil {
		t.Fatalf("credential arg is not base64: %v", err)
	}
	if want := "x-access-token:" + token; string(decoded) != want {
		t.Fatalf("decoded credential = %q, want %q", decoded, want)
	}

	// The token must ride in the header and nowhere else. A URL-embedded
	// token would be written to remote.origin.url by git and echoed back in
	// its own error messages.
	for _, arg := range got {
		if strings.Contains(arg, token) && !strings.HasPrefix(arg, prefix) {
			t.Fatalf("arg %q carries the raw token outside the encoded header", arg)
		}
	}
}

// TestGitAuthArgsReturnsAFreshSliceEachCall guards the aliasing bug every
// caller here is one line away from: each call site does
// `append(GitAuthArgs(c), subcommand...)`, and if two calls shared a backing
// array the second append would overwrite the first invocation's argv.
func TestGitAuthArgsReturnsAFreshSliceEachCall(t *testing.T) {
	t.Parallel()

	credential := Credential{Token: "t"}
	first := append(GitAuthArgs(credential), "clone")
	second := append(GitAuthArgs(credential), "fetch")

	if first[len(first)-1] != "clone" {
		t.Fatalf("first argv was clobbered by the second: %q", first)
	}
	if second[len(second)-1] != "fetch" {
		t.Fatalf("second argv is wrong: %q", second)
	}
}
