package lane

import (
	"reflect"
	"strings"
	"testing"
)

// TestHonestyCheckMakefileRecipeToMultiPhaseCorpusScopesPerPhase pins the
// fix for the defect this brief exists to close: a Makefile-recipe phase
// whose recipe shells to a MULTI-PHASE corpus script (scripts/verify.sh —
// here, its testdata analogue carrying two lane-inputs blocks, one per
// wrapped phase) must not credit that phase with the WHOLE file's reads.
// Before the fix, honestyForMakePhase scanned [1, len(lines)] for every
// such phase, so ANY lane-reads-opaque line anywhere in the file absolved
// EVERY unresolved construct in the file for EVERY phase reached through
// the Makefile — phase-a's own opaque directive would silently cover
// phase-b's genuinely undeclared, undirected read too.
//
// testdata/teeth-multiphase-recipe/ mirrors the real corpus's
// live-e2e/logic-e2e/harness-check shape: phase-a and phase-b are both
// REPO_GATES targets whose recipe is `bash scripts/verify.sh <mode>`, and
// scripts/verify.sh backs BOTH via its own per-phase lane-inputs blocks
// (D-1's verify.sh home) — so each phase's REAL window comes from
// honestyForVerifyPhase (the verifyPhases loop), not from the make-recipe
// arm's now-disabled whole-file fallback. phase-a's own window covers an
// unresolved read with its own lane-reads-opaque line (legitimate); phase-b's
// own window carries a DIFFERENT unresolved read with NO opaque line of its
// own — the refusal must name phase-b specifically, and phase-a's directive
// must not leak into it.
func TestHonestyCheckMakefileRecipeToMultiPhaseCorpusScopesPerPhase(t *testing.T) {
	const dir = "testdata/teeth-multiphase-recipe"

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, opaque, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}

	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want exactly 1 (phase-b's own undeclared, undirected read): %+v", len(refusals), refusals)
	}
	r := refusals[0]
	if !strings.Contains(r.Problem, `phase "phase-b"`) {
		t.Errorf("refusal does not name phase-b: %+v", r)
	}
	if strings.Contains(r.Problem, `phase "phase-a"`) {
		t.Errorf("refusal must not name phase-a — its own opaque directive must not leak into phase-b's verdict: %+v", r)
	}
	if r.Subject != "scripts/verify.sh:20" {
		t.Errorf("Subject = %q, want scripts/verify.sh:20 (phase-b's own cat \"$OTHER_VAR\" line)", r.Subject)
	}

	// Exactly ONE phase (phase-a) needed lane-reads-opaque to pass — the
	// debt has a size, and it must not be inflated by scanning phase-b's
	// window (or the make-recipe arm's now-skipped whole-file fallback)
	// twice against the same declaration.
	if opaque != 1 {
		t.Errorf("opaque = %d, want 1 (only phase-a's own directive)", opaque)
	}
}

func TestTokenizeShellLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"simple", `grep -q foo bar.txt`, []string{"grep", "-q", "foo", "bar.txt"}},
		{"double-quoted-var", `grep -q "$X" "bar.txt"`, []string{"grep", "-q", "$X", "bar.txt"}},
		{"single-quoted-literal-dollar", `sed 's/$/x/' file`, []string{"sed", "s/$/x/", "file"}},
		{"escaped-paren", `find . \( -path './.git' -o -name node_modules \) -prune`, []string{"find", ".", "(", "-path", "./.git", "-o", "-name", "node_modules", ")", "-prune"}},
		{"operators", `a && b || c ; d`, []string{"a", "&&", "b", "||", "c", ";", "d"}},
		{"redirect", `wc -l <"$README_PATH"`, []string{"wc", "-l", "<", "$README_PATH"}},
		{"command-sub", `x=$(cat "$file")`, []string{"x=$", "(", "cat", "$file", ")"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tokenizeShellLine(c.line)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("tokenizeShellLine(%q) = %#v, want %#v", c.line, got, c.want)
			}
		})
	}
}

func TestScanLineForReadsLiteralGrepFileArg(t *testing.T) {
	reads, unresolved := scanLineForReads(`grep -q "quick start" README.md`, 7)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	if len(reads) != 1 || reads[0].Path != "README.md" || reads[0].Line != 7 {
		t.Fatalf("reads = %+v", reads)
	}
}

func TestScanLineForReadsGrepPatternOnlyNoFileArg(t *testing.T) {
	// grep's ONLY non-flag token is the pattern (reading stdin from a
	// pipe) — there is no file argument, so nothing should be classified.
	reads, unresolved := scanLineForReads(`definition_ids "$plan_root" | grep -c . || true`, 3)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("expected no candidates for a pattern-only grep, got reads=%+v unresolved=%+v", reads, unresolved)
	}
}

func TestScanLineForReadsVariableArgIsUnresolved(t *testing.T) {
	reads, unresolved := scanLineForReads(`grep -q "\b${evidence}\b" "$plan_root/13-testing.md"`, 76)
	if len(reads) != 0 {
		t.Fatalf("expected no literal reads, got %+v", reads)
	}
	if len(unresolved) != 1 || unresolved[0].Line != 76 {
		t.Fatalf("unresolved = %+v", unresolved)
	}
}

func TestScanLineForReadsHeadTailCountArgumentIsNotAPath(t *testing.T) {
	reads, unresolved := scanLineForReads(`tail -n 1`, 5)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("tail -n 1 has no file operand — reads=%+v unresolved=%+v", reads, unresolved)
	}

	reads, unresolved = scanLineForReads(`tail -n "$TELEMETRY_LIMIT" "$telemetry"`, 6)
	if len(reads) != 0 {
		t.Fatalf("expected no literal reads, got %+v", reads)
	}
	if len(unresolved) != 1 {
		t.Fatalf("expected exactly one unresolved read (the file operand, not the count), got %+v", unresolved)
	}
}

func TestScanLineForReadsRedirectTarget(t *testing.T) {
	reads, unresolved := scanLineForReads(`wc -l <README.md`, 20)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	if len(reads) != 1 || reads[0].Path != "README.md" {
		t.Fatalf("reads = %+v", reads)
	}

	_, unresolved = scanLineForReads(`wc -l <"$README_PATH"`, 21)
	if len(unresolved) != 1 {
		t.Fatalf("variable redirect target should be unresolved, got %+v", unresolved)
	}
}

func TestScanLineForReadsHeredocIsNotARedirectRead(t *testing.T) {
	reads, unresolved := scanLineForReads(`cat <<EOF`, 1)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("heredoc target is a delimiter word, not a file — reads=%+v unresolved=%+v", reads, unresolved)
	}
}

// A find that NARROWS by -name/-path reads the intersection, not the root:
// `find docs/a -name '*.md'` cannot be flipped by docs/a/binary.png. Reporting
// the bare root would force this gate's author to declare a tree the gate does
// not read, and the only way to satisfy that is a declaration wider than the
// truth — the failure this phase exists to stop. An earlier revision of this
// test asserted the bare roots; it was encoding the extractor's imprecision.
func TestScanLineForReadsFindMultipleRoots(t *testing.T) {
	reads, unresolved := scanLineForReads(`find docs/a docs/b -type f -name '*.md'`, 1)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	want := []string{"docs/a/**/*.md", "docs/b/**/*.md"}
	if len(reads) != len(want) {
		t.Fatalf("reads = %+v, want %v", reads, want)
	}
	for i, w := range want {
		if reads[i].Path != w {
			t.Errorf("reads[%d] = %q, want %q", i, reads[i].Path, w)
		}
	}
}

func TestScanLineForReadsCommandNotAtStartIsIgnored(t *testing.T) {
	// "cat" appears as plain text in a comment/echo, never in command
	// position — must not be misread as an invocation.
	reads, unresolved := scanLineForReads(`echo "please cat the log"`, 1)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("reads=%+v unresolved=%+v, want none", reads, unresolved)
	}
}

func TestScanLineForReadsSedDashFFileOperand(t *testing.T) {
	reads, unresolved := scanLineForReads(`sed -f transform.sed data.txt`, 4)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	want := []string{"transform.sed", "data.txt"}
	if len(reads) != len(want) {
		t.Fatalf("reads = %+v, want %v", reads, want)
	}
	for i, w := range want {
		if reads[i].Path != w {
			t.Errorf("reads[%d] = %q, want %q", i, reads[i].Path, w)
		}
	}
}

func TestScanLineForReadsAwkOnlyDashF(t *testing.T) {
	reads, _ := scanLineForReads(`awk -f program.awk data.txt`, 1)
	if len(reads) != 1 || reads[0].Path != "program.awk" {
		t.Fatalf("awk without -f data operands should not be classified: reads = %+v", reads)
	}
	reads, unresolved := scanLineForReads(`awk '{print $1}' data.txt`, 2)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("plain awk (no -f) is out of D-11's contract on purpose: reads=%+v unresolved=%+v", reads, unresolved)
	}
}

func TestScanGoLineForReadsLiteral(t *testing.T) {
	reads, unresolved := scanGoLineForReads(`raw, err := os.ReadFile("cc-coverage.yaml")`, 9)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	if len(reads) != 1 || reads[0].Path != "cc-coverage.yaml" {
		t.Fatalf("reads = %+v", reads)
	}
}

func TestScanGoLineForReadsVariable(t *testing.T) {
	reads, unresolved := scanGoLineForReads(`f, err := os.Open(profilePath)`, 57)
	if len(reads) != 0 {
		t.Fatalf("unexpected reads: %+v", reads)
	}
	if len(unresolved) != 1 || unresolved[0].Line != 57 {
		t.Fatalf("unresolved = %+v", unresolved)
	}
}

func TestScanLineForReadsFdRedirectNumberIsNotAPath(t *testing.T) {
	// "2>/dev/null" glued after a grep call: the "2" belongs to the
	// redirect operator, not to grep's argument list.
	reads, unresolved := scanLineForReads(`grep -rnE 'uses: +[^ ]+@' .github/workflows 2>/dev/null`, 4)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	if len(reads) != 1 || reads[0].Path != ".github/workflows" {
		t.Fatalf("reads = %+v, want exactly [.github/workflows]", reads)
	}
}

func TestScanLineForReadsCommentPlaceholderIsNotARead(t *testing.T) {
	// Doc-comment placeholder syntax ("`a2a <verb>[ <sub>]`") tokenizes
	// its "<...>" the same way a real redirect does — "verb"/"sub" must
	// not be misread as files.
	reads, unresolved := scanLineForReads("# extract every backtick `a2a <verb>[ <sub>]` citation", 80)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("reads=%+v unresolved=%+v, want none", reads, unresolved)
	}
}

func TestScanLineForReadsSentenceEndingPeriodIsNotSourceCommand(t *testing.T) {
	// A comment sentence ending "...). The outer..." tokenizes "." as its
	// own token sitting right after a ")" boundary — command position for
	// the "." (source) alias — but "The" is prose, not a path.
	reads, unresolved := scanLineForReads("# generated from (`a2a __catalog`). The outer verification runner supplies the", 46)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("reads=%+v unresolved=%+v, want none", reads, unresolved)
	}
}

func TestScanLineForReadsPossessivePathInProseIsNotSourceCommand(t *testing.T) {
	// The apostrophe in "internal/space's" opens a single quote, so the
	// tokenizer folds the rest of the line into one token that contains a
	// "/" — path-shaped by looksPathLike, prose by every other measure.
	// Watched failing as a live refusal against scripts/check-error-codes.sh:238
	// ("phase error-codes reads internal/spaces CarriedFinding is a THIRD").
	reads, unresolved := scanLineForReads("  # qualifier. internal/space's CarriedFinding is a THIRD", 238)
	if len(reads) != 0 || len(unresolved) != 0 {
		t.Fatalf("reads=%+v unresolved=%+v, want none", reads, unresolved)
	}
}

func TestScanLineForReadsRealDotSourceStillDetected(t *testing.T) {
	reads, unresolved := scanLineForReads(`. ./scripts/lib/common.sh`, 1)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	if len(reads) != 1 || reads[0].Path != "scripts/lib/common.sh" {
		t.Fatalf("reads = %+v, want [scripts/lib/common.sh] (the ./ prefix stripped)", reads)
	}
}

func TestScanLineForReadsLineContinuationBackslashIsNotAPath(t *testing.T) {
	reads, unresolved := scanLineForReads(`if printf '%s\n' "$row_names" | grep -qxF "$cmd" \`, 113)
	if len(reads) != 0 {
		t.Fatalf("a trailing line-continuation backslash must not read as a path, got %+v", reads)
	}
	// "$cmd" is grep's single non-flag token (the pattern) — no file
	// argument is present on this physical line at all.
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
}

func TestHeredocBodyLinesSkipsEmbeddedNonShellContent(t *testing.T) {
	lines := []string{
		`cat >"$ANALYZER_DIR/main.go" <<'GO'`,
		`package main`,
		`func main() { x := 0; find(x) }`, // would misread as shell if scanned
		`GO`,
		`echo done`,
	}
	skip := heredocBodyLines(lines)
	for i := 1; i <= 3; i++ {
		if !skip[i] {
			t.Errorf("line %d (0-based) should be inside the heredoc body (through its terminator)", i)
		}
	}
	if skip[0] || skip[4] {
		t.Errorf("the opener and the trailing line must NOT be skipped: %+v", skip)
	}
}

func TestHonestyCheckSkipsHeredocEmbeddedContent(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := readme-lint\n\nreadme-lint:\n\t@bash scripts/check-readme.sh\n")
	writeFixture(t, dir, "scripts/check-readme.sh",
		"#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\nset -euo pipefail\n"+
			"grep -q hi README.md\n"+
			"cat >\"$TMP/main.go\" <<'GO'\n"+
			"package main\n"+
			"func main() { if x == 0 { find(y) } }\n"+
			"GO\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, opaque, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 0 {
		t.Fatalf("heredoc-embedded Go source must not be scanned as shell, got %+v", refusals)
	}
	if opaque != 0 {
		t.Fatalf("opaque = %d, want 0", opaque)
	}
}

// minimalVerifySh gives HonestyCheck fixtures the one thing
// corpusFromVerify hard-requires (a top-level run_phase call site) without
// pulling in a second gate's worth of unrelated declarations.
const minimalVerifySh = "#!/usr/bin/env bash\nset -euo pipefail\n\nrun_phase() { local gate=\"$1\"; shift; \"$@\"; }\n\n" +
	"# lane-inputs:\n#   **/*.go\n#   go.mod\nrun_gofmt() {\n  gofmt -l .\n}\n\nrun_phase gofmt run_gofmt\n"

// writeMinimalGate gives a fixture ONE clean, self-contained Makefile gate
// (readme-lint) so tests that only care about scripts/verify.sh's half of
// the corpus do not also have to satisfy corpusFromMakefile's non-empty
// REPO_GATES requirement by hand each time.
func writeMinimalGate(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, dir, "Makefile", "REPO_GATES := readme-lint\n\nreadme-lint:\n\t@bash scripts/check-readme.sh\n")
	writeFixture(t, dir, "scripts/check-readme.sh", "#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\nset -euo pipefail\ngrep -q hi README.md\n")
}

func TestHonestyCheckCleanScriptPasses(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := readme-lint\n\nreadme-lint:\n\t@bash scripts/check-readme.sh\n")
	writeFixture(t, dir, "scripts/check-readme.sh", "#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\nset -euo pipefail\ngrep -q \"hi\" README.md\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, opaque, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 0 {
		t.Fatalf("expected no refusals, got %+v", refusals)
	}
	if opaque != 0 {
		t.Fatalf("opaque = %d, want 0", opaque)
	}
}

func TestHonestyCheckUndeclaredLiteralReadIsADefect(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := readme-lint\n\nreadme-lint:\n\t@bash scripts/check-readme.sh\n")
	writeFixture(t, dir, "scripts/check-readme.sh", "#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\nset -euo pipefail\ngrep -q \"hi\" README.md\ncat docs/extra.md\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, _, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if !strings.Contains(refusals[0].Problem, "docs/extra.md") {
		t.Errorf("refusal does not name the undeclared path: %+v", refusals[0])
	}
	if refusals[0].Subject != "scripts/check-readme.sh:6" {
		t.Errorf("Subject = %q, want scripts/check-readme.sh:6", refusals[0].Subject)
	}
}

func TestHonestyCheckUnresolvedWithoutOpaqueRefuses(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := readme-lint\n\nreadme-lint:\n\t@bash scripts/check-readme.sh\n")
	writeFixture(t, dir, "scripts/check-readme.sh", "#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\nset -euo pipefail\ngrep -q \"hi\" README.md\ngrep -q \"x\" \"$EXTRA_PATH\"\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, opaque, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if !strings.Contains(refusals[0].Subject, "scripts/check-readme.sh:6") {
		t.Errorf("refusal does not name script:line: %+v", refusals[0])
	}
	if opaque != 0 {
		t.Errorf("opaque = %d, want 0 (no lane-reads-opaque line present)", opaque)
	}
}

func TestHonestyCheckUnresolvedWithOpaquePasses(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile", "REPO_GATES := readme-lint\n\nreadme-lint:\n\t@bash scripts/check-readme.sh\n")
	writeFixture(t, dir, "scripts/check-readme.sh",
		"#!/usr/bin/env bash\n# lane-inputs:\n#   README.md\n# lane-reads-opaque: EXTRA_PATH is a manual debug override, not a repo input\nset -euo pipefail\ngrep -q \"hi\" README.md\ngrep -q \"x\" \"$EXTRA_PATH\"\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, opaque, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 0 {
		t.Fatalf("lane-reads-opaque should suppress the refusal, got %+v", refusals)
	}
	if opaque != 1 {
		t.Errorf("opaque = %d, want 1", opaque)
	}
}

func TestHonestyCheckVerifyFunctionWrapped(t *testing.T) {
	dir := t.TempDir()
	writeMinimalGate(t, dir)
	writeFixture(t, dir, "scripts/verify.sh",
		"#!/usr/bin/env bash\nset -euo pipefail\n\nrun_phase() { local gate=\"$1\"; shift; \"$@\"; }\n\n"+
			"# lane-inputs:\n#   docs/decisions.md\nbuild_notes() {\n  grep -q x docs/wrong.md\n}\n\n"+
			"run_phase build-notes build_notes\n")

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, _, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1 (docs/wrong.md is not declared): %+v", len(refusals), refusals)
	}
	if !strings.Contains(refusals[0].Problem, "docs/wrong.md") {
		t.Errorf("refusal does not name docs/wrong.md: %+v", refusals[0])
	}
}

func TestHonestyCheckGoArmOnGoRunBareStatement(t *testing.T) {
	dir := t.TempDir()
	writeMinimalGate(t, dir)
	writeFixture(t, dir, "internal/toolcheck/toolcheck.go",
		"//go:build ignore\n\npackage main\n\nimport \"os\"\n\nfunc main() {\n\t_, _ = os.ReadFile(\"undeclared.yaml\")\n}\n")
	writeFixture(t, dir, "scripts/verify.sh",
		"#!/usr/bin/env bash\nset -euo pipefail\n\nrun_phase() { local gate=\"$1\"; shift; \"$@\"; }\n\n"+
			"# lane-inputs:\n#   **/*.go\n#   go.mod\nrun_phase toolcheck go run internal/toolcheck/toolcheck.go\n")

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, _, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1 (undeclared.yaml is not covered by **/*.go or go.mod): %+v", len(refusals), refusals)
	}
	if !strings.Contains(refusals[0].Subject, "internal/toolcheck/toolcheck.go") {
		t.Errorf("refusal does not name the Go file: %+v", refusals[0])
	}
}

func TestHonestyCheckMakefileInlineRecipe(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile",
		"REPO_GATES := workflow-lint\n\n# lane-inputs:\n#   .github/workflows/**\nworkflow-lint:\n\t@grep -rn 'uses:' .github/workflows/ci.yml\n\t@cat docs/undeclared.md\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, _, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("HonestyCheck: %v", err)
	}
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if !strings.Contains(refusals[0].Problem, "docs/undeclared.md") {
		t.Errorf("refusal does not name docs/undeclared.md: %+v", refusals[0])
	}
}

// A find with NO narrowing filter really does read its whole root, and must
// still say so — the pair with TestScanLineForReadsFindMultipleRoots is what
// makes narrowFindRoots discriminating rather than a blanket rewrite.
func TestScanLineForReadsFindWithoutFiltersKeepsTheRoot(t *testing.T) {
	reads, unresolved := scanLineForReads(`find docs/a -type f`, 1)
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
	if len(reads) != 1 || reads[0].Path != "docs/a" {
		t.Fatalf("reads = %+v, want exactly docs/a", reads)
	}
}

// A one-line function body must not swallow the read inside it. Before "{"
// and "}" were boundary tokens the classifier returned ZERO reads for this
// whole line — a silent miss, which is the one outcome D-11 forbids.
func TestScanLineForReadsOneLineFunctionBodyIsNotSilentlySkipped(t *testing.T) {
	reads, unresolved := scanLineForReads(`floor_now() { sed -n 1p "$MANIFEST"; }`, 1)
	if len(reads) != 0 {
		t.Fatalf("a $-variable read is not a literal; got reads %+v", reads)
	}
	if len(unresolved) != 1 {
		t.Fatalf("the variable read inside the one-line body must be FLAGGED, got %d unresolved: %+v", len(unresolved), unresolved)
	}
}

// A find that mixes selection with exclusion is OUT of contract: narrowing on
// its -path/-name arms would report the PRUNED paths as inputs, and reporting
// the bare root would demand the gate declare the whole repo. Neither is true,
// so it must reach the unresolved list instead — where lane-reads-opaque is
// the only way past it.
func TestScanLineForReadsPrunedFindIsUnresolvedNotARootRead(t *testing.T) {
	line := `find . \( -path './.git' -o -name node_modules \) -prune -o -path '*/audits/*' -name 'live-e2e-*' -print`
	reads, unresolved := scanLineForReads(line, 1)
	if len(reads) != 0 {
		t.Fatalf("a pruned find must report NO literal read; got %+v", reads)
	}
	if len(unresolved) != 1 {
		t.Fatalf("a pruned find must be flagged unresolved; got %d: %+v", len(unresolved), unresolved)
	}
}

// TestHonestyCheckSkipsAnAbsentPresenceGatedScript covers the case that made
// the private CI red on `main` from 2026-08-09 and would have failed the
// v0.19.10 release candidate's own `make check` at its first phase.
//
// Several harness gates are untracked BY DESIGN — they read paths the
// publisher strips, so they cannot function in a public tree — and their
// recipes guard on `[ -f <script> ]`. When the declaration sits on the tracked
// recipe (so the CLAIM survives, which is the whole point), the honesty pass
// then tries to read a script that is legitimately not there. A gate that does
// not run cannot read anything it failed to declare.
func TestHonestyCheckSkipsAnAbsentPresenceGatedScript(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile",
		"REPO_GATES := private-gate\n\n"+
			"# lane-inputs:\n#   README.md\n"+
			"private-gate:\n"+
			"\t@if [ -f scripts/check-private.sh ]; then \\\n"+
			"\t  bash scripts/check-private.sh; \\\n"+
			"\telse \\\n"+
			"\t  echo \"private-gate: skip — absent (public checkout).\"; \\\n"+
			"\tfi\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)
	// scripts/check-private.sh is deliberately NOT written.

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refusals, _, err := HonestyCheck(dir, decls)
	if err != nil {
		t.Fatalf("an absent PRESENCE-GATED script must not error the honesty pass: %v", err)
	}
	if len(refusals) != 0 {
		t.Fatalf("an absent presence-gated script must produce no refusal, got %+v", refusals)
	}

	// The claim must SURVIVE the script's absence — that is why the
	// declaration was moved onto the recipe in the first place. Without this
	// half the test would pass while Coverage reported README.md unclaimed,
	// which is the original defect wearing a different hat.
	if cov := Coverage(decls, []string{"README.md"}, nil); len(cov) != 0 {
		t.Fatalf("a presence-gated gate's recipe declaration must still claim its paths, got %+v", cov)
	}
}

// TestHonestyCheckStillErrorsOnAnUnguardedMissingScript is the other half, and
// it is what keeps the guard above narrow. A recipe that names a script and
// does NOT guard on its presence is broken when the script is missing: the
// gate silently does nothing and nobody is told. That must stay loud.
func TestHonestyCheckStillErrorsOnAnUnguardedMissingScript(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "Makefile",
		"REPO_GATES := unguarded-gate\n\n"+
			"# lane-inputs:\n#   README.md\n"+
			"unguarded-gate:\n\t@bash scripts/check-unguarded.sh\n")
	writeFixture(t, dir, "scripts/verify.sh", minimalVerifySh)

	decls, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, _, err := HonestyCheck(dir, decls); err == nil {
		t.Fatal("an UNGUARDED recipe naming a missing script must still error — a gate that quietly does nothing is the defect the presence-gate branch must not widen into")
	}
}

// TestRecipeGuardsPresenceMatchesTheSpecificScript pins the narrowness of the
// match itself: guarding on one file and then unconditionally running another
// is not presence-gating for the second.
func TestRecipeGuardsPresenceMatchesTheSpecificScript(t *testing.T) {
	recipe := []string{
		"\t@if [ -f scripts/a.sh ]; then \\",
		"\t  bash scripts/b.sh; \\",
		"\tfi",
	}
	if !recipeGuardsPresence(recipe, "scripts/a.sh") {
		t.Error("the guarded script must be recognised")
	}
	if recipeGuardsPresence(recipe, "scripts/b.sh") {
		t.Error("a script the recipe runs but does not guard must NOT count as presence-gated")
	}
}
