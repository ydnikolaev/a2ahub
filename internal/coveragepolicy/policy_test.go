package coveragepolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProfile writes a coverage profile from raw block lines and returns
// its path. A block line is "<pkgSuffix>/file.go:1.1,2.1 <nStmt> <count>" —
// count>0 means covered.
func writeProfile(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "coverage.out")
	body := "mode: set\n"
	for _, l := range lines {
		body += modulePath + "/" + l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheck_AllAboveThreshold(t *testing.T) {
	t.Parallel()
	// internal/cache 100%, cmd/a2a 40% (>35 threshold), a bare pkg 70% (>60... wait global is 70).
	prof := writeProfile(
		t,
		"internal/cache/a.go:1.1,2.1 10 1",
		"cmd/a2a/a.go:1.1,2.1 4 1", "cmd/a2a/a.go:3.1,4.1 6 0",
		"internal/other/a.go:1.1,2.1 7 1", "internal/other/a.go:3.1,4.1 3 0",
	)
	failures, err := Check(prof)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
}

func TestCheck_PerPackageBelow(t *testing.T) {
	t.Parallel()
	// cmd/a2a at 20% — below its 35% floor.
	prof := writeProfile(
		t,
		"cmd/a2a/a.go:1.1,2.1 2 1", "cmd/a2a/a.go:3.1,4.1 8 0",
	)
	failures, err := Check(prof)
	if err != nil {
		t.Fatal(err)
	}
	var got bool
	for _, f := range failures {
		if strings.Contains(f, "cmd/a2a") {
			got = true
		}
	}
	if !got {
		t.Fatalf("expected a cmd/a2a failure, got %v", failures)
	}
}

func TestCheck_GlobalFloorAppliesToInternalPackages(t *testing.T) {
	t.Parallel()
	// internal/other at 50% — no per-package Threshold entry, so it must
	// clear the 70% Global floor instead; it doesn't, so BOTH its own line
	// and the TOTAL line should fail.
	prof := writeProfile(
		t,
		"internal/other/a.go:1.1,2.1 5 1", "internal/other/a.go:3.1,4.1 5 0",
	)
	failures, err := Check(prof)
	if err != nil {
		t.Fatal(err)
	}
	var gotPkg, gotTotal bool
	for _, f := range failures {
		if strings.HasPrefix(f, "internal/other:") {
			gotPkg = true
		}
		if strings.HasPrefix(f, "TOTAL:") {
			gotTotal = true
		}
	}
	if !gotPkg {
		t.Fatalf("expected an internal/other failure (Global floor), got %v", failures)
	}
	if !gotTotal {
		t.Fatalf("expected a TOTAL global-floor failure, got %v", failures)
	}
}

func TestCheck_ExcludedPackageIgnored(t *testing.T) {
	t.Parallel()
	// internal/e2e and testkit/spacefixture are excluded — even at 0% they
	// must not produce a failure, and must not sink the global.
	prof := writeProfile(
		t,
		"internal/e2e/a.go:1.1,2.1 100 0",
		"testkit/spacefixture/a.go:1.1,2.1 100 0",
		"internal/cache/a.go:1.1,2.1 10 1",
	)
	failures, err := Check(prof)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("excluded package leaked into the gate: %v", failures)
	}
}

func TestCheck_MalformedIsHardError(t *testing.T) {
	t.Parallel()
	prof := writeProfile(t, "internal/cache/a.go:1.1,2.1 notanumber 1")
	if _, err := Check(prof); err == nil {
		t.Fatal("expected a parse error on a malformed profile, got nil")
	}
}

func TestCheck_MissingProfileIsHardError(t *testing.T) {
	t.Parallel()
	if _, err := Check(filepath.Join(t.TempDir(), "nope.out")); err == nil {
		t.Fatal("expected an error for a missing profile, got nil")
	}
}
