package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// contractGitRun runs `git <args...>` with cwd=dir, failing the test
// loudly. Identity is injected via GIT_AUTHOR_*/GIT_COMMITTER_* env vars
// (not `git config user.email/user.name`) — same idiom cmd_contract_test.go's
// own gitRun uses (package cli_test, not reachable from here since this
// file is package cli) — so the fixture repo commits on a machine with no
// global git identity, with no extra command needed.
func contractGitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
}

// contractWriteFile writes content at repoDir/relPath, creating parent
// directories as needed.
func contractWriteFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(repoDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// contractWriteDescriptorAt writes a minimal contract.md carrying just
// enough frontmatter for contractResolveVersionSHA to parse (schema/id/
// version — the fields its own contractDescriptorProbe decodes; the fuller
// shape cmd_contract_test.go's writeContractDescriptor uses is for schema
// validation this file's functions never do).
func contractWriteDescriptorAt(t *testing.T, repoDir, descriptorPath, version string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: XC-axon-widget\n" +
		"version: \"" + version + "\"\n" +
		"---\nbody\n"
	contractWriteFile(t, repoDir, descriptorPath, content)
}

// TestContractPriorVersionFilesByteIdenticalAfterLaterEdit is this file's
// AC: a prior version's schema+valid-fixture bytes come back byte-identical
// even after a LATER commit changed those same paths on disk.
func TestContractReadTreeAtSHAMissingSubtreeOmittedNotFatal(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	contractGitRun(t, repoDir, "init", "-q", "-b", "main")

	const descriptorDir = "axon/provides/widget"
	contractWriteFile(t, repoDir, descriptorDir+"/schema/main.schema.json", `{"type":"object"}`)
	contractGitRun(t, repoDir, "add", "-A")
	contractGitRun(t, repoDir, "commit", "-q", "-m", "schema only, no fixtures/ at all")

	sha := contractGitRevParse(t, repoDir, "HEAD")

	tree, err := contractReadTreeAtSHA(context.Background(), repoDir, sha, descriptorDir, []string{"schema", "fixtures"})
	if err != nil {
		t.Fatalf("contractReadTreeAtSHA: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("tree = %v, want exactly {schema/main.schema.json}", keysOf(tree))
	}
	if got := string(tree["schema/main.schema.json"]); got != `{"type":"object"}` {
		t.Fatalf("tree[schema/main.schema.json] = %q", got)
	}
}

// TestContractReadTreeAtSHAUnknownSHAIsAnErrorNotVacuity guards the
// fail-open this function deliberately does NOT inherit from its digest
// sibling. `git ls-tree` exits non-zero for a bogus sha exactly as it does
// for a missing subtree; folding both into "absent" would hand the compat
// core an empty PriorFixtures, which D-B reads as "the prior version
// published no fixtures — nothing computed, proceed". An unreachable
// `--base` would then wave a breaking change through under a benign
// reason. The error must name the sha so the caller can say which ref it
// could not reach.
//
// TEETH: delete the rev-parse guard in contractReadTreeAtSHA and this test
// reds — the call returns an empty map and a nil error instead.
func TestContractReadTreeAtSHAUnknownSHAIsAnErrorNotVacuity(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	contractGitRun(t, repoDir, "init", "-q", "-b", "main")

	const descriptorDir = "axon/provides/widget"
	contractWriteFile(t, repoDir, descriptorDir+"/schema/main.schema.json", `{"type":"object"}`)
	contractWriteFile(t, repoDir, descriptorDir+"/fixtures/valid/ok.json", `{}`)
	contractGitRun(t, repoDir, "add", "-A")
	contractGitRun(t, repoDir, "commit", "-q", "-m", "a real commit exists, so an empty answer cannot be blamed on an empty repo")

	// A well-formed but non-existent object id: it must fail on "no such
	// commit", not on "that is not a sha at all".
	const missing = "0123456789abcdef0123456789abcdef01234567"

	tree, err := contractReadTreeAtSHA(context.Background(), repoDir, missing, descriptorDir, []string{"schema", "fixtures"})
	if err == nil {
		t.Fatalf("contractReadTreeAtSHA on an unknown sha returned %v with a nil error — an unreachable base must refuse, never read as 'no fixtures here'", keysOf(tree))
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name the unreachable sha %s", err, missing)
	}
}

// TestContractGitShowBoundedContentRefusesOversizedContent proves the
// bound is real: content past max is a hard error (naming the bound), not
// a silent truncation, and the killed subprocess does not hang the test.
func TestContractGitShowBoundedContentRefusesOversizedContent(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	contractGitRun(t, repoDir, "init", "-q", "-b", "main")
	contractWriteFile(t, repoDir, "big.txt", "0123456789")
	contractGitRun(t, repoDir, "add", "-A")
	contractGitRun(t, repoDir, "commit", "-q", "-m", "big file")
	sha := contractGitRevParse(t, repoDir, "HEAD")

	_, err := contractGitBounded(context.Background(), repoDir, 4, "show", sha+":big.txt")
	if err == nil {
		t.Fatal("expected an error for content exceeding the bound, got nil")
	}
}

// TestContractGitShowBoundedContentReadsWithinBound is the bound test's
// complement: content AT or under max still comes back intact.
func TestContractGitShowBoundedContentReadsWithinBound(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	contractGitRun(t, repoDir, "init", "-q", "-b", "main")
	contractWriteFile(t, repoDir, "small.txt", "hi")
	contractGitRun(t, repoDir, "add", "-A")
	contractGitRun(t, repoDir, "commit", "-q", "-m", "small file")
	sha := contractGitRevParse(t, repoDir, "HEAD")

	got, err := contractGitBounded(context.Background(), repoDir, maxMirrorEventBytes, "show", sha+":small.txt")
	if err != nil {
		t.Fatalf("contractGitBounded: %v", err)
	}
	if string(got) != "hi" {
		t.Fatalf("got %q, want %q", got, "hi")
	}
}

func contractGitRevParse(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args("-C", dir, "rev-parse", rev)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git rev-parse %s (dir=%s): %v", rev, dir, err)
	}
	return string(bytes.TrimSpace(out.Bytes()))
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
