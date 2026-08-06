package lane

import "strings"

// rawBlock is one `lane-inputs:` block, still anchored to its file's line
// numbers and not yet resolved to a Phase name — Phase resolution depends
// on where the block lives (D-1's four homes), which is context this file
// does not have.
type rawBlock struct {
	Kind   Kind
	Inputs []string
	Reason string
	// Claims is non-empty only when a KindAlways block carries a
	// `lane-claims:` sub-block (D-10) — see Declaration.Claims.
	Claims    []string
	StartLine int // 1-based, the "lane-inputs:" line itself
	EndLine   int // 1-based, the last line the block consumed
	// Following is the first non-blank line after the block ends, trimmed
	// of trailing whitespace only — callers use it to find "the thing the
	// block describes" (a function definition, a bare run_phase statement,
	// a Makefile target line). Empty if the block is the last content in
	// the file.
	Following string
}

// findLaneBlocks scans lines for every `<prefix> lane-inputs:` block and
// parses each one under the D-1 grammar. prefix is "#" for shell/Makefile
// comments, "//" for Go. It never returns a block silently dropped for
// being malformed — a malformed block is a returned error, aggregated
// across every block found so one scan reports every problem, not just
// the first.
func findLaneBlocks(lines []string, prefix string) ([]rawBlock, error) {
	var blocks []rawBlock
	var errs []error
	for i := 0; i < len(lines); i++ {
		content, ok := commentContent(lines[i], prefix)
		if !ok {
			continue
		}
		trimmed := strings.TrimRight(content, " \t")
		switch trimmed {
		case "lane-inputs: ALWAYS", "lane-inputs: NEVER":
			// Deliberately asymmetric with the scoped case below: ALWAYS/
			// NEVER require lane-reason: on the VERY NEXT comment line, no
			// blank-line skip. The scoped case's one-blank-line tolerance
			// exists only for godoc's "blank line, then indented
			// preformatted block" convention around a glob LIST; nothing in
			// the tree writes "# lane-inputs: ALWAYS\n#\n# lane-reason:
			// ...", and D-1's own canonical example puts lane-reason:
			// directly below. If that ever needs the same tolerance, add it
			// here explicitly rather than by falling through.
			kind := KindAlways
			if trimmed == "lane-inputs: NEVER" {
				kind = KindNever
			}
			if i+1 >= len(lines) {
				errs = append(errs, blockErr(i+1, "lane-inputs: %s with no following lane-reason: line", kind))
				continue
			}
			reasonContent, isComment := commentContent(lines[i+1], prefix)
			if !isComment {
				errs = append(errs, blockErr(i+1, "lane-inputs: %s requires a lane-reason: line immediately below it", kind))
				continue
			}
			const reasonPrefix = "lane-reason:"
			if !strings.HasPrefix(strings.TrimSpace(reasonContent), reasonPrefix) {
				errs = append(errs, blockErr(i+1, "lane-inputs: %s requires a lane-reason: line immediately below it, found %q", kind, reasonContent))
				continue
			}
			reasonParts := []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reasonContent), reasonPrefix))}
			end := i + 1
			// A reason may continue onto subsequent indented comment lines
			// — "#   emits `go-test-scoped:<pkg>` from a package's OWN
			// doc.go lane-inputs block;" — the same prose-wrapping every
			// real declaration in this wave uses. Consumption stops at a
			// blank comment line, a non-comment line, or the start of the
			// next lane-inputs block.
			for j := end + 1; j < len(lines); j++ {
				c, isComment := commentContent(lines[j], prefix)
				if !isComment {
					break
				}
				trimmedC := strings.TrimSpace(c)
				if trimmedC == "" {
					break
				}
				fullTrim := strings.TrimRight(c, " \t")
				if fullTrim == "lane-inputs:" || fullTrim == "lane-inputs: ALWAYS" || fullTrim == "lane-inputs: NEVER" || fullTrim == "lane-claims:" {
					// "lane-claims:" (D-10) is not part of the reason — it
					// is its own sub-block, parsed below. Stopping the
					// reason walk here keeps it out of Reason the same way
					// the other three headers already do.
					break
				}
				reasonParts = append(reasonParts, trimmedC)
				end = j
			}
			reason := strings.TrimSpace(strings.Join(reasonParts, " "))
			if reason == "" {
				errs = append(errs, blockErr(i+1, "lane-reason: is present but empty"))
				continue
			}

			// D-10: a "lane-claims:" header immediately after the reason is
			// legal ONLY on ALWAYS — an otherwise-universal gate that also
			// genuinely reads specific paths (epic-drift / docs/status.md).
			// NEVER declarations opt out entirely (live-e2e) and have
			// nothing to claim; a scoped block already claims via Inputs
			// and never reaches this branch — its own rejection lives in
			// the "lane-inputs:" case below.
			var claims []string
			if end+1 < len(lines) {
				if hc, isHeaderComment := commentContent(lines[end+1], prefix); isHeaderComment && strings.TrimRight(hc, " \t") == "lane-claims:" {
					if kind != KindAlways {
						errs = append(errs, blockErr(end+2, "lane-claims: is only legal on an ALWAYS block (this one is %s)", kind))
						continue
					}
					k := end + 2
					for k < len(lines) {
						cc, isClaimComment := commentContent(lines[k], prefix)
						if !isClaimComment {
							break
						}
						cc = strings.TrimSpace(cc)
						if cc == "" {
							break
						}
						claims = append(claims, cc)
						k++
					}
					if len(claims) == 0 {
						errs = append(errs, blockErr(end+2, "lane-claims: header has no glob lines"))
						continue
					}
					end = k - 1
				}
			}

			b := rawBlock{Kind: kind, Reason: reason, Claims: claims, StartLine: i + 1, EndLine: end + 1}
			b.Following = followingLine(lines, end+1)
			blocks = append(blocks, b)
			i = end
		case "lane-inputs:":
			var inputs []string
			j := i + 1
			// A godoc-convention block ("// lane-inputs:" then a blank "//"
			// paragraph break, then a tab-indented preformatted list — the
			// shape godoc renders as a code block) is legal: skip exactly
			// one leading blank comment line before collecting globs. A
			// genuinely empty block still errors below — skipping the
			// blank does not manufacture an input where there is none.
			if j < len(lines) {
				if c, isComment := commentContent(lines[j], prefix); isComment && strings.TrimSpace(c) == "" {
					j++
				}
			}
			sawClaimsHeader := false
			for j < len(lines) {
				c, isComment := commentContent(lines[j], prefix)
				if !isComment {
					break
				}
				c = strings.TrimSpace(c)
				if c == "" || strings.HasPrefix(c, "lane-reason:") {
					break
				}
				if c == "lane-claims:" {
					// D-10: lane-claims: is legal only on an ALWAYS block.
					// This one is scoped — reject it rather than silently
					// swallowing the header text as a glob pattern.
					sawClaimsHeader = true
					break
				}
				inputs = append(inputs, c)
				j++
			}
			if sawClaimsHeader {
				errs = append(errs, blockErr(j+1, "lane-claims: is only legal on an ALWAYS block (this one is scoped)"))
				continue
			}
			if len(inputs) == 0 {
				errs = append(errs, blockErr(i+1, "lane-inputs: block has no glob lines (and is not ALWAYS/NEVER)"))
				continue
			}
			end := j - 1
			b := rawBlock{Kind: KindScoped, Inputs: inputs, StartLine: i + 1, EndLine: end + 1}
			b.Following = followingLine(lines, end+1)
			blocks = append(blocks, b)
			i = end
		default:
			continue
		}
	}
	if len(errs) > 0 {
		return nil, joinErrors(errs)
	}
	return blocks, nil
}

// commentContent strips a comment prefix ("#" or "//") plus one following
// space, and reports whether the line is a comment line at all.
func commentContent(line, prefix string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	rest := trimmed[len(prefix):]
	rest = strings.TrimPrefix(rest, " ")
	return rest, true
}

// followingLine returns the first non-blank line at or after idx (0-based),
// trimmed of trailing whitespace, or "" if none remains.
func followingLine(lines []string, idx int) string {
	for k := idx; k < len(lines); k++ {
		if strings.TrimSpace(lines[k]) == "" {
			continue
		}
		return strings.TrimRight(lines[k], " \t")
	}
	return ""
}
