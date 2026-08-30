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
	// No `\b` here, deliberately, and the history is worth keeping: it was
	// added to stop `\d+:` matching the `256:` inside this repo's own
	// `sha256:<64-hex>` digests — but the {6,} quantifier ALREADY does that,
	// since `sha256:` carries three digits and this needs six. The boundary
	// was redundant, and it was worse than redundant: the ONLY context a
	// Telegram token appears in is `.../bot<token>/METHOD`, where `bot`'s `t`
	// sits against the token's leading digit. Both are word characters, so
	// there is no boundary there and this shape could never match a leaked
	// token in the one place it actually leaks. Verified both ways before the
	// change: with `\b` the URL form does not match; without it, neither
	// sha256 nor sha512 digests do.
	{"telegram-bot-token", regexp.MustCompile(`[0-9]{6,}:[A-Za-z0-9_-]{30,}`)},
}

// credentialAssignmentPattern and bearerCredentialPattern are computed-not-
// listed-2026-08 P6 §7's collapse of the SECOND heuristic that used to be
// hand-maintained, byte-identical, in both internal/cache (operational.go)
// and internal/operational (project.go): the internal read-model path caught
// `password=abc`/`token: x`/`bearer:y` while the OUTBOUND edge's only guard
// (internal/spacenotify/redact.go) did not — the same literal strings went
// out to Telegram while being redacted from the local operational snapshot
// that never leaves the machine. Declared here, ONCE; both internal packages
// and the outbound edge now call ContainsCredentialText instead of carrying
// their own copies.
//
// They back ContainsCredentialText, NOT ContainsContent, and the split is
// load-bearing — see ContainsContent's own doc comment for the boundary and
// for the measurement that forced it apart again.
//
// Checked SEPARATELY from the contentPatterns loop above (never added to that
// slice, so ContentMatchers' projection stays an honest "pure regex, presence
// alone decides" contract for every entry it DOES carry) — because, unlike
// the closed provider shapes, matching alone is not enough: `secret: TG_BOT_TOKEN` is a SCHEMA-BLESSED space.yaml key
// (space/v1's own notification_routes[].secret field, constrained to
// `^[A-Z][A-Z0-9_]*$` — a real token can never legally live there, which is
// why POL-001 must not refuse the key itself), while `secret = s3cr3t` is a
// leak. Both match the SAME keyword+separator shape; only the right-hand
// SIDE tells them apart. Found empirically: reusing the plain MatchString
// shape here refused every space.yaml carrying a routed secret at the
// `validate --ci` merge gate (TestNotifySetupPrintsARouteTheValidatorAccepts,
// internal/cli) — a real ship-blocker, not a hypothetical one, so the RHS
// check is load-bearing, not decoration.
var (
	credentialAssignmentPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:password|passwd|token|authorization|api[-_]?key|secret)[[:space:]]*[:=][[:space:]]*(\S+)`)
	// bearerCredentialPattern matches "bearer" used as an ASSIGNMENT
	// (`bearer:`, `bearer=`) — never bare "bearer" followed by ordinary
	// prose whitespace.
	//
	// DEVIATION from the two pre-collapse copies, which additionally matched
	// bare `bearer[[:space:]]+` — "the bearer of good news", "bearer bonds
	// are a financial instrument" both matched under that shape, because
	// nothing after the whitespace was ever checked. That was tolerable
	// while this heuristic only ever redacted internal operational text;
	// moved onto the OUTBOUND edge (spec §6: "a false positive in prose —
	// 'bearer' as an English word" is a named test requirement here), it
	// would refuse ordinary artifact prose — and a length-only threshold on
	// the bare-whitespace shape ("bearer <7+ chars>") was tried and
	// rejected: it would have caught "wait-summary-secret" but ALSO
	// "bearer instrument"/"bearer certificate", the exact class of English
	// prose §6 names. The real "Bearer <token>" HTTP-header shape this
	// branch existed to catch keeps its coverage unchanged: contentPatterns'
	// own "bearer-token" entry above (`Bearer\s+[A-Za-z0-9._-]{20,}`)
	// already matches it whenever the token is a real credential-length
	// value; this generic pattern's remaining job is the `:`/`=` assignment
	// shape alone, which has no comparable prose false-positive (nobody
	// writes "bearer: bonds" or "bearer= good news").
	bearerCredentialPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])bearer[[:space:]]*[:=]`)
	// screamingSnakeValuePattern is credentialAssignmentPattern's own RHS
	// exclusion: a value shaped like an ALL-CAPS identifier (an env-var
	// NAME, e.g. `TG_BOT_TOKEN`) is a manifest field pointing AT a secret,
	// never the secret's own value — a real credential is never spelled
	// this way.
	screamingSnakeValuePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// matchesGenericCredentialAssignment reports whether value contains a
// `password:`/`token=`/`secret:`-shaped generic assignment WHOSE VALUE is
// not itself a manifest-field-name shape (screamingSnakeValuePattern) — see
// credentialAssignmentPattern's own doc comment for why the RHS, not just
// the keyword, has to decide.
func matchesGenericCredentialAssignment(value string) bool {
	m := credentialAssignmentPattern.FindStringSubmatch(value)
	if m == nil {
		return false
	}
	return !screamingSnakeValuePattern.MatchString(m[1])
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
// canonical CLOSED credential shapes — a real provider's own literal token
// grammar, nothing else. It performs no decoding or entropy guessing, and it
// applies no keyword heuristic.
//
// THIS PREDICATE'S CONTRACT IS THE ARTIFACT-POLICY ONE, and it is owned by a
// corpus rather than by this comment: schemas/fixtures/secret-corpus/positive
// must be refused and .../negative must PASS (TestSecretScan). It is what
// internal/validate's POL-001 seam calls, so widening it refuses artifacts a
// space is entitled to commit.
//
// P6 §7 first collapsed the generic keyword heuristics INTO this function.
// That was measured wrong the same day and split back out: the corpus's own
// `negative/placeholder-env-var.md` carries the literal `API_KEY=<your-key-
// here>`, which is a documented placeholder and matches the generic
// assignment shape, and TestValidateManifestPolicyRouteShape lost its POL-021
// verdict to a POL-001 that fired first. A heuristic tuned for an OUTBOUND
// edge is not free to redefine what a repository may store.
//
// Callers that guard a boundary where a false positive costs nothing and a
// false negative leaks — the outbound notifier, the local read models — want
// ContainsCredentialText instead. One home, two named boundaries.
func ContainsContent(value string) bool {
	for _, shape := range contentPatterns {
		if shape.pattern.MatchString(value) {
			return true
		}
	}
	return false
}

// ContainsCredentialText reports whether bounded arbitrary text carries a
// closed credential shape OR a generic `password: …`/`token = …`/`bearer: …`
// credential ASSIGNMENT whose right-hand side is not itself a manifest field
// name.
//
// It is ContainsContent's strict sibling and the guard for the boundaries
// where the asymmetry runs the other way: internal/spacenotify (text leaving
// the machine for Telegram — space-notify-2026-08 AC-10's `password=abc`)
// and the internal read models in internal/cache and internal/operational
// (text rendered back to an operator). Redacting one benign line there costs
// a `[redacted]`; missing one leaks a credential.
//
// The two predicates share this package precisely so the shapes have ONE
// definition. What differs is not the regex set — it is which boundary is
// allowed to act on the heuristic half of it.
func ContainsCredentialText(value string) bool {
	if ContainsContent(value) {
		return true
	}
	if matchesGenericCredentialAssignment(value) {
		return true
	}
	return bearerCredentialPattern.MatchString(value)
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
