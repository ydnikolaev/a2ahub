package lane

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// reads.go is D-11's honesty extractor: for every declared phase, does the
// script (or Go file) that BACKS it actually read only what it declared?
// It answers "which repo paths does this script ACTUALLY read" per gate
// script, then checks that against the script's own Declaration — the
// testkit/contractroots idiom this package's doc.go already points at:
// refuse loudly on a shape the classifier does not understand, never
// silently score it "no read".
//
// The contract is bounded (D-11), not "parse bash":
//
//   - IN CONTRACT: a literal string path argument to grep, cat, find,
//     source/"." , wc, head, tail, sed, awk's -f/--file form, or a literal
//     input-redirection target (`< path`) — plus Go's os.ReadFile/os.Open
//     family for the one `go run <file>.go`-backed phase in the corpus
//     (coverage-policy -> covercheck.go). These the extractor classifies,
//     and a path it finds that the phase's own Declaration does not cover
//     is a defect it reports.
//   - OUT OF CONTRACT: a variable-built path, a command-substitution
//     result, anything else the tokenizer cannot resolve to a literal.
//     These are never silently scored "no read" — the script must carry a
//     `# lane-reads-opaque: <reason>` line (`//` in a .go file), or the
//     construct is a refusal naming the script and line (D-9). opaqueCount
//     is the running total of SCRIPTS (not lines) that carry the
//     directive — the debt has a size, and shrinking it is a normal
//     commit.
//
// What it deliberately does NOT do, so the blind spots are named rather
// than discovered later: it does not resolve a read through an imported Go
// package (covercheck.go's own os.ReadFile/os.Open calls are scanned;
// internal/coveragepolicy/policy.go's os.Open, reached only via the
// coveragepolicy import, is not); it does not walk into a `source`d
// helper's own body; and it does not check a `go-test-scoped:./pkg/...`
// declaration's package tests at all — that is Deliverable 2's own
// contract, scoped to gate SCRIPTS.

// ReadRef is one literal, in-contract path read the extractor found.
type ReadRef struct {
	Path string
	Line int // 1-based, within the file actually scanned
}

// UnresolvedRead is one read-shaped construct the extractor found but could
// not resolve to a literal path (D-9's out-of-contract case): a
// variable-built argument, a command-substitution result, or anything else
// the bounded tokenizer does not recognise as safely "no read here". Text
// is the trimmed source line, so a refusal names the actual construct.
type UnresolvedRead struct {
	Line int
	Text string
	// Candidate is the raw path-shaped token classify() found (quotes
	// stripped, escapes resolved) when the construct that produced it was
	// scoped to ONE argument — e.g. narrowFindRoots' own
	// "$FEAT_DIR/active/**/README.md". P1b's subsumption (glob.go's
	// Subsumes, capability b) needs this to test the read's LITERAL parts
	// against a declared pattern; it is empty for the whole-line sentinels
	// (goRunNonLiteralRe, unmodellableFindLine) and for the bare
	// unresolvedCandidate marker, which carry no such structure to test.
	Candidate string
}

var (
	// boundaryTokens end both a command's argument list and a redirect's
	// scope — the same shell-operator set a real parser would use, kept
	// intentionally small: this is a bounded classifier, not a shell.
	boundaryTokens = map[string]bool{
		";": true, "|": true, "&": true, "&&": true, "||": true,
		"(": true, ")": true, "<": true, ">": true, "<<": true, ">>": true, "<<<": true,
		// "{" and "}" are here because a one-line function body —
		// `floor_now() { sed ... "$MANIFEST" | head -1; }` — otherwise never
		// reaches atCommandStart, and the classifier returns ZERO reads for
		// the whole line instead of flagging one it cannot resolve. A silent
		// miss is the one outcome D-11 forbids: an unclassified read is
		// invisible, and invisible is indistinguishable from safe. Found by
		// hand-checking the live run against real gate scripts, not by a test.
		"{": true, "}": true,
	}
	// inContractCommands is D-11's read-shaped command list. "." (the
	// source alias) is handled separately since it is not a normal word.
	inContractCommands = map[string]bool{
		"grep": true, "cat": true, "find": true, "source": true,
		"wc": true, "head": true, "tail": true, "sed": true, "awk": true,
	}

	goRunFileRe = regexp.MustCompile(`\bgo run (\S+\.go)\b`)
	// A `go run` target that is not a plain literal path — it contains a shell
	// variable or a command substitution, so the file it runs does not exist
	// until run time and this scanner can never read it.
	goRunNonLiteralRe   = regexp.MustCompile(`\bgo run\s+\S*[$` + "`" + `]\S*\.go`)
	goReadFileLiteralRe = regexp.MustCompile(`os\.(?:ReadFile|Open)\(\s*"((?:[^"\\]|\\.)*)"\s*\)`)
	goReadFileCallRe    = regexp.MustCompile(`os\.(?:ReadFile|Open)\(`)
)

// tokenizeShellLine splits line into shell-like word tokens: single/double
// quotes keep their contents as one token (quotes stripped), a backslash
// escapes the very next character literally (so `\(` in a find expression
// does not get misread as a subshell paren), and each of `; | & ( ) < >`
// becomes its own token when it appears outside quotes (`&&`/`||`/`<<`/`>>`
// collapse to one two-character token). It is deliberately not a full
// shell parser — D-11 bounds the contract, this bounds the tokenizer to
// match it.
func tokenizeShellLine(line string) []string {
	var tokens []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	const operatorChars = ";|&()<>"
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(c)
			}
		case inDouble:
			switch {
			case c == '"':
				inDouble = false
			case c == '\\' && i+1 < len(runes):
				i++
				cur.WriteRune(runes[i])
			default:
				cur.WriteRune(c)
			}
		case c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
		case c == '\\':
			// A line-continuation backslash — nothing follows it on this
			// physical line. This scanner does not splice the next line
			// in, so the honest reading is "nothing was read here", not a
			// literal "\" argument (which grep's own "\" ... file
			// candidate rule would otherwise happily classify as a read).
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == ' ' || c == '\t':
			flush()
		case strings.ContainsRune(operatorChars, c):
			// A bare fd number glued directly to a redirect ("2>/dev/null")
			// belongs to the OPERATOR, not to the preceding command's
			// argument list — fold it in rather than flushing it as its
			// own word token, or "grep ... 2>/dev/null" reads "2" as a
			// path candidate.
			fdPrefix := ""
			if (c == '<' || c == '>') && cur.Len() > 0 && isAllDigits(cur.String()) {
				fdPrefix = cur.String()
				cur.Reset()
			} else {
				flush()
			}
			switch {
			case c == '<' && i+2 < len(runes) && runes[i+1] == '<' && runes[i+2] == '<':
				// A here-string ("<<<\"$out\"") feeds a shell STRING as
				// stdin, not a file — collapse it to its own token so the
				// single-"<" redirect-read rule below never sees it as a
				// bare "<".
				tokens = append(tokens, fdPrefix+"<<<")
				i += 2
			case (c == '&' || c == '|' || c == '<' || c == '>') && i+1 < len(runes) && runes[i+1] == c:
				tokens = append(tokens, fdPrefix+string([]rune{c, c}))
				i++
			default:
				tokens = append(tokens, fdPrefix+string(c))
			}
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return tokens
}

// looksPathLike is the narrow guard the "." / "source" alias needs (and
// only it — every other in-contract command word is unambiguous, D-11's
// list has no other English homograph): a variable, an explicit relative
// prefix, or a "/" somewhere in the token. A bare word never qualifies.
func looksPathLike(tok string) bool {
	return strings.HasPrefix(tok, "$") || strings.HasPrefix(tok, "./") ||
		strings.HasPrefix(tok, "~") || strings.Contains(tok, "/")
}

// looksLikeRedirectTarget is looksPathLike widened to admit a bare
// extension-bearing filename ("README.md" has no "/" or "$" but is
// unambiguously a file) — legal ONLY for the "<" redirect rule, never for
// "."/"source": an isolated "." is genuine punctuation often enough
// (". The outer...") that a redirect target needs the extra signal a
// command word already has for free (it has to BE the command word, not
// prose that merely contains it).
func looksLikeRedirectTarget(tok string) bool {
	if looksPathLike(tok) {
		return true
	}
	if idx := strings.LastIndex(tok, "."); idx > 0 && idx < len(tok)-1 {
		return true
	}
	return false
}

// isAllDigits reports whether s is non-empty and every rune is 0-9 — the
// fd-number test the tokenizer uses to fold "2>" together.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isBoundaryToken is boundaryTokens widened to recognise a redirect that
// carries its fd number ("2>", "0<") the way the tokenizer now folds it —
// tok is a boundary if it IS one verbatim, or is one with a leading run of
// digits stripped.
func isBoundaryToken(tok string) bool {
	if boundaryTokens[tok] {
		return true
	}
	trimmed := strings.TrimLeft(tok, "0123456789")
	return trimmed != tok && boundaryTokens[trimmed]
}

// candidatePaths applies D-11's per-command shape to cmd's argument tokens
// (already split from the boundary that ends them) and returns the tokens
// that are, structurally, path ARGUMENTS this command reads — still raw
// (quotes stripped, escapes resolved), not yet classified literal-vs-not.
func candidatePaths(cmd string, args []string) []string {
	switch cmd {
	case ".", "source":
		// "." is also plain English punctuation — a comment sentence
		// ending "...). The outer verification runner..." tokenizes with
		// "." sitting right after the ")" boundary, i.e. in command
		// position. Requiring the candidate to look path-shaped (a "/", a
		// leading "./"/"~", or a variable) is what keeps a real
		// `. "$LIB_DIR/common.sh"` detectable while a sentence break is
		// not: no bare English word satisfies it.
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			// A candidate carrying WHITESPACE is prose, not a path, and
			// this is the same English-homograph hazard one case further
			// out than the paragraph above models. An apostrophe in a
			// comment ("internal/space's CarriedFinding is a THIRD") opens
			// a single quote for the tokenizer, which then swallows the
			// rest of the line into ONE token — a token that contains "/"
			// and therefore satisfies looksPathLike. The guard's own
			// rationale stayed true ("no bare English word satisfies it")
			// and stopped covering the case, because a possessive turns a
			// word into a PHRASE. This repo has no tracked path with
			// whitespace in it, and scripts/classify-guard.sh refuses one
			// by name (its teeth seed exactly that violation), so a
			// whitespace-bearing candidate cannot be a read this gate
			// needs to see.
			if looksPathLike(a) && !strings.ContainsAny(a, " \t") {
				return []string{a}
			}
			return nil
		}
		return nil
	case "find":
		// find's leading non-flag tokens are ALL search roots
		// (`find "$A" "$B" -type f ...`); the first flag-shaped token
		// ends the root list and starts find's expression.
		var roots []string
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				break
			}
			roots = append(roots, a)
		}
		// A find whose expression NARROWS by -name/-path reads the
		// intersection, not the root: `find docs/features -path '*/audits/*'
		// -name '*-x.md'` cannot be flipped by docs/features/README.md.
		// Reporting the bare root there forces the gate's author to declare a
		// tree it does not read — and the only way to satisfy that is a
		// declaration wider than the truth, which is the failure this whole
		// phase exists to stop. So narrow it here instead.
		if unmodellableFind(args) {
			// A find with -prune, negation or alternation is a construct this
			// bounded classifier cannot model: its -path/-name arms mix
			// selectors with EXCLUSIONS, and narrowing on them would produce a
			// confident, specific, wrong answer (`**/.git` reported as an
			// input). Reporting the bare root would be just as wrong the other
			// way — it would demand the gate declare the whole repo, and a
			// declaration bent to fit a tool is the failure this phase exists
			// to stop. So it is neither: it is OUT OF CONTRACT, and the script
			// says so with lane-reads-opaque.
			return []string{unresolvedCandidate}
		}
		if narrowed := narrowFindRoots(roots, args); narrowed != nil {
			return narrowed
		}
		return roots
	case "cat", "wc":
		var out []string
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			out = append(out, a)
		}
		return out
	case "head", "tail":
		// -n/-c take a separate value token (a count, never a path) —
		// `tail -n 1` must not misread "1" as a file.
		var out []string
		skipNext := false
		for _, a := range args {
			if skipNext {
				skipNext = false
				continue
			}
			if a == "-n" || a == "-c" {
				skipNext = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			out = append(out, a)
		}
		return out
	case "grep", "sed":
		// The first non-flag token is the pattern/script, not a path —
		// UNLESS it was supplied via -f/--file, in which case every
		// non-flag token is a file operand.
		var out []string
		sawPatternOrScript := false
		skipNext := false
		for _, a := range args {
			if skipNext {
				out = append(out, a) // -f/--file's value IS a real read
				skipNext = false
				continue
			}
			if a == "-f" || a == "--file" {
				skipNext = true
				sawPatternOrScript = true
				continue
			}
			if v, ok := strings.CutPrefix(a, "--file="); ok {
				out = append(out, v)
				sawPatternOrScript = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			if !sawPatternOrScript {
				sawPatternOrScript = true
				continue
			}
			out = append(out, a)
		}
		return out
	case "awk":
		// Only the -f/--file program-file form is in contract (D-11);
		// awk's inline program string and its data-file operands are not
		// classified, by explicit scope, not oversight.
		var out []string
		skipNext := false
		for _, a := range args {
			if skipNext {
				out = append(out, a)
				skipNext = false
				continue
			}
			if a == "-f" || a == "--file" {
				skipNext = true
				continue
			}
			if v, ok := strings.CutPrefix(a, "--file="); ok {
				out = append(out, v)
			}
		}
		return out
	default:
		return nil
	}
}

// scanLineForReads classifies every candidate path this ONE line produces
// (command arguments plus input-redirection targets) as either a literal
// ReadRef or an UnresolvedRead — D-11/D-9's split.
func scanLineForReads(line string, lineNo int) (reads []ReadRef, unresolved []UnresolvedRead) {
	trimmed := strings.TrimSpace(line)
	// A Makefile recipe line's leading tab carries Make's own "@" (silent)
	// / "-" (ignore-error) markers glued directly to the command with no
	// space — "\t@grep ..." — which would otherwise hide the command word
	// inside the token "@grep". Stripping them is a no-op for shell
	// scripts, which do not start lines this way.
	scanLine := line
	if rest, ok := strings.CutPrefix(scanLine, "\t"); ok {
		scanLine = strings.TrimLeft(rest, "@-")
	}
	tokens := tokenizeShellLine(scanLine)
	n := len(tokens)

	atCommandStart := func(i int) bool {
		if i == 0 {
			return true
		}
		return isBoundaryToken(tokens[i-1])
	}

	classify := func(tok string) {
		if tok == "" || tok == "-" {
			return
		}
		if tok == unresolvedCandidate || strings.ContainsAny(tok, "$`") {
			// The bare sentinel carries no literal structure at all (D-9's
			// "I recognise this as a read but cannot model it" marker) —
			// Candidate stays empty so subsumption never gets a chance to
			// (wrongly) claim it backs anything.
			candidate := tok
			if tok == unresolvedCandidate {
				candidate = ""
			}
			unresolved = append(unresolved, UnresolvedRead{Line: lineNo, Text: trimmed, Candidate: candidate})
			return
		}
		reads = append(reads, ReadRef{Path: strings.TrimPrefix(tok, "./"), Line: lineNo})
	}

	// find's own grouping parens arrive here as bare "(" / ")" tokens (the
	// tokenizer resolves the shell escape), and those are boundary tokens — so
	// an argument walk starting at `find` stops dead at `\(` and never sees the
	// -prune/-o that follow. Judging modellability from the RAW LINE instead of
	// the truncated argument list is the honest reading: the exclusion arms are
	// right there in the source even when the tokenizer cannot hand them over.
	if findExpressionRe.MatchString(trimmed) && unmodellableFindLine(trimmed) {
		return nil, []UnresolvedRead{{Line: lineNo, Text: trimmed}}
	}

	// `go run <non-literal>.go` hands the whole question to a program this
	// scanner cannot see. Three REPO_GATES scripts do exactly that: they
	// `cat > "$ANALYZER_DIR/main.go" <<'GO' … GO` a complete Go analyzer and
	// run it, and that analyzer really does os.ReadFile and filepath.WalkDir
	// over the repo. heredocBodyLines correctly stops the SHELL scanner from
	// misreading the embedded Go as shell — but nothing then read it as Go, so
	// those three gates reported zero reads AND zero opaque flags: invisible,
	// not even debt-tracked. Flagging the invocation converts an invisible
	// hole into a counted one, which is the whole contract (D-11). Found by
	// the phase's own audit, not by a test.
	if goRunNonLiteralRe.MatchString(trimmed) {
		return nil, []UnresolvedRead{{Line: lineNo, Text: trimmed}}
	}

	for i := 0; i < n; i++ {
		tok := tokens[i]

		// Input-redirection targets (`wc -l <"$README_PATH"`) are a read
		// regardless of which command consumes the redirected stdin —
		// D-11's own framing is "which paths does the script read", not
		// "which arguments". "<<"/"<<<" (heredoc/here-string) are their
		// own tokens and never reach this branch. The path-shape guard
		// matters here specifically because a bare "<" is not gated on
		// command position the way a command WORD is — prose like
		// `` `a2a <verb>[ <sub>]` `` tokenizes "<" the same way a real
		// redirect does, and "verb"/"sub" are not paths.
		if tok == "<" && i+1 < n && looksLikeRedirectTarget(tokens[i+1]) {
			classify(tokens[i+1])
			continue
		}

		isCommandWord := tok == "." || inContractCommands[tok]
		if !isCommandWord || !atCommandStart(i) {
			continue
		}

		j := i + 1
		var args []string
		for j < n && !isBoundaryToken(tokens[j]) {
			args = append(args, tokens[j])
			j++
		}
		for _, p := range candidatePaths(tok, args) {
			classify(p)
		}
		i = j - 1
	}
	return reads, unresolved
}

// scanGoLineForReads is the Go analogue of scanLineForReads, bounded to
// exactly os.ReadFile/os.Open with a literal double-quoted argument
// (D-11's Go arm).
func scanGoLineForReads(line string, lineNo int) (reads []ReadRef, unresolved []UnresolvedRead) {
	if m := goReadFileLiteralRe.FindStringSubmatch(line); m != nil {
		return []ReadRef{{Path: m[1], Line: lineNo}}, nil
	}
	if goReadFileCallRe.MatchString(line) {
		return nil, []UnresolvedRead{{Line: lineNo, Text: strings.TrimSpace(line)}}
	}
	return nil, nil
}

// findOpaqueDirective looks for a `lane-reads-opaque: <reason>` comment
// line (D-11) within lines[startLine-1:endLine] (1-based, inclusive) —
// scoped to the SAME window a phase's reads are scanned from, so one
// script backing several phases (scripts/verify.sh, the Makefile) needs a
// directive per phase, not one that silently covers all of them.
func findOpaqueDirective(lines []string, startLine, endLine int, prefix string) (declared bool, reason string, err error) {
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		if i < 0 {
			continue
		}
		content, ok := commentContent(lines[i], prefix)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(content)
		if !strings.HasPrefix(trimmed, "lane-reads-opaque:") {
			continue
		}
		reason = strings.TrimSpace(strings.TrimPrefix(trimmed, "lane-reads-opaque:"))
		if reason == "" {
			return false, "", blockErr(i+1, "lane-reads-opaque: is present but empty")
		}
		return true, reason, nil
	}
	return false, "", nil
}

// declarationPatterns returns the path patterns d actually claims coverage
// for — Inputs for KindScoped, Claims for KindAlways (D-10), and nothing for
// KindNever or a Claims-less KindAlways (which "claims nothing" the same way
// Coverage's own doc comment already states). Shared by compareReads
// (reads-without-a-claim) and backedGlobs (P1's mirror — claims-without-a-
// read) so the two directions of the same rule cannot drift apart.
func declarationPatterns(d Declaration) []string {
	switch d.Kind {
	case KindScoped:
		return d.Inputs
	case KindAlways:
		return d.Claims
	default:
		return nil
	}
}

// compareReads checks every literal read this window produced against d's
// own declared coverage — Inputs for KindScoped, Claims for KindAlways
// (D-10; an ALWAYS declaration with no Claims has nothing to compare
// against and is skipped, matching "claims nothing"). KindNever carries no
// Inputs/Claims at all and is skipped the same way live-e2e is exempt from
// Coverage.
func compareReads(relPath string, d Declaration, reads []ReadRef) []Refusal {
	patterns := declarationPatterns(d)
	if len(patterns) == 0 {
		return nil
	}

	var refusals []Refusal
	for _, r := range reads {
		if MatchInputs(patterns, r.Path) {
			continue
		}
		refusals = append(refusals, Refusal{
			Subject: fmt.Sprintf("%s:%d", relPath, r.Line),
			Problem: fmt.Sprintf("phase %q reads %s but its declaration (%s) does not cover it", d.Phase, r.Path, d.Source),
			Fix:     fmt.Sprintf("add %s to the phase's lane-inputs (or lane-claims)", r.Path),
		})
	}
	return refusals
}

// unresolvedRefusals is D-9's refusal: an out-of-contract construct with no
// lane-reads-opaque line, named by file and line.
func unresolvedRefusals(relPath string, d Declaration, unresolved []UnresolvedRead) []Refusal {
	var refusals []Refusal
	for _, u := range unresolved {
		refusals = append(refusals, Refusal{
			Subject: fmt.Sprintf("%s:%d", relPath, u.Line),
			Problem: fmt.Sprintf("phase %q reads via a construct the classifier cannot resolve: %s", d.Phase, u.Text),
			Fix:     "add a `# lane-reads-opaque: <reason>` line (`//` in a .go file), or make the read a literal path the declaration can cover",
		})
	}
	return refusals
}

// PhaseBacking is P1's per-phase evidence for the claims-without-a-read
// direction (the mirror of D-11's own reads-without-a-claim): which of the
// phase's own declared patterns (Inputs for KindScoped, Claims for
// KindAlways) the extractor found at least one literal read covering.
type PhaseBacking struct {
	// BackedPatterns holds, as keys, every one of the phase's own declared
	// patterns that at least one literal ReadRef in its scanned window(s)
	// satisfied (MatchInputs([]string{pattern}, read.Path)).
	BackedPatterns map[string]bool
	// Opaque is true when ANY of the phase's scanned windows declares
	// `# lane-reads-opaque:` (`//` in a .go file) — D-11's escape hatch.
	// It is a DEBT/CEILING signal only (HonestyCheck's opaqueCount,
	// US-5's ratchet): it suppresses D-9's per-line "construct the
	// classifier cannot resolve" refusal the same way it always has, but it
	// is deliberately NOT consulted by claimVerdict (coverage.go) and does
	// NOT back any of the phase's declared globs. A directive that bought
	// coverage for free would be the "declare everything opaque" hole US-5
	// exists to close — `projection`'s own `**` directive is the proof: it
	// covers a genuinely unresolvable read (check-projection.sh's
	// scripts/lib/strip-set.txt), and that claim stays TRUE, but it must
	// not launder an unrelated, never-read path under the same `**` into
	// "covered".
	Opaque bool
	// NoSubject is true when the honesty question has no subject at all —
	// two cases. First, a presence-gated Makefile recipe whose backing
	// script is legitimately absent (a private harness gate the publisher
	// strips; honestyForMakePhase's own comment). Second — measured, not
	// theoretical — a window whose scan produced NEITHER a literal read NOR
	// an unresolved construct: D-11's extractor is bounded to
	// grep/cat/find/source/wc/head/tail/sed/awk (plus Go's os.ReadFile/
	// os.Open), so a script that only invokes `gofmt`, `go vet`, `go test`,
	// `golangci-lint` and similar produces zero evidence either way — not
	// because it reads nothing, but because the classifier has no contract
	// for that command shape. Scoring that identically to a phase whose
	// reads WERE classified and simply miss the declaration (like
	// release-notes-freshness) would be a confident, specific, wrong
	// verdict — measured at 57 corpus phases and 11,754 refusal lines on
	// the live tree before this field absorbed the case. Either way, a gate
	// this instrument never got the chance to judge cannot be scored
	// unbacked for a claim it never had the opportunity to prove, so the
	// claim counts as backed unconditionally.
	NoSubject bool
}

// backedGlobs reports, for each pattern d claims (declarationPatterns),
// whether reads found in ITS OWN window include at least one literal path
// that pattern alone covers — the mirror of compareReads: claims-without-a-
// read instead of reads-without-a-claim. Nil for KindNever or a Claims-less
// KindAlways (declarationPatterns already returns nil there, and nothing to
// back means nothing can be reported unbacked either).
func backedGlobs(d Declaration, reads []ReadRef) map[string]bool {
	patterns := declarationPatterns(d)
	if len(patterns) == 0 {
		return nil
	}
	backed := make(map[string]bool, len(patterns))
	for _, pat := range patterns {
		for _, r := range reads {
			if MatchInputs([]string{pat}, r.Path) {
				backed[pat] = true
				break
			}
		}
	}
	return backed
}

// mergeBacking folds b's patterns/opaque into into — used when a phase's
// honest window is really TWO windows (honestyForVerifyPhase's shell arm
// plus an optional Go arm via honestyForGoFile): a pattern backed by EITHER
// window backs the phase, and a directive declared in EITHER window backs
// every pattern the same way Opaque already does for a single window.
func mergeBacking(into PhaseBacking, b map[string]bool, opaque bool) PhaseBacking {
	if into.BackedPatterns == nil && len(b) > 0 {
		into.BackedPatterns = make(map[string]bool, len(b))
	}
	for pat, ok := range b {
		if ok {
			into.BackedPatterns[pat] = true
		}
	}
	into.Opaque = into.Opaque || opaque
	return into
}

// heredocStartRe matches a shell heredoc opener at end of line
// (`<<'GO'`, `<<-EOF`, `<<"TOKEN"`) — the delimiter word, quoted or not.
var heredocStartRe = regexp.MustCompile(`<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)['"]?\s*$`)

// heredocBodyLines returns every 0-based line index that sits inside a
// shell heredoc body (several real gate scripts embed a whole Go analyzer
// program this way — check_work_checkpoint_schema.sh, check_event_writer_
// receipts.sh). A heredoc body is not shell syntax at all; scanning it
// line-by-line as shell would misread arbitrary embedded-language tokens
// (a Go `== 0` comparison, an `= ` field) as shell reads. `<<-` strips
// leading tabs from the terminator line per bash's own rule.
func heredocBodyLines(lines []string) map[int]bool {
	skip := map[int]bool{}
	for i := 0; i < len(lines); i++ {
		m := heredocStartRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		terminator := m[2]
		stripTabs := strings.Contains(lines[i], "<<-")
		for j := i + 1; j < len(lines); j++ {
			candidate := lines[j]
			if stripTabs {
				candidate = strings.TrimLeft(candidate, "\t")
			}
			if candidate == terminator {
				for k := i + 1; k <= j; k++ {
					skip[k] = true
				}
				i = j
				break
			}
		}
	}
	return skip
}

// honestyForWindow is the shared core: scan lines[startLine-1:endLine]
// (1-based, inclusive) of the file at relPath, classify every read, check
// literal reads against d, and turn any unresolved construct into a
// refusal unless the same window carries lane-reads-opaque.
//
// opaque reports whether the window DECLARES lane-reads-opaque at all — not
// (as an earlier revision read) only when the window also happened to carry
// an unresolved construct today. P1 needs the directive's PRESENCE as its
// own backing signal for whatever the phase claims (a declared-but-honestly-
// unreachable glob is exactly what US-5's escape hatch exists to cover), and
// a signal gated on "and something unresolved happened to be here too" would
// silently miss a directive written for a construct that has since been
// simplified away, or one written pre-emptively. This does widen what
// counts as an opaque PHASE for HonestyCheck's own opaqueCount (previously:
// declared AND unresolved>0; now: declared) — a deliberate redefinition, not
// an oversight; see lanecheck.go's own comment on opaqueCount and this
// phase's reported deviations.
//
// hadEvidence reports whether the window produced ANY read or unresolved
// construct at all — the signal PhaseBacking.NoSubject (via the caller)
// widens to cover, beside the presence-gated-absent-script case. D-11's own
// contract is BOUNDED (grep/cat/find/source/wc/head/tail/sed/awk, plus the
// Go os.ReadFile/os.Open arm) — a phase whose script only invokes `gofmt`,
// `go vet`, `go test`, `golangci-lint` and similar produces ZERO reads and
// ZERO unresolved constructs, not because it reads nothing (it plainly
// does), but because the classifier has no contract for that command shape
// at all. Zero reads is therefore ABSENCE OF EVIDENCE, not evidence of a
// false claim — scoring it identically to release-notes-freshness (which
// DOES have grep/cat-shaped reads that simply miss its declared glob) would
// be the "confident, specific, wrong" failure this package's own doc.go
// warns against. Measured: without this distinction, 57 real corpus phases
// and 11,754 refusal lines went red on the live tree — go-test, gofmt, vet,
// golangci-lint among them — none of them a genuine false claim.
func honestyForWindow(relPath string, allLines []string, startLine, endLine int, isGo bool, d Declaration) (refusals []Refusal, opaque bool, backed map[string]bool, hadEvidence bool, err error) {
	if startLine < 1 || endLine < startLine || endLine > len(allLines) {
		return nil, false, nil, false, fmt.Errorf("honesty check for %q: invalid scan window [%d,%d] in %s (file has %d lines)", d.Phase, startLine, endLine, relPath, len(allLines))
	}
	prefix := "#"
	if isGo {
		prefix = "//"
	}

	// heredocBodyLines is computed over the WHOLE file, not just the
	// window, so a heredoc that opens before the window (or closes after
	// it) is still recognised correctly.
	inHeredoc := heredocBodyLines(allLines)

	var reads []ReadRef
	var unresolved []UnresolvedRead
	var scopeGlobs []string
	for i := startLine - 1; i < endLine; i++ {
		if inHeredoc[i] {
			continue
		}
		var r []ReadRef
		var u []UnresolvedRead
		if isGo {
			r, u = scanGoLineForReads(allLines[i], i+1)
		} else {
			r, u = scanLineForReads(allLines[i], i+1)
			// P1b's command vocabulary (capability a): a line the D-11
			// read-shaped list cannot classify may still be a RECOGNISED
			// external-toolchain invocation (go test, gofmt, go vet,
			// golangci-lint) — evidence of a real read this bounded
			// tokenizer simply has no contract for. It is scanned
			// alongside, not instead of, scanLineForReads: a line can carry
			// both an in-contract read AND (on a later token) a recognised
			// invocation, and neither should shadow the other.
			if globs, ok := scopeReadingInvocation(allLines[i]); ok {
				scopeGlobs = append(scopeGlobs, globs...)
			}
		}
		reads = append(reads, r...)
		unresolved = append(unresolved, u...)
	}

	declared, _, oerr := findOpaqueDirective(allLines, startLine, endLine, prefix)
	if oerr != nil {
		return nil, false, nil, false, fmt.Errorf("%s: %w", relPath, oerr)
	}

	// P1b's glob subsumption (capability b): a variable-built unresolved
	// read whose LITERAL parts plausibly reach only within one of d's own
	// declared patterns is treated as resolved enough to back that pattern
	// — and, since the reach is now accounted for, it no longer needs
	// `lane-reads-opaque` to excuse it (spec 09 AC-8). One that cannot be
	// proven to reach only within a declared pattern (no literal structure
	// at all, or a literal segment that conflicts with every declared
	// pattern) stays in the D-9 "cannot resolve" bucket unchanged —
	// precision over recall: subsumeUnresolved only ever REMOVES an entry
	// from unresolved, never invents backing evidence compareReads/D-9
	// cannot also justify.
	unresolved, subsumedPatterns := subsumeUnresolved(d, unresolved)

	refusals = append(refusals, compareReads(relPath, d, reads)...)
	if len(unresolved) > 0 && !declared {
		refusals = append(refusals, unresolvedRefusals(relPath, d, unresolved)...)
	}
	backed = backedGlobs(d, reads)
	for pat := range subsumedPatterns {
		if backed == nil {
			backed = map[string]bool{}
		}
		backed[pat] = true
	}
	for _, g := range scopeGlobs {
		for _, pat := range declarationPatterns(d) {
			if strings.HasPrefix(pat, "!") {
				continue
			}
			if Subsumes(g, pat) {
				if backed == nil {
					backed = map[string]bool{}
				}
				backed[pat] = true
			}
		}
	}
	hadEvidence = len(reads) > 0 || len(unresolved) > 0 || len(scopeGlobs) > 0 || len(subsumedPatterns) > 0
	return refusals, declared, backed, hadEvidence, nil
}

// subsumeUnresolved is P1b's capability (b) applied to one window's own
// unresolved list: every UnresolvedRead whose Candidate's literal parts
// glob.Subsumes some pattern d declares is pulled OUT of the returned
// unresolved slice (so it stops demanding a lane-reads-opaque directive,
// AC-8) and that pattern is returned in the backed set. An UnresolvedRead
// with no Candidate (the whole-line sentinels) or whose literal parts do
// not reach any declared pattern passes through unchanged.
func subsumeUnresolved(d Declaration, unresolved []UnresolvedRead) (stillUnresolved []UnresolvedRead, backed map[string]bool) {
	patterns := declarationPatterns(d)
	for _, u := range unresolved {
		pat, ok := literalTailScope(u.Candidate)
		if !ok {
			stillUnresolved = append(stillUnresolved, u)
			continue
		}
		matched := false
		for _, dp := range patterns {
			if strings.HasPrefix(dp, "!") {
				continue
			}
			if Subsumes(pat, dp) {
				if backed == nil {
					backed = map[string]bool{}
				}
				backed[dp] = true
				matched = true
			}
		}
		if !matched {
			stillUnresolved = append(stillUnresolved, u)
		}
	}
	return stillUnresolved, backed
}

// literalTailScope turns an UnresolvedRead's raw Candidate (a path-shaped
// token, quotes stripped, escapes resolved) into a glob: every segment that
// carries a shell variable or command-substitution marker ("$"/"`")
// becomes "**" — an unconstrained span, because the classifier does not
// know how many real path segments the variable expands to, only that none
// can be ruled out — and every other segment is kept literally. ok is
// false when candidate is empty or carries no literal segment at all (the
// bare unresolvedCandidate sentinel, or a token that is ENTIRELY a
// variable) — there is nothing to test a declared pattern against, so
// nothing is claimed. Shares glob.go's own segment syntax (splitSegments)
// rather than a second path grammar.
func literalTailScope(candidate string) (pattern string, ok bool) {
	if candidate == "" {
		return "", false
	}
	segs := splitSegments(candidate)
	hasLiteral := false
	for i, s := range segs {
		if strings.ContainsAny(s, "$`") {
			segs[i] = "**"
		} else if s != "**" {
			hasLiteral = true
		}
	}
	if !hasLiteral {
		return "", false
	}
	return strings.Join(segs, "/"), true
}

// goPackageScope turns a `go test`/`go vet`/`gofmt`/`golangci-lint run`
// package-or-directory selector into the glob of Go source it denotes.
// "./..." (or bare "...") and "." mean the whole tree; "./pkg/..." means
// everything under pkg, recursively. Anything else — a single
// non-recursive package, an explicit file — is not a shape any invocation
// in this corpus uses today and is left unrecognised (ok=false) rather
// than guessed at.
func goPackageScope(pkg string) (glob string, ok bool) {
	switch {
	case pkg == "./..." || pkg == "...":
		return "**/*.go", true
	case pkg == ".":
		return "**/*.go", true
	case strings.HasSuffix(pkg, "/..."):
		dir := strings.TrimSuffix(strings.TrimPrefix(pkg, "./"), "/...")
		if dir == "" {
			return "**/*.go", true
		}
		return dir + "/**/*.go", true
	}
	return "", false
}

// scopeInvocationSplitRe strips the shell punctuation that would otherwise
// hide a recognised command inside a word — scopeReadingInvocation's own
// motivating case, `unformatted="$(gofmt -l .)"` (check_gofmt's real body),
// glues "$(" directly onto "gofmt" with no space, so tokenizeShellLine's
// quote-aware, argument-precise splitting (built for candidatePaths, which
// DOES need exact quoting) hides the word inside one opaque quoted token.
// This vocabulary does not need that precision — only "is this word, by
// itself, one of four known tool names, followed by a package-shaped
// operand" — so it trades the shared tokenizer for a coarser one that
// cannot be fooled by an enclosing command substitution.
var scopeInvocationSplitRe = regexp.MustCompile("[\"'`$(){}]")

// lastNonFlagToken returns the last token in tokens that does not start
// with "-" — the package/directory operand every recognised invocation
// carries exactly one of, regardless of how many flags precede or follow
// it (`go vet -tags=livee2e ./...`, `go test ./... -race -count=1`).
func lastNonFlagToken(tokens []string) (tok string, ok bool) {
	for _, t := range tokens {
		if strings.HasPrefix(t, "-") {
			continue
		}
		tok, ok = t, true
	}
	return tok, ok
}

// scopeReadingInvocation is P1b's command vocabulary (capability a, spec 09
// §9): `go test`, `go vet`, `gofmt` and `golangci-lint run` are each a
// stable, EXTERNAL toolchain command whose own contract IS "read the named
// package tree", even though D-11's read-shaped command list
// (grep/cat/find/source/wc/head/tail/sed/awk) has no entry for any of
// them. It is a hand-typed list DELIBERATELY, and only here: it is over an
// external, stable toolchain rather than a repo path, so a stale entry
// costs a loud false UNBACKED (AC-8's ratchet on opaqueCount's sibling
// metric), never a silent false green the way a stale path allowlist
// would. ok is false for anything this vocabulary does not recognise —
// the same refuse-rather-than-guess rule candidatePaths' own per-command
// shapes already follow. go.mod/go.sum (and, for golangci-lint,
// .golangci.yml) are included because the tool genuinely reads them to
// resolve the module and its lint config, not because the declaration
// happens to list them.
//
// It is deliberately looser than scanLineForReads' own atCommandStart
// discipline — scripts/verify.sh's real `vet` declaration is bare
// (`run_phase vet go vet -tags=livee2e ./...`), so the actual invocation
// does not start the line or follow a shell operator, only two ordinary
// words ("run_phase vet"). Measured against the live corpus: only two
// lines anywhere in the gate scripts mention a recognised shape in PROSE
// rather than as a real invocation — check-projection.sh's own comment
// about `go test ./...` logs (inside projection's scanned window, which
// already carries its own lane-reads-opaque directive, so a spurious
// extra backed pattern there changes nothing Coverage decides) and
// Makefile:45's top-of-file documentation about `go vet ./...` (NOT
// checked against any directive: it sits in the file's header, before
// every target, and honestyForMakePhase's per-phase window always starts
// at that phase's OWN declaration — never at line 1 — so no phase's
// window can ever reach it).
func scopeReadingInvocation(line string) (globs []string, ok bool) {
	fields := strings.Fields(scopeInvocationSplitRe.ReplaceAllString(line, " "))
	for i, tok := range fields {
		switch {
		case tok == "gofmt":
			if arg, aok := lastNonFlagToken(fields[i+1:]); aok {
				if scope, sok := goPackageScope(arg); sok {
					return []string{scope}, true
				}
			}
		case tok == "go" && i+1 < len(fields) && (fields[i+1] == "test" || fields[i+1] == "vet"):
			if arg, aok := lastNonFlagToken(fields[i+2:]); aok {
				if scope, sok := goPackageScope(arg); sok {
					return []string{scope, "go.mod", "go.sum"}, true
				}
			}
		case tok == "golangci-lint" && i+1 < len(fields) && fields[i+1] == "run":
			if arg, aok := lastNonFlagToken(fields[i+2:]); aok {
				if scope, sok := goPackageScope(arg); sok {
					return []string{scope, "go.mod", "go.sum", ".golangci.yml"}, true
				}
			}
		}
	}
	return nil, false
}

// scriptForRecipe reports the script a Makefile recipe invokes via
// `bash scripts/X.sh`, reusing makefile.go's own bashScriptCallRe rather
// than a second regex for the same shape.
func scriptForRecipe(recipe []string) (string, bool) {
	for _, line := range recipe {
		if m := bashScriptCallRe.FindStringSubmatch(line); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// parseSourceLine pulls the trailing ":<line>" off a Declaration/Phase
// Source string ("scripts/verify.sh:564" -> 564).
func parseSourceLine(source string) (int, bool) {
	idx := strings.LastIndex(source, ":")
	if idx < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(source[idx+1:])
	if err != nil {
		return 0, false
	}
	return n, true
}

// honestyForMakePhase resolves d's scan region for a Makefile-corpus phase:
// the whole backing script when the recipe shells to one (readme-lint,
// classify-guard, ...), or the recipe's own lines when it does not
// (workflow-lint's inline shell).
//
// hasBacking is false exactly when this arm is punting to a window owned
// elsewhere (the len(blocks) > 1 branch below, where the verifyPhases loop's
// honestyForVerifyPhase owns the phase's real window) — the caller must NOT
// store a PhaseBacking for the phase in that case, or an empty placeholder
// could shadow the real one depending on iteration order. It is true (with
// backing.NoSubject set) for a presence-gated absent script — the claim
// survives the script's absence, the same way Coverage already treats it.
func honestyForMakePhase(root string, doc *makefileDoc, d Declaration) (refusals []Refusal, opaque bool, backing PhaseBacking, hasBacking bool, err error) {
	t, ok := doc.Targets[d.Phase]
	if !ok {
		return nil, false, PhaseBacking{}, false, nil
	}
	if script, sok := scriptForRecipe(t.Recipe); sok {
		raw, rerr := readRepoFile(root, filepath.FromSlash(script))
		if rerr != nil {
			// A PRESENCE-GATED recipe may name a script that is absent here,
			// and that is not a defect: several private harness gates are
			// untracked by design (they read paths the publisher STRIPS), and
			// their recipes guard on `[ -f <script> ]` so `make check` skips
			// them cleanly in CI, in a public checkout, and in the filtered
			// release candidate. A gate that does not run cannot read anything
			// it failed to declare, so the honesty question has no subject.
			//
			// The declaration itself must still exist and is still checked by
			// Coverage — that is the WHOLE point of putting such a gate's
			// lane-inputs on its tracked Makefile recipe rather than in the
			// untracked script. Before that move, the claim vanished with the
			// script and `lane-declarations` reported the paths as unclaimed
			// everywhere the harness was absent: private CI on `main` was red
			// on exactly this from 2026-08-09, and the v0.19.10 candidate would
			// have failed its own `make check` at the first phase.
			//
			// The guard is deliberately narrow. An UNGUARDED recipe naming a
			// missing script still errors, because that one is a real defect —
			// the gate silently does nothing and nobody is told.
			if isNotExist(rerr) && recipeGuardsPresence(t.Recipe, script) {
				return nil, false, PhaseBacking{NoSubject: true}, true, nil
			}
			return nil, false, PhaseBacking{}, false, fmt.Errorf("honesty check for %q: read %s: %w", d.Phase, script, rerr)
		}
		lines := strings.Split(string(raw), "\n")

		// A script whose own header carries MORE THAN ONE lane-inputs block
		// is a declared multi-phase corpus, not "the script backing this one
		// phase" — the same test loadScriptHeaders already applies when it
		// excludes scripts/verify.sh from its own single-block scan ("it
		// carries many blocks, one per wrapped/bare phase"; see its "%d
		// lane-inputs blocks in one script header, want exactly one gate per
		// script" refusal for the single-block case this mirrors). Nothing
		// here names scripts/verify.sh by string — the block count is the
		// signal, so a second script shaped the same way would be recognised
		// without a code change; scripts/verify.sh is simply the one script
		// in this corpus that is shaped that way today.
		//
		// A whole-file scan against ONE phase's declaration is dishonest in
		// both directions for such a script: it credits the phase with every
		// OTHER phase's reads, and — the defect this branch exists to close
		// — it lets a lane-reads-opaque line ANYWHERE in the file absolve an
		// unresolved construct that phase's own declared window never
		// touches (D-9/D-11: a debt with a size, never a blanket exemption).
		//
		// Two of this corpus's three real Makefile-recipe-to-verify.sh cases
		// (live-e2e, logic-e2e) already carry their OWN lane-inputs block
		// inside scripts/verify.sh, so the verifyPhases loop in HonestyCheck
		// already scans their PRECISE window via honestyForVerifyPhase.
		// Re-scanning here would double-count opaqueCount and duplicate
		// every refusal for the same declaration — the honest thing for the
		// recipe arm to do is stop, not re-derive a window the verify.sh arm
		// already owns.
		//
		// The third (harness-check) has NO lane-inputs block of its own
		// inside scripts/verify.sh — its declaration sits on the Makefile
		// target, and the reads it actually describes are
		// `_harness-check`'s presence-gated `[ -f ... ]` guards, which are
		// not a read-shaped construct in D-11's contract at all (grep, cat,
		// find, source, wc, head, tail, sed, awk — not a shell `[ -f ]`
		// test). It therefore has no honest window here either, the same
		// "the honesty question has no subject" answer the presence-gate
		// branch above gives an absent script.
		blocks, berr := findLaneBlocks(lines, "#")
		if berr != nil {
			return nil, false, PhaseBacking{}, false, fmt.Errorf("honesty check for %q: %s: %w", d.Phase, script, berr)
		}
		if len(blocks) > 1 {
			return nil, false, PhaseBacking{}, false, nil
		}
		r, declared, backed, hadEvidence, werr := honestyForWindow(script, lines, 1, len(lines), false, d)
		if werr != nil {
			return nil, false, PhaseBacking{}, false, werr
		}
		return r, declared, PhaseBacking{BackedPatterns: backed, Opaque: declared, NoSubject: !hadEvidence}, true, nil
	}

	startLine, sok := parseSourceLine(d.Source)
	if !sok {
		return nil, false, PhaseBacking{}, false, fmt.Errorf("honesty check for %q: cannot parse declaration source %q", d.Phase, d.Source)
	}
	endLine := t.HeaderIdx + 1 + len(t.Recipe)
	r, declared, backed, hadEvidence, werr := honestyForWindow("Makefile", doc.Lines, startLine, endLine, false, d)
	if werr != nil {
		return nil, false, PhaseBacking{}, false, werr
	}
	return r, declared, PhaseBacking{BackedPatterns: backed, Opaque: declared, NoSubject: !hadEvidence}, true, nil
}

// honestyForVerifyPhase resolves d's scan region for a scripts/verify.sh
// corpus phase: the wrapping function's body when one backs it (build-cli,
// go-test, ...), or just the bare run_phase statement's own line
// otherwise (vet, coverage-policy, harness-teeth) — plus, when that bare
// statement invokes `go run <file>.go`, the Go arm against that file
// (coverage-policy -> covercheck.go, the one shape in this corpus).
//
// Wrapped-vs-bare is decided by RE-DERIVING the declaration's own
// Following line via findLaneBlocks — the same authority
// loadVerifyDeclarations used to parse it in the first place — rather than
// by asking corpusFromVerify's funcToPhase "does some function back this
// phase name". Those two questions coincide for every wrapped phase
// (build-cli, go-test, live-e2e, ...) but NOT for go-test-scoped: its bare
// `run_phase go-test-scoped run_scoped_tests` names a function that DOES
// exist in the file (run_scoped_tests, defined earlier, backing the
// UNRELATED go-test call site too) — funcToPhase happily maps it, and
// scanning that function's body (which sits BEFORE this declaration in the
// file) produces a nonsensical, sometimes-inverted window. Following is
// the one signal that actually distinguishes "this call site opens a
// function" from "this call site's command token happens to also be a
// function name elsewhere".
func honestyForVerifyPhase(root string, lines []string, p Phase, d Declaration) (refusals []Refusal, opaque bool, backing PhaseBacking, err error) {
	startLine, sok := parseSourceLine(d.Source)
	if !sok {
		return nil, false, PhaseBacking{}, fmt.Errorf("honesty check for %q: cannot parse declaration source %q", d.Phase, d.Source)
	}

	blocks, berr := findLaneBlocks(lines, "#")
	if berr != nil {
		return nil, false, PhaseBacking{}, fmt.Errorf("honesty check for %q: scripts/verify.sh: %w", d.Phase, berr)
	}
	var following string
	foundBlock := false
	for _, b := range blocks {
		if b.StartLine == startLine {
			following, foundBlock = b.Following, true
			break
		}
	}
	if !foundBlock {
		return nil, false, PhaseBacking{}, fmt.Errorf("honesty check for %q: no lane-inputs block starts at scripts/verify.sh:%d", d.Phase, startLine)
	}

	if m := funcOpenRe.FindStringSubmatch(following); m != nil {
		_, fnEnd, fok := verifyFunctionBodyLines(lines, m[1])
		if !fok {
			return nil, false, PhaseBacking{}, fmt.Errorf("honesty check for %q: function %s() not found in scripts/verify.sh", d.Phase, m[1])
		}
		r, declared, backed, hadEvidence, werr := honestyForWindow("scripts/verify.sh", lines, startLine, fnEnd, false, d)
		if werr != nil {
			return nil, false, PhaseBacking{}, werr
		}
		return r, declared, PhaseBacking{BackedPatterns: backed, Opaque: declared, NoSubject: !hadEvidence}, nil
	}

	callLine, cok := parseSourceLine(p.Source)
	if !cok {
		return nil, false, PhaseBacking{}, fmt.Errorf("honesty check for %q: cannot parse call site %q", d.Phase, p.Source)
	}
	var backed map[string]bool
	var hadEvidence bool
	refusals, opaque, backed, hadEvidence, err = honestyForWindow("scripts/verify.sh", lines, startLine, callLine, false, d)
	if err != nil {
		return nil, false, PhaseBacking{}, err
	}
	backing = PhaseBacking{BackedPatterns: backed, Opaque: opaque, NoSubject: !hadEvidence}

	if callLine-1 >= 0 && callLine-1 < len(lines) {
		if m := goRunFileRe.FindStringSubmatch(lines[callLine-1]); m != nil {
			goRefusals, goOpaque, goBacked, goHadEvidence, gerr := honestyForGoFile(root, m[1], d)
			if gerr != nil {
				return nil, false, PhaseBacking{}, gerr
			}
			refusals = append(refusals, goRefusals...)
			opaque = opaque || goOpaque
			hadEvidence = hadEvidence || goHadEvidence
			backing = mergeBacking(backing, goBacked, goOpaque)
			backing.NoSubject = !hadEvidence
		}
	}

	// P1b's second window: this corpus's REAL bare declarations sit right
	// above the run_phase CALL SITE, not above a "name() {" line — measured
	// against the live tree (spec 09's own §11 amendment), which is why the
	// funcOpenRe branch above never actually fires for go-test/gofmt/
	// golangci-lint despite each being wrapped by a real function
	// (run_go_tests/check_gofmt/check_lint). Its own real invocation text
	// (`go test ./...`, `gofmt -l .`, `golangci-lint run ./...`) therefore
	// never reaches honestyForWindow above, and the vocabulary this phase
	// adds could never fire either. This closes that gap the SAME way the
	// Go arm above does — a second, ADDITIONAL window, merged rather than
	// substituted, so nothing already proven by the bare window is lost.
	//
	// It follows the call site's own command token ONLY when doing so is
	// unambiguous: the token must name a function actually defined in this
	// file (functions), and no OTHER top-level run_phase call site may use
	// that same command token for a DIFFERENT phase — the shape
	// funcOpenRe's own doc comment above describes (a function backing two
	// phases at once). That comment's specific example does not reproduce
	// in the live tree — grep finds exactly one top-level call site naming
	// run_scoped_tests (go-test-scoped's own, line 1494), not two — so the
	// guard below is currently vacuous in practice. It stays anyway: it is
	// computed from the file being scanned rather than recalled from a
	// prior finding, so it keeps holding if a second call site is ever
	// added.
	if callLine-1 >= 0 && callLine-1 < len(lines) {
		if m := runPhaseRe.FindStringSubmatch(lines[callLine-1]); m != nil {
			cmdTok := strings.Trim(m[2], `"`)
			if fnStart, fnEnd, fok := wrappingFunctionWindow(lines, d.Phase, cmdTok); fok {
				fnRefusals, fnOpaque, fnBacked, fnHadEvidence, ferr := honestyForWindow("scripts/verify.sh", lines, fnStart, fnEnd, false, d)
				if ferr != nil {
					return nil, false, PhaseBacking{}, ferr
				}
				refusals = append(refusals, fnRefusals...)
				opaque = opaque || fnOpaque
				hadEvidence = hadEvidence || fnHadEvidence
				backing = mergeBacking(backing, fnBacked, fnOpaque)
				backing.NoSubject = !hadEvidence
			}
		}
	}
	return refusals, opaque, backing, nil
}

// wrappingFunctionWindow reports the [start, end] (1-based, verifyFunctionBodyLines'
// own convention) line range of the function cmdTok names, but ONLY when
// that is unambiguous: cmdTok must be a real "name() {" definition in
// lines, AND every top-level run_phase call site using cmdTok as its
// command must name phaseName — the same collision corpusFromVerify's own
// funcToPhase construction refuses at Load() time (D-2), re-derived here
// rather than threaded through as a parameter so this proof stays local to
// the one call it backs.
func wrappingFunctionWindow(lines []string, phaseName, cmdTok string) (start, end int, ok bool) {
	callSites, functions, err := scanVerifyPhases(lines)
	if err != nil || !functions[cmdTok] {
		return 0, 0, false
	}
	for _, cs := range callSites {
		if cs.Command == cmdTok && cs.Name != phaseName {
			return 0, 0, false
		}
	}
	return verifyFunctionBodyLines(lines, cmdTok)
}

// honestyForGoFile is D-11's Go arm: scan one `go run <file>.go`-named
// file (whole-file, its own lane-reads-opaque if it needs one) for
// os.ReadFile/os.Open.
func honestyForGoFile(root, relPath string, d Declaration) (refusals []Refusal, opaque bool, backed map[string]bool, hadEvidence bool, err error) {
	raw, rerr := readRepoFile(root, filepath.FromSlash(relPath))
	if errors.Is(rerr, fs.ErrNotExist) {
		// The file the call site names is not in THIS tree. That is not a
		// fault to abort on: it is the same "the honesty question has no
		// subject" answer honestyForMakePhase already gives an absent
		// script, and a declaration pointing at a file that does not exist
		// is Reconcile's finding to report, not this pass's to die on.
		//
		// It is load-bearing rather than defensive. `verify.sh --teeth`
		// scans a deliberately PARTIAL fixture tree, so coverage-policy's
		// `go run internal/coveragepolicy/covercheck.go` names a file that
		// fixture does not carry. Aborting there killed the whole honesty
		// pass, which made `make harness-check` red on a tree whose only
		// defect was that a fixture is smaller than the repo.
		return nil, false, nil, false, nil
	}
	if rerr != nil {
		return nil, false, nil, false, fmt.Errorf("honesty check for %q: read %s: %w", d.Phase, relPath, rerr)
	}
	lines := strings.Split(string(raw), "\n")
	return honestyForWindow(relPath, lines, 1, len(lines), true, d)
}

// verifyFunctionBodyLines finds `name() {` (via verifysh.go's own
// funcOpenRe, not a second pattern) and returns its 1-based [open, close]
// line range, closing at a lone "}" at column 0 — the same single-depth
// rule scanVerifyPhases already documents as exact for this file's actual
// shape.
func verifyFunctionBodyLines(lines []string, name string) (startLine, endLine int, ok bool) {
	for i, line := range lines {
		m := funcOpenRe.FindStringSubmatch(line)
		if m == nil || m[1] != name {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "}" {
				return i + 1, j + 1, true
			}
		}
		return i + 1, len(lines), true
	}
	return 0, 0, false
}

// readVerifyLines reads scripts/verify.sh once for HonestyCheck's own use
// — Load and Corpus each read it separately too; a single package-wide
// cache is not worth the complexity for a CLI that runs once per
// invocation.
func readVerifyLines(root string) ([]string, error) {
	raw, err := readRepoFile(root, "scripts", "verify.sh")
	if err != nil {
		return nil, fmt.Errorf("read scripts/verify.sh: %w", err)
	}
	return strings.Split(string(raw), "\n"), nil
}

// HonestyCheck is D-11's per-gate honesty pass: for every corpus phase that
// carries a declaration, does the script (or Go file) backing it actually
// read only what it declared? It complements Reconcile and Coverage, which
// check the DECLARATION side only — this checks the declaration against
// the script's own body.
//
// opaqueCount is the running total of phases whose backing window(s) carry
// a lane-reads-opaque directive at all — D-11's "the number is the point":
// a silently-unclassified read is invisible, a printed count is a debt with
// a size. P1 widened this from "declared AND an unresolved construct was
// present in that same window" to "declared" alone (honestyForWindow's own
// comment has the reasoning) so the SAME predicate backs a phase's declared
// globs (backing's Opaque field) and feeds this ceiling — a directive that
// backed a glob for free without ever moving this count would be the
// "declare everything opaque" hole US-5 exists to close.
//
// backing is P1's own addition: per phase, which of its declared patterns
// the extractor found a literal read for, whether its window(s) declare
// lane-reads-opaque, and whether the honesty question has no subject at all
// (a presence-gated Makefile recipe whose script is legitimately absent —
// PhaseBacking's own doc comment). Coverage and Derive both consult it
// through the SAME predicate (coverage.go's claimVerdict) so a claim's
// three-valued verdict cannot disagree between `--verify` and `--derive`.
//
// A `go-test-scoped:./pkg/...` declaration is out of this deliverable's
// contract (per-gate SCRIPT, not per-package tests) and is never reached
// here — it has no entry in makePhases/verifyPhases at all, and so never
// gets a backing entry; Coverage/Derive treat a missing entry as "no
// evidence" (the zero value), which for a go-test-scoped declaration is
// harmless because Reconcile/Corpus never route real repo paths through it
// via Coverage's claimers loop the way a Makefile/verify.sh phase's own
// Inputs would be.
func HonestyCheck(root string, decls []Declaration) (refusals []Refusal, opaqueCount int, backing map[string]PhaseBacking, err error) {
	byPhase := map[string]Declaration{}
	for _, d := range decls {
		byPhase[d.Phase] = d
	}
	backing = map[string]PhaseBacking{}

	makePhases, makeDoc, merr := corpusFromMakefile(root)
	if merr != nil {
		return nil, 0, nil, merr
	}
	verifyPhases, _, verr := corpusFromVerify(root)
	if verr != nil {
		return nil, 0, nil, verr
	}
	verifyLines, rerr := readVerifyLines(root)
	if rerr != nil {
		return nil, 0, nil, rerr
	}

	for _, p := range makePhases {
		d, ok := byPhase[p.Name]
		if !ok {
			continue // Reconcile already reports the missing declaration
		}
		r, phaseOpaque, phaseBacking, hasBacking, herr := honestyForMakePhase(root, makeDoc, d)
		if herr != nil {
			return nil, 0, nil, herr
		}
		refusals = append(refusals, r...)
		if phaseOpaque {
			opaqueCount++
		}
		if hasBacking {
			backing[p.Name] = phaseBacking
		}
	}

	for _, p := range verifyPhases {
		d, ok := byPhase[p.Name]
		if !ok {
			continue
		}
		r, phaseOpaque, phaseBacking, herr := honestyForVerifyPhase(root, verifyLines, p, d)
		if herr != nil {
			return nil, 0, nil, herr
		}
		refusals = append(refusals, r...)
		if phaseOpaque {
			opaqueCount++
		}
		backing[p.Name] = phaseBacking
	}

	return refusals, opaqueCount, backing, nil
}

// UnbackedClaimCount reports how many (phase, declared-glob) pairs across
// decls are unbacked (US-2's debt metric, the converse of opaqueCount): a
// KindScoped Inputs entry or a KindAlways Claims entry the extractor found
// no literal read for, and whose phase does not have NoSubject (the
// presence-gated-absent exemption).
//
// Deliberately NOT suppressed by Opaque, and this is the one place where
// that differs from claimVerdict — the two answer different questions.
// claimVerdict asks "does this claim COUNT AS COVERAGE", and an honest
// directive is evidence enough. This asks "how many globs has the extractor
// failed to resolve a read for", which a directive does not change: the
// construct is still unresolved, it is just excused. That is a debt worth a
// size, and shrinking it (by narrowing a glob, or by teaching the extractor
// the construct) is a normal commit.
//
// The two halves are reported separately by the caller, because the
// aggregate hides the number that matters: an unresolved glob whose phase
// carries NO directive is the class this phase exists to catch, and it must
// not be averaged into the excused population.
//
// Independent of Coverage and the universe on purpose — like opaqueCount,
// the debt has a size whether or not a currently-tracked repo path happens
// to exercise the claim today.
func UnbackedClaimCount(decls []Declaration, backing map[string]PhaseBacking) int {
	excused, bare := UnbackedClaimSplit(decls, backing)
	return excused + bare
}

// UnbackedClaimSplit is UnbackedClaimCount's two halves: excused counts the
// unresolved globs whose phase carries a lane-reads-opaque directive, bare
// counts the ones that carry nothing. bare is the number this phase exists
// to drive to zero; excused is the debt the US-5 ceiling bounds.
func UnbackedClaimSplit(decls []Declaration, backing map[string]PhaseBacking) (excused, bare int) {
	for _, d := range decls {
		patterns := declarationPatterns(d)
		if len(patterns) == 0 {
			continue
		}
		pb := backing[d.Phase]
		if pb.NoSubject {
			continue
		}
		for _, pat := range patterns {
			if pb.BackedPatterns[pat] {
				continue
			}
			if pb.Opaque {
				excused++
			} else {
				bare++
			}
		}
	}
	return excused, bare
}

// CheckOpaqueCeiling is US-5's ratchet over opaqueCount, the same shape
// scripts/check-refusal-ratchet.sh's own per-file budget uses: a fall (or
// staying level) passes silently, growth past the stored ceiling refuses by
// name — so the escape hatch this phase adds (a claim counts as backed when
// its phase declares lane-reads-opaque) cannot be defeated by declaring
// everything opaque without the debt's size moving somewhere visible.
func CheckOpaqueCeiling(opaqueCount, ceiling int) []Refusal {
	if opaqueCount <= ceiling {
		return nil
	}
	return []Refusal{{
		Subject: "scripts/lib/lane-opaque-ceiling.txt",
		Problem: fmt.Sprintf("%d phase(s) now declare lane-reads-opaque, ceiling is %d", opaqueCount, ceiling),
		Fix:     "lower it (resolve a construct to a literal path, or narrow the read) — or, if the growth is deliberate, re-seed with `go run internal/lane/lanecheck.go --write-opaque-ceiling` and review the diff",
	}}
}

// narrowFindRoots turns a find invocation's roots plus its -path/-name
// filters into the path shapes the find can actually return. It returns nil
// when the expression carries no narrowing filter, so the caller falls back to
// the roots — over-reporting is safe, under-reporting is not.
//
// The composition is deliberately simple: each root is joined with each
// -path pattern (already glob-shaped, so a leading "*/" becomes "**/"), and
// -name patterns become a trailing "/**/<name>" segment. It does not attempt
// glob subsumption; it produces a pattern a human can read next to the
// declaration and see whether the two describe the same set.
func narrowFindRoots(roots, args []string) []string {
	var paths, names []string
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-path", "-ipath":
			paths = append(paths, args[i+1])
		case "-name", "-iname":
			names = append(names, args[i+1])
		}
	}
	if len(paths) == 0 && len(names) == 0 {
		return nil
	}
	joinUnder := func(root, rest string) string {
		root = strings.TrimSuffix(strings.TrimPrefix(root, "./"), "/")
		rest = strings.TrimPrefix(rest, "*/")
		if root == "" || root == "." {
			return "**/" + rest
		}
		return root + "/**/" + rest
	}
	var out []string
	for _, root := range roots {
		switch {
		case len(paths) > 0 && len(names) > 0:
			for _, p := range paths {
				for _, n := range names {
					out = append(out, joinUnder(root, strings.TrimSuffix(p, "*")+"**/"+n))
				}
			}
		case len(paths) > 0:
			for _, p := range paths {
				out = append(out, joinUnder(root, p))
			}
		default:
			for _, n := range names {
				out = append(out, joinUnder(root, n))
			}
		}
	}
	return out
}

// unresolvedCandidate is the sentinel a candidate rule returns when it
// recognises a read but cannot resolve it to a literal path. It exists so
// "I cannot model this" travels the SAME route as "$VAR" — into the
// unresolved list, where lane-reads-opaque is the only way past it — instead
// of returning no candidates, which would be scored as "no read here". That
// distinction is the whole of D-11: a miss the classifier knows about is a
// debt with a size; a miss it does not know about is invisible.
const unresolvedCandidate = "\x00lane-unresolved"

// unmodellableFind reports whether a find expression mixes selection with
// exclusion or alternation (-prune, -not, !, -o). narrowFindRoots treats every
// -path/-name as a SELECTOR; against `find . \( -path './.git' -o -name
// node_modules \) -prune -o -path '*/audits/*' ...` that reading is simply
// false, and a classifier that is confidently wrong is worse than one that
// says it does not know.
func unmodellableFind(args []string) bool {
	for _, a := range args {
		switch a {
		case "-prune", "-not", "!", "-o", "-or":
			return true
		}
	}
	return false
}

var (
	findExpressionRe   = regexp.MustCompile(`(^|[;|&(]\s*)find\s`)
	unmodellableFindRe = regexp.MustCompile(`(^|\s)(-prune|-not|-o|-or|!)(\s|$)`)
)

// unmodellableFindLine is unmodellableFind read off the raw source line rather
// than the tokenized argument list, because find's escaped grouping parens
// (`\( … \)`) tokenize to boundary tokens and truncate that list before the
// exclusion arms are reached. Same rule, a source the tokenizer cannot hide.
func unmodellableFindLine(line string) bool {
	return unmodellableFindRe.MatchString(line)
}

// isNotExist reports whether err is a "file does not exist" error, wrapped or
// not. Split out so honestyForMakePhase's presence-gate branch cannot widen
// into "any read error is fine" — a permission error or an I/O failure on a
// script that IS there must still be loud.
func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// recipeGuardsPresence reports whether recipe protects its invocation of
// script behind a `[ -f <script> ]` test — the shape every presence-gated
// private harness gate in this repo's Makefile uses:
//
//	@if [ -f scripts/check-feedback-corpus.sh ]; then \
//	  bash scripts/check-feedback-corpus.sh; \
//	else \
//	  echo "feedback-corpus: skip — ... absent (public checkout)."; \
//	fi
//
// It matches the SPECIFIC script the recipe would run, not merely "some -f
// test appears somewhere in this recipe": a recipe that guards on one file and
// then unconditionally runs another is not presence-gated for the second, and
// treating it as such would restore the silent-skip this function exists to
// keep narrow.
func recipeGuardsPresence(recipe []string, script string) bool {
	needle := "[ -f " + script + " ]"
	for _, line := range recipe {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
