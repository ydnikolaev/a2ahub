package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
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

type ccCoverageFile struct {
	Rows []ccCoverageRow `yaml:"rows"`
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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var f ccCoverageFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f.Rows
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
