package lane

import (
	"fmt"
	"regexp"
	"strings"
)

// verifyCallSite is one `run_phase <name> <command...>` invocation found at
// TOP LEVEL of scripts/verify.sh — not lexically inside a function
// definition. Function-nested call sites are deliberately excluded from
// the corpus: run_repo_gates' `run_phase "$gate" make … "$gate"`
// (verify.sh:222) is the REPO_GATES loop, not a phase named "$gate", and
// run_teeth's `run_phase deliberate-red false` (verify.sh:435) only tests
// the run_phase/telemetry plumbing itself under `verify.sh --teeth` — it
// is not a selectable verification phase and nobody could legitimately
// declare it. One rule (top-level only) is enough to exclude both, so
// D-2's "skip the variable call site" rule falls out of it rather than
// needing a second, separately-stated carve-out.
type verifyCallSite struct {
	Name    string // the phase name, e.g. "go-test"
	Command string // the first token of the invoked command, e.g. "run_go_tests"
	Line    int    // 1-based
}

var (
	funcOpenRe     = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(\)\s*\{\s*$`)
	runPhaseRe     = regexp.MustCompile(`^\s*run_phase\s+(\S+)\s+(\S+)`)
	runPhaseBareRe = regexp.MustCompile(`^\s*run_phase\s+(\S+)`)
)

// scanVerifyPhases walks scripts/verify.sh's lines and returns every
// top-level run_phase call site, plus the set of function names defined in
// the file (used to resolve which function backs a wrapped phase).
//
// Function boundaries are tracked at column 0 only ("name() {" opens,
// a lone "}" at column 0 closes) rather than by brace-depth counting:
// this file's bodies contain "${VAR}", "$((end - start))" and
// "awk '{print $1}'", all of which would miscount a naive brace counter.
// verify.sh defines no nested functions, so single-depth column-0
// tracking is exact for this file's actual shape.
func scanVerifyPhases(lines []string) (callSites []verifyCallSite, functions map[string]bool, err error) {
	functions = map[string]bool{}
	insideFunc := false
	var errs []error
	for i, line := range lines {
		if !insideFunc {
			if m := funcOpenRe.FindStringSubmatch(line); m != nil {
				functions[m[1]] = true
				insideFunc = true
				continue
			}
		} else {
			if line == "}" {
				insideFunc = false
			}
			continue
		}

		m := runPhaseRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		nameTok, cmdTok := m[1], m[2]
		if strings.HasPrefix(nameTok, "$") || strings.HasPrefix(nameTok, `"$`) {
			// A variable-named phase (verify.sh:222's REPO_GATES loop) is
			// not a literal phase at all — D-2's "skip the variable call
			// site" rule.
			continue
		}
		name := strings.Trim(nameTok, `"`)
		if name == "" {
			errs = append(errs, blockErr(i+1, "run_phase call site names an empty phase"))
			continue
		}
		callSites = append(callSites, verifyCallSite{Name: name, Command: strings.Trim(cmdTok, `"`), Line: i + 1})
	}
	if len(errs) > 0 {
		return nil, nil, joinErrors(errs)
	}
	return callSites, functions, nil
}

// corpusFromVerify derives the verify.sh half of the corpus: every
// top-level run_phase call site, deduped by name — logic-e2e is invoked
// from two call sites (verify.sh:484 and :515) and one declaration must
// cover both (D-2's second rule).
//
// It also returns funcToPhase: for every call site whose command token
// names a function actually defined in the file (the "wrapped" phases —
// build-cli/build_cli, go-test/run_go_tests, …), the function name that
// backs it. vet/coverage-policy/harness-teeth invoke a bare command
// ("go vet …", "go run …", "make …") rather than a wrapping function and
// so never appear on the right of funcToPhase — their declaration lives
// directly above the bare run_phase statement instead (D-1).
func corpusFromVerify(root string) ([]Phase, map[string]string, error) {
	raw, err := readRepoFile(root, "scripts", "verify.sh")
	if err != nil {
		return nil, nil, fmt.Errorf("read scripts/verify.sh: %w", err)
	}
	lines := strings.Split(string(raw), "\n")
	callSites, functions, err := scanVerifyPhases(lines)
	if err != nil {
		return nil, nil, err
	}

	var phases []Phase
	seen := map[string]bool{}
	funcToPhase := map[string]string{}
	for _, cs := range callSites {
		if !seen[cs.Name] {
			seen[cs.Name] = true
			phases = append(phases, Phase{Name: cs.Name, Source: fmt.Sprintf("scripts/verify.sh:%d", cs.Line)})
		}
		if functions[cs.Command] {
			if existing, ok := funcToPhase[cs.Command]; ok && existing != cs.Name {
				return nil, nil, fmt.Errorf("scripts/verify.sh:%d: function %s already backs phase %q, cannot also back %q", cs.Line, cs.Command, existing, cs.Name)
			}
			funcToPhase[cs.Command] = cs.Name
		}
	}
	if len(phases) == 0 {
		// A vacuity guard, not a style nicety: insideFunc tracking depends
		// on a lone "}" at column 0 closing every function this file
		// defines. If that ever stops holding — a function closed as
		// "} # done", a "function foo() {" declaration — insideFunc sticks
		// true and every real top-level run_phase site silently vanishes.
		// Corpus would then return the Makefile half only, and Reconcile
		// would report every verify.sh declaration as an orphan, pointing
		// an operator at exactly the wrong file — the same "confident,
		// specific, wrong" failure check-operational-confidence.sh:11-31
		// names, one layer down.
		return nil, nil, fmt.Errorf("scripts/verify.sh: no top-level run_phase call site found — the extractor no longer understands this file's shape")
	}
	return phases, funcToPhase, nil
}
