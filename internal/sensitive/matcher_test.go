package sensitive

import (
	"strings"
	"testing"
)

func TestContainsContentCredentialCorpus(t *testing.T) {
	t.Parallel()
	values := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_" + strings.Repeat("a", 36),
		"gho_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("a", 60),
		"xoxa-" + strings.Repeat("1", 12),
		"https://hooks.slack.com/services/A/B/C",
		"glpat-" + strings.Repeat("a", 20),
		"eyJabc.eyJdef.signature",
		"AIza" + strings.Repeat("A", 35),
		"sk_live_" + strings.Repeat("1", 24),
		"Bearer " + strings.Repeat("a", 20),
		"-----BEGIN PRIVATE KEY-----",
	}
	for _, value := range values {
		if !ContainsContent(value) {
			t.Fatalf("credential shape was not detected: %q", value)
		}
	}
}

func TestIdentifierAddsClosedPrefixDenylistWithoutContentFalsePositives(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"token:run", "PASSWORD:value", "sk-session", "xoxp-local"} {
		if !Identifier(value) {
			t.Fatalf("credential-like identifier was not detected: %q", value)
		}
		if ContainsContent(value) {
			t.Fatalf("short identifier prefix leaked into arbitrary-content policy: %q", value)
		}
	}
	for _, value := range []string{"session-1", "sha256:" + strings.Repeat("a", 64), "sketch notes"} {
		if Identifier(value) || ContainsContent(value) {
			t.Fatalf("safe value was classified as sensitive: %q", value)
		}
	}
}
