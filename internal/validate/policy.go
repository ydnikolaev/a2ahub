package validate

import (
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/sensitive"
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
// scanForSecrets is the V2 policy class's secret-scan rule (CC-010,
// AC-203.1): ALL text content crossing the boundary is in scope (§10.4);
// callers pass the artifact's full raw bytes (envelope + body), not just
// decoded fields. A match blocks; there is no in-engine G5 override grant
// — §5.5/§7 are explicit that V2 only *flags* the override path (the
// PR-identity mechanism that could grant it is host-side, out of this
// pure engine's reach).
func scanForSecrets(raw []byte) []Violation {
	if matchesSecretPattern(string(raw)) {
		return []Violation{secretViolation("", "content matches a forbidden secret/credential pattern")}
	}
	return nil
}

// scanSecretIdentifier is the canonical POL-001 policy seam for a bounded
// identifier. It reuses every general secret pattern above and additionally
// applies the case-insensitive closed prefix denylist for session IDs.
func scanSecretIdentifier(value, path string) []Violation {
	if sensitive.Identifier(value) {
		return []Violation{secretViolation(path, "identifier matches a forbidden secret/credential pattern")}
	}
	return nil
}

func matchesSecretPattern(content string) bool {
	return sensitive.ContainsContent(content)
}

func secretViolation(path, message string) Violation {
	return Violation{
		Code:     "POL-001",
		Class:    ClassPolicy,
		Path:     path,
		Message:  message,
		CCRef:    "CC-010",
		Severity: SeverityReject,
	}
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
