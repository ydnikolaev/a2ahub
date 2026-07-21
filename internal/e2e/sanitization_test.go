package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestSanitization is spec 10 §8 AC-7 (13.4, narrowed to the statusline
// surface per §11's amendment — v1-min has no dashboard/local-HTML render
// surface): malicious titles/bodies (script tags, raw HTML, control
// characters) render INERT on `a2a statusline`'s output. "Inert" here
// means exactly what §7.5's own contract already guarantees rather than
// anything this phase adds: the rendered line stays a SINGLE line (no
// embedded control character breaks out of the one-line invariant or
// injects a terminal escape sequence) and the raw content is visible as
// PLAIN TEXT (Go's %q quoting, which cache.Store.Statusline's own
// fmt.Fprintf %q verb already applies to a title) — never executed,
// because a one-line terminal string has no markup-consuming surface to
// execute against in the first place.
func TestSanitization(t *testing.T) {
	t.Run("script_tag_and_raw_html", func(t *testing.T) {
		t.Parallel()
		stdout := runStatuslineWithFixture(t, "script-tag.md", "XQ-beta-20260721-xss1")
		assertSingleLine(t, stdout)
		if !strings.Contains(stdout, "<script>alert(1)</script>") {
			t.Fatalf("expected the malicious title to render as visible, INERT plain text (not stripped), got:\n%q", stdout)
		}
	})

	t.Run("control_chars_stay_one_line", func(t *testing.T) {
		t.Parallel()
		stdout := runStatuslineWithFixture(t, "control-chars.md", "XQ-beta-20260721-ctl1")
		assertSingleLine(t, stdout)
		if strings.ContainsRune(stdout, '\x1b') || strings.ContainsRune(stdout, '\x00') {
			t.Fatalf("expected NO raw control bytes in the rendered line (must be %%q-escaped, not passed through), got:\n%q", stdout)
		}
		if !strings.Contains(stdout, "FAKE-LINE") {
			t.Fatalf("expected the malicious title's visible text to still appear (rendered inert, not silently dropped), got:\n%q", stdout)
		}
	})
}

// runStatuslineWithFixture seeds ONE malicious artifact (from testdata/
// sanitization/<fixtureFile>, addressed beta -> axon, p1 priority so it is
// actionable/urgent and actually renders) into a throwaway fixture space,
// then execs the BUILT a2a binary's `statusline` verb as axon and returns
// its stdout.
func runStatuslineWithFixture(t *testing.T, fixtureFile, id string) string {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("axon")
	// See helpers_test.go's properManifestYAML doc comment: the exec'd
	// binary's own read surface needs a LIST-shaped space.yaml, not
	// spacefixture's own auto-seeded MAP-shaped one. mirrorDir is read
	// directly (never re-fetched), so a plain on-disk overwrite suffices.
	writeMirrorFile(t, mirrorDir, "space.yaml", properManifestYAML("fixture-space", "axon", "beta"))

	root := repoRootForTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "internal/e2e/testdata/sanitization", fixtureFile))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixtureFile, err)
	}
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+id+".md", string(raw))
	writeLifecycleEvent(t, mirrorDir, "beta", 0, id, "submit", "beta")

	stdout, stderr, code := runReadVerbAs(t, mirrorDir, "fixture-space", "axon", "statusline")
	if code != 11 && code != 10 {
		t.Fatalf("statusline: exit = %d, want 10 or 11 (an urgent/pending p1 item is present); stderr=%s", code, stderr)
	}
	return stdout
}

func assertSingleLine(t *testing.T, stdout string) {
	t.Helper()
	trimmed := strings.TrimRight(stdout, "\n")
	if trimmed == "" {
		t.Fatal("expected a non-empty statusline (a p1 item addressed to axon is pending)")
	}
	if strings.Count(trimmed, "\n") != 0 {
		t.Fatalf("expected exactly ONE line (no embedded newline broke the one-line invariant), got:\n%q", stdout)
	}
}
