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
		"123456789:" + strings.Repeat("A", 35),
	}
	for _, value := range values {
		if !ContainsContent(value) {
			t.Fatalf("credential shape was not detected: %q", value)
		}
	}
}

func TestContentMatchersProjectsContentPatterns(t *testing.T) {
	t.Parallel()
	shapes := ContentMatchers()
	if len(shapes) != len(contentPatterns) {
		t.Fatalf("ContentMatchers returned %d shapes, want %d (one per contentPatterns entry)", len(shapes), len(contentPatterns))
	}
	var sawTelegram bool
	for i, shape := range shapes {
		if shape.Name == "" {
			t.Fatalf("shape %d has an empty Name", i)
		}
		if shape.Pattern != contentPatterns[i].pattern.String() {
			t.Fatalf("shape %d Pattern = %q, want %q (contentPatterns[%d]'s own source)", i, shape.Pattern, contentPatterns[i].pattern.String(), i)
		}
		if shape.Name == "telegram-bot-token" {
			sawTelegram = true
			if !ContainsContent("123456789:" + strings.Repeat("A", 35)) {
				t.Fatalf("telegram-bot-token shape %q does not match its own worked example", shape.Pattern)
			}
		}
	}
	if !sawTelegram {
		t.Fatal("ContentMatchers did not carry a telegram-bot-token shape")
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

// TestTelegramShapeSeesATokenInsideABotURL is the regression the closing audit
// of space-notify-2026-08 forced.
//
// The shape shipped with a leading `\b`, added to stop it matching the `256:`
// inside this repo's own `sha256:<64-hex>` digests. It did stop that — but the
// {6,} quantifier already did, since `sha256:` carries three digits. The
// boundary was redundant, and it made this matcher structurally blind in the
// ONE context a Telegram token ever appears in: `.../bot<token>/METHOD`, where
// `bot`'s `t` sits against the token's leading digit with no boundary between
// them. Every "defensive second layer" that calls ContainsContent was therefore
// blind exactly where a token can leak.
func TestTelegramShapeSeesATokenInsideABotURL(t *testing.T) {
	t.Parallel()
	const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"

	if !ContainsContent("https://api.telegram.org/bot" + token + "/sendMessage") {
		t.Error("a token inside a bot URL is not detected — the one place it actually leaks")
	}
	if !ContainsContent("token=" + token) {
		t.Error("a standalone token is not detected")
	}
	// And the collision the boundary was added for must stay refused.
	if ContainsContent("sha256:" + strings.Repeat("a", 64)) {
		t.Error("a sha256 digest is matched as a credential — the false positive is back")
	}
	if ContainsContent("sha512:" + strings.Repeat("b", 128)) {
		t.Error("a sha512 digest is matched as a credential")
	}
}
