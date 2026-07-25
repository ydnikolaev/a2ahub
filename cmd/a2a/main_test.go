package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens this package's own git spawns against git's gc --auto
// grandchild racing a t.TempDir() cleanup's RemoveAll — see
// testkit/gitfixture/gitfixture.go's package doc for the flake this
// prevents. cmd/a2a's own production code has no direct git exec site, but
// a test in this package (mcp_equivalence_test.go's CC-093 suite, and any
// sibling driving runContract in-process) calls space.CloneOrFetch, whose
// git IS a grandchild of this test binary — that's what this hook makes
// safe.
func TestMain(m *testing.M) {
	os.Exit(gitfixture.RunTests(m))
}

func TestRun_noSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run(nil) exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("run(nil) wrote to stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("run(nil) stderr = %q, want usage text", stderr.String())
	}
}

func TestRun_version(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run(version) exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("run(version) wrote to stderr: %q", stderr.String())
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("run(version) produced an empty stamp")
	}
	if strings.Count(out, "\n") != 0 {
		t.Fatalf("run(version) stamp is not one line: %q", out)
	}
}

func TestRun_unknownCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run(bogus) exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("run(bogus) wrote to stdout: %q", stdout.String())
	}
	want := `unknown command "bogus"`
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("run(bogus) stderr = %q, want to contain %q", stderr.String(), want)
	}
}

func TestVersionStamp_nonEmpty(t *testing.T) {
	t.Parallel()

	if versionStamp() == "" {
		t.Fatal("versionStamp() is empty")
	}
}
