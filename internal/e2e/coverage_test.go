package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// TestE2ECoverageParity is spec 26 §1.3/§8 AC-1's self-enforcing gate: (a)
// every LIVE catalog CLI verb (read from the BUILT binary's own `a2a
// __catalog` output — never a second, hand-maintained list) is named by at
// least one coverageManifest row (evidence or skip); (b) every row's
// evidence actually resolves — a Txtar row to an existing, verb-invoking
// testdata/t3/*.txtar file, a GoTest row via `go test -list` (cccoverage's
// own resolveTestRef, reused verbatim); (c) every row has exactly one of
// Txtar/GoTest/Skip set, and a Skip row's justification is therefore
// non-empty by construction. Bidirectional (mirrors
// cmd/a2a/catalog_test.go's TestCatalogCommandNameParity rigor): a manifest/
// skip row naming a verb that ISN'T a real catalog verb also fails — a
// stale row surviving a rename/removal is caught, not silently ignored.
func TestE2ECoverageParity(t *testing.T) {
	root := repoRootForTest(t)
	bin := filepath.Join(binDir, "a2a")

	catalogOutput, err := runCatalogBinary(bin)
	if err != nil {
		t.Fatalf("exec `a2a __catalog`: %v", err)
	}
	catalogVerbs := parseCatalogVerbs(catalogOutput)
	if len(catalogVerbs) == 0 {
		t.Fatal("parseCatalogVerbs: got zero verbs from `a2a __catalog` output — parsing likely broken")
	}

	// (a) every catalog verb is named by the manifest (evidence or skip).
	if missing := missingVerbs(catalogVerbs, coverageManifest); len(missing) > 0 {
		t.Errorf("catalog verb(s) with NO coverage manifest row (mapped or skipped): %v", missing)
	}

	// Bidirectional: no stale manifest/skip row names a verb that no longer
	// exists in the catalog.
	if stale := staleEntries(catalogVerbs, coverageManifest); len(stale) > 0 {
		for _, e := range stale {
			t.Errorf("coverage manifest row for verb %q does not match any real catalog verb (stale row)", e.Verb)
		}
	}

	// (b)+(c) every row resolves. GoTest refs are deduped across rows —
	// several verbs (e.g. every OP-211 lifecycle verb TestT3LifecycleVerbs
	// covers) share one Go test func; resolving it once per distinct ref
	// keeps subprocess fan-out (`go test -list`) sane under -count=2.
	t3Dir := filepath.Join(root, "internal", "e2e", "testdata", "t3")
	goTestCache := map[string]error{}
	for _, e := range coverageManifest {
		kind, err := e.evidenceKind()
		if err != nil {
			t.Errorf("command %q: %v", e.target(), err)
			continue
		}
		switch kind {
		case "txtar":
			if err := resolveTxtarEntry(t3Dir, e.Txtar, e.target()); err != nil {
				t.Errorf("command %q: %v", e.target(), err)
			}
		case "goTest":
			if _, ok := goTestCache[e.GoTest]; !ok {
				goTestCache[e.GoTest] = resolveTestRef(root, e.GoTest)
			}
			if err := goTestCache[e.GoTest]; err != nil {
				t.Errorf("verb %q: GoTest ref %q does not resolve: %v", e.Verb, e.GoTest, err)
			}
		case "skip":
			// evidenceKind already proved Skip is non-empty.
		}
	}
}

// TestE2EWorkActionCoverage closes the one-family catalog seam: the command
// catalog has a `work` row, while the transport dispatches a closed action
// enum. Every action must have non-skip behavior evidence and stale action
// rows fail in the reverse direction.
func TestE2EWorkActionCoverage(t *testing.T) {
	t.Parallel()

	const semanticEvidence = "cmd/a2a.TestWorkProductionWiringMutations"
	want := cli.WorkSubcommands()
	covered := make(map[string]bool, len(want))
	valid := make(map[string]bool, len(want))
	for _, action := range want {
		valid[action] = true
	}
	for _, entry := range coverageManifest {
		if entry.Verb != "work" || entry.Action == "" {
			continue
		}
		if !valid[entry.Action] {
			t.Errorf("stale work action coverage row %q", entry.target())
			continue
		}
		kind, err := entry.evidenceKind()
		if err != nil {
			t.Errorf("work action %q: %v", entry.Action, err)
			continue
		}
		if kind == "skip" {
			t.Errorf("work action %q has a permanent skip; executable behavior evidence is required", entry.Action)
			continue
		}
		if entry.Action != "status" && (kind != "goTest" || entry.GoTest != semanticEvidence) {
			t.Errorf("work mutation %q must resolve to exact production-wiring integration evidence %q; direct fake backends and usage-only txtar evidence are insufficient", entry.Action, semanticEvidence)
			continue
		}
		covered[entry.Action] = true
	}
	for _, action := range want {
		if !covered[action] {
			t.Errorf("work action %q has no executable behavior coverage row", action)
		}
	}
}

// TestE2EContractP6OperationCoverage prevents parser-usage probes from being
// reintroduced as evidence for operations that have successful production-
// wiring coverage through the hermetic host rig.
func TestE2EContractP6OperationCoverage(t *testing.T) {
	t.Parallel()
	const exactEvidence = "internal/e2e.TestHostLoopContractFamily"
	want := map[string]bool{
		"contract preflight":   false,
		"contract materialize": false,
		"contract check":       false,
	}
	for _, entry := range coverageManifest {
		if _, tracked := want[entry.target()]; !tracked {
			continue
		}
		kind, err := entry.evidenceKind()
		if err != nil {
			t.Errorf("%s: %v", entry.target(), err)
			continue
		}
		if kind != "goTest" || entry.GoTest != exactEvidence {
			t.Errorf("%s must resolve to successful production-wiring evidence %q; usage-only txtar evidence is insufficient", entry.target(), exactEvidence)
			continue
		}
		want[entry.target()] = true
	}
	for target, covered := range want {
		if !covered {
			t.Errorf("%s has no exact successful production-wiring evidence", target)
		}
	}
}

// --- teeth: this gate's own self-tests (spec 26 §6/§8 AC-2) --------------
// Every check below drives the PURE resolution helpers directly against a
// synthetic fixture or a throwaway manifest slice — never the real
// coverageManifest — so a broken gate (one that would pass ANY input) is
// caught independently of whether today's real manifest happens to be
// clean.

// TestE2ECoverageGateCatchesMissingVerb proves missingVerbs flags a real
// catalog verb absent from every manifest row (AC-2's "adding a catalog
// verb without a scenario turns CI red").
func TestE2ECoverageGateCatchesMissingVerb(t *testing.T) {
	catalogVerbs := []string{"alpha", "beta", "gamma"}
	entries := []coverageEntry{
		{Verb: "alpha", Skip: "not covered yet"},
		{Verb: "beta", Skip: "not covered yet"},
		// "gamma" deliberately absent from entries.
	}
	got := missingVerbs(catalogVerbs, entries)
	want := []string{"gamma"}
	sort.Strings(got)
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("missingVerbs(%v, entries) = %v, want %v", catalogVerbs, got, want)
	}

	// And the gate is not a no-op the other way: a fully-covered set must
	// report nothing missing.
	if got := missingVerbs(catalogVerbs, append(entries, coverageEntry{Verb: "gamma", Skip: "now covered"})); len(got) != 0 {
		t.Fatalf("expected no missing verbs once every catalog verb has a row, got %v", got)
	}
}

// TestE2ECoverageGateCatchesStaleManifestEntry proves staleEntries flags a
// manifest/skip row naming a verb that is NOT a real catalog verb (the
// reverse-direction half of AC-1's bidirectional check).
func TestE2ECoverageGateCatchesStaleManifestEntry(t *testing.T) {
	catalogVerbs := []string{"alpha", "beta"}
	entries := []coverageEntry{
		{Verb: "alpha", Skip: "fine"},
		{Verb: "beta", Skip: "fine"},
		{Verb: "zzz-removed-verb", Skip: "stale — this verb no longer ships"},
	}
	stale := staleEntries(catalogVerbs, entries)
	if len(stale) != 1 || stale[0].Verb != "zzz-removed-verb" {
		t.Fatalf("staleEntries(%v, entries) = %v, want exactly one stale row for zzz-removed-verb", catalogVerbs, stale)
	}
}

// TestE2ECoverageGateCatchesBadTxtarRef proves resolveTxtarEntry FAILS both
// when the named file doesn't exist and when it exists but never actually
// invokes the claimed verb (AC-1(b)'s grep check) — using a t.TempDir()
// fixture, never the real testdata/t3 tree.
func TestE2ECoverageGateCatchesBadTxtarRef(t *testing.T) {
	dir := t.TempDir()

	if err := resolveTxtarEntry(dir, "does_not_exist.txtar", "connect"); err == nil {
		t.Fatal("expected a missing txtar file to fail resolution, but it resolved")
	}

	writeTempTxtar(t, dir, "no_invoke.txtar", "exec a2a doctor\nstdout 'PASS'\n")
	if err := resolveTxtarEntry(dir, "no_invoke.txtar", "connect"); err == nil {
		t.Fatal("expected a txtar that never invokes `a2a connect` to fail resolution, but it resolved")
	}

	// And the gate isn't broken the other way: a genuinely invoking script
	// must resolve, including a MULTI-TOKEN catalog verb — `contract` is one
	// dispatch key whose sub-verb is its first argument, so the verb name
	// and the invocation are both two words. (They used to disagree: the
	// catalog stitched them with a hyphen and verbInvocationPattern rewrote
	// that back, which is how seven uninvocable command names shipped in
	// commands.md.)
	writeTempTxtar(t, dir, "invokes.txtar", "exec a2a connect $ORIGIN\nstdout 'registered'\n")
	if err := resolveTxtarEntry(dir, "invokes.txtar", "connect"); err != nil {
		t.Fatalf("expected a genuinely invoking txtar to resolve, got: %v", err)
	}

	writeTempTxtar(t, dir, "contract_new.txtar", "exec a2a contract new widget\nstdout 'staged'\n")
	if err := resolveTxtarEntry(dir, "contract_new.txtar", "contract new"); err != nil {
		t.Fatalf("expected `a2a contract new` to resolve the `contract new` verb, got: %v", err)
	}
	// The hyphenated spelling is no longer a verb anywhere, and must not
	// resolve — otherwise the rename could be silently half-reverted.
	if err := resolveTxtarEntry(dir, "contract_new.txtar", "contract-new"); err == nil {
		t.Fatal("expected the retired hyphenated spelling `contract-new` NOT to resolve, but it did")
	}
}

// TestE2ECoverageGateCatchesBrokenGoTestRef proves a GoTest ref that does
// not resolve (a renamed/removed test func) fails — reusing
// cccoverage_test.go's own resolveTestRef, never a re-implementation.
func TestE2ECoverageGateCatchesBrokenGoTestRef(t *testing.T) {
	root := repoRootForTest(t)
	if err := resolveTestRef(root, "internal/e2e.TestDoesNotExistNoReally"); err == nil {
		t.Fatal("expected a deliberately-broken GoTest ref to FAIL resolution, but it resolved")
	}
	if err := resolveTestRef(root, "internal/e2e.TestT3Submit"); err != nil {
		t.Fatalf("expected a genuine GoTest ref to resolve, got: %v", err)
	}
}

// TestE2ECoverageGateCatchesEmptySkipJustification proves evidenceKind
// rejects a row with no Txtar/GoTest AND no Skip justification (AC-2's
// "empty skip justification" teeth clause) — and, symmetrically, a row with
// MORE than one of the three set (an equally malformed, ambiguous row).
func TestE2ECoverageGateCatchesEmptySkipJustification(t *testing.T) {
	empty := coverageEntry{Verb: "ghost"}
	if _, err := empty.evidenceKind(); err == nil {
		t.Fatal("expected an all-empty coverage entry (empty skip justification) to fail evidenceKind, but it passed")
	}

	ambiguous := coverageEntry{Verb: "ghost", Txtar: "x.txtar", Skip: "also skipped?"}
	if _, err := ambiguous.evidenceKind(); err == nil {
		t.Fatal("expected a coverage entry with BOTH Txtar and Skip set to fail evidenceKind, but it passed")
	}

	valid := coverageEntry{Verb: "ghost", Skip: "a real one-line reason"}
	kind, err := valid.evidenceKind()
	if err != nil || kind != "skip" {
		t.Fatalf("expected a valid skip-only entry to resolve to kind \"skip\", got (%q, %v)", kind, err)
	}
}

// TestE2ECoverageParseCatalogVerbsExcludesHiddenAndMCP proves
// parseCatalogVerbs drops the hidden `__catalog` row and any `a2a_*` MCP
// tool row, against a synthetic catalog document (never the real binary's
// output — that's TestE2ECoverageParity's job).
func TestE2ECoverageParseCatalogVerbsExcludesHiddenAndMCP(t *testing.T) {
	doc := "# a2a command / MCP tool catalog\n\n" +
		"## Commands\n\n" +
		"- `__catalog` — hidden\n" +
		"- `connect` — register a space\n" +
		"- `submit` — validate and submit\n\n" +
		"## MCP tools\n\n" +
		"- `a2a_submit` — validate and submit staged drafts\n"

	got := parseCatalogVerbs(doc)
	want := []string{"connect", "submit"}
	if len(got) != len(want) {
		t.Fatalf("parseCatalogVerbs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseCatalogVerbs = %v, want %v", got, want)
		}
	}
}

func writeTempTxtar(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp txtar %s: %v", path, err)
	}
}

// refusalScenarioFloor is the number of t3 scenarios that assert a REFUSAL —
// a non-zero exit or an expected stderr — as opposed to only a success.
//
// Measured, not chosen: 8 of 22 when this was written on 2026-07-26, then raised
// to 9 in the same commit by giving `new_validate.txtar` the two refusals it was
// missing — so the ratchet is demonstrated moving rather than merely declared.
// It is a FLOOR: the number can only go up; raising it is a normal commit,
// lowering it needs a reason in the message.
//
// The thirteen still on the happy path are named in this test's own output every
// time it runs, so the state is visible rather than inferable.
//
// # This floor is NOT a coverage target, and that matters
//
// Checked after measuring: every one of those verbs except `disconnect` has unit
// tests naming refusals, and `disconnect` has none because it HAS no refusal —
// it is idempotent by design. And cmd/a2a's wire tier drives EVERY lifecycle verb
// to a refusal through the real entry points, in `make check`, by handing each an
// artifact that does not exist.
//
// So refusal LOGIC is covered at the unit tier, refusal WIRING per verb at the
// wire tier, and what these thirteen lack is only the third slice — the same
// refusal through the BUILT BINARY — which the other two bracket. Raise the floor
// when a scenario is being edited anyway; grinding thirteen scenarios to move a
// number is make-work, and a gate that induces make-work is worse than no gate.
//
// The 14-of-22 figure that prompted this was true and read as a hole because it
// measured one tier in isolation. Left here as a caution rather than deleted: a
// true number framed as a gap is its own kind of wrong answer.
//
// # Why this exists, and what it admits
//
// TestE2ECoverageParity above gates VERB parity: every catalog verb has at least
// one scenario, and every scenario resolves. That is real and it works — 43 of 45
// verbs carry evidence, the two skips are justified. But its own doc comment is
// explicit that it requires "at least ONE row", and it says nothing about which
// BEHAVIOURS of a verb are covered.
//
// The cost showed up on 2026-07-26. `a2a doctor`'s skill advisory named a remedy
// that cannot work in the state it was advising about — and every layer of
// coverage was green: the verb had evidence, the check had six test references,
// and the specific test for that message was CORRECT (it created a `.claude/`
// surface, so it exercised the branch where the remedy does work). The defect was
// a missing DISTINCTION: one code branch where reality has two states. No
// coverage metric finds a state the code never separated.
//
// This gate does not fix that class — nothing counting scenarios could. What it
// does is stop the measurable half from drifting: two thirds of this tier's
// scenarios never assert a refusal at all, so "the verb is covered" mostly means
// "the happy path runs". Naming the number makes that visible instead of
// inferable, and makes it a ratchet instead of a fact somebody rediscovers.
const refusalScenarioFloor = 9

// TestT3ScenariosAssertRefusalsNotOnlySuccess pins refusalScenarioFloor.
func TestT3ScenariosAssertRefusalsNotOnlySuccess(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRootForTest(t), "internal", "e2e", "testdata", "t3")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var total int
	var withRefusal []string
	var happyOnly []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txtar") {
			continue
		}
		total++
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Errorf("read %s: %v", e.Name(), err)
			continue
		}
		body := string(raw)
		// testscript's own vocabulary for "this must fail": a `!`-negated
		// command, or an assertion on stderr.
		if strings.Contains(body, "! exec") || strings.Contains(body, "! a2a") || strings.Contains(body, "stderr ") {
			withRefusal = append(withRefusal, e.Name())
			continue
		}
		happyOnly = append(happyOnly, e.Name())
	}

	if total == 0 {
		t.Fatal("no t3 scenarios found — this gate is guarding nothing")
	}
	t.Logf("t3 scenarios: %d total, %d assert a refusal, %d happy-path only", total, len(withRefusal), len(happyOnly))
	t.Logf("happy-path only: %s", strings.Join(happyOnly, ", "))

	if len(withRefusal) < refusalScenarioFloor {
		t.Errorf("only %d of %d t3 scenarios assert a refusal, below the floor of %d.\n"+
			"A tier where the happy path runs and nothing is ever refused certifies that the verb "+
			"EXISTS, not that it guards anything. Raising this floor is a normal commit; lowering it "+
			"needs a reason.\nhappy-path only: %s",
			len(withRefusal), total, refusalScenarioFloor, strings.Join(happyOnly, ", "))
	}
}
