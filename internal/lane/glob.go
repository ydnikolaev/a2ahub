package lane

import (
	"path"
	"strings"
)

// Match reports whether name (a repo-relative path using "/" separators,
// never "\" and never a leading "/") matches pattern.
//
// "**" crosses path separators and consumes zero or more whole path
// segments — so a trailing "/**" matches the directory itself and its
// entire subtree, and "a/**/b" matches "a/b" as well as "a/x/b". Any other
// segment is matched with single-component wildcard semantics ("*" and "?"
// never cross "/"), so "*.go" does NOT match "x/y.go" — it has one fewer
// segment than the name.
func Match(pattern, name string) bool {
	return matchSegments(splitSegments(pattern), splitSegments(name))
}

// MatchInputs applies a declaration's Inputs list to one path, in order.
// A leading "!" excludes. Patterns are evaluated in order and the LAST
// matching pattern wins — an exclusion after a broad include narrows it,
// and a later re-include after an exclusion widens it back. A path matched
// by no pattern at all is not selected.
func MatchInputs(inputs []string, name string) bool {
	matched, _ := MatchingInput(inputs, name)
	return matched
}

// MatchingInput is MatchInputs plus WHICH declared pattern decided the
// result — the literal Inputs entry (with its leading "!" if it was the
// deciding exclusion) an operator would point at to answer "why did this
// gate get selected". It is the same "last matching pattern wins" walk
// MatchInputs does, just also remembering which entry that was.
func MatchingInput(inputs []string, name string) (matched bool, pattern string) {
	for _, pat := range inputs {
		if excl := strings.HasPrefix(pat, "!"); excl {
			if Match(pat[1:], name) {
				matched, pattern = false, pat
			}
			continue
		}
		if Match(pat, name) {
			matched, pattern = true, pat
		}
	}
	return matched, pattern
}

// MatchesAnyGlob reports whether path matches any pattern in list — no "!"
// exclusion semantics (that is MatchInputs' job for a declaration's own
// Inputs), just "does at least one glob claim this path". This is the
// lane-ungated.txt shape (D-3/D-6): a flat OR-list of globs, not a
// declaration's ordered include/exclude walk. Both Coverage (this package)
// and lanecheck.go's runDerive (package main, hence exported) use it for
// the same reason — an exact-string ungatedSet silently matches none of a
// root-shaped adjudication entry like "integrations/macos-notifier/**".
func MatchesAnyGlob(list []string, path string) bool {
	for _, pat := range list {
		if Match(pat, path) {
			return true
		}
	}
	return false
}

func splitSegments(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchSegments(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], name[1:])
}
