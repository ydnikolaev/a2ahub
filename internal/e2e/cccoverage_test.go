package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// ccCoverageRow is one cc-coverage.yaml row (spec 10 §T5 shape).
type ccCoverageRow struct {
	CCID    string `yaml:"cc_id"`
	TestRef string `yaml:"test_ref"`
	Tier    string `yaml:"tier"`
	Status  string `yaml:"status"`
}

// ccUncoveredRow is one `uncovered:` entry (agent-exchange-2026-08 P11 AC7):
// a §12 corner case that carries NO coverage row, named with a verdict and
// the evidence behind it, so the size of the gap is a published number
// rather than a vague one.
type ccUncoveredRow struct {
	CCID     string `yaml:"cc_id"`
	Verdict  string `yaml:"verdict"`
	Summary  string `yaml:"summary"`
	Evidence string `yaml:"evidence"`
	Reason   string `yaml:"reason"`
}

type ccCoverageFile struct {
	Rows      []ccCoverageRow  `yaml:"rows"`
	Uncovered []ccUncoveredRow `yaml:"uncovered"`
}

// ccUncoveredVerdicts is the closed set an `uncovered:` entry may declare.
// An unrecognized token is rejected rather than tolerated — the point of the
// block is that every corner case has a STATED kind of absence.
var ccUncoveredVerdicts = map[string]struct{}{
	// a real test exercises it; it is simply not recorded as coverage yet
	"evidence-exists-unrecorded": {},
	// genuinely untested, about behaviour this epic shipped or touched
	"uncovered-in-scope": {},
	// genuinely untested, belonging to a capability this epic did not build
	"not-this-epic": {},
	// describes a human/process judgment, not product behaviour a test drives
	"not-testable-here": {},
}

// ccTierVocabulary is the set cc-coverage.yaml's own header declares, plus
// `in-process` — which names the ABSENCE of a binary-tier claim rather than
// a new tier, exactly as internal/e2e/coverage.go's tierInProcess does one
// file away. It was added by P11 wave C for CC-084, whose evidence is a
// direct-construction command test: neither fold determinism (T2) nor an
// exec'd binary (T3), and the row had been carrying a false `T3` because
// nothing checked it.
var ccTierVocabulary = map[string]struct{}{
	"T1": {}, "T2": {}, "T3": {},
	"E2E-1": {}, "E2E-4": {},
	"in-process": {},
}

// TestCCCoverageGate is spec 10 §8 AC-5: parses the repo-root
// cc-coverage.yaml and FAILS if any row's test_ref does not resolve to a
// real, listable Go test (`go test -list`, the documented resolution
// mechanism this phase's own plan brief names).
//
// P11 wave C (spec 11 AC9) added the SECOND half. The `tier` column was
// parsed and never read — the file's header declared a vocabulary that
// nothing enforced, which is how CC-084 came to carry `tier: T3` beside a
// test that never execs the binary. A rule that is true and inert is the
// defect class this whole phase exists to remove, so the tier is now
// checked the same way internal/e2e/coverage.go's is: the token must be in
// the declared vocabulary, and a row CLAIMING the binary tier must name an
// internal/e2e test whose body actually reaches an exec seam.
func TestCCCoverageGate(t *testing.T) {
	root := repoRootForTest(t)
	rows := loadCCCoverage(t, filepath.Join(root, "cc-coverage.yaml"))
	if len(rows) == 0 {
		t.Fatal("cc-coverage.yaml: expected at least one row")
	}
	for _, row := range rows {
		if err := resolveTestRef(root, row.TestRef); err != nil {
			t.Errorf("cc-coverage.yaml: %s: test_ref %q does not resolve: %v", row.CCID, row.TestRef, err)
		}
		if err := checkCCTier(root, row); err != nil {
			t.Errorf("cc-coverage.yaml: %s: %v", row.CCID, err)
		}
	}
}

// checkCCTier validates one row's tier claim. An empty tier is rejected
// rather than defaulted: an undeclared tier must stay distinguishable from
// a declared one.
func checkCCTier(root string, row ccCoverageRow) error {
	if _, ok := ccTierVocabulary[row.Tier]; !ok {
		return &ccTierError{row.Tier, "not in cc-coverage.yaml's declared tier vocabulary (T1|T2|T3|E2E-1|E2E-4|in-process); an empty tier is undeclared, never a default"}
	}
	if row.Tier != tierT3 {
		return nil
	}
	// A T3 claim is checkable only for this package's own tests; a ref into
	// another package is refused rather than waved through, the same way
	// resolveGoTestExecSeam refuses one.
	return resolveGoTestExecSeam(root, row.TestRef)
}

type ccTierError struct {
	tier   string
	reason string
}

func (e *ccTierError) Error() string { return "tier " + strconv.Quote(e.tier) + ": " + e.reason }

// TestCCCoverageGateCatchesFalseTierClaim is the tier half's teeth: both an
// unrecognized token and a `T3` claim over an in-process test must fail.
// Without this, the check could silently become a no-op again.
func TestCCCoverageGateCatchesFalseTierClaim(t *testing.T) {
	root := repoRootForTest(t)

	for _, bad := range []string{"", "T7", "t3", "binary"} {
		row := ccCoverageRow{CCID: "CC-000", TestRef: "internal/e2e.TestT3Scripts", Tier: bad}
		if err := checkCCTier(root, row); err == nil {
			t.Errorf("tier %q: expected rejection, got none", bad)
		}
	}

	// A T3 claim over a test that provably never execs the binary.
	false3 := ccCoverageRow{CCID: "CC-000", TestRef: "internal/e2e.TestNotificationsStatusThroughStubbedBackend", Tier: "T3"}
	if err := checkCCTier(root, false3); err == nil {
		t.Fatal("expected a T3 claim over an in-process test to FAIL, but it passed")
	}

	// And the gate is not broken in the other direction: a genuine T3 row
	// and a genuine in-process row both pass.
	for _, good := range []ccCoverageRow{
		{CCID: "CC-000", TestRef: "internal/e2e.TestT3Scripts", Tier: "T3"},
		{CCID: "CC-000", TestRef: "internal/e2e.TestNotificationsStatusThroughStubbedBackend", Tier: "in-process"},
	} {
		if err := checkCCTier(root, good); err != nil {
			t.Errorf("expected %s/%s to pass, got: %v", good.TestRef, good.Tier, err)
		}
	}
}

// TestCCCoverageGateCatchesBrokenRef is this gate's own self-test (spec 10
// §8 AC-5's "prove it" clause): a THROWAWAY copy of the real rows plus one
// deliberately-broken test_ref must fail resolution — proving the gate has
// teeth, never a no-op that would pass any file.
func TestCCCoverageGateCatchesBrokenRef(t *testing.T) {
	root := repoRootForTest(t)
	broken := ccCoverageRow{TestRef: "internal/e2e.TestDoesNotExistNoReally"}
	if err := resolveTestRef(root, broken.TestRef); err == nil {
		t.Fatal("expected a deliberately-broken test_ref to FAIL resolution, but it resolved")
	}

	// And the real file's own rows must all still resolve (the gate isn't
	// broken in the other direction either).
	good := ccCoverageRow{TestRef: "internal/e2e.TestT3Scripts"}
	if err := resolveTestRef(root, good.TestRef); err != nil {
		t.Fatalf("expected a genuine test_ref to resolve, got: %v", err)
	}
}

func loadCCCoverage(t *testing.T, path string) []ccCoverageRow {
	t.Helper()
	return loadCCCoverageFile(t, path).Rows
}

func loadCCCoverageFile(t *testing.T, path string) ccCoverageFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var f ccCoverageFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

var ccIDPattern = regexp.MustCompile(`CC-[0-9]{3}`)

// declaredCornerCases reads every CC-### the master plan's §12 declares. It
// is the SET the two blocks of cc-coverage.yaml must together account for —
// asked of the plan on every run rather than pinned as a literal, because a
// pinned count is exactly what lets a published figure stop being true
// without anybody noticing.
func declaredCornerCases(t *testing.T, root string) map[string]struct{} {
	t.Helper()
	path := filepath.Join(root, "docs", "the-plan", "plan", "12-corner-cases.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[string]struct{}{}
	for _, m := range ccIDPattern.FindAllString(string(raw), -1) {
		out[m] = struct{}{}
	}
	if len(out) == 0 {
		t.Fatalf("%s declared no CC-### at all — the extractor is broken, not the corpus", path)
	}
	return out
}

// TestCCCoverageAccountsForEveryDeclaredCornerCase is P11 AC7's gate: the
// gap between this file and §12 must be MEASURED and PUBLISHED, not merely
// deferred. `rows:` (covered) and `uncovered:` (named, with a verdict and
// evidence) must together equal §12's own declared set, exactly, with no
// entry in both.
//
// The point is what happens NEXT: a corner case appended to
// 12-corner-cases.md tomorrow reds this gate until somebody classifies it.
// Without that, the published number rots the same way this file's `tier`
// column rotted while nothing read it — the defect P11 wave C found one
// block above.
func TestCCCoverageAccountsForEveryDeclaredCornerCase(t *testing.T) {
	root := repoRootForTest(t)
	f := loadCCCoverageFile(t, filepath.Join(root, "cc-coverage.yaml"))
	declared := declaredCornerCases(t, root)

	for _, problem := range accountCornerCases(declared, f) {
		t.Error(problem)
	}
	t.Logf("corner-case accounting: %d declared = %d covered + %d named-uncovered", len(declared), len(f.Rows), len(f.Uncovered))
}

// accountCornerCases is the whole verdict, as a pure function over its two
// inputs, so the teeth test below can feed it a corpus that is deliberately
// wrong. A gate whose logic is reachable only through the real files can be
// self-tested only by editing them, which nobody does twice.
func accountCornerCases(declared map[string]struct{}, f ccCoverageFile) []string {
	var problems []string
	accounted := map[string]string{}
	for _, r := range f.Rows {
		accounted[r.CCID] = "rows"
	}
	for _, u := range f.Uncovered {
		if where, dup := accounted[u.CCID]; dup {
			problems = append(problems, fmt.Sprintf("cc-coverage.yaml: %s appears in both %s and uncovered — a corner case is covered or it is not", u.CCID, where))
			continue
		}
		accounted[u.CCID] = "uncovered"
		if _, ok := ccUncoveredVerdicts[u.Verdict]; !ok {
			problems = append(problems, fmt.Sprintf("cc-coverage.yaml: %s declares verdict %q, which is not one of the four recognized kinds of absence", u.CCID, u.Verdict))
		}
		if strings.TrimSpace(u.Reason) == "" || strings.TrimSpace(u.Summary) == "" {
			problems = append(problems, fmt.Sprintf("cc-coverage.yaml: %s must carry a non-empty summary AND reason — naming a gap without saying what it is publishes nothing", u.CCID))
		}
	}

	var missing, extra []string
	for cc := range declared {
		if _, ok := accounted[cc]; !ok {
			missing = append(missing, cc)
		}
	}
	for cc := range accounted {
		if _, ok := declared[cc]; !ok {
			extra = append(extra, cc)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d corner case(s) declared by docs/the-plan/plan/12-corner-cases.md are in NEITHER cc-coverage.yaml block — classify each as covered or as one of the four absences: %s",
			len(missing), strings.Join(missing, ", ")))
	}
	if len(extra) > 0 {
		problems = append(problems, fmt.Sprintf("%d cc-coverage.yaml entr(ies) name a CC-### §12 does not declare (a typo, or a corner case that was removed): %s",
			len(extra), strings.Join(extra, ", ")))
	}
	return problems
}

// TestCCCoverageAccountingHasTeeth feeds accountCornerCases five corpora,
// each wrong in exactly one way, plus one that is right.
func TestCCCoverageAccountingHasTeeth(t *testing.T) {
	t.Parallel()
	declared := map[string]struct{}{"CC-001": {}, "CC-002": {}, "CC-003": {}}
	base := ccUncoveredRow{Verdict: "not-this-epic", Summary: "s", Reason: "r"}
	with := func(cc string, mod func(*ccUncoveredRow)) ccUncoveredRow {
		u := base
		u.CCID = cc
		if mod != nil {
			mod(&u)
		}
		return u
	}
	rows := []ccCoverageRow{{CCID: "CC-001"}}

	good := ccCoverageFile{Rows: rows, Uncovered: []ccUncoveredRow{with("CC-002", nil), with("CC-003", nil)}}
	if problems := accountCornerCases(declared, good); len(problems) != 0 {
		t.Fatalf("a complete, well-formed corpus must produce no problems, got: %v", problems)
	}

	for _, c := range []struct {
		name string
		f    ccCoverageFile
		want string
	}{
		{"an unclassified corner case",
			ccCoverageFile{Rows: rows, Uncovered: []ccUncoveredRow{with("CC-002", nil)}},
			"in NEITHER cc-coverage.yaml block"},
		{"a CC named in both blocks",
			ccCoverageFile{Rows: rows, Uncovered: []ccUncoveredRow{with("CC-001", nil), with("CC-002", nil), with("CC-003", nil)}},
			"appears in both"},
		{"a verdict outside the closed set",
			ccCoverageFile{Rows: rows, Uncovered: []ccUncoveredRow{with("CC-002", func(u *ccUncoveredRow) { u.Verdict = "probably-fine" }), with("CC-003", nil)}},
			"not one of the four recognized kinds of absence"},
		{"a named gap with no reason",
			ccCoverageFile{Rows: rows, Uncovered: []ccUncoveredRow{with("CC-002", func(u *ccUncoveredRow) { u.Reason = "  " }), with("CC-003", nil)}},
			"must carry a non-empty summary AND reason"},
		{"an entry the plan does not declare",
			ccCoverageFile{Rows: rows, Uncovered: []ccUncoveredRow{with("CC-002", nil), with("CC-003", nil), with("CC-999", nil)}},
			"name a CC-### §12 does not declare"},
	} {
		problems := accountCornerCases(declared, c.f)
		if len(problems) == 0 {
			t.Errorf("%s: expected a problem, got none", c.name)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("%s: problems %v do not mention %q", c.name, problems, c.want)
		}
	}
}

type listedPackageTests struct {
	names map[string]struct{}
	out   string
	err   error
}

var testListCache = struct {
	sync.Mutex
	byPackage map[string]listedPackageTests
}{byPackage: make(map[string]listedPackageTests)}

// resolveTestRef splits "pkg/path.TestName" and resolves it against one
// canonical `go test -list ^Test ./pkg/path` result cached per package. The
// former one-subprocess-per-reference shape rebuilt the e2e CLI through
// TestMain for every row; batching preserves the Go runner's own definition of
// a listable test while removing repeated setup.
func resolveTestRef(root, testRef string) error {
	i := strings.LastIndex(testRef, ".")
	if i < 0 {
		return &testRefError{testRef, "not in \"pkg/path.TestName\" shape"}
	}
	pkgPath, testName := testRef[:i], testRef[i+1:]

	listed := listPackageTests(root, pkgPath)
	if listed.err != nil {
		return &testRefError{testRef, "go test -list failed: " + listed.err.Error() + ": " + listed.out}
	}
	if _, ok := listed.names[testName]; ok {
		return nil
	}
	return &testRefError{testRef, "not listed by `go test -list ^Test ./" + pkgPath + "`: " + listed.out}
}

func listPackageTests(root, pkgPath string) listedPackageTests {
	key := root + "\x00" + pkgPath
	testListCache.Lock()
	defer testListCache.Unlock()
	if cached, ok := testListCache.byPackage[key]; ok {
		return cached
	}

	var cmd *exec.Cmd
	if pkgPath == "internal/e2e" {
		// We are already running the canonical Go test binary for this
		// package. Ask that exact artifact to list its tests instead of making
		// the go command compile/link the same package again. TestMain detects
		// -test.list and skips unrelated CLI setup.
		exe, err := os.Executable()
		if err != nil {
			result := listedPackageTests{names: make(map[string]struct{}), err: err}
			testListCache.byPackage[key] = result
			return result
		}
		cmd = exec.Command(exe, "-test.list=^Test")
	} else {
		// Cross-package references retain the Go runner as the authority and
		// are still batched to one invocation per package.
		cmd = exec.Command("go", "test", "-list", "^Test", "./"+pkgPath)
		cmd.Dir = root
	}
	out, err := cmd.CombinedOutput()
	result := listedPackageTests{names: make(map[string]struct{}), out: string(out), err: err}
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Test") {
				result.names[line] = struct{}{}
			}
		}
	}
	testListCache.byPackage[key] = result
	return result
}

type testRefError struct {
	ref    string
	reason string
}

func (e *testRefError) Error() string { return e.ref + ": " + e.reason }
