// Package sensitive owns the closed, pure credential-shape matchers shared by
// validation and read-model redaction. It reports shapes only; callers own the
// policy decision, error code, or presentation replacement.
package sensitive

import (
	"regexp"
	"strings"
)

var contentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`gh[ours]_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`github_pat_[0-9a-zA-Z_]{60,}`),
	regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`),
	regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/]+`),
	regexp.MustCompile(`glpat-[0-9A-Za-z_-]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	regexp.MustCompile(`sk_live_[0-9A-Za-z]{24,}`),
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9._-]{20,}`),
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
	for _, pattern := range contentPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
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
