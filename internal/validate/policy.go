package validate

import (
	"regexp"
	"unicode/utf8"
)

// DefaultMaxArtifactBytes is CC-006's bounded-read limit: a "configurable,
// generous" size beyond which an artifact is rejected with guidance to
// use refs instead of inlining content (§12). It is exported so a future
// caller (P6 cmd/a2a config layer) can surface it without this package
// reading config itself (go-conventions.md "Config & secrets": os.Getenv
// lives only in the config layer).
const DefaultMaxArtifactBytes = 2 << 20 // 2 MiB

// secretPatterns opens with the three shapes documented by
// schemas/fixtures/secret-corpus/README.md (§13.4): AWS access-key ID,
// GitHub personal access token, and a PEM private-key block. This is a
// best-effort, documented-false-negative scan (§10.4) — encoded/obfuscated
// secrets are explicitly out of reach, per the corpus's own README.
//
// The remaining patterns are the spec 25 §11 "sensitive-content
// hardening" sub-wave (13f, operator review 2026-07-23): the feedback
// channel is PUBLIC and human-free auto-merge publishes a match
// irreversibly, so every addition here is prefix/shape-anchored (never
// entropy-based) to keep the false-positive rate at the floor a
// fail-closed gate requires (a false positive here blocks a legitimate
// envelope submit AND a legitimate feedback report, §11). Deliberately
// NOT added, per that same false-positive budget: bare email/PII, a
// generic long hex/base64 blob (legit hashes/commit SHAs/IDs), and any
// entropy-only heuristic — see this phase's Deviations report.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	// GitHub extra token prefixes (OAuth/user-to-server/server-to-server/
	// refresh) alongside the existing ghp_ (personal access token).
	regexp.MustCompile(`gh[ours]_[A-Za-z0-9]{36}`),
	// GitHub fine-grained personal access token.
	regexp.MustCompile(`github_pat_[0-9a-zA-Z_]{60,}`),
	// Slack bot/app/legacy/refresh token shapes.
	regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`),
	// Slack incoming-webhook URL.
	regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/]+`),
	// GitLab personal access token.
	regexp.MustCompile(`glpat-[0-9A-Za-z_-]{20,}`),
	// JWT: three base64url segments (header.payload.signature); both the
	// header and a typical claims-object payload start with the base64
	// encoding of `{"` (`eyJ`), a distinctive-enough anchor.
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	// Google API key.
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	// Stripe live secret key.
	regexp.MustCompile(`sk_live_[0-9A-Za-z]{24,}`),
	// Authorization: Bearer <token> header, inline in a body.
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9._-]{20,}`),
}

// scanForSecrets is the V2 policy class's secret-scan rule (CC-010,
// AC-203.1): ALL text content crossing the boundary is in scope (§10.4);
// callers pass the artifact's full raw bytes (envelope + body), not just
// decoded fields. A match blocks; there is no in-engine G5 override grant
// — §5.5/§7 are explicit that V2 only *flags* the override path (the
// PR-identity mechanism that could grant it is host-side, out of this
// pure engine's reach).
func scanForSecrets(raw []byte) []Violation {
	content := string(raw)
	for _, p := range secretPatterns {
		if p.MatchString(content) {
			return []Violation{{
				Code:     "POL-001",
				Class:    ClassPolicy,
				Path:     "",
				Message:  "content matches a forbidden secret/credential pattern",
				CCRef:    "CC-010",
				Severity: SeverityReject,
			}}
		}
	}
	return nil
}

// checkAdmission runs the two boundary structural guards §6 requires of
// V1's schema-class scope but which happen BEFORE any JSON-Schema
// validation can even run (there is no decoded instance yet if either
// fails): CC-006 (oversized artifact) and CC-007 (non-UTF-8). Both are
// reported under the policy class (§5.5: "gates, classification,
// single-intent structural rules") — see this phase's Deviations report
// for why these two CCs land here rather than as new SCH- codes (this
// phase may not add new schema-class registry rows; those are P2's
// authored SSOT).
func checkAdmission(raw []byte) []Violation {
	var out []Violation
	if len(raw) > DefaultMaxArtifactBytes {
		out = append(out, Violation{
			Code:     "POL-003",
			Class:    ClassPolicy,
			Message:  "artifact exceeds the size limit; use refs instead of inlining content",
			CCRef:    "CC-006",
			Severity: SeverityReject,
		})
	}
	if !utf8.Valid(raw) {
		out = append(out, Violation{
			Code:     "POL-004",
			Class:    ClassPolicy,
			Message:  "artifact is not valid UTF-8",
			CCRef:    "CC-007",
			Severity: SeverityReject,
		})
	}
	return out
}

// malformedFrontmatterViolation is CC-001 (malformed frontmatter YAML):
// internal/artifact.ParseFrontmatter already distinguishes missing
// delimiters from invalid YAML inside them; this phase reports either as
// the same POL-002 (a single "cannot admit this artifact at all" code —
// there is no decoded instance to run schema/referential/authz/lifecycle
// classes against once this fails).
func malformedFrontmatterViolation() Violation {
	return Violation{
		Code:     "POL-002",
		Class:    ClassPolicy,
		Message:  "artifact frontmatter is missing or is not valid YAML",
		CCRef:    "CC-001",
		Severity: SeverityReject,
	}
}
