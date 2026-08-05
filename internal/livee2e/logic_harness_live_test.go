//go:build livee2e

package livee2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/testkit/fakegithub"
)

// logicOrg/logicRepo are the local tier's own stand-ins for Config.Org/
// DefaultRepo (harness_live.go, catalogue.go). testkit/fakegithub never
// validates either string against anything it serves (empirical fact #3 in
// this wave's brief: cmd/a2a's parseGitHubRepo takes only the last two
// `/`-separated segments of whatever it is handed), so these exist to keep
// h.Org/h.Repo — and any failure message that names them — legible, never to
// name a real GitHub location.
const (
	logicOrg  = "livee2e-logic"
	logicRepo = "logic-space"
)

// logicProvisionerCredential/logicParticipantCredential are the two
// synthetic credentials LogicProber and the harness's ghClients answer for.
// They are opaque fixture strings, never real secrets: testkit/fakegithub
// authenticates nothing (fakegithub.go's own package doc — no route ever
// inspects the Authorization header), so any two DISTINCT values here
// satisfy RunPreflight's "two distinct logins" guard the same way a real
// pair of machine-account tokens would.
const (
	logicProvisionerCredential = "logic-provisioner-credential"
	logicParticipantCredential = "logic-participant-credential"
	logicProvisionerLogin      = "logic-provisioner"
	logicParticipantLogin      = "logic-participant"
)

// logicRepoRoot resolves the product repo root from this source file's own
// location (internal/livee2e/logic_harness_live_test.go -> ../.. is the repo
// root) — the same runtime.Caller-based, go.mod-validated approach
// internal/e2e/main_test.go's own repoRoot uses, rather than trusting the
// test's cwd.
func logicRepoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("livee2e: logicRepoRoot: runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("livee2e: logicRepoRoot: %w", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		return "", fmt.Errorf("livee2e: logicRepoRoot: resolved root %s has no go.mod: %w", abs, err)
	}
	return abs, nil
}

// buildLogicBinary builds a plain, version-stamped `a2a` binary into dir —
// no candidate attestation, no detached-checkout ceremony (unlike
// workspace_live.go's buildBinary): the logic tier proves scenario LOGIC,
// never release evidence (plan D-4), so there is nothing here for an
// attestation to bind.
//
// The stamp is contract.ContractPublicationFloor, never a literal in this
// file: it is the highest write floor the product itself currently
// declares (internal/contract/publication_plan.go — "part of the public
// package API"), strictly at or above space-template/space.yaml's own
// min_binary_version. `a2a space init` writes min_binary_version from the
// RUNNING BINARY's own version (internal/cli/cmd_space.go's
// spaceApplySubstitutions), so this stamp is self-consistent with whatever
// it scaffolds either way — but asking the product for its own floor,
// instead of a literal chosen here, is what stays correct if that floor
// ever moves out from under an unrelated check (CC-085) this rig does not
// otherwise exercise.
func buildLogicBinary(ctx context.Context, dir string) (string, error) {
	root, err := logicRepoRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "a2a")
	// #nosec G204 -- executable/package/ldflags are fixed literals; the only
	// variable is a product-owned version constant, never artifact text.
	cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false",
		"-ldflags", "-X main.version="+contract.ContractPublicationFloor,
		"-o", bin, "./cmd/a2a")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("livee2e: buildLogicBinary: go build ./cmd/a2a: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return bin, nil
}

// newLogicHarness is the LOCAL sibling of newHarness (harness_live.go): it
// builds and stamps the binary, provisions a brand-new local space
// (provisionLocalSpace, logic_provision_live_test.go), starts
// testkit/fakegithub over it, and wires the SAME *harness type every
// scenario family already takes — but VerificationCandidate and
// ExecutionCandidate are left at their zero value (D-4: see
// TestNewLogicHarnessLeavesExecutionCandidateZero below for why that
// property is asserted, not assumed).
//
// t is required (not merely a *testing.T convenience) because
// testkit/fakegithub.New takes a testing.TB — the fake registers its own
// httptest.Server shutdown via t.Cleanup, so every file that touches it is a
// _test.go file (this wave's brief, "Empirical facts" section).
//
// The returned cleanup func removes this run's own temp workspace; callers
// must defer it unconditionally and report its error, exactly like
// newHarness's own contract (it is never nil, even on an error return).
func newLogicHarness(ctx context.Context, t *testing.T) (*harness, func() error, error) {
	t.Helper()
	noop := func() error { return nil }

	cfg := Config{Org: logicOrg, ProvisionerToken: logicProvisionerCredential, ParticipantToken: logicParticipantCredential}
	prober := NewLogicProber(logicProvisionerCredential, logicProvisionerLogin, logicParticipantCredential, logicParticipantLogin)
	// RunPreflight is called for real, with this prober, rather than
	// stubbed out — the identity model the live tier asserts (spec 36 §T5)
	// is the same shape asserted here, just answered from a static map
	// instead of a network call (logicprober.go's own doc).
	pre, err := RunPreflight(ctx, prober, cfg, logicOrg, logicRepo)
	if err != nil {
		return nil, noop, fmt.Errorf("livee2e: newLogicHarness: preflight: %w", err)
	}

	work, err := os.MkdirTemp("", "livee2e-logic-*")
	if err != nil {
		return nil, noop, fmt.Errorf("livee2e: newLogicHarness: %w", err)
	}
	cleanup := func() error {
		if err := os.RemoveAll(work); err != nil {
			return fmt.Errorf("livee2e: newLogicHarness: remove workspace %s: %w", work, err)
		}
		return nil
	}

	bin, err := buildLogicBinary(ctx, work)
	if err != nil {
		return nil, cleanup, fmt.Errorf("livee2e: newLogicHarness: %w", err)
	}

	origin, err := provisionLocalSpace(ctx, bin, work, pre)
	if err != nil {
		return nil, cleanup, fmt.Errorf("livee2e: newLogicHarness: %w: %w", ErrProvisionFailed, err)
	}

	fake := fakegithub.New(t, origin)

	seam := NewLocalHostSeam(fake.URL, origin)
	// Validated and asserted non-real-GitHub BEFORE the first write this
	// harness's checkouts will make (D-5's structural guard — see
	// logicseam_test.go's own mixed-seam pinning for the refusal this
	// leans on).
	if err := seam.Validate(); err != nil {
		return nil, cleanup, fmt.Errorf("livee2e: newLogicHarness: %w", err)
	}
	if seam.IsRealGitHub() {
		return nil, cleanup, fmt.Errorf("livee2e: newLogicHarness: seam unexpectedly reports real GitHub — refusing before the first write")
	}

	h := &harness{
		Cfg:       cfg,
		Pre:       pre,
		Org:       logicOrg,
		Repo:      logicRepo,
		SpaceSlug: harnessSpaceSlug,
		// Tier is what lets a scenario body ask AssertionJudgeableBy before
		// asserting something its own catalogue row declared provider-decided.
		// Without it every carve-out is declared and unconsulted, which shows
		// up as either a row failing against a fact the fake does not have, or
		// — worse — a row passing because the condition its assertion guards
		// cannot occur locally at all.
		Tier: TierLogic,
		Seam: seam,
		Bin:  bin,
		Prov: &ghClient{Token: logicProvisionerCredential, APIRoot: fake.URL},
		Part: &ghClient{Token: logicParticipantCredential, APIRoot: fake.URL},
		// VerificationCandidate/ExecutionCandidate are deliberately left
		// unset (zero value) — see TestNewLogicHarnessLeavesExecutionCandidateZero.
	}

	// PRFloor is RESOLVED, not assumed to be zero — the same call ResetSpace
	// itself makes (provision_live.go's own maxPRNumber doc), even though a
	// freshly created origin+fake pair has nothing to floor against today.
	floor, err := h.maxPRNumber(ctx)
	if err != nil {
		return h, cleanup, fmt.Errorf("livee2e: newLogicHarness: record PR floor: %w", err)
	}
	h.PRFloor = floor

	// Each checkout gets its OWN isolated HOME (internal/e2e/host_loop_test.go's
	// hostRig/peer own precedent: "HOME="+r.homeDir per participant) — never
	// the ambient one. Without this, an operator's real ~/.config/a2a/
	// config.yaml (mirror_root in particular) and ~/.cache/a2a/mirrors are
	// what every child process this checkout spawns would read and write,
	// exactly the machine-config sharing workspace_live.go's own
	// machineConfigPath doc says the LIVE tier deliberately wants. A logic
	// run has no real GitHub and therefore no reason to share ANY machine
	// state: five early runs of this test each left a directory in the
	// real ~/.cache/a2a/mirrors, next to a real space's own mirror, and the
	// gap was found only because two of them collided (mirror_root keys a
	// space's cached clone by its URL basename — cmd_init.go's
	// initSpaceIDFromURL). With no machine config file to find here (an
	// empty, freshly created home has none), space.ResolveMirrorLocation
	// falls back to ITS OWN project-relative default — entirely inside
	// each checkout's own directory, itself inside work — so nothing this
	// harness does can land outside its own temp tree.
	// TestLogicTierWritesNothingOutsideItsOwnTempDirs (logic_runner_live_test.go)
	// is the gate that keeps this true.
	homeA := filepath.Join(work, "home-A")
	homeB := filepath.Join(work, "home-B")
	if err := os.MkdirAll(homeA, 0o755); err != nil {
		return h, cleanup, fmt.Errorf("livee2e: newLogicHarness: mkdir %s: %w", homeA, err)
	}
	if err := os.MkdirAll(homeB, 0o755); err != nil {
		return h, cleanup, fmt.Errorf("livee2e: newLogicHarness: mkdir %s: %w", homeB, err)
	}

	h.A = &checkout{
		Dir: filepath.Join(work, "A"), System: systemAlpha,
		Token: logicProvisionerCredential, Login: pre.ProvisionerLogin,
		Bin: bin, SpaceSlug: harnessSpaceSlug, Peer: systemBravo,
		// Both checkouts carry APIRoot = the fake's URL — unlike the live
		// harness, which leaves it empty so checkoutEnv strips
		// A2A_GITHUB_API back to its default (workspace_live.go's own doc).
		// This tier's whole point is to redirect at every child process.
		APIRoot: fake.URL,
		Home:    homeA,
	}
	h.B = &checkout{
		Dir: filepath.Join(work, "B"), System: systemBravo,
		Token: logicParticipantCredential, Login: pre.ParticipantLogin,
		Bin: bin, SpaceSlug: harnessSpaceSlug, Peer: systemAlpha,
		APIRoot: fake.URL,
		Home:    homeB,
	}

	if err := setupCheckout(ctx, h.A, seam.WebRoot); err != nil {
		return h, cleanup, fmt.Errorf("livee2e: newLogicHarness: checkout A: %w", err)
	}
	if err := setupCheckout(ctx, h.B, seam.WebRoot); err != nil {
		return h, cleanup, fmt.Errorf("livee2e: newLogicHarness: checkout B: %w", err)
	}

	return h, cleanup, nil
}

// TestNewLogicHarnessLeavesExecutionCandidateZero pins D-4/D-5's structural
// guarantee: TestLiveMatrix (runner_live_test.go) writes a P7 evidence
// bundle only when run.ExecutionCandidate.SourceSHA != "". That is a
// property of code this wave writes, one field assignment away from
// disappearing (plan D-4's own text) — so it is asserted here rather than
// left as a comment nobody re-checks. A logic run must never populate
// either candidate attestation: that is what makes it structurally
// INCAPABLE of producing release evidence, not merely a run that happens
// not to have any today.
func TestNewLogicHarnessLeavesExecutionCandidateZero(t *testing.T) {
	ctx := t.Context()

	h, cleanup, err := newLogicHarness(ctx, t)
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Errorf("newLogicHarness cleanup failed: %v", cleanupErr)
		}
	}()
	if err != nil {
		t.Fatalf("newLogicHarness: %v", err)
	}

	if h.VerificationCandidate != (CandidateAttestation{}) {
		t.Errorf("VerificationCandidate = %+v, want the zero value", h.VerificationCandidate)
	}
	if h.ExecutionCandidate != (CandidateAttestation{}) {
		t.Errorf("ExecutionCandidate = %+v, want the zero value", h.ExecutionCandidate)
	}
}
