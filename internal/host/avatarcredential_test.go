package host

import (
	"context"
	"testing"
)

// TestAvatarAPICredentialUsesTheMachineGitHubLogin pins the authentication
// that was missing, and the reason it mattered more than "an image is stale".
//
// The login-resolution call hits api.github.com, whose anonymous budget is 60
// requests per hour PER IP, shared with every other anonymous caller on that
// address. Unauthenticated it 403s on an ordinary developer machine — and once
// the avatar cache record schema moved to v3, every record an older binary had
// written was rejected as invalid. A disposable cache may be invalidated
// freely only while a refresh can repopulate it; an unauthenticated refresh
// cannot, so the two decisions together produced permanently empty avatars.
func TestAvatarAPICredentialUsesTheMachineGitHubLogin(t *testing.T) {
	restore := lookupGitHubCLIToken
	t.Cleanup(func() { lookupGitHubCLIToken = restore })

	lookupGitHubCLIToken = func(context.Context) (string, bool) { return "from-gh-cli", true }
	if got := avatarAPICredential(context.Background()); got.Token != "from-gh-cli" {
		t.Fatalf("credential = %+v, want the gh CLI token — an anonymous /users call burns a 60/hour per-IP budget", got)
	}
}

// TestAvatarAPICredentialDegradesToAnonymous keeps the fallback intact: a
// machine with no `gh` must still resolve avatars, because the endpoint serves
// public data and works unauthenticated. Failing here would break CI runners
// and fresh installs in order to fix a rate limit.
func TestAvatarAPICredentialDegradesToAnonymous(t *testing.T) {
	restore := lookupGitHubCLIToken
	t.Cleanup(func() { lookupGitHubCLIToken = restore })

	lookupGitHubCLIToken = func(context.Context) (string, bool) { return "", false }
	if got := avatarAPICredential(context.Background()); got.Token != "" {
		t.Fatalf("credential = %+v, want empty — anonymous must remain a working fallback", got)
	}
}
