// Package coveragepolicy is the SINGLE SOURCE OF TRUTH for a2ahub's test-
// coverage thresholds — one global floor plus per-package minimums — shared
// by two consumers: the coverage gate that runs under `go test`
// (policy_test.go) and the CI/local checker (covercheck.go, run by
// `make coverage`). Keeping the numbers in one Go package means a threshold
// can never drift between the local gate and CI.
//
// Ratchet policy: raise a threshold when a package clears it with margin;
// NEVER lower one to make a red gate green — that silently forfeits coverage
// the project already earned.
package coveragepolicy

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// modulePath is the go.mod module path, stripped from profile entries so
// thresholds are keyed by the short package suffix (e.g. "internal/cache").
const modulePath = "github.com/ydnikolaev/a2ahub"

// Global is the floor every non-excluded package's statements must clear IN
// AGGREGATE (%) — a2ahub's internal floor (spec 04-lint-coverage.md AC3;
// go-conventions.md testing rails).
const Global = 70.0

// Thresholds are per-package minimums (%), keyed by the path suffix after
// the module path. A package absent here must still clear Global — the 70
// rail is specifically for internal/...; cmd/a2a is CLI wiring covered by
// the e2e suite (internal/e2e drives the built binary), so it gets its own,
// realistic, lower floor rather than forcing 70 on wiring code.
var Thresholds = map[string]float64{
	"cmd/a2a": 35, // CLI dispatch/wiring — low unit-test value; internal/e2e (built-binary testscripts) covers the real dispatch loop
}

// Excluded packages are exempt from BOTH the per-package and the Global gate
// (they carry no meaningful statement coverage of their own, or are covered
// end-to-end elsewhere).
var Excluded = map[string]bool{
	"internal/e2e":         true, // drives the BUILT binary via testscript; every file is _test.go, so it contributes no statements to its own profile
	"testkit/spacefixture": true, // a test-fixture helper library consumed BY other packages' tests, not itself the subject of a coverage floor
	"testkit/fakegithub":   true, // same shape as spacefixture: a host test double consumed BY internal/e2e's tests; its own behaviour is asserted by fakegithub_test.go, not by a floor
}

type pkgStat struct{ total, covered int }

// Check reads a Go coverage profile and returns a sorted list of human-
// readable threshold violations (empty when everything passes). The second
// return is a non-nil error only for I/O or parse failures — a fail-closed
// checker treats those as hard errors, never as pass.
func Check(profilePath string) ([]string, error) {
	f, err := os.Open(profilePath)
	if err != nil {
		return nil, fmt.Errorf("open coverage profile: %w", err)
	}
	defer func() { _ = f.Close() }()

	pkgs := map[string]*pkgStat{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// Format: <file>:<startLine>.<col>,<endLine>.<col> <numStmt> <count>
		colon := strings.LastIndex(line, ":")
		fields := strings.Fields(line)
		if colon < 0 || len(fields) < 3 {
			return nil, fmt.Errorf("malformed coverage line: %q", line)
		}
		n, err1 := strconv.Atoi(fields[len(fields)-2])
		c, err2 := strconv.Atoi(fields[len(fields)-1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("malformed coverage counts: %q", line)
		}
		pkg := shortPkg(path.Dir(line[:colon]))
		if Excluded[pkg] {
			continue
		}
		s := pkgs[pkg]
		if s == nil {
			s = &pkgStat{}
			pkgs[pkg] = s
		}
		s.total += n
		if c > 0 {
			s.covered += n
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read coverage profile: %w", err)
	}

	var failures []string
	var gTotal, gCovered int
	for pkg, s := range pkgs {
		gTotal += s.total
		gCovered += s.covered
		if s.total == 0 {
			continue
		}
		pct := 100 * float64(s.covered) / float64(s.total)
		floor := Global
		if t, ok := Thresholds[pkg]; ok {
			floor = t
		}
		if pct < floor {
			failures = append(failures, fmt.Sprintf("%s: %.1f%% < %.0f%% threshold", pkg, pct, floor))
		}
	}
	if gTotal > 0 {
		if gpct := 100 * float64(gCovered) / float64(gTotal); gpct < Global {
			failures = append(failures, fmt.Sprintf("TOTAL: %.1f%% < %.0f%% global floor", gpct, Global))
		}
	}
	sort.Strings(failures)
	return failures, nil
}

// shortPkg strips the module path so "github.com/ydnikolaev/a2ahub/internal/cache" → "internal/cache".
func shortPkg(importPath string) string {
	return strings.TrimPrefix(strings.TrimPrefix(importPath, modulePath), "/")
}
