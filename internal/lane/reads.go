package lane

import (
	"fmt"
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
			if looksPathLike(a) {
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
			unresolved = append(unresolved, UnresolvedRead{Line: lineNo, Text: trimmed})
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

// compareReads checks every literal read this window produced against d's
// own declared coverage — Inputs for KindScoped, Claims for KindAlways
// (D-10; an ALWAYS declaration with no Claims has nothing to compare
// against and is skipped, matching "claims nothing"). KindNever carries no
// Inputs/Claims at all and is skipped the same way live-e2e is exempt from
// Coverage.
func compareReads(relPath string, d Declaration, reads []ReadRef) []Refusal {
	var patterns []string
	switch d.Kind {
	case KindScoped:
		patterns = d.Inputs
	case KindAlways:
		if len(d.Claims) == 0 {
			return nil
		}
		patterns = d.Claims
	case KindNever:
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
func honestyForWindow(relPath string, allLines []string, startLine, endLine int, isGo bool, d Declaration) (refusals []Refusal, opaque bool, err error) {
	if startLine < 1 || endLine < startLine || endLine > len(allLines) {
		return nil, false, fmt.Errorf("honesty check for %q: invalid scan window [%d,%d] in %s (file has %d lines)", d.Phase, startLine, endLine, relPath, len(allLines))
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
		}
		reads = append(reads, r...)
		unresolved = append(unresolved, u...)
	}

	declared, _, oerr := findOpaqueDirective(allLines, startLine, endLine, prefix)
	if oerr != nil {
		return nil, false, fmt.Errorf("%s: %w", relPath, oerr)
	}

	refusals = append(refusals, compareReads(relPath, d, reads)...)
	if len(unresolved) > 0 {
		if declared {
			opaque = true
		} else {
			refusals = append(refusals, unresolvedRefusals(relPath, d, unresolved)...)
		}
	}
	return refusals, opaque, nil
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
func honestyForMakePhase(root string, doc *makefileDoc, d Declaration) (refusals []Refusal, opaque bool, err error) {
	t, ok := doc.Targets[d.Phase]
	if !ok {
		return nil, false, nil
	}
	if script, sok := scriptForRecipe(t.Recipe); sok {
		raw, rerr := readRepoFile(root, filepath.FromSlash(script))
		if rerr != nil {
			return nil, false, fmt.Errorf("honesty check for %q: read %s: %w", d.Phase, script, rerr)
		}
		lines := strings.Split(string(raw), "\n")
		return honestyForWindow(script, lines, 1, len(lines), false, d)
	}

	startLine, sok := parseSourceLine(d.Source)
	if !sok {
		return nil, false, fmt.Errorf("honesty check for %q: cannot parse declaration source %q", d.Phase, d.Source)
	}
	endLine := t.HeaderIdx + 1 + len(t.Recipe)
	return honestyForWindow("Makefile", doc.Lines, startLine, endLine, false, d)
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
func honestyForVerifyPhase(root string, lines []string, p Phase, d Declaration) (refusals []Refusal, opaque bool, err error) {
	startLine, sok := parseSourceLine(d.Source)
	if !sok {
		return nil, false, fmt.Errorf("honesty check for %q: cannot parse declaration source %q", d.Phase, d.Source)
	}

	blocks, berr := findLaneBlocks(lines, "#")
	if berr != nil {
		return nil, false, fmt.Errorf("honesty check for %q: scripts/verify.sh: %w", d.Phase, berr)
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
		return nil, false, fmt.Errorf("honesty check for %q: no lane-inputs block starts at scripts/verify.sh:%d", d.Phase, startLine)
	}

	if m := funcOpenRe.FindStringSubmatch(following); m != nil {
		_, fnEnd, fok := verifyFunctionBodyLines(lines, m[1])
		if !fok {
			return nil, false, fmt.Errorf("honesty check for %q: function %s() not found in scripts/verify.sh", d.Phase, m[1])
		}
		return honestyForWindow("scripts/verify.sh", lines, startLine, fnEnd, false, d)
	}

	callLine, cok := parseSourceLine(p.Source)
	if !cok {
		return nil, false, fmt.Errorf("honesty check for %q: cannot parse call site %q", d.Phase, p.Source)
	}
	refusals, opaque, err = honestyForWindow("scripts/verify.sh", lines, startLine, callLine, false, d)
	if err != nil {
		return nil, false, err
	}

	if callLine-1 >= 0 && callLine-1 < len(lines) {
		if m := goRunFileRe.FindStringSubmatch(lines[callLine-1]); m != nil {
			goRefusals, goOpaque, gerr := honestyForGoFile(root, m[1], d)
			if gerr != nil {
				return nil, false, gerr
			}
			refusals = append(refusals, goRefusals...)
			opaque = opaque || goOpaque
		}
	}
	return refusals, opaque, nil
}

// honestyForGoFile is D-11's Go arm: scan one `go run <file>.go`-named
// file (whole-file, its own lane-reads-opaque if it needs one) for
// os.ReadFile/os.Open.
func honestyForGoFile(root, relPath string, d Declaration) (refusals []Refusal, opaque bool, err error) {
	raw, rerr := readRepoFile(root, filepath.FromSlash(relPath))
	if rerr != nil {
		return nil, false, fmt.Errorf("honesty check for %q: read %s: %w", d.Phase, relPath, rerr)
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
// opaqueCount is the running total of phases whose backing script(s)
// needed a lane-reads-opaque line to pass — D-11's "the number is the
// point": a silently-unclassified read is invisible, a printed count is a
// debt with a size.
//
// A `go-test-scoped:./pkg/...` declaration is out of this deliverable's
// contract (per-gate SCRIPT, not per-package tests) and is never reached
// here — it has no entry in makePhases/verifyPhases at all.
func HonestyCheck(root string, decls []Declaration) (refusals []Refusal, opaqueCount int, err error) {
	byPhase := map[string]Declaration{}
	for _, d := range decls {
		byPhase[d.Phase] = d
	}

	makePhases, makeDoc, merr := corpusFromMakefile(root)
	if merr != nil {
		return nil, 0, merr
	}
	verifyPhases, _, verr := corpusFromVerify(root)
	if verr != nil {
		return nil, 0, verr
	}
	verifyLines, rerr := readVerifyLines(root)
	if rerr != nil {
		return nil, 0, rerr
	}

	for _, p := range makePhases {
		d, ok := byPhase[p.Name]
		if !ok {
			continue // Reconcile already reports the missing declaration
		}
		r, phaseOpaque, herr := honestyForMakePhase(root, makeDoc, d)
		if herr != nil {
			return nil, 0, herr
		}
		refusals = append(refusals, r...)
		if phaseOpaque {
			opaqueCount++
		}
	}

	for _, p := range verifyPhases {
		d, ok := byPhase[p.Name]
		if !ok {
			continue
		}
		r, phaseOpaque, herr := honestyForVerifyPhase(root, verifyLines, p, d)
		if herr != nil {
			return nil, 0, herr
		}
		refusals = append(refusals, r...)
		if phaseOpaque {
			opaqueCount++
		}
	}

	return refusals, opaqueCount, nil
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
