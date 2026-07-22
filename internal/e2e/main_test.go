package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// binDir holds the directory containing the ONE `a2a` binary TestMain
// builds — every testscript-driven T3 script puts this on $PATH (plan 10
// Placement decision: "testscript drives the BUILT binary via exec, not
// in-process" — cmd/a2a is package main, not importable).
var binDir string

// TestMain builds `a2a` exactly once (the scripts/e2e-authoring-smoke.sh
// idiom: `go build -o $bin ./cmd/a2a`) into a package-level temp directory,
// then runs the rest of this package's tests. The repo root is resolved
// robustly via runtime.Caller (this file's own path, two directories up
// from internal/e2e) rather than assuming the test's cwd — `go test` always
// runs with cwd = the package directory, but resolving from the source
// file's own location is the documented-robust approach the plan's Context
// section names.
func TestMain(m *testing.M) {
	// os.Exit is called exactly once, OUTSIDE runTestMain's own scope, so
	// its deferred temp-dir cleanup always runs (gocritic exitAfterDefer:
	// an os.Exit inside the same scope as the defer would skip it).
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) int {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal/e2e: TestMain: resolve repo root:", err)
		return 1
	}

	dir, err := os.MkdirTemp("", "a2a-e2e-bin-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal/e2e: TestMain: mkdir temp:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	bin := filepath.Join(dir, "a2a")
	// -ldflags stamps a real dotted version (cmd/a2a/main.go's own default
	// is the literal string "dev", which doctorCheckVersions cannot parse
	// as a semver — this phase's own read-surface tests need a real
	// min_binary_version comparison to succeed, matching the "0.1.0" this
	// package's direct-construction tests already use for binaryVersion).
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version=0.1.0", "-o", bin, "./cmd/a2a")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "internal/e2e: TestMain: go build ./cmd/a2a: %v\n%s\n", err, out)
		return 1
	}

	binDir = dir
	return m.Run()
}

// repoRoot resolves the product repo root from this source file's own
// location (internal/e2e/main_test.go -> ../.. is the repo root).
func repoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		return "", fmt.Errorf("resolved root %s has no go.mod: %w", abs, err)
	}
	return abs, nil
}
