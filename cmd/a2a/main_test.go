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
	if !strings.Contains(stderr.String(), "https://a2ahub.dev/") {
		t.Fatalf("run(nil) stderr = %q, want canonical project site", stderr.String())
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
	// THE FIRST LINE IS STILL THE STAMP, and that is the part this test
	// guards. `a2a version` stopped being one line on 2026-08-22 — it now
	// reports the release and per-space axes too, because a consumer had no
	// way to answer "which versions am I running?" and an agent wrote to a
	// space from a stale binary for want of one. What must not drift is the
	// stamp's position: anything parsing this output reads line 1.
	first, _, _ := strings.Cut(out, "\n")
	if !strings.HasPrefix(first, "a2a ") || !strings.HasSuffix(first, ")") {
		t.Fatalf("run(version) first line is not the `a2a <version> (<commit>)` stamp: %q", first)
	}
}

// TestRun_versionAliases pins the two spellings every consumer tries first.
// They are aliases of the same dispatch entry, so the assertion is byte
// equality with `version` — a second implementation is what would drift.
func TestRun_versionAliases(t *testing.T) {
	t.Parallel()

	var canonical bytes.Buffer
	if code := run([]string{"version"}, &canonical, &bytes.Buffer{}); code != 0 {
		t.Fatalf("run(version) exit code = %d", code)
	}
	for _, alias := range []string{"--version", "-v"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{alias}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run(%s) exit code = %d, want 0 (stderr=%q)", alias, code, stderr.String())
		}
		if stdout.String() != canonical.String() {
			t.Fatalf("run(%s) differs from run(version):\n%s\n---\n%s", alias, stdout.String(), canonical.String())
		}
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
