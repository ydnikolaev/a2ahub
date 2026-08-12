package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestTroubleshootingEnumeratesEveryDoctorCheck(t *testing.T) {
	t.Parallel()
	cmd := NewDoctorCommand(host.NewFakeHost(), "0.16.0", "/unused/project.yaml", "/unused/machine.yaml", t.TempDir())
	// A connected space is REQUIRED here, not incidental: with zero spaces
	// doctorVisibilityRows returns nothing and the eighteenth printed row
	// (`repository visibility`) never appears, which is exactly how this
	// gate went blind to a wrong per-space cardinality claim in the doc.
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/getvisa.git"}}}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.cachePath = func() (string, error) { return "/unused/no-update-cache.json", nil }
	cmd.lookupGit = func() error { return nil }
	// Hermetic: doctorCheckSpaceAccess would otherwise run a real git
	// clone/fetch against the space's mirror URL.
	cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }

	var stdout, stderr bytes.Buffer
	// Do NOT gate on the exit code. With a connected space configured
	// against a fake host and no real credential, several of the seventeen
	// checks legitimately FAIL (credentials, CI presence, ...) and doctor
	// returns 1 — that is expected, not a test failure. This test is about
	// the roster the binary prints, not about a clean bill of health.
	cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})

	raw, err := os.ReadFile("../../skill/a2ahub/troubleshooting.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)

	fixedNames := make(map[string]bool)
	visibilityRows := 0
	for _, line := range strings.Split(stdout.String(), "\n") {
		name, rest, found := strings.Cut(line, ": ")
		if !found {
			continue
		}
		// Every check line carries one of these four statuses (repository
		// visibility never FAILs but can read PASS/WARN/UNVERIFIED; the
		// fixed seventeen only ever read PASS/FAIL). Filtering on the status
		// prefix, rather than reusing the old "PASS only" cut, is what lets
		// this test see checks that fail against the fake host instead of
		// silently dropping them from the roster.
		switch {
		case strings.HasPrefix(rest, "PASS"), strings.HasPrefix(rest, "FAIL"),
			strings.HasPrefix(rest, "WARN"), strings.HasPrefix(rest, "UNVERIFIED"):
		default:
			continue
		}
		// The visibility row's name carries a trailing " [<space-id>]" that
		// the documented table row does not (and must not: the table lists
		// CHECKS, one row each, not one row per connected space). Strip it
		// before the lookup so the row is still checked against the doc —
		// relaxing the "| **name** |" match instead would let a stray space
		// id silently defeat the lookup.
		rosterName := name
		if idx := strings.Index(rosterName, " ["); idx != -1 {
			rosterName = rosterName[:idx]
		}
		if !strings.Contains(doc, "| **"+rosterName+"** |") {
			t.Errorf("troubleshooting omits doctor check %q", rosterName)
		}
		if rosterName == "repository visibility" {
			visibilityRows++
			continue
		}
		fixedNames[rosterName] = true
	}
	if len(fixedNames) != 17 {
		t.Fatalf("doctor emitted %d fixed checks (%v), want 17; update the documented count and this tripwire together", len(fixedNames), fixedNames)
	}
	// One connected space was configured above, so exactly one visibility
	// row must have been printed (and checked against the doc) — a count of
	// zero would mean the fixture regressed back to proving nothing about
	// the eighteenth row.
	if visibilityRows != 1 {
		t.Fatalf("doctor emitted %d repository-visibility rows, want 1 (one connected space was configured)", visibilityRows)
	}
	if !strings.Contains(doc, "## The seventeen checks") {
		t.Fatal("troubleshooting doctor-count heading does not match the executable list")
	}
}
