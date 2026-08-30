package lane

import (
	"strings"
	"testing"
)

// --- P1b: proven-empty (spec 09 §11's third capability) --------------------
//
// These tests pin the third arm alongside capabilities (a) (scope-reading
// command vocabulary) and (b) (glob subsumption), already covered above and
// in glob_test.go: a window is PROVEN EMPTY only when EVERY command-start
// token in it is confirmed non-reading — universal quantification, never
// existential (the shortcut spec 09 §11 evaluated and rejected: a recognised
// non-reading command's mere PRESENCE is not proof, because a phase with a
// real hidden read through an unmodelled tool may also happen to invoke git
// or sort).

// TestLineCommandsRecognized is a table-driven unit test of this
// capability's own per-line predicate: does every command-start token on
// this one line belong to a vocabulary this section trusts (D-11's own
// read-shaped commands, a locally-defined function call, an assignment, a
// git subcommand backed by the object database/index/refs, or a bash
// keyword/builtin), or is at least one unaccounted for?
func TestLineCommandsRecognized(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		line           string
		isContinuation bool
		funcNames      map[string]bool
		want           bool
	}{
		{"git ls-files is confirmed non-reading", `git -C "$ROOT" ls-files 'docs/*.md'`, false, nil, true},
		{"git log with --format is confirmed non-reading", `git -C "$ROOT" log -1 --format=%H -- "$newest"`, false, nil, true},
		{"git show is excluded — it reveals real blob content", `git show HEAD:README.md`, false, nil, false},
		{"git with no subcommand at all is unrecognised", `git --version`, false, nil, false},
		{"sort with no operand is a safe stdin filter", `sort |`, false, nil, true},
		{"sort with a file operand genuinely reads it", `sort file.txt`, false, nil, false},
		{"cut with no operand is a safe stdin filter", `cut -f2-`, false, nil, true},
		{"cut with a file operand genuinely reads it", `cut -f2 file.txt`, false, nil, false},
		{"tr never takes a file operand at all — unconditionally safe", `tr -d '\n' <"$in"`, false, nil, true},
		{"mkdir creates a directory, never reads one", `mkdir -p "$tmp/docs"`, false, nil, true},
		{"a bash keyword needs no independent recognition", `if true; then`, false, nil, true},
		{"an unrecognised external tool blocks the proof", `curl https://example.invalid`, false, nil, false},
		{"a case-arm pattern label is not a command", `check) run_check ;;`, false, map[string]bool{"run_check": true}, true},
		{"a locally-defined function call is recognised", `run_check`, false, map[string]bool{"run_check": true}, true},
		{"a bare word matching no local function is not", `run_check`, false, nil, false},
		{"an output redirect target is not a command word", `printf '%s\n' x >"$tmp/out.txt"`, false, nil, true},
		{"an fd-duplication target (>&2) is not a command word", `echo "x" >&2`, false, nil, true},
		{"a here-string target is not a command word", `done <<< "$out"`, false, nil, true},
		{"a continuation line's own first token is never a new command", `internal/ cmd/ schemas/`, true, nil, true},
		{"a nested-quote assignment with nothing risky inside is safe", `ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"`, false, nil, true},
		{"a fused git assignment with a safe subcommand is safe", `anchor="$(git -C "$ROOT" log -1 --format=%H -- "$newest")"`, false, nil, true},
		{"an assignment whose value names a risky command is not waved through", `X="$(grep foo bar.txt)"`, false, nil, false},
		{"an if-guarded grep with a variable operand is not safe", `if grep -q pattern "$FILE"; then`, false, nil, false},
		{"an if-guarded grep with no file operand at all is safe", `if grep -q "pattern only, reads stdin"; then`, false, nil, true},
		{"a negated pipeline's command still needs recognition", `! curl https://example.invalid`, false, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			funcNames := c.funcNames
			if funcNames == nil {
				funcNames = map[string]bool{}
			}
			got := lineCommandsRecognized(c.line, c.isContinuation, funcNames)
			if got != c.want {
				t.Errorf("lineCommandsRecognized(%q) = %v, want %v", c.line, got, c.want)
			}
		})
	}
}

// TestWindowProvenEmptyRequiresUniversalRecognition is the soundness test:
// a window built entirely from recognised non-reading commands is PROVEN
// EMPTY, but adding exactly ONE unrecognised command must keep it
// UNCLASSIFIED, never PROVEN EMPTY. This is the shape spec 09 §11's own
// rejected shortcut ("existential" — a recognised command's mere presence)
// would have gotten wrong, and it is the case that matters most.
func TestWindowProvenEmptyRequiresUniversalRecognition(t *testing.T) {
	t.Parallel()
	allRecognized := []string{
		"#!/usr/bin/env bash",
		`git -C "$ROOT" ls-files 'docs/*.md' |`,
		`  sort |`,
		`  tail -n 1 |`,
		`  cut -f1`,
	}
	contLines := continuationLineSet(allRecognized)
	quoteSkip := multilineQuoteStartLines(allRecognized)
	inHeredoc := map[int]bool{}
	if !windowProvenEmpty(allRecognized, 1, len(allRecognized), contLines, quoteSkip, inHeredoc) {
		t.Fatalf("a window built entirely from recognised non-reading commands must be PROVEN EMPTY")
	}

	withOneUnrecognized := append(append([]string{}, allRecognized...), `curl https://example.invalid/manifest.json`)
	contLines2 := continuationLineSet(withOneUnrecognized)
	quoteSkip2 := multilineQuoteStartLines(withOneUnrecognized)
	if windowProvenEmpty(withOneUnrecognized, 1, len(withOneUnrecognized), contLines2, quoteSkip2, inHeredoc) {
		t.Fatalf("ONE unrecognised command must keep the window UNCLASSIFIED, not PROVEN EMPTY — " +
			"existential presence of a recognised command is not proof of emptiness (spec 09 §11's own rejected shortcut)")
	}
}

// TestMultilineQuoteStartLinesIgnoresCommentApostrophes pins a real defect
// this capability's own first run against the live tree found (and this
// package's `it's`/`gate's own`/`doesn't` prose triggers constantly):
// tracking a bare "'" across the WHOLE file without stopping at an unquoted
// "#" turns every prose apostrophe into a quote-state flip, so a stray one
// two hundred lines above silently marks a real command line as "inside a
// multi-line quote" and exempts it from recognition — a false PROVEN EMPTY
// for scripts/verify.sh's own "projection" phase, before the fix.
func TestMultilineQuoteStartLinesIgnoresCommentApostrophes(t *testing.T) {
	t.Parallel()
	lines := []string{
		`# it's a comment, and so is this one — gate's own reasoning, it doesn't matter`,
		`# another apostrophe: don't, can't, won't`,
		`run_phase projection bash "$ROOT/scripts/check-projection.sh"`,
	}
	got := multilineQuoteStartLines(lines)
	if got[2] {
		t.Fatalf("line 2 (0-based) must not be reported as starting inside a quote — the apostrophes above it are all inside real comments: %v", got)
	}
}

// TestUnbackedClaimSplitSkipsExclusionPatterns pins the fix alongside this
// capability: a "!"-prefixed pattern can never be backed (backedGlobs and
// subsumeUnresolved both skip it by construction — Subsumes/MatchInputs are
// never asked to satisfy an exclusion), so counting one as "unbacked" here
// would be a bookkeeping artefact, not a real debt. Before ProvenEmpty this
// was masked for every phase that also carried NoSubject; ProvenEmpty
// unmasks it the moment NoSubject turns false.
func TestUnbackedClaimSplitSkipsExclusionPatterns(t *testing.T) {
	t.Parallel()
	decls := []Declaration{{
		Phase:  "example",
		Kind:   KindScoped,
		Inputs: []string{"internal/**", "!internal/lane/**", "!**/*_test.go"},
	}}
	backing := map[string]PhaseBacking{
		"example": {ProvenEmpty: true, BackedPatterns: map[string]bool{}},
	}
	excused, bare := UnbackedClaimSplit(decls, backing)
	if excused != 0 || bare != 1 {
		t.Fatalf("excused=%d bare=%d, want excused=0 bare=1 (only internal/** counts — the two \"!\" exclusions can never be backed by construction)", excused, bare)
	}
}

// --- end-to-end via HonestyCheck --------------------------------------------

// releaseNotesFreshnessShapeScript is a condensed, faithful replica of
// scripts/check-release-notes-freshness.sh's own structure — the epic's own
// motivating example (spec 01 §11, spec 09 §0) — carrying every construct
// this capability's own fixes were found against on the live tree: a
// nested-quote ROOT= assignment, a git-subcommand-backed fused assignment
// (anchor=), an inline awk program spanning several physical lines, a bare
// sort/tail/cut pipeline, a multi-line git log invocation with a
// backslash-continued pathspec, an if-guarded test, and a bare call to a
// locally-defined function.
const releaseNotesFreshnessShapeScript = "#!/usr/bin/env bash\n" +
	"# lane-inputs:\n" +
	"#   releasenotes/*.yaml\n" +
	"#   internal/**\n" +
	"#   cmd/**\n" +
	"#   !internal/lane/**\n" +
	"#   !**/*_test.go\n" +
	"set -uo pipefail\n" +
	"ROOT=\"${ROOT:-$(cd \"$(dirname \"${BASH_SOURCE[0]}\")/..\" && pwd)}\"\n" +
	"\n" +
	"latest_notes_file() {\n" +
	"  git -C \"$ROOT\" ls-files 'releasenotes/*.yaml' |\n" +
	"    awk -F'[/.]' '\n" +
	"      $2 ~ /^[0-9]+$/ { print }\n" +
	"    ' |\n" +
	"    sort |\n" +
	"    tail -n 1 |\n" +
	"    cut -f2-\n" +
	"}\n" +
	"\n" +
	"run_check() {\n" +
	"  local newest anchor\n" +
	"  newest=\"$(latest_notes_file)\"\n" +
	"  anchor=\"$(git -C \"$ROOT\" log -1 --format=%H -- \"$newest\")\"\n" +
	"  if [ -z \"$anchor\" ]; then\n" +
	"    echo \"FAIL\" >&2\n" +
	"    return 1\n" +
	"  fi\n" +
	"  git -C \"$ROOT\" log \"$anchor..HEAD\" --format='%H%x09%s' -- \\\n" +
	"    internal/ cmd/ \\\n" +
	"    ':(exclude)internal/lane/' \\\n" +
	"    ':(exclude,glob)**/*_test.go'\n" +
	"}\n" +
	"\n" +
	"run_check\n"

// TestHonestyCheckReleaseNotesFreshnessShapeIsProvenEmptyAndUnbacked is
// AC-1/AC-2's own end-to-end proof: the motivating example is reported
// PROVEN EMPTY, none of its declared patterns are backed, and it is
// therefore a genuine CLAIMED-BUT-UNBACKED claimant (claimVerdict's own
// verdict) rather than a claim this instrument never had the chance to
// judge.
func TestHonestyCheckReleaseNotesFreshnessShapeIsProvenEmptyAndUnbacked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := release-notes-freshness\n\nrelease-notes-freshness:\n\t@bash scripts/check-release-notes-freshness.sh\n")
	writeFixture(t, dir, "scripts/check-release-notes-freshness.sh", releaseNotesFreshnessShapeScript)
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, _, backing, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	for _, r := range refusals {
		if strings.Contains(r.Subject, "check-release-notes-freshness.sh") {
			t.Errorf("no D-9/D-11 refusal was expected against this fixture: %+v", r)
		}
	}

	pb, ok := backing["release-notes-freshness"]
	if !ok {
		t.Fatalf("release-notes-freshness has no backing entry at all")
	}
	if !pb.ProvenEmpty {
		t.Fatalf("release-notes-freshness's window must be PROVEN EMPTY: %+v", pb)
	}
	if pb.NoSubject {
		t.Fatalf("ProvenEmpty must turn NoSubject OFF — a proven-empty window is a real finding, not an absence of one: %+v", pb)
	}
	if pb.Opaque {
		t.Fatalf("this fixture carries no lane-reads-opaque directive: %+v", pb)
	}
	for _, pat := range []string{"internal/**", "cmd/**"} {
		if pb.BackedPatterns[pat] {
			t.Errorf("pattern %q must NOT be backed — nothing in this script reads it: %+v", pat, pb.BackedPatterns)
		}
	}

	// The claimVerdict predicate Coverage/Derive both consult must now
	// call this a genuine CLAIMED-BUT-UNBACKED claim rather than exempting
	// it — the whole point of AC-1.
	d := decls[0]
	for _, dd := range decls {
		if dd.Phase == "release-notes-freshness" {
			d = dd
		}
	}
	claims, backed, _ := claimVerdict(d, "internal/widget/widget.go", backing)
	if !claims {
		t.Fatalf("the fixture's own declared internal/** must claim internal/widget/widget.go")
	}
	if backed {
		t.Fatalf("a proven-empty phase must not be scored backed for a path nothing in it reads")
	}
}

// TestHonestyCheckRealModeledReadStaysBackedUnchanged is the "no
// regression" case §6 of the spec's own testing table asks for: a window
// with a REAL, literal, modelled read is BACKED exactly as it was before
// this capability existed — hadEvidence short-circuits windowProvenEmpty
// entirely (honestyForWindow's own guard), so ProvenEmpty must stay false.
func TestHonestyCheckRealModeledReadStaysBackedUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := readme-lint\n\nreadme-lint:\n\t@bash scripts/check-readme.sh\n")
	writeFixture(t, dir, "scripts/check-readme.sh", "#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\nset -euo pipefail\ngrep -q \"hi\" README.md\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, _, backing, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 0 {
		t.Fatalf("expected no refusals, got %+v", refusals)
	}
	pb := backing["readme-lint"]
	if pb.ProvenEmpty {
		t.Fatalf("a window with a real modelled read must never be ProvenEmpty: %+v", pb)
	}
	if pb.NoSubject {
		t.Fatalf("a window with real evidence must not fall back to NoSubject: %+v", pb)
	}
	if !pb.BackedPatterns["README.md"] {
		t.Errorf("README.md must be backed by its own literal grep read: %+v", pb.BackedPatterns)
	}
}

// TestHonestyCheckIfGuardedVariableReadIsNotProvenEmpty is the soundness
// regression this capability's own build found on the live tree
// (scripts/check-roadmap-release-decisions.sh): an `if grep ... "$file"`
// guarded read is invisible to scanLineForReads' own atCommandStart (spec
// 09 §11's own final paragraph — deliberately not widened there), so
// hadEvidence stays false and this capability alone decides the verdict.
// Recognising "if"/"!" as introducing a real command (this section's own
// widened, LOCAL atStart) closes the gap THIS capability would otherwise
// have opened: without it, a phase that genuinely reads its declared scope
// through an if-guard would be reported PROVEN EMPTY — a false accusation.
func TestHonestyCheckIfGuardedVariableReadIsNotProvenEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := roadmap-release-decisions\n\nroadmap-release-decisions:\n\t@bash scripts/check-roadmap.sh\n")
	writeFixture(t, dir, "scripts/check-roadmap.sh",
		"#!/usr/bin/env bash\n# lane-inputs:\n#   releasenotes/*.yaml\nset -euo pipefail\n"+
			"check_one() {\n  local file=\"$1\"\n"+
			"  if grep -Eq '^ *kind: feat$' \"$file\"; then\n"+
			"    echo \"has a feature\"\n"+
			"  fi\n}\n"+
			"check_one \"$1\"\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, _, backing, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	pb := backing["roadmap-release-decisions"]
	if pb.ProvenEmpty {
		t.Fatalf("an if-guarded grep with a real variable operand must NOT be provable empty — the phase may genuinely read its declared scope through it: %+v", pb)
	}
	if !pb.NoSubject {
		t.Fatalf("scanLineForReads' own if-guard gap is unchanged by this capability — the claim still keeps NoSubject's benefit of the doubt: %+v", pb)
	}
}
