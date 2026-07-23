package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/fakegithub"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// hostRig is a throwaway project wired to a fake host: a real bare-repo
// space, a real project + machine config, and the EXEC'd binary pointed at
// an in-process GitHub stand-in via A2A_GITHUB_API.
//
// This is the tier the suite was missing. Every other write-path test in
// this package constructs commands directly with host.FakeHost, which skips
// cmd/a2a/wire.go entirely — and wire.go is where the config load, the
// credential resolution, the space-ref resolution and the mirror handling
// live. Those closures had ZERO coverage, and three of the defects the
// first external consumer reported lived in exactly them.
type hostRig struct {
	t          *testing.T
	fx         *spacefixture.Fixture
	gh         *fakegithub.Server
	projectDir string
	homeDir    string
	spaceID    string
	system     string
}

const hostRigSpaceID = "fixture-space"

// newHostRig builds the rig for ownSystem, with every named system present
// in the space's manifest.
func newHostRig(t *testing.T, ownSystem string, systems ...string) *hostRig {
	t.Helper()
	fx := spacefixture.New(t, systems...)
	fixOriginManifest(t, fx.RemoteURL(), hostRigSpaceID)

	r := &hostRig{
		t: t, fx: fx,
		gh:         fakegithub.New(t, fx.RemoteURL()),
		projectDir: t.TempDir(),
		homeDir:    t.TempDir(),
		spaceID:    hostRigSpaceID,
		system:     ownSystem,
	}

	mustMkdirAll(t, filepath.Join(r.projectDir, ".a2a", "staging"))
	mustWrite(t, filepath.Join(r.projectDir, ".a2a", "config.yaml"), fmt.Sprintf(
		"system: %s\nspaces:\n  - id: %s\n    repo_url: %s\n", ownSystem, r.spaceID, fx.RemoteURL()))

	cfgDir := filepath.Join(r.homeDir, ".config", "a2a")
	mustMkdirAll(t, cfgDir)
	mustWrite(t, filepath.Join(cfgDir, "config.yaml"),
		fmt.Sprintf("credentials:\n  %s: \"env:FIXTURE_TOKEN\"\n", r.spaceID))

	return r
}

// run execs the built binary in the rig's project, against the fake host.
func (r *hostRig) run(args ...string) (stdout, stderr string, code int) {
	r.t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "a2a"), args...)
	cmd.Dir = r.projectDir
	cmd.Env = append(os.Environ(),
		"HOME="+r.homeDir,
		"FIXTURE_TOKEN=dummy-token",
		"A2A_GITHUB_API="+r.gh.URL,
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	if err != nil {
		exitErr := &exec.ExitError{}
		if !errors.As(err, &exitErr) {
			r.t.Fatalf("exec a2a %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errBuf.String(), code
}

// mustRun execs and fails the test on a non-zero exit.
func (r *hostRig) mustRun(args ...string) string {
	r.t.Helper()
	stdout, stderr, code := r.run(args...)
	if code != 0 {
		r.t.Fatalf("a2a %v: exit %d\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
	}
	return stdout
}

// stageQuestion writes a schema-valid question addressed to `to` into the
// project's staging dir and returns its path and id.
func (r *hostRig) stageQuestion(id, to string) (path string) {
	r.t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: does the write path reach a host\n" +
		"space: " + r.spaceID + "\n" +
		"from: " + r.system + "\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-07-23T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\n" +
		"body\n"
	path = filepath.Join(r.projectDir, ".a2a", "staging", id+".md")
	mustWrite(r.t, path, content)
	return path
}

// peer returns a second project, acting as another system, wired to the
// SAME space and the SAME host — an exchange needs two participants, and
// every cross-system precondition (a consumer's ack, a consumer registry)
// is only real when the other side is a separate config on disk.
func (r *hostRig) peer(system string) *hostRig {
	r.t.Helper()
	p := &hostRig{
		t: r.t, fx: r.fx, gh: r.gh,
		projectDir: r.t.TempDir(), homeDir: r.t.TempDir(),
		spaceID: r.spaceID, system: system,
	}
	mustMkdirAll(r.t, filepath.Join(p.projectDir, ".a2a", "staging"))
	mustWrite(r.t, filepath.Join(p.projectDir, ".a2a", "config.yaml"), fmt.Sprintf(
		"system: %s\nspaces:\n  - id: %s\n    repo_url: %s\n", system, p.spaceID, r.fx.RemoteURL()))
	cfgDir := filepath.Join(p.homeDir, ".config", "a2a")
	mustMkdirAll(r.t, cfgDir)
	mustWrite(r.t, filepath.Join(cfgDir, "config.yaml"),
		fmt.Sprintf("credentials:\n  %s: \"env:FIXTURE_TOKEN\"\n", p.spaceID))
	return p
}

// stageContract writes a schema-valid contract draft into staging.
func (r *hostRig) stageContract(slug, version string) (path, id string) {
	r.t.Helper()
	id = "XC-" + r.system + "-" + slug
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: the export contract under test\n" +
		"space: " + r.spaceID + "\n" +
		"from: " + r.system + "\n" +
		"to: [beta]\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-07-23T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: " + version + "\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"---\n" +
		"# Export\n\nWhat this contract covers.\n"
	path = filepath.Join(r.projectDir, ".a2a", "staging", id+".md")
	mustWrite(r.t, path, content)
	return path, id
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// TestHostLoopSubmit drives `a2a submit` through the REAL binary: real
// config load, real credential resolution, real mirror clone, real `git
// push` into a real bare repo, real PR open — then reads the artifact back
// after the host merged it. Nothing here is a direct construction, and
// nothing leaves the machine.
func TestHostLoopSubmit(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	id := "XQ-axon-20260723-h001"
	path := r.stageQuestion(id, "beta")

	out := r.mustRun("submit", path)
	if !strings.Contains(out, "pr") && !strings.Contains(out, "PR") {
		t.Errorf("submit stdout does not mention the PR: %q", out)
	}

	prs := r.gh.PRs()
	if len(prs) != 1 {
		t.Fatalf("PRs = %d, want 1 (host calls: %v)", len(prs), r.gh.Requests())
	}
	wantHead := "a2a/axon/submit/" + id
	if prs[0].Head != wantHead {
		t.Errorf("PR head = %q, want %q", prs[0].Head, wantHead)
	}
	if !prs[0].Merged {
		t.Errorf("PR was not merged by auto-merge: %+v", prs[0])
	}

	// The artifact really landed on the space's main branch.
	if got := gitOutput(t, r.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, id) {
		t.Errorf("origin main does not carry %s after the merge:\n%s", id, got)
	}

	// And the acting system can read it back through its own verbs.
	r.mustRun("sync")
	if out := r.mustRun("outbox"); !strings.Contains(out, id) {
		t.Errorf("outbox does not list %s after submit+sync:\n%s", id, out)
	}
}

// TestHostLoopSubmitIsIdempotent re-runs the same submit against the same
// host: the funnel's step-0 lookup must find the merged PR and open no
// second one.
func TestHostLoopSubmitIsIdempotent(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	id := "XQ-axon-20260723-h002"
	path := r.stageQuestion(id, "beta")

	r.mustRun("submit", path)
	stdout, stderr, code := r.run("submit", path)
	if code != 0 {
		t.Fatalf("re-submit: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if prs := r.gh.PRs(); len(prs) != 1 {
		t.Fatalf("PRs = %d after a re-run, want 1", len(prs))
	}
}

// TestHostLoopFeedbackFromANonCollaborator is P28's claim, proven through
// the binary instead of a hand-wired FakeHost: the origin refuses the push
// with GitHub's own wording (a real pre-receive hook, so the REAL stderr
// classifier decides), and the verb still opens a cross-fork PR.
func TestHostLoopFeedbackFromANonCollaborator(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon")
	r.gh.DenyPushes("seomatrix")

	draft := writeFeedbackDraft(t, filepath.Join(r.projectDir, "drafts"))
	stdout, stderr, code := r.runFeedback("submit", draft)
	if code != 0 {
		t.Fatalf("feedback submit: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	prs := r.gh.PRs()
	if len(prs) != 1 {
		t.Fatalf("PRs = %d, want 1 (host calls: %v)", len(prs), r.gh.Requests())
	}
	wantHead := fakegithub.ForkLogin + ":a2a/feedback/submit/" + feedbackSubmitTestID
	if prs[0].Head != wantHead {
		t.Errorf("PR head = %q, want %q", prs[0].Head, wantHead)
	}
	if r.gh.ForkDir(fakegithub.ForkLogin) == "" {
		t.Error("no fork was created")
	}
}

// runFeedback execs the feedback family, which targets its own repo rather
// than a connected space (A2A_FEEDBACK_REPO overrides the product repo).
func (r *hostRig) runFeedback(args ...string) (stdout, stderr string, code int) {
	r.t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "a2a"), append([]string{"feedback"}, args...)...)
	cmd.Dir = r.projectDir
	cmd.Env = append(os.Environ(),
		"HOME="+r.homeDir,
		"A2A_FEEDBACK_TOKEN=dummy-token",
		"A2A_FEEDBACK_REPO="+r.fx.RemoteURL(),
		"A2A_GITHUB_API="+r.gh.URL,
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	if err != nil {
		exitErr := &exec.ExitError{}
		if !errors.As(err, &exitErr) {
			r.t.Fatalf("exec a2a feedback %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errBuf.String(), code
}

// TestHostLoopContractFamily is the family the first external consumer
// could not use at all, driven end to end through the binary: publish a
// contract, have a SECOND system adopt it, then try to retire it while
// that consumer is still registered.
//
// Every step here goes through cmd/a2a/wire.go's runContract closure,
// which no test executed before this one.
func TestHostLoopContractFamily(t *testing.T) {
	t.Parallel()

	provider := newHostRig(t, "axon", "axon", "beta")
	consumer := provider.peer("beta")

	// 1. Submit the descriptor — the step that was IMPOSSIBLE before P27:
	// the id guard red every contract, so the family could not enter the
	// space at all, through the funnel or a hand-opened PR.
	draft, id := provider.stageContract("export", "1.0.0")
	provider.mustRun("submit", draft)
	if got := gitOutput(t, provider.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, "provides/export/contract.md") {
		t.Fatalf("origin main does not carry the contract at its §4.2 path:\n%s", got)
	}

	// 2. Publish the version.
	provider.mustRun("sync")
	provider.mustRun("contract", "publish", "--version", "1.0.0", id)
	if len(provider.gh.PRs()) != 2 {
		t.Fatalf("PRs = %d after submit+publish, want 2 (host calls: %v)",
			len(provider.gh.PRs()), provider.gh.Requests())
	}

	// 3. The consumer registers itself.
	consumer.mustRun("sync")
	consumer.mustRun("contract", "adopt", id)
	if got := gitOutput(t, consumer.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, "beta/consumes.yaml") {
		t.Fatalf("origin main does not carry beta's consumer registry:\n%s", got)
	}

	// 4. Deprecate — the transition retire requires first. This is the
	// THIRD write by the provider on this artifact, and the second after a
	// merge: exactly the shape that used to vanish.
	provider.mustRun("sync")
	// NOTE: flags BEFORE the id — Go's flag package stops at the first
	// non-flag token, and only `contract adopt` lifts the id out first
	// (P27). See the P30 finding on argument order.
	provider.mustRun("contract", "deprecate",
		"--successor", id+"@2.0.0", "--sunset", "2027-01-01", id)

	// 5. Retire must REFUSE: beta is a registered consumer that has not
	// acked. This is the read-side fail-closed guard, reached only because
	// every write above actually landed.
	provider.mustRun("sync")
	stdout, stderr, code := provider.run("contract", "retire", id)
	if code == 0 {
		t.Fatalf("contract retire succeeded with a registered consumer\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "POL-006") {
		t.Errorf("the refusal is not the consumer-ack precondition:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}
