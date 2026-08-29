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

// Subsumes reports whether every concrete path glob `narrow` can match is
// also matched by glob `broad` — glob-vs-glob containment, decided
// syntactically on segments, sharing Match's own "**" (zero or more whole
// segments) / "*"/"?" (one segment, fnmatch) syntax rather than a second
// pattern language.
//
// P1b's own two capabilities both reduce to this one predicate rather than
// needing separate logic (spec 09 §5's anti-duplication):
//
//   - the scope-reading command vocabulary (reads.go) turns `go test
//     ./internal/lane/...` into the glob `internal/lane/**/*.go`; a
//     declared pattern P is backed when Subsumes(thatGlob, P) — the phase
//     really does read (at least) everything P could ever match. This is
//     what makes `go test ./internal/lane/...` back `internal/lane/**/*.go`
//     but not `cmd/**`, and correctly not back `internal/lane/**` either
//     (that includes non-Go files `go test` never opens).
//   - a variable-built read's literal parts (reads.go's
//     literalTailScope) become a glob with each unresolved segment
//     replaced by "**" (an unconstrained span — the classifier does not
//     know how many real segments the variable expands to, only that it
//     cannot rule any out); Subsumes(thatGlob, P) is true exactly when
//     every literal segment the read DOES carry lines up with P, which is
//     the "back the globs it can plausibly reach" contract (spec 09 US-3).
//
// Undecidable or ambiguous segment pairs (two different non-"*" patterns
// that are not textually equal, or a bare `narrow` "**" opposite a
// non-"**" `broad` segment) return false — precision over recall governs
// throughout: a case this predicate cannot prove sound is never claimed as
// backing evidence.
func Subsumes(broad, narrow string) bool {
	return subsumeSegments(splitSegments(broad), splitSegments(narrow))
}

// subsumeSegments is Subsumes' recursive core: does every segment sequence
// `narrow` can produce also satisfy `broad`? It mirrors matchSegments' own
// "** absorbs a variable number of segments" handling, but on TWO pattern
// sequences instead of one pattern against one concrete name — so a "**" on
// either side needs its own case, not just the pattern side's.
func subsumeSegments(broad, narrow []string) bool {
	switch {
	case len(broad) == 0:
		return len(narrow) == 0
	case broad[0] == "**":
		// broad's "**" absorbs zero or more of narrow's own segments,
		// whatever shape they are (literal, "*"/"?", or narrow's own
		// "**") — try absorbing zero first (drop broad's "**" and compare
		// the rest as-is), then try absorbing one more narrow segment and
		// recursing with broad UNCHANGED (still able to absorb further).
		// narrow only ever shrinks across the two branches, so this always
		// terminates on narrow's finite length.
		if subsumeSegments(broad[1:], narrow) {
			return true
		}
		if len(narrow) > 0 {
			return subsumeSegments(broad, narrow[1:])
		}
		return false
	case len(narrow) == 0:
		// broad still has a non-"**" segment pending (literal or "*"/"?")
		// that DEMANDS a corresponding narrow segment — narrow being
		// exhausted here means broad cannot be proven to cover it.
		return false
	case narrow[0] == "**":
		// narrow's "**" can expand to an arbitrary number of segments here
		// (including more than the single slot broad's non-"**" segment
		// offers) — broad cannot prove it covers every expansion without
		// its own "**" at this position, already handled above.
		return false
	default:
		if !segmentSubsumes(broad[0], narrow[0]) {
			return false
		}
		return subsumeSegments(broad[1:], narrow[1:])
	}
}

// segmentSubsumes decides ONE segment pair: does broad (an fnmatch pattern
// or literal) cover every string narrow (also a pattern or literal) can
// produce? "*" trivially covers anything single-segment. A literal narrow
// is decided soundly via path.Match. Two different patterned (non-"*")
// segments that are not textually identical are undecidable by this
// syntactic check and refuse rather than guess.
func segmentSubsumes(broad, narrow string) bool {
	if broad == narrow {
		return true
	}
	if broad == "*" {
		return true
	}
	if !strings.ContainsAny(narrow, "*?") {
		ok, err := path.Match(broad, narrow)
		return err == nil && ok
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
