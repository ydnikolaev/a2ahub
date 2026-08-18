package contractwiring

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestFreezePublicationCandidateFreezesMirrorCandidate exercises
// FreezePublicationCandidate's no-staging branch against a real one-commit
// git mirror — the same production path cmd/a2a's contractP6Core.publish
// (via freezePublicationCandidate) and cmd/a2a's own offline integration
// test both drive, just with a minimal single-file candidate rather than a
// full product-rendered descriptor.
func TestFreezePublicationCandidateFreezesMirrorCandidate(t *testing.T) {
	t.Parallel()

	mirrorDir := t.TempDir()
	contractDir := filepath.Join(mirrorDir, "axon", "provides", "orders")
	if err := os.MkdirAll(contractDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contractDir, "contract.md"), []byte("descriptor\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runFixtureGit(t, mirrorDir, "init", "-q")
	runFixtureGit(t, mirrorDir, "add", "-A")
	runFixtureGit(t, mirrorDir, "commit", "-q", "-m", "seed candidate")

	reader, source, err := FreezePublicationCandidate(context.Background(), "axon", "", mirrorDir, "XC-axon-orders", "")
	if err != nil {
		t.Fatalf("FreezePublicationCandidate: %v", err)
	}
	if source.Kind != contract.CandidateSourceMirror || source.Fingerprint == "" {
		t.Fatalf("source = %+v, want a non-empty mirror-sourced fingerprint", source)
	}
	snapshot, err := reader.ReadContractPublicationCandidate(context.Background())
	if err != nil {
		t.Fatalf("ReadContractPublicationCandidate: %v", err)
	}
	if snapshot.Fingerprint != source.Fingerprint {
		t.Fatalf("snapshot fingerprint %q != source fingerprint %q", snapshot.Fingerprint, source.Fingerprint)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Path != "contract.md" || string(snapshot.Files[0].Raw) != "descriptor\n" {
		t.Fatalf("snapshot files = %+v, want exactly contract.md = %q", snapshot.Files, "descriptor\n")
	}

	// A second read of the same frozen candidate must return the identical
	// bytes and fingerprint — proving ReadContractPublicationCandidate
	// re-reads its own frozen snapshot rather than the live filesystem.
	again, err := reader.ReadContractPublicationCandidate(context.Background())
	if err != nil {
		t.Fatalf("second ReadContractPublicationCandidate: %v", err)
	}
	if again.Fingerprint != snapshot.Fingerprint {
		t.Fatalf("second read fingerprint %q != first read fingerprint %q", again.Fingerprint, snapshot.Fingerprint)
	}
}

// TestFreezePublicationCandidateOverlaysStagingOnMirror exercises
// FreezePublicationCandidate's staging branch: a mirror candidate (one
// commit, legacy fixed-v1 shape — no `artifacts:` inventory) overlaid by a
// staging directory carrying a revised descriptor. This is the branch
// TestFreezePublicationCandidateFreezesMirrorCandidate does not reach, and
// the one production path exercises when a caller supplies --staging (this
// package's own doc comment on Host names contractP6Core as the sole
// production caller of both branches).
func TestFreezePublicationCandidateOverlaysStagingOnMirror(t *testing.T) {
	t.Parallel()

	mirrorDir := t.TempDir()
	contractDir := filepath.Join(mirrorDir, "axon", "provides", "orders")
	if err := os.MkdirAll(contractDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mirrorDescriptor := "---\nschema: envelope/v1\nid: XC-axon-orders\nversion: 0.0.0\n---\n# Orders\n"
	if err := os.WriteFile(filepath.Join(contractDir, "contract.md"), []byte(mirrorDescriptor), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runFixtureGit(t, mirrorDir, "init", "-q")
	runFixtureGit(t, mirrorDir, "add", "-A")
	runFixtureGit(t, mirrorDir, "commit", "-q", "-m", "seed candidate")

	projectRoot := t.TempDir()
	stagingRel := "staging"
	stagingDir := filepath.Join(projectRoot, stagingRel)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stagedDescriptor := "---\nschema: envelope/v1\nid: XC-axon-orders\nversion: 1.0.0\n---\n# Orders, revised\n"
	if err := os.WriteFile(filepath.Join(stagingDir, "contract.md"), []byte(stagedDescriptor), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reader, source, err := FreezePublicationCandidate(context.Background(), "axon", projectRoot, mirrorDir, "XC-axon-orders", stagingRel)
	if err != nil {
		t.Fatalf("FreezePublicationCandidate: %v", err)
	}
	if source.Kind != contract.CandidateSourceStaging || source.Location != stagingRel || source.Fingerprint == "" {
		t.Fatalf("source = %+v, want a non-empty staging-sourced fingerprint at %q", source, stagingRel)
	}
	if got := reader.ContractPublicationCandidateSource(); got != source {
		t.Fatalf("reader.ContractPublicationCandidateSource() = %+v, want the returned source %+v", got, source)
	}
	snapshot, err := reader.ReadContractPublicationCandidate(context.Background())
	if err != nil {
		t.Fatalf("ReadContractPublicationCandidate: %v", err)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Path != "contract.md" || string(snapshot.Files[0].Raw) != stagedDescriptor {
		t.Fatalf("snapshot files = %+v, want exactly the staged descriptor", snapshot.Files)
	}
}

// runFixtureGit runs git inside dir with background maintenance disabled
// (gitfixture.Args — see its own package doc for why every fixture git
// invocation in this repo goes through it) and a deterministic committer
// identity.
func runFixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (dir=%q): %v\n%s", args, dir, err, out)
	}
}
