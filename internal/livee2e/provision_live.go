//go:build livee2e

package livee2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// testSpaceDescription is written on the ONE throwaway repo this tier ever
// creates or resets — docs/runbooks/live-e2e/reset.sh's own text, ported
// verbatim: it is what a human sees on the repo page, and "nothing here is
// real" earns its keep on a space a mis-set env could otherwise reach.
const testSpaceDescription = "a2ahub live-e2e test space — reset by the harness (spec 36). Nothing here is real."

// requiredCheckContext is the compound required check branch protection
// names (spec 33 §11 / spec 34 §2) — the exact string internal/host's
// CheckStatus resolves against (AC-961.1).
const requiredCheckContext = "a2a-validate / validate"

// ResetSpace brings the dedicated test space to the clean, two-participant
// scaffold every run starts from (spec 36 §T4 idempotency; ports
// docs/runbooks/live-e2e/reset.sh with the two corrections the plan
// requires: logins are PARAMETERS — read from h.Pre, never literals — and
// the scaffold text comes from the sibling's PatchSpaceParticipants /
// RenderCODEOWNERS, never an inline heredoc).
//
// Sequence mirrors reset.sh: refuse anything outside the dedicated org
// (Config.CheckRepo — no second guard), ensure the repo exists, scaffold via
// `a2a space init`, patch space.yaml + write CODEOWNERS, seed both systems'
// exchanges/ dirs, commit, drop protection (a 404 there is a no-op — the
// first-ever run has none), force-push main, prune every non-main branch,
// then re-arm repo settings and protection MIRRORING PRODUCTION
// (enforce_admins: false — §T6-a: the rig tests the config production
// actually runs, not a hardened one it does not).
func (h *harness) ResetSpace(ctx context.Context) error {
	if err := h.Cfg.CheckRepo(h.Org, h.Repo); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: %w", err)
	}

	exists, err := h.Prov.Repo(ctx, h.Org, h.Repo)
	if err != nil {
		return fmt.Errorf("livee2e: ResetSpace: check repo exists: %w", err)
	}
	if !exists {
		if err := h.Prov.CreateRepo(ctx, h.Org, h.Repo, testSpaceDescription); err != nil {
			return fmt.Errorf("livee2e: ResetSpace: create repo: %w", err)
		}
	}

	work, err := os.MkdirTemp("", "livee2e-reset-*")
	if err != nil {
		return fmt.Errorf("livee2e: ResetSpace: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	spaceDir := filepath.Join(work, "space")

	if err := runCmd(ctx, h.Bin, "space", "init", h.SpaceSlug, "--dir", spaceDir); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: a2a space init: %w", err)
	}

	if err := scaffoldParticipants(spaceDir, h.Org, h.Pre); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: scaffold participants: %w", err)
	}
	for _, sys := range []string{systemAlpha, systemBravo} {
		exchanges := filepath.Join(spaceDir, sys, "exchanges")
		if err := os.MkdirAll(exchanges, 0o755); err != nil {
			return fmt.Errorf("livee2e: ResetSpace: mkdir %s: %w", exchanges, err)
		}
		keep := filepath.Join(exchanges, ".gitkeep")
		if err := os.WriteFile(keep, nil, 0o644); err != nil {
			return fmt.Errorf("livee2e: ResetSpace: write %s: %w", keep, err)
		}
	}

	if err := gitCommitScaffold(ctx, spaceDir); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: %w", err)
	}

	// Protection forbids force-pushing main; drop it for the reset push and
	// re-arm it below. A 404 (no protection yet) is a no-op, not an error —
	// the first-ever run has nothing to remove.
	if err := h.Prov.DeleteProtection(ctx, h.Org, h.Repo, "main"); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: delete protection: %w", err)
	}

	remoteURL := "https://github.com/" + h.Org + "/" + h.Repo + ".git"
	if err := gitForcePush(ctx, spaceDir, remoteURL, h.Cfg.ProvisionerToken, "HEAD", "main"); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: force-push main: %w", err)
	}

	branches, err := h.Prov.ListBranches(ctx, h.Org, h.Repo)
	if err != nil {
		return fmt.Errorf("livee2e: ResetSpace: list branches: %w", err)
	}
	for _, b := range branches {
		if b == "main" {
			continue
		}
		if err := h.Prov.DeleteRef(ctx, h.Org, h.Repo, "heads/"+b); err != nil {
			return fmt.Errorf("livee2e: ResetSpace: prune branch %s: %w", b, err)
		}
	}

	if err := h.Prov.PatchRepo(ctx, h.Org, h.Repo, map[string]any{
		"allow_auto_merge":       true,
		"delete_branch_on_merge": true,
	}); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: patch repo settings: %w", err)
	}

	if err := h.Prov.PutProtection(ctx, h.Org, h.Repo, "main", map[string]any{
		"required_status_checks": map[string]any{
			"strict": false,
			"checks": []map[string]any{{"context": requiredCheckContext}},
		},
		// §T6-a: mirrors production deliberately. Setting this true would
		// test a config production does not run.
		"enforce_admins": false,
		"required_pull_request_reviews": map[string]any{
			"require_code_owner_reviews":      true,
			"required_approving_review_count": 0,
		},
		"restrictions":       nil,
		"allow_force_pushes": false,
		"allow_deletions":    false,
	}); err != nil {
		return fmt.Errorf("livee2e: ResetSpace: arm protection: %w", err)
	}

	// The last thing a reset does: record the PR number every write in THIS
	// run must exceed.
	//
	// Live run 3 (2026-07-25) is why. `contract-publish-deprecate-retire`
	// failed with "more than one branch matches this artifact id ... matches
	// 2 branches" on a space that had just been reset. The branch pruning
	// above is not the gap — it deletes every non-main ref, and it worked.
	// The residue is PULL REQUESTS: GitHub has no API to delete one, a
	// closed PR keeps its `head.ref` recorded forever, and every lookup here
	// lists `state=all` (it must — this space auto-merges, so a fast-green
	// write is closed before the caller looks). So each run's writes reuse
	// the same deterministic branch names as the last run's ghosts, and
	// matchCompositeBranch correctly refuses an ambiguity the harness
	// manufactured.
	//
	// Fixing it at the matcher would be wrong twice over: an ambiguity
	// WITHIN one run is a real defect the refusal must keep catching, and a
	// run-unique slug would hide the collision while letting the listing
	// grow forever. The harness's actual question was always "the PR THIS
	// RUN opened" — it was just asking "any PR ever". This is that question,
	// written down.
	//
	// Set AFTER everything else so a PR opened by provisioning itself (none
	// today) could never land above the floor.
	floor, err := h.maxPRNumber(ctx)
	if err != nil {
		return fmt.Errorf("livee2e: ResetSpace: record PR floor: %w", err)
	}
	h.PRFloor = floor

	return nil
}

// maxPRNumber returns the highest pull-request number the space currently
// carries, or 0 for a space that has never had one. Unfiltered by design —
// it is what ESTABLISHES the filter.
func (h *harness) maxPRNumber(ctx context.Context) (int, error) {
	pulls, err := h.Prov.ListPulls(ctx, h.Org, h.Repo, "all")
	if err != nil {
		return 0, err
	}
	max := 0
	for _, p := range pulls {
		if p.Number > max {
			max = p.Number
		}
	}
	return max, nil
}

// scaffoldParticipants patches the freshly-scaffolded space.yaml with the
// two-system topology and writes CODEOWNERS, using the sibling's untagged
// renderers — never an inline heredoc (plan §"Constraints a faithful port
// would get wrong"). Owners come from Preflight, never a literal login.
func scaffoldParticipants(spaceDir, org string, pre Preflight) error {
	joined := time.Now().UTC().Format("2006-01-02")
	participants := []Participant{
		{System: systemAlpha, Org: org, Section: systemAlpha + "/", Owner: pre.ProvisionerLogin, Joined: joined},
		{System: systemBravo, Org: org, Section: systemBravo + "/", Owner: pre.ParticipantLogin, Joined: joined},
	}

	manifestPath := filepath.Join(spaceDir, "space.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read space.yaml: %w", err)
	}
	patched, err := PatchSpaceParticipants(string(raw), participants)
	if err != nil {
		return fmt.Errorf("patch space.yaml participants: %w", err)
	}
	if err := os.WriteFile(manifestPath, []byte(patched), 0o644); err != nil {
		return fmt.Errorf("write space.yaml: %w", err)
	}

	codeowners := RenderCODEOWNERS(pre.ProvisionerLogin)
	codeownersPath := filepath.Join(spaceDir, "CODEOWNERS")
	if err := os.WriteFile(codeownersPath, []byte(codeowners), 0o644); err != nil {
		return fmt.Errorf("write CODEOWNERS: %w", err)
	}
	return nil
}

// gitCommitScaffold init/adds/commits the freshly scaffolded tree, mirroring
// reset.sh's own git invocation (a bare `git init -b main` + one commit —
// this is a throwaway working tree, never a real history).
func gitCommitScaffold(ctx context.Context, dir string) error {
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "a2a live-e2e"},
		{"config", "user.email", "live-e2e@a2ahub.invalid"},
		{"add", "-A"},
		{"commit", "-q", "-m", "chore: reset live-e2e space to a clean scaffold"},
	}
	for _, args := range steps {
		full := append([]string{"-C", dir}, args...)
		if err := runCmd(ctx, "git", full...); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// gitForcePush force-pushes localRef to branch on remoteURL, authenticated
// via a per-invocation `-c http.extraheader` git config override — the same
// credential-handling shape internal/host.GitHubHost.PushBranch uses
// (internal/host/github.go), never a token embedded in the remote URL (which
// git happily echoes back into an error message on failure; reset.sh's own
// `x-access-token:$PROV@github.com/...` form is exactly what this
// deliberately does NOT port — see the wave report's deviations).
//
// --force is added because this function exists specifically to reset a
// possibly-diverged history to a fresh scaffold; PushBranch's own non-force
// push is the wrong primitive for that, and is not widened here to add
// it — every existing PushBranch caller wants a fast-forward guarantee,
// which this one deliberately does not.
func gitForcePush(ctx context.Context, dir, remoteURL, token, localRef, branch string) error {
	basicAuth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	args := []string{
		"-C", dir,
		"-c", "http.extraheader=AUTHORIZATION: basic " + basicAuth,
		"push", "--force", remoteURL, localRef + ":refs/heads/" + branch,
	}
	return runCmd(ctx, "git", args...)
}

// runCmd execs name with args, capturing stderr for an actionable error.
func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
