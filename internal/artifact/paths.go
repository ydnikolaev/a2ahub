package artifact

import (
	"path"
	"sort"
	"strings"
)

// This file holds the pure path predicates shared by the contract side
// (internal/contract's cleanPortablePath and caseCollisionIssues) and the
// data-package side (internal/datapackage's safe-walk and install). Neither
// caller's vocabulary lives here: no contract.Issue, no datapackage sentinel
// — plain bool/string/slice in, plain bool/string/slice out, so each side can
// wrap the answer in its own refusal without a second implementation to
// drift from the first.
//
// Spec: docs/features/active/agent-ops-2026-07/specs/05a-contract-data-exchange-loop.md §5
// Plan: docs/features/active/agent-ops-2026-07/plans/05a-contract-data-exchange-loop.plan.md D-3

// CleanRelativePath reports whether value is a portable, contained,
// slash-separated relative path: not empty, not ".", not rooted, no
// backslash, no NUL byte, no ".." component anywhere, exactly equal to its
// own path.Clean form, and built only from ASCII letters, digits, ".", "_",
// "/" and "-".
//
// That last, most restrictive rule is deliberate and does double duty: it
// is what makes a Unicode homoglyph or a normalization mismatch (NFC vs
// NFD) impossible to smuggle through this predicate — a path that differs
// only by Unicode form contains a non-ASCII byte either way, so both forms
// are refused by the same rule rather than by a second, easier-to-miss
// normalization step. It also makes a Windows drive letter (e.g. "C:foo")
// refused for free: ":" is not in the allowed character set, so no separate
// drive-letter check is needed alongside it.
func CleanRelativePath(value string) bool {
	if value == "" || value == "." || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "\\") || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	if path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("._/-", rune(c)):
		default:
			return false
		}
	}
	return true
}

// CaseCollisions reports, from paths, every path whose ASCII-lowercase form
// collides with a lexicographically earlier path in the set — the same
// predicate internal/contract's caseCollisionIssues computes, without the
// Issue vocabulary. An exact duplicate (the identical string appearing
// twice) is not reported as a collision; only a *different* path that
// lowercases to the same key is. The result is sorted for determinism.
//
// This matters wherever a set of paths may later be written to a
// case-insensitive filesystem (macOS's default, Windows): two declared
// entries that differ only by case name one file there, not two.
func CaseCollisions(paths []string) []string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	canonical := make(map[string]string, len(sorted))
	var collisions []string
	for _, p := range sorted {
		lower := strings.ToLower(p)
		prior, exists := canonical[lower]
		if !exists {
			canonical[lower] = p
			continue
		}
		if prior == p {
			continue
		}
		collisions = append(collisions, p)
	}
	return collisions
}
