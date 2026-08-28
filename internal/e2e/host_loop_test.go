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
		"thread: " + e2eFixtureThread + "\n" +
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
		"thread: " + e2eFixtureThread + "\n" +
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

	// D-D/POL-009: carry a real schema/fixture baseline into staging at the
	// SAME space-relative shape `a2a contract new`'s own scaffold uses
	// (internal/cli/cmd_new.go's newScaffoldContractFiles) and `a2a submit`
	// now carries along (internal/cli/cmd_submit.go's
	// submitContractSidecars) — without it, `contract publish`'s own
	// POL-009 check refuses this contract the first time anyone publishes
	// it, exactly the gap this end-to-end rig exists to catch.
	schemaDir := filepath.Join(r.projectDir, ".a2a", "staging", r.system, "provides", slug, "schema")
	fixturesValidDir := filepath.Join(r.projectDir, ".a2a", "staging", r.system, "provides", slug, "fixtures", "valid")
	fixturesInvalidDir := filepath.Join(r.projectDir, ".a2a", "staging", r.system, "provides", slug, "fixtures", "invalid")
	mustMkdirAll(r.t, schemaDir)
	mustMkdirAll(r.t, fixturesValidDir)
	mustMkdirAll(r.t, fixturesInvalidDir)
	mustWrite(r.t, filepath.Join(schemaDir, slug+".schema.json"),
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"example":{"type":"string"}},"additionalProperties":true}`+"\n")
	mustWrite(r.t, filepath.Join(fixturesValidDir, slug+".json"), `{"example":"replace-me"}`+"\n")
	mustWrite(r.t, filepath.Join(fixturesInvalidDir, slug+".json"), "null\n")

	// The staging candidate's OWN descriptor, at the layout `a2a contract
	// new`'s ScaffoldContractCandidateInStaging produces. Written here
	// because this rig authors the envelope by hand rather than through the
	// scaffold, and because `contract preflight|publish --staging <dir>`
	// reads `<dir>/contract.md` unconditionally
	// (space.ContractCandidateReader.Read).
	//
	// REQUIRED SINCE 2026-08-28 (answers-that-hold P2). Publish already
	// overlaid this staging tree on the committed mirror candidate; what
	// changed is that leaving the tree here and NOT passing --staging is now
	// a REFUSAL rather than an implicit pickup, so every caller of this
	// helper passes the flag and the flag needs this file. The version is
	// 0.0.0 for the same reason contractCompatOverlayDescriptor uses it: the
	// candidate descriptor carries no version of its own, the publish verb's
	// --version does.
	mustWrite(r.t, filepath.Join(r.projectDir, ".a2a", "staging", r.system, "provides", slug, "contract.md"),
		contractCompatOverlayDescriptor(r.system, id))

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

// mustReadFile reads path and fails the test on error — mustWrite's own
// read-back counterpart, used by every test in this package that re-reads a
// file a verb just wrote.
func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
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
	r.seedFeedbackHubBranch()

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

// seedFeedbackHubBranch creates the branch inbound reports land on, in the
// fixture standing in for the product repo.
//
// It exists because P15 moved the feedback hub of record OFF the branch a
// release force-pushes: `a2a feedback submit` now bases its pull request on
// `feedbackBaseBranch`, and the space fixture only carries `main`. Without
// this the submit fails at `rev-parse origin/<hub>` with "Needed a single
// revision" — which is the fixture disagreeing with production, not a
// product defect, and is exactly how this test surfaced the move.
//
// Deliberately a branch OFF main rather than an orphan: this fixture stands
// in for "a repository that has the hub branch", and what the submit path
// needs is a resolvable base commit. The real hub's orphan shape is a
// property of the real repository, not of this contract.
func (r *hostRig) seedFeedbackHubBranch() {
	r.t.Helper()
	gitRun(r.t, r.fx.RemoteURL(), "branch", "--force", feedbackHubBranchForFixtures, "main")
}

// The branch name production uses (cmd/a2a's feedbackBaseBranch). Restated
// here rather than imported because `internal/e2e` drives the BUILT BINARY
// through its command line and shares no symbol with `package main` — so
// this is a fixture's copy of an external contract, the same way the fake
// host restates GitHub's wire shapes. If the two ever diverge, this test is
// what says so, by failing to resolve the base.
const feedbackHubBranchForFixtures = "feedback-hub"

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
	// The submitted descriptor is an unpublished 0.0.0 candidate. Publishing
	// 1.0.0 must establish a real descriptor-changing historical commit; a
	// fixture pre-seeded at 1.0.0 would create an invalid no-change event.
	draft, id := provider.stageContract("export", "0.0.0")
	provider.mustRun("submit", draft)
	if got := gitOutput(t, provider.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, "provides/export/contract.md") {
		t.Fatalf("origin main does not carry the contract at its §4.2 path:\n%s", got)
	}

	// 2. Preflight and publish the version through the production P6
	// composition. Preflight is read-only: the fake host must observe no PR.
	provider.mustRun("sync")
	beforePreflightPRs := len(provider.gh.PRs())
	preflight := provider.mustRun("contract", "preflight", "--version", "1.0.0", "--staging", ".a2a/staging/"+provider.system+"/provides/export", "--json", id)
	if !strings.Contains(preflight, `"target_version":"1.0.0"`) || len(provider.gh.PRs()) != beforePreflightPRs {
		t.Fatalf("preflight result/host mutation mismatch: stdout=%s PRs before=%d after=%d", preflight, beforePreflightPRs, len(provider.gh.PRs()))
	}
	provider.mustRun("contract", "publish", "--version", "1.0.0", "--staging", ".a2a/staging/"+provider.system+"/provides/export", id)
	if len(provider.gh.PRs()) != 2 {
		t.Fatalf("PRs = %d after submit+publish, want 2 (host calls: %v)",
			len(provider.gh.PRs()), provider.gh.Requests())
	}

	// 3. Resolve the exact merged historical version, materialize it beneath
	// an existing rooted parent, then run its complete self-suite. These are
	// successful built-binary paths, not parser/usage probes.
	provider.mustRun("sync")
	mustMkdirAll(t, filepath.Join(provider.projectDir, "vendor"))
	materialized := provider.mustRun("contract", "materialize", "--to", "vendor/export", "--json", id+"@1.0.0")
	if !strings.Contains(materialized, `"outcome":"materialized"`) {
		t.Fatalf("materialize result does not report a real write: %s", materialized)
	}
	if _, err := os.Stat(filepath.Join(provider.projectDir, "vendor", "export", "contract.md")); err != nil {
		t.Fatalf("materialized descriptor missing: %v", err)
	}
	checked := provider.mustRun("contract", "check", "--suite", "--json", id+"@1.0.0")
	if !strings.Contains(checked, `"passed":true`) || !strings.Contains(checked, `"outcome":"suite-consistent"`) {
		t.Fatalf("contract suite did not execute successfully: %s", checked)
	}

	// 4. The consumer registers itself.
	consumer.mustRun("sync")
	consumer.mustRun("contract", "adopt", id)
	if got := gitOutput(t, consumer.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, "beta/consumes.yaml") {
		t.Fatalf("origin main does not carry beta's consumer registry:\n%s", got)
	}

	// 5. Deprecate — the transition retire requires first. This is the
	// THIRD write by the provider on this artifact, and the second after a
	// merge: exactly the shape that used to vanish.
	provider.mustRun("sync")
	// NOTE: flags BEFORE the id — Go's flag package stops at the first
	// non-flag token, and only `contract adopt` lifts the id out first
	// (P27). See the P30 finding on argument order.
	provider.mustRun("contract", "deprecate",
		"--successor", id+"@2.0.0", "--sunset", "2027-01-01", id)

	// 6. Retire must REFUSE: beta is a registered consumer that has not
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
