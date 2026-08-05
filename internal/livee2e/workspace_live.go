//go:build livee2e

package livee2e

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// githubAPIEnv mirrors cmd/a2a/wire.go's own seam name (A2A_GITHUB_API) —
// restated rather than imported (cmd/a2a is off-limits to this wave).
// §T2's whole point is that a live run is the SAME scenarios with this
// variable at its default, so the harness must assert that, not assume it.
const githubAPIEnv = "A2A_GITHUB_API"

// ErrAmbientAPIRoot is returned when A2A_GITHUB_API is already set in the
// process environment newHarness runs in. An operator whose shell still
// points at a fakegithub server (or any override) would otherwise get a
// fully "green" live run that never touched real GitHub — the exact failure
// mode §T2 exists to rule out.
var ErrAmbientAPIRoot = errors.New("livee2e: A2A_GITHUB_API is set in the ambient environment — a live run needs the seam at its default (spec 36 §T2)")

func attestCandidateCheck(want CandidateExpectation) (CandidateAttestation, error) {
	raw, err := readBoundedRuntimeFile(want.CheckLog, maxCandidateCheckLogBytes)
	if err != nil {
		return CandidateAttestation{}, fmt.Errorf("%w: read retained candidate check log: %w", ErrCandidateSource, err)
	}
	attestation, err := candidateCheckAttestation(raw, want.CheckLog, want)
	if err != nil {
		return CandidateAttestation{}, fmt.Errorf("%w: %w", ErrCandidateSource, err)
	}
	return attestation, nil
}

func runCandidateCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	var cmd *exec.Cmd
	switch name {
	case "git":
		// #nosec G204 -- executable is a fixed literal and argv comes only from the harness's closed attestation calls.
		cmd = exec.CommandContext(ctx, "git", args...)
	default:
		if !filepath.IsAbs(name) {
			return "", fmt.Errorf("candidate command must be git or an absolute attested binary: %q", name)
		}
		// #nosec G204 -- name is the absolute just-built candidate binary; its digest/build-info are attested before provider use.
		cmd = exec.CommandContext(ctx, filepath.Clean(name), args...)
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// attestCandidateSource repeats the execution checkout's identity,
// cleanliness, template, and prior-check assertions immediately before build.
func attestCandidateSource(ctx context.Context, want CandidateExpectation) (CandidateAttestation, error) {
	return attestCandidateSourceWith(ctx, want, runCandidateCommand, os.ReadFile, filepath.EvalSymlinks)
}

// attestCandidateBinary binds the built bytes and both independent Go/CLI
// stamps to the candidate before ResetSpace can mutate the provider.
func attestCandidateBinary(ctx context.Context, bin string, want CandidateExpectation, source CandidateAttestation) (CandidateAttestation, error) {
	return attestCandidateBinaryWith(ctx, bin, want, source, runCandidateCommand, buildinfo.ReadFile)
}

// assertNoAmbientAPIRoot refuses to proceed if the seam driving CLI behavior
// toward a fake host is already set for this process.
func assertNoAmbientAPIRoot(getenv func(string) string) error {
	if v := getenv(githubAPIEnv); v != "" {
		return fmt.Errorf("%w: got %q", ErrAmbientAPIRoot, v)
	}
	return nil
}

// buildBinary attests the explicit fresh candidate checkout immediately before
// building, stamps both its floor and immutable revision, then attests the
// resulting bytes. It performs no provider operation; callers must not invoke
// ResetSpace until it returns successfully.
func buildBinary(ctx context.Context, dir string, want CandidateExpectation) (string, CandidateAttestation, CandidateAttestation, error) {
	verification, err := attestCandidateCheck(want)
	if err != nil {
		return "", CandidateAttestation{}, CandidateAttestation{}, err
	}
	execution, err := attestCandidateSource(ctx, want)
	if err != nil {
		return "", CandidateAttestation{}, CandidateAttestation{}, err
	}
	bin := filepath.Join(dir, "a2a")
	stampFlags := "-X main.version=" + want.Floor + " -X main.commit=" + want.SHA
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "-ldflags", stampFlags, "./cmd/a2a") //nolint:gosec // reason: executable/package are fixed and stamp values are validated candidate SHA/floor inputs.
	cmd.Dir = want.Root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", CandidateAttestation{}, CandidateAttestation{}, fmt.Errorf("livee2e: go build candidate ./cmd/a2a: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	execution, err = attestCandidateBinary(ctx, bin, want, execution)
	if err != nil {
		return "", CandidateAttestation{}, CandidateAttestation{}, err
	}
	return bin, verification, execution, nil
}

// checkout is one of the two local systems (the axon/seomatrix shape, spec
// 36 §T1) driving the real binary against the live space.
//
// Bin, SpaceSlug and Peer are NOT in the brief's minimal struct sketch
// ({Dir, System, Token, Login}) — Run cannot exec anything without a binary
// path, the credential override env var needs the connected space id, and
// Draft needs to know the OTHER system to fill DraftContext.Peer. Recorded
// as a deviation in the wave report rather than silently widening the
// sketch without comment.
type checkout struct {
	Dir    string
	System string
	Token  string
	Login  string

	// Bin is the a2a binary path, shared across both checkouts (built once
	// per run by newHarness).
	Bin string
	// SpaceSlug is the connected space id — needed to name the credential
	// override env var A2A_TOKEN_<SPACEID> (space.CredentialEnvVar).
	SpaceSlug string
	// Peer is the OTHER local system's id, needed to fill DraftContext.Peer.
	Peer string
}

// checkoutEnv builds the child process environment for a checkout's exec:
// the parent's environment, MINUS A2A_GITHUB_API (§T2: live means this seam
// sits at its default, asserted rather than assumed — even though
// assertNoAmbientAPIRoot already refused an ambient value at harness
// construction, filtering it here is the second, cheaper half of "assert,
// don't assume" for any process spawned from this checkout), PLUS the
// per-space credential override run.sh itself exports.
func checkoutEnv(token, spaceSlug string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, githubAPIEnv+"=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, space.CredentialEnvVar(spaceSlug)+"="+token)
}

// Run execs the a2a binary in this checkout's directory with args, returning
// its captured stdout/stderr. err is exec's own error (non-zero exit, exec
// failure) — callers that need the process's stderr for a message read it
// off the second return value regardless of err.
func (c *checkout) Run(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	return c.RunIn(ctx, c.Dir, args...)
}

// RunIn is Run with an explicit working directory, for the verbs that are not
// about the project at all.
//
// `validate --ci` is the case that forced it: in the space's CI it runs
// INSIDE the checked-out space repo and reads `space.yaml` from the working
// directory, so running it from a participant's project directory fails with
// "cannot read space.yaml" no matter how healthy the space is. That is not a
// product defect and must not be reported as one — it is the harness calling
// a repo-scoped verb from the wrong repo. MirrorDir names the right one.
func (c *checkout) RunIn(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...) //nolint:gosec // reason: c.Bin is the attested candidate and argv is supplied only by closed live scenario code.
	cmd.Dir = dir
	cmd.Env = checkoutEnv(c.Token, c.SpaceSlug)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return out.String(), errBuf.String(), runErr
}

// MainContains reports whether sha is an ancestor of the checkout's fetched
// origin/main AND the mirror working tree is actually parked on that exact
// base tip. Readers fold files from the working tree, not from a remote ref,
// so proving only origin/main moved would allow a stale ephemeral branch to
// masquerade as a successful sync.
func (c *checkout) MainContains(ctx context.Context, sha string) (bool, error) {
	return gitMainContains(ctx, c.MirrorDir(), sha)
}

// RunWithAPIRoot execs the a2a binary exactly like Run, except A2A_GITHUB_API
// is set to apiRoot for THIS ONE invocation only — the seam spec 38's Layer
// 3 fault-injection row uses to point a single real write at a proxy in
// front of production GitHub (spec 38 §6-Q1, plan D-G) instead of letting
// checkoutEnv filter the seam back to its documented default.
//
// This is a deliberate, narrow puncture of checkoutEnv's own "the seam sits
// at its default, asserted rather than assumed" invariant (checkoutEnv's own
// doc comment above) — recorded as a deviation in wave H's report. It is
// safe only because the one caller (scenarios_failure_recovery_live.go) is a
// row the report labels a DISTINCT evidence class (Result.EvidenceClass,
// report.go): every other call in this package still goes through Run/RunIn,
// which checkoutEnv still filters unconditionally.
func (c *checkout) RunWithAPIRoot(ctx context.Context, apiRoot string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...) //nolint:gosec // reason: c.Bin is the attested candidate and argv is supplied only by closed live scenario code.
	cmd.Dir = c.Dir
	cmd.Env = append(checkoutEnv(c.Token, c.SpaceSlug), githubAPIEnv+"="+apiRoot)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return out.String(), errBuf.String(), runErr
}

// MirrorDir is the checkout's local clone of the space — the working tree
// every read verb folds over, and the only directory a repo-scoped verb like
// `validate --ci` may run from.
//
// It ASKS the product where the mirror is rather than recomputing the rule
// itself (spec 35 §9's scar: a rule with two homes eventually disagrees with
// itself). Before today's product fix, `a2a init --space <url>` persisted a
// ref with no MirrorLocation — which always resolved project-locally, keyed
// by the space id, and ignored `mirror_root` — while `connect` cloned into
// the URL-id-keyed, `mirror_root`-aware location and never repaired the ref:
// the clone and every read pointed at two different directories. `connect`
// now repairs MirrorLocation (space.ResolveMirrorLocation's own doc), so the
// one true mirror is whatever that resolver says it is; this method loads
// the same two config files the product loads and asks the same question.
//
// NOTE, corrected after re-checking against today's fix (the prior version
// of this comment claimed `a2a connect` leaves this directory EMPTY and only
// `a2a sync` populates it): `connect` itself calls space.CloneOrFetch against
// the resolved mirror location (cmd_init.go's ConnectCommand.Run), so the
// mirror is already populated with the space's tree as of connect. `a2a
// sync` re-fetches and resets it to the remote's current head; it is a
// refresh, not the first write. The "empty after connect" observation was a
// symptom of the very bug this fix addresses: the harness's old hardcoded
// path here was project-local/id-keyed, which is where CONNECT NEVER WROTE
// (it wrote to the id-agnostic, mirror_root-aware location) — so reading it
// found a hole that had nothing to do with connect vs. sync timing.
//
// Residual gap, deliberately not hidden: MirrorDir cannot return an error
// (its one call site assigns a bare string — scenarios_happy_live.go:206,
// off-limits to this wave), so a config that HAS entries but none whose ID
// matches c.SpaceSlug degrades to the same project-local/id-keyed fallback
// as "no config at all" rather than failing loudly. That fallback is the
// PRE-fix path shape and is silently wrong if it is ever hit for real —
// pinned by TestMirrorDirUnmatchedSpaceIDFallsBackToLegacyPath so the
// behavior is at least documented, not a surprise. In this run it is not
// hit: harnessSpaceSlug ("livee2e") is what `a2a space init` seeds as the
// manifest id, so `connect`'s ref repair always lands a ref with that exact
// ID.
func (c *checkout) MirrorDir() string {
	projectConfigPath := filepath.Join(c.Dir, ".a2a", "config.yaml")
	// Absent/unreadable config is fine — the same tolerance the product's
	// own connect/sync/doctor call sites use ("absent config is fine",
	// cmd_init.go ConnectCommand.Run): a fresh checkout has no config yet,
	// and the fallback ref below reproduces the pre-fix, project-local/
	// id-keyed default in that case.
	cfg, _ := space.LoadProjectConfig(projectConfigPath)
	machine, _ := space.LoadMachineConfig(machineConfigPath())

	// The config's space id is the MANIFEST id `connect` repairs the ref to
	// (harnessSpaceSlug, "livee2e" — matched to c.SpaceSlug, not the URL id
	// `connect` starts from), so the ref is looked up BY ID, never by
	// position. A ref carrying no MirrorLocation (the pre-fix shape, and
	// this method's own zero-value fallback below) still resolves — it just
	// falls back to the project-local, id-keyed default ResolveMirrorLocation
	// itself documents.
	ref := space.Ref{ID: c.SpaceSlug}
	for _, r := range cfg.Spaces {
		if r.ID == c.SpaceSlug {
			ref = r
			break
		}
	}

	return space.ResolveMirrorLocation(c.Dir, ref, machine)
}

// machineConfigPath resolves `~/.config/a2a/config.yaml` exactly as
// cmd/a2a/wire.go's resolvePaths does (home + ".config/a2a/config.yaml").
// The harness never sets HOME per checkout — both checkouts' child
// processes inherit the ambient one via checkoutEnv's os.Environ() base —
// so os.UserHomeDir() here resolves to the SAME machine config the child
// `a2a` binary itself reads, and is also why a configured `mirror_root`
// applies to this rig at all. An unresolvable home degrades to an empty
// path, which LoadMachineConfig turns into a (correctly) ignored read
// error, not a panic.
func machineConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "a2a", "config.yaml")
}

// draftedIDPattern extracts the id `a2a new` mints from its own stdout line
// (internal/cli/cmd_new.go: "new: drafted %s -> %s\n"). Not part of the pinned
// cross-agent API — a coordination point named in the wave report.
var draftedIDPattern = regexp.MustCompile(`(?m)^new: drafted (\S+) ->`)

func parseDraftedID(stdout string) (string, bool) {
	m := draftedIDPattern.FindStringSubmatch(stdout)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Draft authors scenario data exclusively through the shipped `a2a new`
// interface. No staged frontmatter is reopened or regex-patched by the
// harness. extra is appended last so a scenario can explicitly override a
// default with another repeatable --field.
func (c *checkout) Draft(ctx context.Context, artifactType string, extra ...string) (id string, unfilled []string, err error) {
	fields, ok := draftFieldArgs(artifactType, c.SpaceSlug, c.System, c.Peer)
	if !ok {
		return "", nil, fmt.Errorf("livee2e: no public authoring fields for artifact type %q", artifactType)
	}
	if standingDraftTypes[artifactType] && !hasSlugArg(extra) {
		return "", nil, fmt.Errorf("livee2e: a2a new %s is a standing type and cannot be drafted without --slug; "+
			"scope it with liveRunSlug(<base>, h.PRFloor) as every other standing draft in this tier does", artifactType)
	}
	args := append([]string{"new", artifactType}, fields...)
	args = append(args, extra...)
	stdout, stderr, runErr := c.Run(ctx, args...)
	if runErr != nil {
		return "", nil, fmt.Errorf("livee2e: a2a new %s (%s): %w: %s", artifactType, c.System, runErr, strings.TrimSpace(stderr))
	}
	id, ok = parseDraftedID(stdout)
	if !ok {
		return "", nil, fmt.Errorf("livee2e: a2a new %s (%s): could not parse drafted id from: %s", artifactType, c.System, strings.TrimSpace(stdout))
	}
	return id, nil, nil
}

// setupCheckout runs `a2a init` then `a2a connect`, exactly as
// docs/runbooks/live-e2e/run.sh's setup_project does.
func setupCheckout(ctx context.Context, c *checkout, spaceURL string) error {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return fmt.Errorf("livee2e: mkdir %s: %w", c.Dir, err)
	}
	if _, stderr, err := c.Run(ctx, "init",
		"--system", c.System,
		"--space", spaceURL,
		"--no-skill", "--no-skill-link", "--no-agents-pointer",
	); err != nil {
		return fmt.Errorf("livee2e: a2a init (%s): %w: %s", c.System, err, strings.TrimSpace(stderr))
	}
	if _, stderr, err := c.Run(ctx, "connect", spaceURL); err != nil {
		return fmt.Errorf("livee2e: a2a connect (%s): %w: %s", c.System, err, strings.TrimSpace(stderr))
	}
	return nil
}
