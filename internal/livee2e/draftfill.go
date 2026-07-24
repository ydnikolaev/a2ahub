package livee2e

import (
	"regexp"
	"strings"
)

// DraftContext supplies the values FillDraft substitutes into a staged
// draft's placeholders — the author-edit step between `a2a new` and
// `a2a submit`, modeled after docs/runbooks/live-e2e/fill.py.
type DraftContext struct {
	// Space is the target space id (`space: <space-id>`).
	Space string
	// Me is this system's own identity — used both directly (e.g. the
	// default `fulfills` id) and as one of the `required_approvers`.
	Me string
	// Peer is the OTHER system's identity — the `to:` recipient and the
	// other `required_approvers` entry.
	Peer string
	// Extra supplies overrides fill.py reads from argv kv pairs: "fulfills"
	// and "parent". Absent keys fall back to fill.py's own defaults; a
	// PRESENT key with an empty value is an explicit override, not "absent"
	// (checked with the two-value map form, never `v == ""`).
	Extra map[string]string
}

// filledPattern is one line- or block-shaped substitution: Pattern matches
// the placeholder, Replacement is the literal (never treated as a regexp
// backreference template — see FillDraft) text it becomes. Applied in
// ORDER, exactly as fill.py's `subs` list and its two block-substitution
// statements run sequentially — the two `context:` patterns below are
// order-dependent by construction (an unquoted `context: <...>` line would
// also match the quoted pattern's charclass if it ran first).
type filledPattern struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// Compiled once at package level: these run per draft, per scenario, across
// a live matrix run, so paying MustCompile's cost per call would be wasted
// work on every scenario.
var (
	pSpace              = regexp.MustCompile(`(?m)^space: <space-id>.*$`)
	pTo                 = regexp.MustCompile(`(?m)^to: \[<[^\]]*\].*$`)
	pActorAgent         = regexp.MustCompile(`actor: \{kind: agent[^}]*\}`)
	pActorHuman         = regexp.MustCompile(`actor: \{kind: human[^}]*\}`)
	pCategory           = regexp.MustCompile(`(?m)^category: <[^>]*>.*$`)
	pInterimBehavior    = regexp.MustCompile(`(?m)^interim_behavior:.*$`)
	pNeededBy           = regexp.MustCompile(`(?m)^needed_by: <[^>]*>.*$`)
	pContextAngle       = regexp.MustCompile(`(?m)^context: <[^>]*>.*$`)
	pContextQuoted      = regexp.MustCompile(`(?m)^context: "<[^"]*>".*$`)
	pOptionsConsidered  = regexp.MustCompile(`(?m)^options_considered:.*$`)
	pRequiredApprovers  = regexp.MustCompile(`(?m)^required_approvers:.*$`)
	pVerification       = regexp.MustCompile(`(?m)^verification: .*$`)
	pAcceptanceCriteria = regexp.MustCompile(`(?m)^acceptance_criteria: \[.*$`)
	pFulfills           = regexp.MustCompile(`(?m)^fulfills:.*$`)
	pParent             = regexp.MustCompile(`(?m)^parent: <[^>]*>.*$`)
	pResult             = regexp.MustCompile(`(?m)^result: <[^>]*>.*$`)
	pVersion            = regexp.MustCompile(`(?m)^version: <[^>]*>.*$`)
	pStatus             = regexp.MustCompile(`(?m)^status: <[^>]*>.*$`)

	// Block-valued placeholders: a bulleted/mapping item whose only content
	// is a placeholder, replaced wholesale rather than patched line by line
	// (fill.py's post-loop statements).
	pBlockAC      = regexp.MustCompile(`(?m)^\s+- "<measurable AC \d+>".*$`)
	pBlockShape   = regexp.MustCompile(`(?m)^\s+shape: "<[^"]*>".*$`)
	pRefsEmpty    = regexp.MustCompile(`(?m)^refs:\s*\n\s+- \{ref: "<[^"]*>"[^\n]*\n`)
	pRefsArtifact = regexp.MustCompile(`(?m)^\s+- \{name: "<[^"]*>", ref: "<[^"]*>", kind: \w+\}.*$`)

	// unfilledPlaceholder finds an ACTIVE placeholder in a frontmatter line
	// once the comment portion has already been stripped by the caller.
	unfilledPlaceholder = regexp.MustCompile(`<[a-zA-Z][^>]*>`)
)

// FillDraft ports docs/runbooks/live-e2e/fill.py's substitution pass to Go,
// faithfully, in the SAME order fill.py applies them (see filledPattern's
// doc comment for why order matters).
//
// unfilled reports every ACTIVE frontmatter line that STILL holds a
// `<placeholder>` after the pass — fill.py's own final loop, with its two
// exclusions carried over exactly: a line that is itself a comment (starts
// with `#` once leading whitespace is stripped), and a placeholder that only
// appears inside a trailing `# comment` (prose, not a field — the comment
// portion is stripped before the placeholder search runs). This report is
// load-bearing: it is how the matrix learns a template asks for something it
// cannot describe.
func FillDraft(src string, dc DraftContext) (filled string, unfilled []string) {
	fulfills, ok := dc.Extra["fulfills"]
	if !ok {
		fulfills = "XR-" + dc.Me + "-matrix-req"
	}
	// fill.py's own default for "parent" IS "" (an explicit YAML null, not
	// an omitted key), so an absent Extra["parent"] and a present-but-empty
	// one are identical here — unlike "fulfills" above, this two-value read
	// has no non-empty default to fall back to. Spelled out anyway so the
	// absent/present distinction stays visible if "parent" ever grows one.
	parent, ok := dc.Extra["parent"]
	if !ok {
		parent = ""
	}

	subs := []filledPattern{
		{pSpace, "space: " + dc.Space},
		{pTo, "to: [" + dc.Peer + "]"},
		{pActorAgent, `actor: {kind: agent, name: 'live-e2e'}`},
		{pActorHuman, `actor: {kind: human, name: 'live-e2e-operator'}`},
		{pCategory, "category: other"},

		{pInterimBehavior, `interim_behavior: "we proceed with the current shape"`},
		{pNeededBy, "needed_by: 2026-12-31"},
		{pContextAngle, `context: "the live matrix needs this artifact"`},
		{pContextQuoted, `context: "the live matrix needs this artifact"`},
		{pOptionsConsidered, `options_considered: ["option A", "option B"]`},
		{pRequiredApprovers, "required_approvers: [" + dc.Me + ", " + dc.Peer + "]"},
		{pVerification, `verification: "the matrix re-ran the suite; all green"`},
		// Inline form only — the block form is filled at its child item
		// below (pBlockAC).
		{pAcceptanceCriteria, `acceptance_criteria: ["the artifact validates"]`},
		{pFulfills, "fulfills: [" + fulfills + "]"},
		{pParent, "parent: " + parent},
		{pResult, "result: answered"},
		{pVersion, "version: 1.0.0"},
		{pStatus, "status: active"},
	}

	s := src
	for _, sub := range subs {
		// ReplaceAllLiteralString, never ReplaceAllString: fill.py's
		// replacements have zero backreferences, and dc.Space/dc.Me/dc.Peer/
		// dc.Extra values flow straight into these strings — a caller-
		// supplied "$1" or "${x}" must not be interpreted as a Go regexp
		// replacement template.
		s = sub.Pattern.ReplaceAllLiteralString(s, sub.Replacement)
	}

	// Block-valued placeholders (fill.py's four post-loop re.sub calls).
	s = pBlockAC.ReplaceAllLiteralString(s, `  - "the artifact validates"`)
	s = pBlockShape.ReplaceAllLiteralString(s, `  shape: "a short prose answer"`)
	s = pRefsEmpty.ReplaceAllLiteralString(s, "refs: []\n")
	s = pRefsArtifact.ReplaceAllLiteralString(s, `  - {name: "matrix artifact", ref: "live-e2e/matrix@1.0.0", kind: doc}`)

	filled = s
	unfilled = findUnfilled(s)
	return filled, unfilled
}

// findUnfilled ports fill.py's closing loop: split at the frontmatter
// delimiters (only when there are at least two `---` markers, exactly as
// fill.py's `s.count('---') >= 2` guard), then scan the frontmatter's lines.
func findUnfilled(s string) []string {
	fm := s
	if strings.Count(s, "---") >= 2 {
		parts := strings.SplitN(s, "---", 3)
		if len(parts) >= 2 {
			fm = parts[1]
		}
	}

	var out []string
	for _, ln := range strings.Split(fm, "\n") {
		body := ln
		if idx := strings.IndexByte(ln, '#'); idx >= 0 {
			// A placeholder inside a trailing `#` comment is prose, not a
			// field — fill.py strips the comment before searching.
			body = ln[:idx]
		}
		trimmedBody := strings.TrimSpace(body)
		trimmedLine := strings.TrimSpace(ln)
		if trimmedBody == "" {
			continue
		}
		if strings.HasPrefix(trimmedLine, "#") {
			// The line itself is a comment, not an active field.
			continue
		}
		if unfilledPlaceholder.MatchString(body) {
			out = append(out, truncateRunes(trimmedLine, 110))
		}
	}
	return out
}

// truncateRunes mirrors fill.py's `line[:110]` slice — but rune-based, not
// byte-based: Python's slice is by codepoint, and a byte-based Go slice at
// the same index could cut a multi-byte UTF-8 rune in half and emit invalid
// UTF-8. Frontmatter lines can contain non-ASCII (e.g. the "§" section sign
// used elsewhere in this repo's prose), so this is not a hypothetical.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
