package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
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
	// Hardens THIS process's git spawns (and, via txtar_test.go's Setup
	// copying the same GIT_CONFIG_* trio into the testscript env, the
	// exec'd `a2a` binary's own git children too) against git's gc --auto
	// grandchild racing a t.TempDir() cleanup's RemoveAll — see
	// testkit/gitfixture/gitfixture.go's package doc. Called before the go
	// build below so the build subprocess (which inherits os.Environ())
	// sees it as well, though that subprocess itself never spawns git.
	gitfixture.HardenEnv()

	// A `go test -list` subprocess proves that a manifest's TestName still
	// exists; it executes no tests and therefore needs no black-box artifact.
	// Skipping the build here turns N reference checks from N linker runs into
	// one cheap package listing, without weakening what `-list` proves.
	if testListRequested(os.Args[1:]) {
		return m.Run()
	}

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal/e2e: TestMain: resolve repo root:", err)
		return 1
	}

	if supplied := os.Getenv("A2A_VERIFY_BINARY"); supplied != "" {
		info, statErr := os.Stat(supplied)
		if statErr != nil {
			fmt.Fprintln(os.Stderr, "internal/e2e: TestMain: shared A2A_VERIFY_BINARY:", statErr)
			return 1
		}
		if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			fmt.Fprintln(os.Stderr, "internal/e2e: TestMain: shared A2A_VERIFY_BINARY is not an executable regular file:", supplied)
			return 1
		}
		binDir = filepath.Dir(supplied)
		return m.Run()
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
	// This is a synthetic test artifact with an explicit version; VCS stamps
	// add no assertion and Go 1.26 resolves them through a writable module
	// stat cache. Keep the shared GOMODCACHE a read-only input in sandboxes.
	cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X main.version=0.1.0", "-o", bin, "./cmd/a2a")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "internal/e2e: TestMain: go build ./cmd/a2a: %v\n%s\n", err, out)
		return 1
	}

	binDir = dir
	return m.Run()
}

func testListRequested(args []string) bool {
	for _, arg := range args {
		if arg == "-test.list" || strings.HasPrefix(arg, "-test.list=") {
			return true
		}
	}
	return false
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
