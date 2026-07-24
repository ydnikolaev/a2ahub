//go:build livee2e

package livee2e

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
	spacetemplate "github.com/ydnikolaev/a2ahub/space-template"
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

// assertNoAmbientAPIRoot refuses to proceed if the seam driving CLI behavior
// toward a fake host is already set for this process.
func assertNoAmbientAPIRoot(getenv func(string) string) error {
	if v := getenv(githubAPIEnv); v != "" {
		return fmt.Errorf("%w: got %q", ErrAmbientAPIRoot, v)
	}
	return nil
}

// harnessBinaryVersion is what -ldflags "-X main.version=..." stamps into the
// binary the tier builds. It MUST be a bare, parseable major.minor.patch,
// because the write funnel's CC-085 min_binary_version guard (and `a2a
// doctor`) parse it as one — an unparseable stamp like main.go's own "dev"
// default breaks every write scenario in the matrix, not just the version
// ones.
//
// It is READ FROM THE TEMPLATE the harness also scaffolds the space from
// (TemplateMinBinaryVersion, scaffold.go), never hand-maintained.
// docs/runbooks/live-e2e/reset.sh used a hardcoded fallback, which works right
// up until the template's floor moves past it — and then every write row in
// the matrix reds on a CC-085 refusal that has nothing to do with the product.
// One source, so the two versions cannot drift apart.
func harnessBinaryVersion() (string, error) {
	raw, err := fs.ReadFile(spacetemplate.Files, "space.yaml")
	if err != nil {
		return "", fmt.Errorf("livee2e: read the embedded space template: %w", err)
	}
	return TemplateMinBinaryVersion(string(raw))
}

// repoRootFromGoEnv discovers the repository root via `go env GOMOD` rather
// than a relative-path guess: the test process's working directory is the
// package directory, not the repo root, so ".." arithmetic would be fragile
// wherever `go test` happens to run from.
func repoRootFromGoEnv(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("livee2e: go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("livee2e: go env GOMOD reported no module (working directory is outside a Go module)")
	}
	return filepath.Dir(gomod), nil
}

// buildBinary builds the a2a binary ONCE per run into dir, from the repo
// root discovered via repoRootFromGoEnv, stamped with version.
func buildBinary(ctx context.Context, dir, version string) (string, error) {
	root, err := repoRootFromGoEnv(ctx)
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "a2a")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "-ldflags", "-X main.version="+version, "./cmd/a2a")
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("livee2e: go build ./cmd/a2a: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return bin, nil
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
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Dir = dir
	cmd.Env = checkoutEnv(c.Token, c.SpaceSlug)
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
// NOTE, learned live: `a2a connect` leaves this directory EMPTY. It is
// populated by the first `a2a sync`, so anything reading a file out of it
// must sync first or it reads a hole (filed to the backlog as a product
// question — connect arguably owes the caller a populated mirror).
func (c *checkout) MirrorDir() string {
	return filepath.Join(c.Dir, ".a2a", "cache", "mirrors", c.SpaceSlug)
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

// Draft drafts an artifactType via `a2a new`, then runs the sibling's
// FillDraft over the staged draft and writes it back — the author-edit step
// docs/runbooks/live-e2e/fill.py models. extra is passed straight through as
// additional `a2a new` flags (e.g. --slug for a standing-type artifact),
// exactly as run.sh's own draft() helper does.
//
// unfilled is returned rather than swallowed: a placeholder FillDraft could
// not resolve is either a field the matrix needs to learn to fill, or a
// template asking for something it cannot describe — either way the caller
// (a wave-3-2 scenario) decides what that means for the row, not this
// function.
func (c *checkout) Draft(ctx context.Context, artifactType string, extra ...string) (id string, unfilled []string, err error) {
	args := append([]string{"new", artifactType, "--field", "title=matrix " + artifactType, "--field", "space=" + c.SpaceSlug}, extra...)
	stdout, stderr, runErr := c.Run(ctx, args...)
	if runErr != nil {
		return "", nil, fmt.Errorf("livee2e: a2a new %s (%s): %w: %s", artifactType, c.System, runErr, strings.TrimSpace(stderr))
	}
	id, ok := parseDraftedID(stdout)
	if !ok {
		return "", nil, fmt.Errorf("livee2e: a2a new %s (%s): could not parse drafted id from: %s", artifactType, c.System, strings.TrimSpace(stdout))
	}

	path := filepath.Join(c.Dir, ".a2a", "staging", id+".md")
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return id, nil, fmt.Errorf("livee2e: read staged draft %s: %w", path, readErr)
	}
	filled, unfilled := FillDraft(string(raw), DraftContext{Space: c.SpaceSlug, Me: c.System, Peer: c.Peer})
	if writeErr := os.WriteFile(path, []byte(filled), 0o644); writeErr != nil {
		return id, unfilled, fmt.Errorf("livee2e: write filled draft %s: %w", path, writeErr)
	}
	return id, unfilled, nil
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
