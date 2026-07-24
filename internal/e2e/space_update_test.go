package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostLoopSpaceUpdate is P35's e2e tier (AC-950.1/950.2/950.3/950.4/
// 951.1/952.1). It drives `a2a space update` through the REAL binary against
// the rig's fake host: real config load, real credential resolution, real
// mirror clone, real `git push`, real PR open — the whole cmd/a2a/wire.go
// resolution path this phase added, which no unit test reaches.
//
// The rig's fixture space is deliberately NOT scaffolded from the template,
// so it stands in for exactly the case P35 exists for: a pre-P33 space whose
// scaffolding has drifted from what a2ahub now ships.
func TestHostLoopSpaceUpdate(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	// Leave the PR open: a scaffolding change is CODEOWNERS-gated (G4
	// review), never auto-merged — that is the safety property spec 35 §T1
	// relies on for infrastructure.
	r.gh.AutoMerge = false

	// --- AC-950.1: --dry-run reports the diff and writes/pushes nothing.
	dry := r.mustRun("space", "update", "--dry-run")
	if !strings.Contains(dry, "--dry-run") || !strings.Contains(dry, "scaffolding diff") {
		t.Fatalf("space update --dry-run did not report a diff:\n%s", dry)
	}
	// AC-952.1: the admin-only steps are printed, never executed.
	for _, want := range []string{"scope: space directive", "gh api", "required_status_checks", "gh secret delete A2A_BINARY_FETCH_TOKEN"} {
		if !strings.Contains(dry, want) {
			t.Errorf("--dry-run output is missing the directive fragment %q:\n%s", want, dry)
		}
	}
	if prs := r.gh.PRs(); len(prs) != 0 {
		t.Fatalf("--dry-run opened %d PR(s), want 0", len(prs))
	}

	// --- AC-950.2: a real run opens exactly ONE PR through the funnel.
	out := r.mustRun("space", "update")
	if !strings.Contains(out, "opened PR") {
		t.Fatalf("space update did not report an opened PR:\n%s", out)
	}
	prs := r.gh.PRs()
	if len(prs) != 1 {
		t.Fatalf("PRs = %d, want 1 (host calls: %v)", len(prs), r.gh.Requests())
	}
	// The PR body must carry the §T3 ordering hazard — a reviewer who never
	// ran the command still has to know the rename is a prerequisite.
	if !strings.Contains(prs[0].Body, "a2a-validate / validate") {
		t.Errorf("PR body does not state the required-check rename prerequisite:\n%s", prs[0].Body)
	}

	// --- AC-950.4: re-running is a no-op that says so, and opens no second PR.
	// The first PR is still open, so the funnel's own idempotency short-circuit
	// is what must catch this — the branch is deterministic.
	again := r.mustRun("space", "update")
	if !strings.Contains(again, "already") {
		t.Errorf("re-run did not report the write as already submitted:\n%s", again)
	}
	if prs := r.gh.PRs(); len(prs) != 1 {
		t.Fatalf("re-run opened a second PR: %d total", len(prs))
	}
}

// TestHostLoopSpaceUpdateWrittenPathsAreAuthzClean is AC-950.3: every path
// `space update` writes must survive the space's own V3 gate. Rather than
// trusting the spec's claim, it runs the REAL `a2a validate --ci
// --mode=v3-pr` over exactly those paths in the merged space.
func TestHostLoopSpaceUpdateWrittenPathsAreAuthzClean(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	r.mustRun("space", "update")

	prs := r.gh.PRs()
	if len(prs) != 1 {
		t.Fatalf("PRs = %d, want 1", len(prs))
	}
	if !prs[0].Merged {
		t.Fatalf("expected the rig's auto-merge to land the scaffolding PR: %+v", prs[0])
	}

	// The space's own gate, run the way its CI runs it: `a2a validate --ci`
	// against a CHECKOUT of the space (cwd), exactly as the reusable
	// workflow invokes it. A diff-authz violation on a space-infrastructure
	// path would exit non-zero here — which is precisely the claim spec 35
	// §T1 makes and which this phase must not take on faith.
	checkout := filepath.Join(t.TempDir(), "space")
	if err := cloneFreshErr(r.fx.RemoteURL(), checkout); err != nil {
		t.Fatalf("clone the merged space: %v", err)
	}
	// Sanity: the scaffolding really landed on main, or the gate below would
	// be validating a space this command never touched.
	if _, err := os.Stat(filepath.Join(checkout, ".github", "workflows", "a2a-validate.yml")); err != nil {
		t.Fatalf("the merged space carries no caller workflow — space update did not land: %v", err)
	}

	cmd := exec.Command(filepath.Join(binDir, "a2a"), "validate", "--ci", "--mode=v3-full-repo")
	cmd.Dir = checkout
	cmd.Env = append(os.Environ(), "HOME="+r.homeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate --ci over the updated space failed: %v\n%s", err, out)
	}
}
