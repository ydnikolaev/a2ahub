package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestCCCoverageGate is spec 10 §8 AC-5: parses the repo-root
// cc-coverage.yaml and FAILS if any row's test_ref does not resolve to a
// real, listable Go test (`go test -list`, the documented resolution
// mechanism this phase's own plan brief names).
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

// resolveTestRef splits "pkg/path.TestName" and runs `go test -list
// ^TestName$ ./pkg/path` from root, returning an error if TestName is not
// among the listed tests (or the list itself fails to run).
func resolveTestRef(root, testRef string) error {
	i := strings.LastIndex(testRef, ".")
	if i < 0 {
		return &testRefError{testRef, "not in \"pkg/path.TestName\" shape"}
	}
	pkgPath, testName := testRef[:i], testRef[i+1:]

	cmd := exec.Command("go", "test", "-list", "^"+testName+"$", "./"+pkgPath)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &testRefError{testRef, "go test -list failed: " + err.Error() + ": " + string(out)}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == testName {
			return nil
		}
	}
	return &testRefError{testRef, "not listed by `go test -list ^" + testName + "$ ./" + pkgPath + "`: " + string(out)}
}

type testRefError struct {
	ref    string
	reason string
}

func (e *testRefError) Error() string { return e.ref + ": " + e.reason }
