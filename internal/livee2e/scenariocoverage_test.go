package livee2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// scenariocoverage_test.go is P8's AC3 gate (plan 08-paths.plan.md D2, spec
// 08-paths.md AC3): "every scenario in P0-P7 exists as a declared path row".
//
// D2 forbids the obvious shape — a hand-typed scenario list living in this
// file — because that is exactly the defect P8 exists to remove: *"an
// asserted total inside a phase built on derivation would be the epic
// contradicting itself in its last act."* `TransitionRows()` and
// `RestingStates()` are Go enumerators, so a seeded row reds a gate by
// construction; nothing in this repository parses a spec, so the epic's own
// prose scenarios have no such enumerator. D2's decision: "declare a
// scenario-id list in each spec's frontmatter and derive the universe from
// it — one convention, one parser."
//
// **Form chosen, and why not frontmatter.** check-convention.md's own
// `lane-inputs:` block is the named precedent — "the same discipline
// `lane-inputs` already uses": a typed list, in the file that OWNS the
// thing, read by a parser rather than a human summarising prose. That block
// lives inside a `#`/`//` COMMENT because a shell script or a Go doc.go has
// one; a markdown spec does not. None of these specs carry YAML
// frontmatter today (their header is bold key-value prose —
// `08-paths.plan.md`'s own YAML block lives on the PLAN, a different file,
// not the spec), so inventing a frontmatter convention here for three of
// nine files would fork the corpus's own document shape rather than reuse
// it. The direct markdown analogue of "a typed list a parser reads without
// understanding prose" is a fenced code block under a stable heading — see
// parseScenarioIDs.
//
// **Which specs carry a block, and why not all nine.** "Do NOT invent
// scenarios; declare what each spec already claims" (the brief). The only
// machine-recognisable scenario claim already in this corpus is an
// Acceptance-criteria row whose own "How to verify" cell reads literally
// `conformance path` — spec 00 has the phrase once, in a Future-proof
// prose cell, not an AC row; specs 01, 04 and 05 have it in AC rows and
// carry a declaration block naming exactly those rows; specs 02, 03, 06 and
// 07 have NO such row (02's ACs point at "the new test", 03's at schema
// grep gates, 06's at "replay `XS-...`" — a live-artifact id for
// AC4/incidents_test.go, not a path name, and out of this phase's own
// allowlist — and 07's at a docs walkthrough). Declaring an id for any of
// those five would be this file inventing a scenario the spec itself never
// named as one. Reported as a deviation from the plan's own "Consumes"
// section, which lists P4/P5/P6 scenarios and P7's walkthrough — the
// walkthrough and P6's replay ids are real, but neither is expressed in
// the one convention this phase can derive from without guessing.
const conformanceScenarioHeading = "## Conformance scenario ids"

// conformanceEpicSpecsDir is the epic this phase's own catalogue widening
// serves (08-paths.md, "Every scenario in P0-P7"). Relative to this
// package's own directory, matching completeness.go's own
// filepath.Join("..", "fold") precedent.
const conformanceEpicSpecsDir = "../../docs/features/active/agent-exchange-2026-08/specs"

// scenarioIDPattern matches the catalogue's own id convention
// (pathcatalogue_paths.go: "decision-approved-superseded",
// "work-request-supersede-from-blocked") — lowercase, hyphen-separated,
// never a path, a space or an uppercase letter that would silently fail a
// case-sensitive lookup against Path.ID.
var scenarioIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// parseScenarioIDs extracts one spec file's own declared conformance-
// scenario ids from the fenced code block under conformanceScenarioHeading.
// Deliberately pure (a string in, a slice or an error out, no I/O) so it is
// unit-testable against a literal string with no spec file on disk — the
// same split completeness.go's own foldKindSet/missingCoveredKinds already
// use: keep the RULE separate from the disk read that feeds it.
//
// A spec with no heading at all returns (nil, nil) — declaring nothing is
// legal (five of nine specs do). A heading with no fenced block, an
// unterminated fence, an empty fence or a malformed id are all reported
// distinctly, by line, rather than silently treated as "no declaration" —
// the same discipline uncoveredTransitions()'s own empty-reason guard
// applies (pathcoverage_test.go's TestUncoveredTransitionsNamesOnlyRealTriples):
// a malformed declaration must fail LOUDLY, never be read as zero
// scenarios.
func parseScenarioIDs(content string) ([]string, error) {
	lines := strings.Split(content, "\n")

	headingLine := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == conformanceScenarioHeading {
			headingLine = i
			break
		}
	}
	if headingLine == -1 {
		return nil, nil
	}

	i := headingLine + 1
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
		// One optional blockquote explanation paragraph, mirroring the
		// three specs' own shape — skip contiguous ">" lines, then the
		// blank line that follows them, before looking for the fence.
		for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
			i++
		}
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
	}
	if i >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
		return nil, fmt.Errorf("line %d: %q is not followed by a fenced code block", headingLine+1, conformanceScenarioHeading)
	}
	fenceStart := i
	i++

	var ids []string
	closed := false
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			closed = true
			break
		}
		id := strings.TrimSpace(lines[i])
		if id == "" {
			continue
		}
		if !scenarioIDPattern.MatchString(id) {
			return nil, fmt.Errorf("line %d: %q is not a lowercase-hyphenated scenario id", i+1, id)
		}
		ids = append(ids, id)
	}
	if !closed {
		return nil, fmt.Errorf("line %d: fenced block under %q never closes", fenceStart+1, conformanceScenarioHeading)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("line %d: %q block is present but declares no ids — remove the heading instead of leaving it empty", headingLine+1, conformanceScenarioHeading)
	}
	sort.Strings(ids)
	return ids, nil
}

// declaredScenarios reads every "*.md" spec under dir and returns, for each
// file that carries a declaration block, its own parsed scenario ids.
// Enumerates the directory (os.ReadDir) rather than a hardcoded nine-file
// list, matching foldKindSet's own reasoning (completeness.go): a spec
// added later (P9, ...) is picked up without anyone editing this file.
func declaredScenarios(dir string) (map[string][]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("livee2e: declaredScenarios: read %s: %w", dir, err)
	}

	out := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, fmt.Errorf("livee2e: declaredScenarios: read %s: %w", path, rerr)
		}
		ids, perr := parseScenarioIDs(string(raw))
		if perr != nil {
			return nil, fmt.Errorf("livee2e: declaredScenarios: %s: %w", e.Name(), perr)
		}
		if len(ids) > 0 {
			out[e.Name()] = ids
		}
	}
	return out, nil
}

// missingScenarioPaths is AC3's own finding shape (D2 / the brief's point
// 4): a declared scenario id with no ConformancePaths() Path carrying it as
// its own ID is reported by name — file and id — never silently dropped
// and never padded with a fake row to make the count agree.
func missingScenarioPaths(declared map[string][]string, paths []Path) []string {
	known := map[string]bool{}
	for _, p := range paths {
		known[p.ID] = true
	}

	var files []string
	for f := range declared {
		files = append(files, f)
	}
	sort.Strings(files)

	var missing []string
	for _, f := range files {
		for _, id := range declared[f] {
			if !known[id] {
				missing = append(missing, fmt.Sprintf("%s: %s", f, id))
			}
		}
	}
	return missing
}

// duplicateScenarioIDs reports a scenario id declared by more than one
// spec — each id must resolve to exactly one Path, so two specs claiming
// the same id would make missingScenarioPaths' own per-file report
// ambiguous about which spec's claim a later catalogue row actually
// answers.
func duplicateScenarioIDs(declared map[string][]string) []string {
	var files []string
	for f := range declared {
		files = append(files, f)
	}
	sort.Strings(files)

	seenIn := map[string]string{}
	var dups []string
	for _, f := range files {
		for _, id := range declared[f] {
			if prev, ok := seenIn[id]; ok {
				dups = append(dups, fmt.Sprintf("%s declared in both %s and %s", id, prev, f))
				continue
			}
			seenIn[id] = f
		}
	}
	return dups
}

// acceptanceCriteriaHeading and conformancePathMarker anchor the REVERSE
// half of this gate (completeness.go's own unknownDeclaredKinds shape:
// forward — every declared id has a row — AND backward — every spec that
// already claims a scenario actually declares it). Without this half, a
// spec whose "## Conformance scenario ids" heading gets silently renamed
// or deleted (all five OTHER specs legitimately have none, so the
// non-empty guard on the total can't tell that apart from "this file
// dropped its own declaration") would leave that spec's scenarios
// permanently out of the universe, one file at a time, exactly the failure
// D2 exists to prevent.
const acceptanceCriteriaHeading = "## 8. Acceptance criteria"
const conformancePathMarker = "conformance path"

// acceptanceCriteriaClaimsConformancePath reports whether content's own
// "## 8. Acceptance criteria" section (up to the next "## " heading, or
// EOF) contains a conformancePathMarker line. Scoped to that ONE section
// deliberately — spec 00 uses the same two words once, in a Future-proof
// prose cell under §9, which is not an AC row and must not be misread as a
// declaration obligation. Pure, unit-tested without disk (TestScenarioParseIDs'
// own sibling shape).
func acceptanceCriteriaClaimsConformancePath(content string) bool {
	lines := strings.Split(content, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == acceptanceCriteriaHeading {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return false
	}
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			break
		}
		if strings.Contains(lines[i], conformancePathMarker) {
			return true
		}
	}
	return false
}

// specsClaimingConformancePath returns the base file names (sorted) of
// every "*.md" spec under dir whose own §8 table names a `conformance
// path` row — the derivable "must declare" obligation
// acceptanceCriteriaClaimsConformancePath computes per file.
func specsClaimingConformancePath(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("livee2e: specsClaimingConformancePath: read %s: %w", dir, err)
	}

	var claiming []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			return nil, fmt.Errorf("livee2e: specsClaimingConformancePath: read %s: %w", filepath.Join(dir, e.Name()), rerr)
		}
		if acceptanceCriteriaClaimsConformancePath(string(raw)) {
			claiming = append(claiming, e.Name())
		}
	}
	sort.Strings(claiming)
	return claiming, nil
}

// undeclaredConformancePathSpecs is the pure comparison: every file
// specsClaimingConformancePath names that declaredScenarios has no entry
// for at all — a spec that claims a conformance path but declares zero
// ids, caught distinctly from a declared-but-uncovered id (missingScenarioPaths'
// own job).
func undeclaredConformancePathSpecs(claiming []string, declared map[string][]string) []string {
	var missing []string
	for _, f := range claiming {
		if len(declared[f]) == 0 {
			missing = append(missing, f)
		}
	}
	return missing
}

// TestParseScenarioIDs is the parser's own teeth: every shape
// declaredScenarios/parseScenarioIDs must tell apart, pinned without
// touching disk.
func TestScenarioParseIDs(t *testing.T) {
	t.Run("no heading returns nil, nil", func(t *testing.T) {
		ids, err := parseScenarioIDs("# P9 — Something\n\n## 8. Acceptance criteria\n\nnothing here\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ids != nil {
			t.Fatalf("ids = %v, want nil", ids)
		}
	})

	t.Run("well-formed block parses and sorts", func(t *testing.T) {
		content := "## Conformance scenario ids\n\n" +
			"> explanation\n> more explanation\n\n" +
			"```text\n" +
			"zebra-scenario\n" +
			"alpha-scenario\n" +
			"```\n"
		ids, err := parseScenarioIDs(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"alpha-scenario", "zebra-scenario"}
		if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	})

	t.Run("heading with no fence errors", func(t *testing.T) {
		_, err := parseScenarioIDs("## Conformance scenario ids\n\nprose, no fence\n")
		if err == nil {
			t.Fatal("want an error, got nil")
		}
	})

	t.Run("unterminated fence errors", func(t *testing.T) {
		_, err := parseScenarioIDs("## Conformance scenario ids\n\n```text\nsome-id\n")
		if err == nil {
			t.Fatal("want an error, got nil")
		}
	})

	t.Run("empty fence errors", func(t *testing.T) {
		_, err := parseScenarioIDs("## Conformance scenario ids\n\n```text\n```\n")
		if err == nil {
			t.Fatal("want an error, got nil")
		}
	})

	t.Run("malformed id errors", func(t *testing.T) {
		_, err := parseScenarioIDs("## Conformance scenario ids\n\n```text\nNot Lowercase\n```\n")
		if err == nil {
			t.Fatal("want an error, got nil")
		}
	})
}

// TestMissingScenarioPathsNamesFileAndID pins the exact report shape
// (file: id) and proves both directions: a declared id present among Paths
// is silent, a declared id absent is named — never merged into a bare
// count.
func TestScenarioMissingPathsNamesFileAndID(t *testing.T) {
	declared := map[string][]string{
		"01-resting-totality.md": {"has-a-row", "no-row-for-this-one"},
	}
	paths := []Path{{ID: "has-a-row"}}

	got := missingScenarioPaths(declared, paths)
	want := []string{"01-resting-totality.md: no-row-for-this-one"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("missingScenarioPaths = %v, want %v", got, want)
	}
}

// TestMissingScenarioPathsEmptyWhenAllCovered proves the OTHER half: a
// fully-covered declared set reports nothing, so the gate is not
// vacuously red on a correct catalogue.
func TestScenarioMissingPathsEmptyWhenAllCovered(t *testing.T) {
	declared := map[string][]string{
		"01-resting-totality.md": {"has-a-row"},
	}
	paths := []Path{{ID: "has-a-row"}, {ID: "unrelated-row"}}

	got := missingScenarioPaths(declared, paths)
	if len(got) != 0 {
		t.Fatalf("missingScenarioPaths = %v, want empty", got)
	}
}

// TestDuplicateScenarioIDsAcrossSpecs proves a scenario id claimed by two
// specs is caught, naming both files.
func TestScenarioDuplicateIDsAcrossSpecs(t *testing.T) {
	declared := map[string][]string{
		"01-resting-totality.md": {"shared-id"},
		"04-possession.md":       {"shared-id"},
	}
	got := duplicateScenarioIDs(declared)
	if len(got) != 1 {
		t.Fatalf("duplicateScenarioIDs = %v, want exactly one entry", got)
	}
}

// TestScenarioAcceptanceCriteriaClaimsConformancePath pins the reverse
// check's own scoping: a marker inside §8 is a claim, the SAME marker
// inside a LATER section (spec 00's real shape — the phrase sits in §9's
// Future-proof table) is not.
func TestScenarioAcceptanceCriteriaClaimsConformancePath(t *testing.T) {
	t.Run("marker inside section 8 is a claim", func(t *testing.T) {
		content := "## 8. Acceptance criteria\n\n| 1 | US-1 | thing | conformance path |\n\n## 9. Future-proof considerations\n"
		if !acceptanceCriteriaClaimsConformancePath(content) {
			t.Fatal("want true, got false")
		}
	})

	t.Run("marker outside section 8 is not a claim", func(t *testing.T) {
		content := "## 8. Acceptance criteria\n\n| 1 | US-1 | thing | some other check |\n\n## 9. Future-proof considerations\n\n| Roadmap conflicts | a conformance path may only assert what a shipped surface answers |\n"
		if acceptanceCriteriaClaimsConformancePath(content) {
			t.Fatal("want false, got true")
		}
	})

	t.Run("no section 8 at all is not a claim", func(t *testing.T) {
		if acceptanceCriteriaClaimsConformancePath("# Some spec\n\nno AC section here\n") {
			t.Fatal("want false, got true")
		}
	})
}

// TestScenarioUndeclaredConformancePathSpecs proves the comparison itself:
// a claiming file with no declared entry is named; a claiming file WITH a
// declared entry is silent.
func TestScenarioUndeclaredConformancePathSpecs(t *testing.T) {
	claiming := []string{"01-resting-totality.md", "05-declared-nature.md"}
	declared := map[string][]string{
		"01-resting-totality.md": {"some-id"},
	}
	got := undeclaredConformancePathSpecs(claiming, declared)
	want := []string{"05-declared-nature.md"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("undeclaredConformancePathSpecs = %v, want %v", got, want)
	}
}

// TestScenarioClaimingSpecsAllDeclare is the reverse half of AC3, run
// against the real corpus: every spec whose own §8 table claims a
// `conformance path` row must carry a "## Conformance scenario ids" block
// — the check that catches a spec silently losing its own declaration
// (advisor review's finding: the non-empty total guard alone cannot see a
// single file dropping to zero while the corpus-wide total stays
// positive).
func TestScenarioClaimingSpecsAllDeclare(t *testing.T) {
	claiming, err := specsClaimingConformancePath(conformanceEpicSpecsDir)
	if err != nil {
		t.Fatalf("specsClaimingConformancePath(%s): %v", conformanceEpicSpecsDir, err)
	}
	if len(claiming) == 0 {
		t.Fatalf("specsClaimingConformancePath(%s) found no spec claiming a conformance path — the marker text or the §8 heading moved; this parser needs to change with it, not silently report zero", conformanceEpicSpecsDir)
	}

	declared, err := declaredScenarios(conformanceEpicSpecsDir)
	if err != nil {
		t.Fatalf("declaredScenarios(%s): %v", conformanceEpicSpecsDir, err)
	}

	if missing := undeclaredConformancePathSpecs(claiming, declared); len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d spec(s) claim a conformance path in §8 but carry no %q block:\n  %s",
			len(missing), conformanceScenarioHeading, joinLines(missing))
	}
}

// TestDeclaredScenariosParsesRealSpecs is the disk-reading half: every real
// spec under conformanceEpicSpecsDir parses cleanly, and — the guard
// advisor review flagged as the likeliest way this gate goes vacuous — the
// derived universe is NON-EMPTY. A wrong relative path, a renamed heading
// or a moved specs directory would otherwise make every later assertion
// pass over zero scenarios and report nothing wrong, mirroring
// completeness.go's own sawKindType guard ("no `Kind`-typed const
// declaration found ... this parser needs to change with it, not silently
// report zero kinds").
func TestScenarioDeclaredParsesRealSpecs(t *testing.T) {
	declared, err := declaredScenarios(conformanceEpicSpecsDir)
	if err != nil {
		t.Fatalf("declaredScenarios(%s): %v", conformanceEpicSpecsDir, err)
	}

	total := 0
	for _, ids := range declared {
		total += len(ids)
	}
	if total == 0 {
		t.Fatalf("declaredScenarios(%s) found ZERO scenario ids across %d file(s) — either every spec genuinely declares nothing (implausible after this phase) or the parser/path is broken; declared=%v",
			conformanceEpicSpecsDir, len(declared), declared)
	}

	if dups := duplicateScenarioIDs(declared); len(dups) > 0 {
		sort.Strings(dups)
		t.Fatalf("%d scenario id(s) declared by more than one spec:\n  %s", len(dups), joinLines(dups))
	}
}

// TestScenarioIDsAreCoveredByTheCatalogue is AC3's own gate: every scenario
// id any spec under conformanceEpicSpecsDir declares must be some declared
// Path's own ID (ConformancePaths(), pathcatalogue_paths.go). A declared
// scenario with no catalogue row reds this test, naming the exact spec
// file and id — the FINDING this phase reports rather than silently drops.
//
// pathcatalogue_paths.go IS on this wave's own allowlist (2026-08-09,
// unlike the earlier claim this comment used to carry — that claim is now
// false and is corrected here rather than left standing, same discipline
// 05-declared-nature.md's own append-only amendments use). Of the SIX ids
// the gate once named red, THREE are now CLOSED (2026-08-11, P8 wave 31 —
// item 1 below, correcting wave 30B's own genesis-timed attempt at the
// same three) and three remain red (items 2-3). This comment must not
// blur the two: closing an id means a declared Path answers THAT id
// directly (missingScenarioPaths finds it, not merely the underlying
// transition under some other id's name) — the exact conflation the brief
// named and this file's own reasons below keep separate.
//
// A second such path, response-disputed-superseded, existed alongside it
// from 2026-08-08 until 2026-08-09, when it was deleted along with its
// underlying fold row (spec 06's amendment, epic-backlog B8): P8's tagged
// conformance matrix proved no shipped verb ever reached
// {response, disputed, supersede}, and the exit it existed to provide
// already exists one level up, on the parent a dispute reopens. It is
// omitted from the list above because there is nothing left it drives —
// neither the path, the transition, nor the {response, superseded} resting
// state it used to answer for (restingcoverage_test.go) exist any more.
//
// Re-derived per-id reasons, in code, for all six, rather than leaving the
// bare missingScenarioPaths() failure to speak for itself:
//
//  1. CLOSED 2026-08-11 (P8 wave 31, correcting wave 30B's own genesis-timed
//     attempt at the same three ids). 01-resting-totality.md's all THREE
//     declared ids (decision-proposed-withdrawn-by-author-after-approvers-
//     left, decision-proposed-superseded-by-author-after-approvers-left,
//     handoff-submitted-superseded-by-producer-after-receiver-left) now
//     each have their own declared Path (Family 15, pathcatalogue_paths.go)
//     and driver (pathdriver_live.go's runDepartedCounterpartyPaths).
//
//     WAVE 30B TRIED GENESIS DEPARTURE AND WAS WRONG, and this is worth
//     stating plainly rather than quietly overwriting: it reasoned from
//     D-017 (fold/types.go, MembershipView's own doc comment —
//     "authorization is evaluated against the manifest as of the event's
//     commit") and fold.go's authorized()/legalRole() (which resolve as
//     `legalRole(role, env, actorSystem, membership(actorSystem))` — ONLY
//     the ACTING system's own status is ever consulted) that a participant
//     already "left" at space genesis and one that flips to "left"
//     mid-path are "provably identical to every check these three
//     transitions execute." That is CORRECT about the TRANSITION's own
//     authorization and WRONG about the PATH as a whole: running the real
//     tagged conformance matrix (not merely reasoning about the table)
//     showed `a2a submit` refuses, with REF-006/CC-008
//     (internal/validate/authz.go's checkAddressees), an envelope whose
//     `to`/`required_approvers` names an already-`left` system — so a
//     genesis-departed counterparty made the FIRST submission (`propose`/
//     `submit`) itself unreachable, before the transition this class is
//     about was ever attempted. Genesis departure does not construct the
//     scenario; it makes it unconstructible. Two waves (29's correction,
//     then 30B's) reasoned about this; only running the matrix settled it.
//
//     DECISION (the wave's own brief: STEP or path-level precondition),
//     now settled correctly: the departure is a MID-PATH manifest
//     mutation, landing AFTER the create+first-transition step that
//     addresses the counterparty (while it is still active — REF-006 has
//     nothing to refuse) and BEFORE the final transition (so the
//     scenario's own "already gone" claim is true when that transition
//     runs). Still not a pathgrammar.go Step: the manifest edit itself has
//     no fold transition (pathgrammar.go's own Step doc comment — an act
//     with no fold transition is never expressible as one), so the driver
//     performs and asserts it directly, around the declared Steps, exactly
//     as that doc comment already describes for every other transition-
//     free domain act.
//
//     THE PRIMITIVE THAT CLOSED IT: scaffold.go's SetParticipantStatus
//     rewrites ONE already-committed participant row's own status field in
//     place (PatchSpaceParticipants, by contrast, can only ever render a
//     status ONCE, at genesis, against the `participants: []` placeholder
//     `a2a space init` emits — it has no way to rewrite a row it or an
//     earlier path already committed). provision_live.go's
//     SetParticipantStatusMidPath applies it against the space's CURRENT
//     history: clone, edit, commit, push straight back — no PR, no `a2a`
//     CLI verb (none writes this field, in either direction). A DEDICATED
//     harness (pathdriver_live.go's runDepartedCounterpartyPaths) is what
//     actually drives all three, never one of the ordinary round-robin
//     path-space harnesses runConformancePaths splits drivenPathIDs()
//     across — pathdrivability.go's own departedCounterpartyPathIDs() doc
//     comment says why sharing a space would be wrong (every OTHER path in
//     that space still assumes an active counterparty) — but, UNLIKE wave
//     30B, the three DO share that one dedicated harness with EACH OTHER:
//     each driver calls ensureFamily15CounterpartyActive
//     (pathdriver_live.go) before its own first step, restoring the
//     counterparty if an earlier Family 15 path already departed it, so
//     all three stay independent of run order.
//
//     WHAT "closed" verifies: the tagged conformance matrix was run for
//     real this wave, scoped to exactly these three paths — `go test
//     -tags=livee2e ./internal/livee2e/ -run
//     'TestLogicMatrix/departed-counterparty' -count=1` — and is GREEN,
//     including CC-062's own orphaned-counterparty transfer
//     (pending_on/expected_transition) observed against the real,
//     offline-binary-driven cache/threadview surface, not merely derived
//     from fold's own table. Mutation discipline was also checked: with
//     the mid-path SetParticipantStatusMidPath call disabled (the
//     assertion left in place), all three RED on the CC-062 assertion
//     itself (`got waiting_on=[bravo], want [alpha]` /
//     `got expected_transition="approve"/"acknowledge", want ""`) — never
//     on REF-006, confirming the departure is what the assertion actually
//     depends on, not an artifact of step ordering.
//
//  2. 04-possession.md's work-request-attaches-bytes-without-contract-pin-
//     or-fulfillment. The earlier finding here said the gap was purely
//     grammar-shaped and closed with "neither file is on this wave's
//     allowlist" — that clause is now FALSE (pathgrammar.go and
//     pathdriver_live.go both ARE on this wave's allowlist) and the rest is
//     corrected too (2026-08-11): this wave traced the actual product path
//     rather than stopping at the grammar, and the gap survives for a
//     reason the earlier text never checked.
//
//     `a2a attach` (internal/cli/cmd_attach.go) is confirmed offline-
//     drivable and still a genuine "domain act with no fold transition"
//     (pathgrammar.go's own doc comment — the same shape as `contract
//     adopt`; no Step needed for the act itself). The body-content surface
//     the scenario needs is `a2a show <id> --json` -> `.attachments[]`
//     (cache.ShowResult.Attachments, cache/types.go) — it exists and DOES
//     carry `conforms_to` (datapackage.Attachment, attach.go) — but it only
//     reads a COMMITTED artifact; `a2a show` cannot see a still-staged
//     draft (the SAME I9 gap checkFoldedState's own doc comment already
//     names for `folded_state == draft`, pathdriver_live.go). Reaching that
//     surface needs the attach-carrying draft to be SUBMITTED first.
//
//     Submitting it is refused. `a2a attach` mints Ref as the SAME value as
//     Digest (attach.go: "content-addressed ... no second, independent
//     id-minting scheme"), a bare `sha256:...` string. Every funnel
//     Submit() runs checkSubmitAttachmentPossession
//     (internal/space/funnel.go, wired into submitPreparedRequest before
//     any commit/push), which resolves every declared attachments[].ref
//     through ResolveDataPackage (internal/space/data_resolve.go) — whose
//     FIRST step, dataPackagePrefixParsed, requires a `DP-`-prefixed
//     package id; a bare `sha256:` ref fails it immediately
//     (ErrDataPackageInvalidReference), which possession.go turns into
//     ErrAttachmentUnresolvable. The only way to mint a `DP-` id the space
//     can resolve is `a2a data pack --contract <XC-id>@<version> --from
//     <dir>` (cmd_data.go: `--contract` is required, no default) — the
//     EXACT contract pin this scenario asserts the ABSENCE of. So the only
//     route to the body-content surface would introduce the one fact the
//     scenario says must be absent; there is no path to a committed,
//     `a2a show`-readable, unpinned attachment through the real binary
//     today.
//
//     The other candidate, `a2a validate <path>` (cmd_submit.go), IS
//     reachable pre-commit and DOES emit JSON unconditionally
//     ("JSON output is always written to stdout, even on a non-zero exit")
//     — but its wire shape, validate.Result (internal/validate/result.go),
//     carries only Valid/ArtifactID/Violations/ConsistencyFlags, never an
//     `attachments[]` echo. It answers "did this draft pass V1", not "what
//     does the attachment say" — it cannot tell an unpinned attachment from
//     a pinned one or from none at all, so it is the wrong-shaped surface
//     for THIS scenario's own claim, not a substitute for the missing one.
//
//     So: still needs a body-content PredicateKind (pathgrammar.go) reading
//     `.attachments[].conforms_to`, exactly as the earlier text said — but
//     landing it needs either a shipped surface that reads a STILL-STAGED
//     draft (none exists in internal/cli today) or a
//     internal/space/possession.go policy change letting an unpinned
//     attach-only submit land — a product decision, not a catalogue or
//     grammar one, and out of pathgrammar.go/pathdriver_live.go's own reach
//     either way. I9's own rule stands: build the surface, or drop the
//     assertion; this wave drops it, with the reason it actually checked.
//
//  3. 05-declared-nature.md's both ids (contract-published-registered-
//     consumer-yields-named-owner-with-no-new-field-set, contract-binding-
//     none-refused-at-adopt-and-pin). Verified against the product itself,
//     not the harness: `binding` exists ONLY as a schema property on
//     schemas/envelope/v2/work_request.schema.json (per that spec's own
//     2026-08-09 amendment) — grep finds no reader anywhere in
//     internal/cli/cmd_contract.go or internal/validate, so nothing
//     refuses an adopt or a pin on `binding: none` today. `operational[]`
//     greps to zero occurrences in internal/ or schemas/ — the field does
//     not exist. The spec's own amendments name why: the clearing write is
//     unnamed, `binding: none` is independently unreachable at floor
//     0.19.0+ (POL-009/POL-013 both reject a carried-set-less contract),
//     and producer departure is unaddressed. Neither scenario can be
//     driven because the behaviour it asserts has not shipped — a product
//     gap, not a catalogue or harness one; no Path row, however written,
//     could make either pass against the binary as it stands.
//
// undrivenScenario is one declared scenario id that no catalogue row can
// answer YET, with the capability that is missing named.
//
// This is the same mechanism uncoveredTransitions() uses one file over, and
// it is here for the same reason: a gate whose universe is derived meets
// members it cannot reach, and the choice is between a reasoned exemption
// and a gate nobody can ever make green. The discipline that keeps the first
// from becoming the second is that a reason names a MISSING CAPABILITY, not
// a difficulty — and that the reason is re-read when the capability lands.
//
// It is deliberately NOT the same as silence. Every entry below says what
// would have to exist, and two of them say plainly that the product does not
// do the thing yet, which is an unmet acceptance criterion recorded in the
// tracker rather than absorbed here.
type undrivenScenario struct {
	ID     string
	Reason string
}

func undrivenScenarios() []undrivenScenario {
	return []undrivenScenario{
		{
			ID: "work-request-attaches-bytes-without-contract-pin-or-fulfillment",
			Reason: "REASON REPLACED 2026-08-11 — pathgrammar.go and " +
				"pathdriver_live.go are now BOTH on this wave's allowlist (the " +
				"previous reason's closing clause naming them off-limits is false " +
				"and is corrected here). Verified against the product, not just " +
				"the grammar: `a2a show --json` -> .attachments[] " +
				"(cache.ShowResult, cache/types.go) is the real body-content " +
				"surface and it DOES carry conforms_to, but it only reads a " +
				"COMMITTED artifact, and submitting an attach-only draft is " +
				"refused — `a2a attach` mints Ref as the bytes' own sha256 digest " +
				"(attach.go), and internal/space/funnel.go's " +
				"checkSubmitAttachmentPossession resolves every " +
				"attachments[].ref through ResolveDataPackage " +
				"(internal/space/data_resolve.go), whose first step requires a " +
				"`DP-`-prefixed package id — a bare sha256 ref fails it " +
				"immediately (ErrAttachmentUnresolvable). The only way to mint a " +
				"resolvable `DP-` id is `a2a data pack --contract " +
				"<XC-id>@<version>` (cmd_data.go: --contract is required, no " +
				"default) — the exact contract pin this scenario asserts the " +
				"ABSENCE of, so making the body-content surface reachable would " +
				"require introducing the one fact the scenario says must be " +
				"absent. The other pre-commit surface, `a2a validate --json` " +
				"(always emitted, cmd_submit.go), cannot substitute: " +
				"validate.Result (internal/validate/result.go) carries only " +
				"Valid/ArtifactID/Violations/ConsistencyFlags, never an " +
				"attachments[] echo, so it cannot tell an unpinned attachment " +
				"from a pinned one or from none at all. Needs: PredicateKind " +
				"widening for envelope body content PLUS either a shipped " +
				"surface that reads a still-staged draft (none exists) or a " +
				"possession.go policy change letting an unpinned attach-only " +
				"submit land — the second is a product change, not a catalogue " +
				"or grammar one, and out of this wave's allowlist either way.",
		},
		{
			ID: "contract-binding-none-refused-at-adopt-and-pin",
			Reason: "STILL UNDRIVEN, and the reason is REPLACED for the second " +
				"time — the previous one is now FALSE and is worth stating so " +
				"nobody restores it. On 2026-08-09 this said the scenario was " +
				"unreachable by construction because `binding` lives only on " +
				"schemas/envelope/v2/work_request.schema.json, so no contract " +
				"could declare it and no adopt could refuse on it. Both halves " +
				"shipped on 2026-08-10: contract.schema.json carries " +
				"`x_binding` (the `x_` prefix because that schema is deployed), " +
				"and runAdopt refuses a descriptor whose x_binding is the `none` " +
				"sentinel or carries `adoptable: false` — before --major " +
				"resolution, so the flag cannot route around it. " +
				"The remaining blocker is THIS TIER's grammar, not the product: " +
				"`a2a contract adopt` writes the consumer's own consumes.yaml " +
				"dependency, which is not an artifact state change, so it " +
				"corresponds to no transition triple — exactly the entry above " +
				"this one, and exactly why P4's AC3 moved its evidence to " +
				"internal/e2e. The refusal IS proven, through the built " +
				"command, by internal/cli/cmd_contract_test.go's TestContractAdopt " +
				"subtests (both refusing shapes, both adopting shapes, and one " +
				"proving --major cannot bypass it). " +
				"Note the id's own wording is looser than the product: there is " +
				"no separate `pin` verb, because `adopt --major` IS the pin — it " +
				"writes space.Dependency{Contract, Major}. So \"adopt and pin\" " +
				"is one refusable act, not two. " +
				"Needs: PredicateKind widening plus a registry-act driver. " +
				"See specs/05-declared-nature.md's 2026-08-10 amendments.",
		},
		{
			ID: "contract-published-registered-consumer-yields-named-owner-with-no-new-field-set",
			Reason: "REASON REPLACED 2026-08-10 — the previous one is FALSE and is " +
				"stated here so nobody restores it. It said P5's P-1 derivation " +
				"'has not shipped'. It has: internal/pendency's (contract, " +
				"published) row is conditional on a caller-resolved " +
				"OperationalDebtOwed, internal/cache resolves it from the " +
				"registry and the space floor, and the P-1 property — an owner " +
				"derived with EVERY new field absent — has its own test. " +
				"This id is UNDRIVEN HERE FOR A DIFFERENT AND WEAKER REASON, and " +
				"it is NOT the structural miss its sibling above carries: nothing " +
				"about a registry-plus-floor read makes it inexpressible as a " +
				"declared path. What it needs is a catalogue path that stands up " +
				"a published contract WITH a registered consumer in another " +
				"system's consumes.yaml and then asserts the inbox verdict — " +
				"setup this tier does not have a shape for yet. That is P8's " +
				"work, not P5's, and P5's criterion is met without it. " +
				"Needs: a registered-consumer fixture in the path driver.",
		},
	}
}

// TestUndrivenScenariosNameOnlyDeclaredIdsWithReasons refuses a stale or
// bare exemption, exactly as TestUncoveredTransitionsNamesOnlyRealTriples
// does for the transition universe. An exemption that outlives its reason is
// worse than none, because it reads as a decision somebody made on purpose.
func TestUndrivenScenariosNameOnlyDeclaredIdsWithReasons(t *testing.T) {
	declared, err := declaredScenarios(conformanceEpicSpecsDir)
	if err != nil {
		t.Fatalf("declaredScenarios(%s): %v", conformanceEpicSpecsDir, err)
	}
	declaredIDs := map[string]bool{}
	for _, ids := range declared {
		for _, id := range ids {
			declaredIDs[id] = true
		}
	}
	covered := map[string]bool{}
	for _, p := range ConformancePaths() {
		covered[p.ID] = true
	}

	seen := map[string]bool{}
	for _, u := range undrivenScenarios() {
		if strings.TrimSpace(u.Reason) == "" {
			t.Errorf("undrivenScenarios() entry %q carries an empty reason — an exemption without one reads as a decision", u.ID)
		}
		if !declaredIDs[u.ID] {
			t.Errorf("undrivenScenarios() names %q, which no spec declares — a stale exemption", u.ID)
		}
		if covered[u.ID] {
			t.Errorf("%q is BOTH declared undriven and covered by a catalogue row — remove it from undrivenScenarios()", u.ID)
		}
		if seen[u.ID] {
			t.Errorf("undrivenScenarios() names %q twice", u.ID)
		}
		seen[u.ID] = true
	}
}

func TestScenarioIDsAreCoveredByTheCatalogue(t *testing.T) {
	declared, err := declaredScenarios(conformanceEpicSpecsDir)
	if err != nil {
		t.Fatalf("declaredScenarios(%s): %v", conformanceEpicSpecsDir, err)
	}

	exempt := map[string]bool{}
	for _, u := range undrivenScenarios() {
		exempt[u.ID] = true
	}

	var missing []string
	for _, id := range missingScenarioPaths(declared, ConformancePaths()) {
		// The id arrives as "<spec file>: <scenario id>"; the exemption
		// list is keyed on the scenario id alone, because an id is unique
		// across the corpus and a spec being renamed must not silently
		// drop an exemption.
		bare := id
		if i := strings.LastIndex(id, ": "); i >= 0 {
			bare = id[i+2:]
		}
		if exempt[bare] {
			continue
		}
		missing = append(missing, id)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d declared scenario id(s) have no ConformancePaths() row and no reasoned entry in undrivenScenarios() (internal/livee2e/pathcatalogue_paths.go):\n  %s",
			len(missing), joinLines(missing))
	}
}
