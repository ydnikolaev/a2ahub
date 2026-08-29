package livee2e

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// makefilePath is repo-relative from this package's directory.
const makefilePath = "../../Makefile"

// verifyScriptPath is the cache-owning outer runner used by public validation
// targets. The Makefile keeps the stable target name; this script owns the
// actual Go invocation.
const verifyScriptPath = "../../scripts/verify.sh"

// buildTagDirective is assembled rather than written literally so this file's
// own source can never satisfy the check that looks for it.
const buildTagDirective = "//go:" + "build livee2e"

// preamble returns everything before a Go file's package clause — the only
// place a build constraint may legally appear. Scanning whole files instead
// would let THIS file match on the directive it mentions in its own strings,
// which is how the first version of this guard passed with the tagged file
// deleted.
func preamble(src string) string {
	if strings.HasPrefix(src, "package ") {
		return ""
	}
	if idx := strings.Index(src, "\npackage "); idx >= 0 {
		return src[:idx]
	}
	return src
}

func readMakefile(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read %s: %v", makefilePath, err)
	}
	return strings.Split(string(b), "\n")
}

// liveTaggedInvocation matches a full `go test ./internal/livee2e/...
// -tags=livee2e ...` line wherever it appears in verify.sh. D-5 gives the
// logic tier its own such invocation alongside the live matrix's, so a
// grep for the literal string `live-e2e` can no longer tell the two apart
// (plan D-4/D-5) — this pattern is what lets the checks below read what
// each invocation actually is instead.
var liveTaggedInvocation = regexp.MustCompile(`(?m)^[ \t]*go test \./internal/livee2e/\.\.\.[^\n]*-tags=livee2e[^\n]*$`)

// logicRunFilterArg captures the argument of a `-run` flag regardless of
// whether it is single- or double-quoted, so a filtered invocation written
// with the other quote style is still read by the checks below instead of
// silently falling through the "-run " containment check that classified it
// as filtered in the first place.
var logicRunFilterArg = regexp.MustCompile(`-run (?:'([^']*)'|"([^"]*)")`)

// logicSkipFilterArg is logicRunFilterArg's inverse-polarity twin, and it
// exists because the logic lane stopped being an ALLOWLIST on 2026-08-29.
//
// It used to name four tests in `-run`. The tagged tree declares 77, so every
// tagged test written after that list was authored ran NOWHERE — the build tag
// keeps them out of `go-test`, and the allowlist kept them out of here. The lane
// now says `-skip '^TestLiveMatrix$'`: one name excluded, everything else
// included by construction, which cannot go stale when somebody adds a test.
//
// Both forms answer the SAME question and this file must ask it of both, which
// is the whole reason this variable is not just a copy. The property guarded is
// "the non-live lane cannot select TestLiveMatrix", and the two forms prove it
// with opposite polarity: a `-run` pattern proves it by NOT matching that name,
// a `-skip` pattern by matching it. Reading the flag's spelling instead of its
// effect is what made (g) below red on a lane that had just become STRICTLY
// safer — the same "guard reads a proxy for the fact" defect verify.sh's own
// phase_is_dispatchable comment describes one layer down.
var logicSkipFilterArg = regexp.MustCompile(`-skip (?:'([^']*)'|"([^"]*)")`)

// shellFunctionBody returns the body lines of a top-level bash function
// definition shaped `name() {` ... a lone `}` at column 0 — the formatting
// convention every function in verify.sh already uses. found is false when
// no such function exists, which callers below treat as fatal: silently
// reading zero lines from a renamed function is not the same as the
// function containing nothing.
func shellFunctionBody(lines []string, name string) (body []string, found bool) {
	header := name + "() {"
	started := false
	for _, line := range lines {
		if !started {
			if strings.TrimSpace(line) == header {
				started = true
			}
			continue
		}
		if line == "}" {
			return body, true
		}
		body = append(body, line)
	}
	return nil, false
}

// targetRecipe returns the prerequisite text of `name:` and the recipe lines
// that follow it (tab-indented), which is enough to assert what a target
// depends on and what it actually runs.
func targetRecipe(lines []string, name string) (prereqs string, recipe []string, found bool) {
	for i, line := range lines {
		if !strings.HasPrefix(line, name+":") {
			continue
		}
		prereqs = strings.TrimPrefix(line, name+":")
		if idx := strings.Index(prereqs, "##"); idx >= 0 {
			prereqs = prereqs[:idx]
		}
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "\t") {
				recipe = append(recipe, next)
				continue
			}
			if strings.TrimSpace(next) == "" {
				continue
			}
			break
		}
		return prereqs, recipe, true
	}
	return "", nil, false
}

// TestLiveTierIsNotAMergeGate is AC-962.1. `make check` must neither run nor
// depend on the live tier: it needs network, two credentials and Actions
// latency, so a `check` that reached it would be flaky and expensive on every
// commit.
//
// This is asserted against the Makefile rather than trusted, because the
// failure mode is silent — a live target wired into REPO_GATES does not
// announce itself, it just makes the merge gate start failing for reasons
// that have nothing to do with the diff.
//
// Extended for plan D-4/D-5 (wave W6): `check` now legitimately enters the
// `-tags=livee2e` tree through the logic lane, so (a)-(d)'s literal grep for
// the string `live-e2e` can no longer distinguish "reaches the live matrix"
// from "reaches the logic tier". Blocks (e)-(i) below read the actual
// invocations and assert the semantic property instead: exactly one
// unfiltered invocation exists, it is reachable only from `live` mode, and
// every other invocation's -run filter provably excludes TestLiveMatrix and
// provably includes the logic entry point it claims to run.
func TestLiveTierIsNotAMergeGate(t *testing.T) {
	t.Parallel()
	lines := readMakefile(t)

	// (a) The target must EXIST, or every assertion below is vacuous —
	// "live-e2e is absent from the merge gate" is trivially true when
	// live-e2e is absent from the Makefile entirely.
	_, liveRecipe, ok := targetRecipe(lines, "live-e2e")
	if !ok {
		t.Fatal("no `live-e2e` target in the Makefile — the guards below would pass vacuously")
	}

	// (b) It must enter the live mode of the outer runner, and that mode must
	// run the tier behind its build tag. Without the tag a plain
	// `go test ./...` would compile (and could execute) the live scenarios,
	// which is the separation this whole test defends.
	if !strings.Contains(strings.Join(liveRecipe, "\n"), "scripts/verify.sh live") {
		t.Errorf("`live-e2e` does not enter verify.sh live mode; recipe:\n%s", strings.Join(liveRecipe, "\n"))
	}
	verifyScript, err := os.ReadFile(verifyScriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", verifyScriptPath, err)
	}
	if !strings.Contains(string(verifyScript), "go test ./internal/livee2e/... -tags=livee2e") {
		t.Errorf("verify.sh live mode does not pass -tags=livee2e")
	}

	// (b2) …and something must actually be behind that tag. With no
	// `//go:build livee2e` file, -tags=livee2e is decorative: the target
	// would run only the hermetic tests in this package and exit 0 with no
	// credentials configured — precisely the silent green AC-962.2 forbids.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	tagged := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		b, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatalf("read %s: %v", entry.Name(), readErr)
		}
		// Only the preamble counts. A build constraint must precede the
		// package clause, and scanning the whole file would let THIS test
		// match its own directive literal below — a guard that is satisfied
		// by its own source is no guard at all.
		if strings.Contains(preamble(string(b)), buildTagDirective) {
			tagged = true
			break
		}
	}
	if !tagged {
		t.Errorf("no file carries the %q constraint — the build tag guards nothing and `make live-e2e` would exit 0 unconfigured", buildTagDirective)
	}

	// (c) REPO_GATES is the single list both `check` and `check-validators`
	// consume — the one place a live target would leak into both at once.
	var gates string
	for _, line := range lines {
		if strings.HasPrefix(line, "REPO_GATES") {
			gates = line
			break
		}
	}
	if gates == "" {
		t.Fatal("no REPO_GATES assignment found in the Makefile")
	}
	if strings.Contains(gates, "live-e2e") {
		t.Errorf("live-e2e is in REPO_GATES: %q", gates)
	}

	// (d) …and neither `check` nor `check-validators` may reach it by any
	// other route: not as a prerequisite, not from inside the recipe.
	for _, target := range []string{"check", "check-validators"} {
		prereqs, recipe, found := targetRecipe(lines, target)
		if !found {
			t.Fatalf("no `%s` target in the Makefile", target)
		}
		if strings.Contains(prereqs, "live-e2e") {
			t.Errorf("`%s` lists live-e2e as a prerequisite: %q", target, prereqs)
		}
		if body := strings.Join(recipe, "\n"); strings.Contains(body, "live-e2e") {
			t.Errorf("`%s` recipe references live-e2e:\n%s", target, body)
		}
	}

	// (e) D-5 gives the logic tier its own `-tags=livee2e` invocation, so
	// (a)-(d) above no longer rule out a second unfiltered one sneaking in
	// beside the live matrix's. A second filter-less invocation — anywhere,
	// reachable from anything but the `live` mode — would mean `full` (or
	// the fast inner loop) can now touch a real GitHub credential check on
	// every commit, the exact outcome this whole test exists to prevent.
	verifyText := string(verifyScript)
	verifyLines := strings.Split(verifyText, "\n")
	invocations := liveTaggedInvocation.FindAllString(verifyText, -1)
	if len(invocations) == 0 {
		t.Fatal("no `go test ./internal/livee2e/... -tags=livee2e` invocation found in verify.sh — assertions (e)-(i) below would be vacuous")
	}
	//
	// "Unfiltered" means NEITHER `-run` NOR `-skip`, and the second half was
	// added 2026-08-29 with the lane's move to `-skip`. Counting only `-run`
	// classified the newly widened logic lane as a second UNFILTERED
	// invocation — reporting the safest arrangement this lane has ever had as
	// the precise danger this assertion exists to catch. The question is
	// whether an invocation is CONSTRAINED, not which flag spells it.
	var unfiltered []string
	for _, inv := range invocations {
		if !strings.Contains(inv, "-run ") && !strings.Contains(inv, "-skip ") {
			unfiltered = append(unfiltered, inv)
		}
	}
	if len(unfiltered) != 1 {
		t.Errorf("expected exactly one -tags=livee2e invocation with no -run/-skip filter (the live matrix); found %d: %v", len(unfiltered), unfiltered)
	}

	// (f) That one unfiltered invocation must live inside run_live_tests,
	// and run_live_tests must be reachable from nowhere but the `live` mode
	// branch. A second call site — even one added by accident while wiring
	// a later wave — would give `full` a second, unguarded path to the
	// exact invocation (e) polices.
	liveBody, liveFound := shellFunctionBody(verifyLines, "run_live_tests")
	if !liveFound {
		t.Fatal("no run_live_tests() function in verify.sh")
	}
	if len(unfiltered) == 1 && !strings.Contains(strings.Join(liveBody, "\n"), strings.TrimSpace(unfiltered[0])) {
		t.Errorf("the unfiltered live invocation is not inside run_live_tests():\n%s", unfiltered[0])
	}
	// Counted over CODE only. A comment naming run_live_tests — and the
	// logic lane's own comment does name it, to say why it omits -race —
	// is not a path to it, and a guard that cannot tell the two apart
	// teaches the only lesson available: stop explaining yourself near the
	// thing being guarded. The stripping is deliberately crude (a `#` at
	// the start of a trimmed line) because verify.sh has no trailing-comment
	// convention, and a cleverer parser here would be its own thing to be
	// wrong.
	var codeOnly []string
	for _, line := range verifyLines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		codeOnly = append(codeOnly, line)
	}
	if callSites := regexp.MustCompile(`\brun_live_tests\b`).FindAllString(strings.Join(codeOnly, "\n"), -1); len(callSites) != 2 {
		t.Errorf("run_live_tests is referenced %d times in verify.sh's CODE (want exactly 2: its own definition plus one call site) — an extra reference is a second path to the live matrix's unfiltered invocation", len(callSites))
	}
	// Asked of the comment-STRIPPED text, for the same reason the call-site
	// count above is: P12's lane grammar puts a phase's `# lane-inputs:`
	// declaration directly above the `run_phase` line it describes, which
	// lands it between this `if` and this call. A comment there is not a
	// second path to the live matrix — the property being guarded is that
	// the sole call site sits inside the `live` branch, and that is exactly
	// as true with an explanation above it. Matching raw text made the guard
	// answer a question about formatting instead, and the fix for that is to
	// ask the real question, never to drop the check.
	codeOnlyText := strings.Join(codeOnly, "\n") + "\n"
	if !strings.Contains(codeOnlyText, "if [ \"$MODE\" = live ]; then\n  run_phase live-e2e run_live_tests\n") {
		t.Error("run_live_tests's one call site is not inside `if [ \"$MODE\" = live ]; then` — a rewritten guard that still calls it from elsewhere would defeat this test's purpose while every check above kept passing")
	}

	// (g) At least one FILTERED invocation must exist — the logic lane.
	// Without one, assertions (h) and (i) below hold vacuously over an empty
	// set: a `full` mode that dropped the logic lane entirely would still
	// pass every check in this function.
	//
	// "Filtered" means `-run` OR `-skip`. It meant `-run` alone until
	// 2026-08-29, and that spelling-check is what made this assertion FAIL on
	// a lane that had just been widened from four named tests to the whole
	// tagged tree minus the live matrix — strictly more coverage, and strictly
	// the same safety property. See logicSkipFilterArg's comment.
	var filtered []string
	for _, inv := range invocations {
		if strings.Contains(inv, "-run ") || strings.Contains(inv, "-skip ") {
			filtered = append(filtered, inv)
		}
	}
	if len(filtered) == 0 {
		t.Fatal("no -run- or -skip-filtered -tags=livee2e invocation found in verify.sh — the logic lane is missing")
	}

	// (h)/(i) Every filtered invocation's -run argument is checked by
	// actually compiling it and matching, not by string comparison: a
	// hand-written "the string does not mention TestLiveMatrix" check would
	// pass for a pattern like `Test.*Matrix`, which matches both names and
	// would let `full` run the live matrix with no credentials configured.
	// This walks ALL filtered invocations, not just the first one a future
	// wave might add beside this one: a second filtered lane that widened
	// its own -run to include TestLiveMatrix must red here even though the
	// first lane's filter is still exactly right. At least one filter must
	// also reach TestLogicMatrix — not every filter, since a later lane
	// scoped to a different logic entry point is legitimate — or a typo
	// that matches nothing anywhere would make the whole lane green because
	// it silently ran zero tests.
	sawLogicMatrix := false
	for _, inv := range filtered {
		// Which polarity is this invocation written in? A `-skip` lane
		// EXCLUDES what it names; a `-run` lane SELECTS it. Everything below
		// is the same two questions asked with the sign flipped, never a
		// second copy of the logic.
		flag := "-run"
		m := logicRunFilterArg.FindStringSubmatch(inv)
		if m == nil {
			if m = logicSkipFilterArg.FindStringSubmatch(inv); m != nil {
				flag = "-skip"
			}
		}
		if m == nil {
			t.Errorf("invocation carries \"-run \" or \"-skip \" but no parseable argument (checked both quote styles): %s", inv)
			continue
		}
		arg := m[1]
		if arg == "" {
			arg = m[2]
		}
		filterRe, err := regexp.Compile(arg)
		if err != nil {
			t.Errorf("verify.sh's %s filter %q does not compile as a regexp: %v", flag, arg, err)
			continue
		}
		// The property, in both spellings: this lane must not be able to
		// reach TestLiveMatrix. Compiled and matched rather than string-
		// compared, because a hand-written "does not mention TestLiveMatrix"
		// check passes for `Test.*Matrix`, which selects both names.
		reachesLive := filterRe.MatchString("TestLiveMatrix")
		if flag == "-skip" {
			reachesLive = !reachesLive
		}
		if reachesLive {
			t.Errorf("verify.sh's %s filter %q leaves TestLiveMatrix reachable — `full`/the inner-loop mode could run the live matrix with no credentials configured", flag, arg)
		}
		// And the converse, which is what stops a filter that selects nothing
		// from reporting a green pass over zero tests.
		reachesLogic := filterRe.MatchString("TestLogicMatrix")
		if flag == "-skip" {
			reachesLogic = !reachesLogic
		}
		if reachesLogic {
			sawLogicMatrix = true
		}
	}
	if !sawLogicMatrix {
		t.Error("no filtered -tags=livee2e invocation reaches TestLogicMatrix — the logic lane would build the tagged tree and run nothing, reporting a green pass for a filter that selected zero tests")
	}
}

// TestLiveTimeoutCoversTheRunCeiling holds the one relationship that used to
// exist only as prose: `go test -timeout` must outlast LiveRunCeiling. If it
// does not, the go-test harness kills the process before the run's own
// deadline fires, and the report — the entire product of an hours-long matrix
// against real GitHub — never renders. It runs untagged, under `make check`,
// because a guard that only runs with -tags=livee2e fires at the start of the
// very run it protects, when the window has already been committed.
func TestLiveTimeoutCoversTheRunCeiling(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(verifyScriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", verifyScriptPath, err)
	}
	match := liveTimeoutFlag.FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatalf("no `-timeout <n>m` on the livee2e go test line in %s", verifyScriptPath)
	}
	minutes, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("timeout %q is not an integer count of minutes: %v", match[1], err)
	}
	timeout := time.Duration(minutes) * time.Minute
	if timeout <= LiveRunCeiling {
		t.Fatalf("verify.sh -timeout %s does not outlast LiveRunCeiling %s: go test would kill the run before its own deadline and the report would never render",
			timeout, LiveRunCeiling)
	}
}

// liveTimeoutFlag captures the minute count from the LIVE invocation only.
// Tightened for wave W6 (D-5): verify.sh now carries a second
// `-tags=livee2e` invocation for the logic lane, which also sets its own
// `-timeout`, and both lines start with `go test ./internal/livee2e/...
// -tags=livee2e`. A pattern anchored only that far would match whichever of
// the two invocations happens to appear first in the file — an ordering
// accident, not a property of which one is the live matrix. Anchoring
// through the live invocation's exact flag sequence (`-count=1 -v -timeout`,
// which the -run-filtered logic invocation does not share) makes the match
// depend on what the invocation IS, not where it sits in the file.
var liveTimeoutFlag = regexp.MustCompile(`go test \./internal/livee2e/\.\.\. -tags=livee2e -count=1 -v -timeout (\d+)m`)
