package lane

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// makeTarget is one Makefile target: its header line and the tab-indented
// recipe lines that follow it, up to the next target or a blank line.
type makeTarget struct {
	Name      string
	HeaderIdx int // 0-based line index of "name: ..." (or "name:")
	Recipe    []string
}

// makefileDoc is a parsed Makefile: every target found, plus the REPO_GATES
// list (Makefile:56's ONE list, consumed by both `check` and
// `check-validators`).
type makefileDoc struct {
	Lines     []string
	RepoGates []string
	Targets   map[string]*makeTarget
}

var (
	targetHeaderRe   = regexp.MustCompile(`^([A-Za-z0-9_.-]+):(\s|$)`)
	repoGatesRe      = regexp.MustCompile(`^REPO_GATES\s*:=\s*(.*)$`)
	presenceTestRe   = regexp.MustCompile(`if\s+\[\s+-f\s+(\S+)\s+\]`)
	bashScriptCallRe = regexp.MustCompile(`\bbash\s+(\S+\.sh)\b`)
)

// parseMakefile parses a Makefile's targets and its REPO_GATES assignment.
// It fails loudly if REPO_GATES is absent — a Makefile this package cannot
// find that ONE list in is not a Makefile the derivation can trust at all.
func parseMakefile(content string) (*makefileDoc, error) {
	lines := strings.Split(content, "\n")
	doc := &makefileDoc{Lines: lines, Targets: map[string]*makeTarget{}}
	var cur *makeTarget
	for i, line := range lines {
		if m := repoGatesRe.FindStringSubmatch(line); m != nil {
			doc.RepoGates = strings.Fields(m[1])
			continue
		}
		if strings.HasPrefix(line, "\t") {
			if cur != nil {
				cur.Recipe = append(cur.Recipe, line)
			}
			continue
		}
		if m := targetHeaderRe.FindStringSubmatch(line); m != nil {
			t := &makeTarget{Name: m[1], HeaderIdx: i}
			doc.Targets[m[1]] = t
			cur = t
			continue
		}
		if strings.TrimSpace(line) == "" {
			cur = nil
		}
	}
	if len(doc.RepoGates) == 0 {
		return nil, errors.New("in Makefile: no REPO_GATES assignment found — the corpus has no anchor")
	}
	return doc, nil
}

// presenceGateScript reports the script a target's recipe presence-gates
// itself on ("if [ -f scripts/check-feature-lint.sh ]; then …"), and
// whether it found one at all. A target with no such guard is not
// presence-gated.
func presenceGateScript(recipe []string) (string, bool) {
	for _, line := range recipe {
		if m := presenceTestRe.FindStringSubmatch(line); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// corpusFromMakefile derives the Makefile half of the corpus: every
// REPO_GATES entry, minus any whose recipe presence-gates itself on a
// script absent from this checkout (D-2's third rule — feature-lint,
// epic-drift, skill-citations skip cleanly on a public checkout, and the
// corpus must skip them the same way the Makefile recipe does, or the
// derivation is loud, specific and wrong on every public checkout).
//
// It returns the parsed doc alongside the phases so callers (Load's
// script-header resolution) can reuse it without re-reading the file.
func corpusFromMakefile(root string) ([]Phase, *makefileDoc, error) {
	raw, err := readRepoFile(root, "Makefile")
	if err != nil {
		return nil, nil, fmt.Errorf("read Makefile: %w", err)
	}
	doc, err := parseMakefile(string(raw))
	if err != nil {
		return nil, nil, err
	}
	var phases []Phase
	for _, name := range doc.RepoGates {
		t, ok := doc.Targets[name]
		if !ok {
			return nil, nil, fmt.Errorf("in Makefile: REPO_GATES names %q but no such target exists", name)
		}
		if script, gated := presenceGateScript(t.Recipe); gated {
			if _, err := os.Stat(filepath.Join(root, script)); err != nil {
				continue // presence-gated absent: skip cleanly, same as the recipe does.
			}
		}
		phases = append(phases, Phase{Name: name, Source: fmt.Sprintf("Makefile:%d", t.HeaderIdx+1)})
	}
	// Every OTHER real target is an optional phase. A declaration may name
	// one (web-quality does) and is then reconciled against a target that
	// demonstrably exists; a target without a declaration is simply not a
	// lane phase and is never refused for it. Admitting them by existence
	// rather than by a second hand-kept list is the same rule D-2 applies
	// everywhere else in this package.
	required := map[string]bool{}
	for _, p := range phases {
		required[p.Name] = true
	}
	names := make([]string, 0, len(doc.Targets))
	for name := range doc.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if required[name] {
			continue
		}
		t := doc.Targets[name]
		phases = append(phases, Phase{
			Name:     name,
			Source:   fmt.Sprintf("Makefile:%d", t.HeaderIdx+1),
			Optional: true,
		})
	}
	return phases, doc, nil
}

// scriptToPhase builds the REVERSE map from a script path (as invoked,
// "scripts/check-readme.sh") to the corpus phase name whose Makefile
// recipe invokes it — restricted to the phases actually IN corpusPhases.
//
// Restricting to the corpus (rather than scanning every target) matters:
// _harness-check re-invokes ~20 of the same gate scripts with --teeth
// (Makefile:179-215), and space-template-baseline invokes the same script
// as space-template-baseline-check. Mapping across every target makes
// nearly every script ambiguous; mapping only the corpus's own recipes
// keeps the map exactly as wide as what declarations get reconciled
// against.
func scriptToPhase(doc *makefileDoc, corpusPhases []Phase) map[string]string {
	m := map[string]string{}
	for _, p := range corpusPhases {
		// OPTIONAL phases are excluded on purpose. Corpus() admits every real
		// Makefile target as an opt-in phase, and mapping across all of them
		// reintroduces exactly the ambiguity this function was written to
		// avoid: space-template-baseline invokes the same script as
		// space-template-baseline-check, and _harness-check re-invokes ~20 gate
		// scripts with --teeth. Widening this map silently cost
		// roadmap-release-decisions and space-template-baseline-check their
		// script->phase resolution, which then read as "phase has no
		// declaration" — a confident, specific, wrong refusal.
		if p.Optional {
			continue
		}
		t, ok := doc.Targets[p.Name]
		if !ok {
			continue
		}
		for _, line := range t.Recipe {
			if sm := bashScriptCallRe.FindStringSubmatch(line); sm != nil {
				m[sm[1]] = p.Name
				break
			}
		}
	}
	return m
}
