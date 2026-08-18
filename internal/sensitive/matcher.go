// Package sensitive owns the closed, pure credential-shape matchers shared by
// validation and read-model redaction. It reports shapes only; callers own the
// policy decision, error code, or presentation replacement.
package sensitive

import (
	"regexp"
	"strings"
)

// contentShape names one closed credential shape and its matching pattern.
// contentPatterns stays the single definition of the closed matcher list —
// ContainsContent iterates it directly, and ContentMatchers (below) projects
// the same slice as data for a caller that needs to ask the SET rather than
// test one string, instead of a second hand-maintained copy.
type contentShape struct {
	name    string
	pattern *regexp.Regexp
}

var contentPatterns = []contentShape{
	{"aws-access-key-id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github-personal-access-token-classic", regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
	{"pem-private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"github-oauth-or-refresh-token", regexp.MustCompile(`gh[ours]_[A-Za-z0-9]{36}`)},
	{"github-fine-grained-personal-access-token", regexp.MustCompile(`github_pat_[0-9a-zA-Z_]{60,}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`)},
	{"slack-incoming-webhook", regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/]+`)},
	{"gitlab-personal-access-token", regexp.MustCompile(`glpat-[0-9A-Za-z_-]{20,}`)},
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
	{"stripe-live-secret-key", regexp.MustCompile(`sk_live_[0-9A-Za-z]{24,}`)},
	{"bearer-token", regexp.MustCompile(`Bearer\s+[A-Za-z0-9._-]{20,}`)},
	// space-notify-2026-08 P1 rule 4: a Telegram bot token is
	// `<numeric bot id>:<base64ish secret>` — the manifest's own `secret`
	// field pattern (^[A-Z][A-Z0-9_]*$) already rejects a pasted token
	// there, so the real exposure is the token pasted anywhere ELSE in a
	// public space's manifest.
	//
	// DEVIATION from spec 01 §"Policy checks" rule 4's literal regex
	// (`\d+:[A-Za-z0-9_-]{30,}`): that pattern false-positives on this
	// repo's own `sha256:<64-hex>` digest convention (attachments[].digest,
	// REF-014/REF-017's carried-set language, and the "generic long hex/
	// base64 blob (legit hashes/commit SHAs/IDs)" exclusion policy.go
	// already documents) — "sha256:" contains "256:" and a bare `\d+`
	// happily matches the tail of "sha256" with no anchor. Proven, not
	// assumed: TestIdentifierAddsClosedPrefixDenylistWithoutContentFalsePositives's
	// existing `"sha256:" + strings.Repeat("a", 64)` safe-value case reds
	// under the spec's literal pattern. `\b` before the digit run keeps
	// the exact shape (bot id : base64ish secret, unanchored elsewhere in
	// arbitrary text) while requiring the digit run not be glued to a
	// preceding word character, which a real Telegram bot id never is.
	{"telegram-bot-token", regexp.MustCompile(`\b[0-9]{6,}:[A-Za-z0-9_-]{30,}`)},
}

var identifierPrefixes = []string{
	"token:",
	"password:",
	"bearer:",
	"ghp_",
	"github_pat_",
	"glpat-",
	"sk-",
	"xoxb-",
	"xoxp-",
}

// ContainsContent reports whether bounded arbitrary text contains one of the
// canonical credential shapes. It performs no decoding or entropy guessing.
func ContainsContent(value string) bool {
	for _, shape := range contentPatterns {
		if shape.pattern.MatchString(value) {
			return true
		}
	}
	return false
}

// MatcherShape is one closed credential shape in the matcher set, as data: a
// stable name and its pattern source. It carries no compiled *regexp.Regexp
// so a caller outside this package (a projection, a gate) can serialize it
// without depending on the regexp package's own encoding.
type MatcherShape struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// ContentMatchers returns the closed matcher set ContainsContent tests
// against, as data (name + regex source), in the set's own fixed order. This
// is the surface a coherence-audit gate (space-notify-2026-08 P1 §"the
// surface P7 needs") asks for instead of inlining a second copy of the
// regex: contentPatterns stays the single, unexported definition, and this
// is a read-only projection of it — adding a shape to contentPatterns
// appears here with no second edit.
func ContentMatchers() []MatcherShape {
	shapes := make([]MatcherShape, len(contentPatterns))
	for i, shape := range contentPatterns {
		shapes[i] = MatcherShape{Name: shape.name, Pattern: shape.pattern.String()}
	}
	return shapes
}

// Identifier reports whether a bounded opaque identifier is credential-like.
// Identifiers additionally reject short closed prefixes that would be too
// noisy in arbitrary prose.
func Identifier(value string) bool {
	lower := strings.ToLower(value)
	for _, prefix := range identifierPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return ContainsContent(value)
}
