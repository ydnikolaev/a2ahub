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

// TestContainsCredentialTextGenericAssignmentAndBearerShapes is computed-not-
// listed-2026-08 P6 AC-1/AC-2's own corpus: the exact strings the spec's §0.5
// measured-facts row reproduced against HEAD (`password=abc`, `token:
// hunter2words`, `secret = s3cr3t`, `api_key: abc123` all returned false and
// were forwarded to Telegram) must now be caught by the predicate the
// outbound edge (internal/spacenotify/redact.go:48) calls — one home in this
// package, no second heuristic, no redact.go regex.
//
// It asserts the SPLIT in both directions, which is the point: these shapes
// reach ContainsCredentialText and must NOT reach ContainsContent. P6 first
// put them in ContainsContent, and that silently redefined what a repository
// may STORE — schemas/fixtures/secret-corpus/negative/placeholder-env-var.md
// carries the literal `API_KEY=<your-key-here>` and went red. The heuristic
// belongs to the edge that transmits, not to the gate that admits.
func TestContainsCredentialTextGenericAssignmentAndBearerShapes(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"password=abc",
		"token: hunter2words",
		"secret = s3cr3t",
		"api_key: abc123",
		"Authorization: Bearer abc",
		"passwd=x",
		"Bearer: abc",
		"bearer=abc",
	} {
		if !ContainsCredentialText(value) {
			t.Fatalf("generic credential-assignment/bearer shape was not detected: %q", value)
		}
		if ContainsContent(value) {
			t.Fatalf("generic heuristic leaked into the artifact-policy predicate: %q", value)
		}
	}
	// §6's own edge case: "bearer" as an ENGLISH WORD (no scheme marker —
	// no colon/equals, no following whitespace-then-token shape) must not
	// false-positive, or every sentence discussing who bears a cost gets
	// redacted/refused.
	for _, value := range []string{
		"the bearer of good news",
		"bearer bonds are a financial instrument",
		"whoever bears the risk",
	} {
		if ContainsCredentialText(value) {
			t.Fatalf("bearer as an English word false-positived: %q", value)
		}
	}
}

// TestContainsCredentialTextGenericAssignmentExemptsScreamingSnakeFieldNames closes
// a real regression found while wiring this widening in: space/v1's own
// `notification_routes[].secret` manifest field is constrained to
// `^[A-Z][A-Z0-9_]*$` (an env-var NAME, e.g. TG_BOT_TOKEN) — a real
// credential can never legally live there. Before this exemption, ANY
// space.yaml carrying a routed secret field was refused by POL-001 at the
// `validate --ci` merge gate (observed via
// TestNotifySetupPrintsARouteTheValidatorAccepts, internal/cli), which is a
// SCHEMA-BLESSED KEY, not a leak — categorically different from a real
// assignment's lowercase/mixed-case right-hand side.
func TestContainsCredentialTextGenericAssignmentExemptsScreamingSnakeFieldNames(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"secret: TG_BOT_TOKEN",
		"token: MY_API_TOKEN",
		"password: ADMIN_PASSWORD",
		"api_key: SERVICE_API_KEY",
	} {
		if ContainsCredentialText(value) {
			t.Fatalf("a manifest field-name value (SCREAMING_SNAKE) false-positived as a credential: %q", value)
		}
	}
	// The exemption is the RHS's SHAPE, not merely its being uppercase — a
	// real leaked value that happens to contain lowercase letters, digits,
	// hyphens, or mixed case must still be caught.
	for _, value := range []string{
		"secret: s3cr3t-Value",
		"token: MixedCase123",
		"password: Not_Screaming_snake",
	} {
		if !ContainsCredentialText(value) {
			t.Fatalf("a real-looking assignment value was wrongly exempted: %q", value)
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
	// computed-not-listed-2026-08 P6 §7: "token:"/"PASSWORD:"/"bearer:" moved
	// OUT of this bucket. Those three prefixes are now ALSO the generic
	// credentialAssignmentPattern/bearerCredentialPattern shapes
	// ContainsCredentialText tests directly (the outbound-edge widening this
	// phase's AC-1 requires), so they no longer prove what this test's name
	// claims: that a SHORT IDENTIFIER-ONLY prefix — no provider shape, no
	// generic assignment/bearer shape — does not leak into ContainsContent's
	// arbitrary-prose scan. ghp_/github_pat_/glpat-/sk-/xoxb-/xoxp- still
	// prove exactly that, at a length too short to satisfy their own FULL
	// provider pattern.
	for _, value := range []string{"ghp_x", "github_pat_x", "glpat-x", "sk-session", "xoxb-local", "xoxp-local"} {
		if !Identifier(value) {
			t.Fatalf("credential-like identifier was not detected: %q", value)
		}
		if ContainsContent(value) {
			t.Fatalf("short identifier prefix leaked into arbitrary-content policy: %q", value)
		}
	}
	// "token:"/"password:"/"bearer:" reach ContainsCredentialText — the
	// boundary AC-1 moves — and are ALSO caught by Identifier, via this
	// bucket's own closed prefix list. They must NOT reach ContainsContent:
	// the outbound Telegram edge (spacenotify.boundAndRedact) consults the
	// strict predicate, while the artifact-policy seam consults the narrow
	// one. Asserted in all three directions so the split cannot be
	// re-collapsed without this file going red.
	for _, value := range []string{"token:run", "password:value", "bearer:x"} {
		if !Identifier(value) {
			t.Fatalf("generic assignment prefix must stay in Identifier's denylist: %q", value)
		}
		if !ContainsCredentialText(value) {
			t.Fatalf("generic assignment/bearer prefix must reach the outbound-edge predicate: %q", value)
		}
		if ContainsContent(value) {
			t.Fatalf("generic assignment/bearer prefix leaked into arbitrary-content policy: %q", value)
		}
	}
	for _, value := range []string{"session-1", "sha256:" + strings.Repeat("a", 64), "sketch notes"} {
		if Identifier(value) || ContainsContent(value) || ContainsCredentialText(value) {
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
