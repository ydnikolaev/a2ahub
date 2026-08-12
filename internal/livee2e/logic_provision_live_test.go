//go:build livee2e

package livee2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// provisionLocalSpace brings a brand-new local space to the same clean,
// two-participant scaffold ResetSpace (provision_live.go) brings the
// dedicated GitHub test space to — everything ResetSpace does that is NOT
// GitHub administration (plan D-6):
//
//   - no repo creation — `git init` beneath work IS the creation
//   - no branch protection, no repo settings — provider facts the fake
//     deliberately 404s (testkit/fakegithub.go's own forbidden list)
//   - no branch pruning — a freshly created origin never carries a prior
//     run's branches or pull requests to prune in the first place (compare
//     harness_live.go's own PRFloor doc, which explains why pruning exists
//     on the LIVE tier at all: a reset there reuses one long-lived repo)
//   - no force-push over https — a plain, non-force `git clone --bare` of a
//     brand-new tree needs no force
//
// It reuses provision_live.go's own untagged primitives —
// scaffoldParticipants (itself PatchSpaceParticipants + RenderCODEOWNERS,
// scaffold.go) and gitCommitScaffold — rather than a second, silently
// drifting copy of either; that is ResetSpace's own recorded rule ("never an
// inline heredoc") and it applies here for the same reason.
//
// bin is the already-built, version-stamped a2a binary; work is this run's
// own scratch directory. Returns the bare origin directory the fake will
// serve.
func provisionLocalSpace(ctx context.Context, bin, work string, pre Preflight) (originDir string, err error) {
	spaceDir := filepath.Join(work, "space")
	if err := runCmd(ctx, bin, "space", "init", harnessSpaceSlug, "--dir", spaceDir); err != nil {
		return "", fmt.Errorf("livee2e: provisionLocalSpace: a2a space init: %w", err)
	}

	if err := scaffoldParticipants(spaceDir, logicOrg, pre); err != nil {
		return "", fmt.Errorf("livee2e: provisionLocalSpace: scaffold participants: %w", err)
	}

	for _, sys := range []string{systemAlpha, systemBravo} {
		exchanges := filepath.Join(spaceDir, sys, "exchanges")
		if err := os.MkdirAll(exchanges, 0o755); err != nil {
			return "", fmt.Errorf("livee2e: provisionLocalSpace: mkdir %s: %w", exchanges, err)
		}
		keep := filepath.Join(exchanges, ".gitkeep")
		if err := os.WriteFile(keep, nil, 0o644); err != nil {
			return "", fmt.Errorf("livee2e: provisionLocalSpace: write %s: %w", keep, err)
		}
	}

	if err := gitCommitScaffold(ctx, spaceDir); err != nil {
		return "", fmt.Errorf("livee2e: provisionLocalSpace: %w", err)
	}

	// origin's basename is the plain constant "origin.git" again — a first
	// draft of this function made it per-run UNIQUE (derived from work's
	// own os.MkdirTemp suffix) to route around a real collision: `a2a
	// connect` keys a space's cached mirror location by exactly this
	// basename (cmd_init.go's initSpaceIDFromURL: URL/path, ".git" and
	// trailing slash trimmed) under the OPERATOR'S OWN machine config
	// mirror_root when one is configured, and two separate runs on a
	// machine with mirror_root set collided there — the second run's `a2a
	// connect` found a cached mirror still pointing at the FIRST run's
	// already-removed temp directory. That fix treated the symptom: it made
	// the collision unlikely without stopping the tier writing outside
	// itself. The actual fix is newLogicHarness giving each checkout its
	// own isolated HOME (workspace_live.go's checkout.Home), so no machine
	// config is ever found here and space.ResolveMirrorLocation always
	// falls back to its OWN project-relative default, inside work — at
	// which point the basename is no longer a shared cache key at all, and
	// a constant name is simpler and equally safe.
	// TestLogicTierWritesNothingOutsideItsOwnTempDirs (logic_runner_live_test.go)
	// is the gate that keeps this true.
	origin := filepath.Join(work, "origin.git")
	if err := runCmd(ctx, "git", "clone", "--bare", "-q", spaceDir, origin); err != nil {
		return "", fmt.Errorf("livee2e: provisionLocalSpace: clone bare origin: %w", err)
	}
	// Every push in this tier lands here; receive-pack's detached
	// post-push maintenance would otherwise outlive the test.
	if err := gitfixture.HardenRepoDir(origin); err != nil {
		return "", fmt.Errorf("livee2e: provisionLocalSpace: %w", err)
	}

	return origin, nil
}

// gitShowLogic reads path at ref inside the bare repo at dir (e.g.
// "main:space.yaml") — a direct, fake-free inspection of what
// provisionLocalSpace actually wrote, so this file's own pinning test does
// not need a running fake to verify the scaffold.
func gitShowLogic(t *testing.T, dir, ref string) string {
	t.Helper()
	// Through gitfixture.Args like every other git exec in this tree: it
	// disables git's own background gc/maintenance, which otherwise races
	// t.TempDir()'s cleanup and fails an unrelated test with
	// "unlinkat .git/objects: directory not empty". A hygiene gate enforces
	// this, and it caught these two sites.
	// #nosec G204 -- dir/ref are test-owned values (a temp-dir bare repo this
	// same test created, and a literal ref string), never artifact text.
	cmd := exec.Command("git", gitfixture.Args("-C", dir, "show", ref)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s show %s: %v: %s", dir, ref, err, out)
	}
	return string(out)
}

// TestProvisionLocalSpaceScaffoldsCleanSpace pins the scaffold
// provisionLocalSpace produces, read directly off the bare origin — no fake
// needed for this: space.yaml carries the harness space id and both
// participant logins, CODEOWNERS roots on the provisioner, both systems'
// exchanges/ dirs exist, and main is the only branch (nothing to prune).
func TestProvisionLocalSpaceScaffoldsCleanSpace(t *testing.T) {
	ctx := t.Context()
	work := t.TempDir()

	bin, err := buildLogicBinary(ctx, work)
	if err != nil {
		t.Fatalf("buildLogicBinary: %v", err)
	}

	pre := Preflight{ProvisionerLogin: logicProvisionerLogin, ParticipantLogin: logicParticipantLogin}
	origin, err := provisionLocalSpace(ctx, bin, work, pre)
	if err != nil {
		t.Fatalf("provisionLocalSpace: %v", err)
	}

	spaceYAML := gitShowLogic(t, origin, "main:space.yaml")
	if !strings.Contains(spaceYAML, harnessSpaceSlug) {
		t.Errorf("space.yaml does not carry the harness space id %q:\n%s", harnessSpaceSlug, spaceYAML)
	}
	if !strings.Contains(spaceYAML, logicProvisionerLogin) {
		t.Errorf("space.yaml does not carry the provisioner login %q:\n%s", logicProvisionerLogin, spaceYAML)
	}
	if !strings.Contains(spaceYAML, logicParticipantLogin) {
		t.Errorf("space.yaml does not carry the participant login %q:\n%s", logicParticipantLogin, spaceYAML)
	}

	codeowners := gitShowLogic(t, origin, "main:CODEOWNERS")
	if !strings.Contains(codeowners, logicProvisionerLogin) {
		t.Errorf("CODEOWNERS does not carry the provisioner login %q:\n%s", logicProvisionerLogin, codeowners)
	}

	for _, sys := range []string{systemAlpha, systemBravo} {
		// git show on a missing path exits non-zero; gitShowLogic would
		// t.Fatalf, so this alone is the pinning assertion.
		gitShowLogic(t, origin, "main:"+sys+"/exchanges/.gitkeep")
	}

	// #nosec G204 -- origin is this test's own freshly-cloned temp-dir repo.
	branchesOut, err := exec.Command("git", gitfixture.Args("-C", origin, "for-each-ref", "--format=%(refname:short)", "refs/heads/")...).CombinedOutput()
	if err != nil {
		t.Fatalf("list branches: %v: %s", err, branchesOut)
	}
	branches := strings.Fields(string(branchesOut))
	if len(branches) != 1 || branches[0] != "main" {
		t.Errorf("origin branches = %v, want exactly [main] — a fresh space has nothing to prune", branches)
	}
}
