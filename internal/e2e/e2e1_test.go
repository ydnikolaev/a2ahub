package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestE2E1Cascade is spec 10 §8 AC-2: the full §1.3 north-star cascade,
// git-fallback path (steps 2 hub-webhook and 7 hub-dashboard/local-HTML
// are v2 — spec §11 amendment; every OTHER step runs for real).
//
// WRITE steps use the plan's binding mechanism (real space.WriteFunnel +
// host.NewFakeHost + testkit/spacefixture, direct construction —
// FakeHost cannot be injected into the exec'd binary, see txtar_test.go's
// doc comment). Because FakeHost's PushBranch/OpenPR are no-ops (they
// never actually push/merge), each step's commit is made observable to
// the OTHER systems' clones via mergeBranchToMain (this package's own
// "simulate the PR got auto-merged" step, spec 10 §11: "the G2 required-
// review half is exercised via the host adapter's test double, standing
// in for the GitHub-side gate" — generalized here to every write, not
// just the gated ones, since NO system's clone in this fixture has a
// real GitHub host to perform the merge for it).
//
// READ steps (the OP-contract-level observable this test asserts against)
// run the BUILT a2a binary via exec, one isolated "read project" per
// system, each pointed at that system's OWN spacefixture clone (the exact
// directory the write steps just mutated) via a machine-config
// mirror_root + mirror_location pair — never re-implementing
// cache.Store's own read logic, never reaching into fold/cache internals
// directly.
//
// Cascade mapping (disclosed simplification, spec 10 §11-style
// disclosure): KindRequirement's own fold table (internal/fold/table.go)
// has NO respond/verify transition — `satisfy` is a requirement's sole
// closing move, direct from `acknowledged`. So "response"/"verify" (steps
// 5b/6) run against a SEPARATE, real `question` exchange (axon -> gamma,
// exercising the shipped respond/verify verbs exactly as P8 ships them),
// referenced by axon's final `satisfy` on its ORIGINAL requirement
// (step 7) alongside gamma's newly-published contract version — this is
// the closest real mapping the ACTUALLY SHIPPED OP-211 verb set supports
// for every one of the vision's 7 named steps, not an invented shortcut.
func TestE2E1Cascade(t *testing.T) {
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	origin := fx.RemoteURL()
	axonMirror := fx.Clone("axon")
	betaMirror := fx.Clone("beta")
	gammaMirror := fx.Clone("gamma")
	// See helpers_test.go's properManifestYAML doc comment: every
	// exec'd-binary read-surface assertion below (assertShow,
	// assertStatuslineExit) needs a LIST-shaped space.yaml, not
	// spacefixture's own auto-seeded MAP-shaped one.
	fixOriginManifest(t, origin, "fixture-space", axonMirror, betaMirror, gammaMirror)

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	fakeHost := host.NewFakeHost()
	// The cascade's own mergeBranchToMain ALREADY simulates every step's PR
	// having auto-merged into main (D-002, uniform auto-merge). A real
	// GitHub repo with "auto-delete head branches" enabled (the norm for
	// this kind of automation) deletes the branch once merged, so a LATER
	// verb touching the SAME artifact id (same deterministic branch name,
	// `a2a/<system>/<id>` — e.g. step 1's submit and step 7's satisfy both
	// act as axon on XR-axon-e2e1) finds NO existing PR and proceeds with a
	// fresh commit, rather than the funnel's retry-dedup short-circuit
	// (WriteStateAlreadyOpen/AlreadyMerged) silently swallowing its write.
	// FakeHost's own default FindPRByHeadBranch has no such expiry (byBranch
	// never forgets), so this override keeps it consistent with the
	// merge-then-delete reality this test already simulates. Retry/dedup
	// itself is proven elsewhere (TestT3RespondIdempotentRetryReturnsAlreadyOpen),
	// with its OWN, unmodified FakeHost.
	fakeHost.FindPRFunc = func(_ context.Context, _ host.FindPRRequest) (*host.PRInfo, error) { return nil, nil }
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0") // lifecycle verbs: nil validator (local legality gate precedes the funnel)

	// --- step 1: requirement (axon -> beta) -----------------------------
	reqID := "XR-axon-e2e1"
	submitArtifact(t, axonMirror, "requirement", reqID, "axon", []string{"beta"}, engine, fakeHost)
	mergeBranchToMain(t, axonMirror, lastOpenedBranch(fakeHost))
	fetchMain(t, betaMirror)

	assertShow(t, axonMirror, "fixture-space", reqID, "published")

	// --- step 2: hub webhook — SKIPPED (v2, spec §11 amendment) ---------

	// --- step 3: ack (beta acks axon's requirement) ---------------------
	ackCmd := cli.NewAckCommand(funnel, betaMirror, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", origin), e2eActorResolver("agent", "bot"))
	runLifecycleCmd(t, ackCmd, reqID)
	mergeBranchToMain(t, betaMirror, lastOpenedBranch(fakeHost))
	fetchMain(t, axonMirror)

	assertShow(t, betaMirror, "fixture-space", reqID, "acknowledged")

	// --- step 4: downstream requirement (beta -> gamma) ------------------
	downID := "XR-beta-e2e1-down"
	submitArtifact(t, betaMirror, "requirement", downID, "beta", []string{"gamma"}, engine, fakeHost)
	mergeBranchToMain(t, betaMirror, lastOpenedBranch(fakeHost))
	fetchMain(t, gammaMirror)

	assertShow(t, betaMirror, "fixture-space", downID, "published")

	// A real clarifying question, axon -> gamma, so this package can
	// exercise the SHIPPED respond/verify verbs for the vision's
	// "response"/"verify" pair (see this test's doc comment).
	qID := "XQ-axon-20260721-e2e1"
	submitArtifact(t, axonMirror, "question", qID, "axon", []string{"gamma"}, engine, fakeHost)
	mergeBranchToMain(t, axonMirror, lastOpenedBranch(fakeHost))
	fetchMain(t, gammaMirror)

	gammaAck := cli.NewAckCommand(funnel, gammaMirror, "fixture-space", "gamma", e2eManifest(), e2eHostConfig("gamma", origin), e2eActorResolver("agent", "bot"))
	runLifecycleCmd(t, gammaAck, qID)
	mergeBranchToMain(t, gammaMirror, lastOpenedBranch(fakeHost))

	gammaAccept := cli.NewAcceptCommand(funnel, gammaMirror, "fixture-space", "gamma", e2eManifest(), e2eHostConfig("gamma", origin), e2eActorResolver("agent", "bot"))
	runLifecycleCmd(t, gammaAccept, qID)
	mergeBranchToMain(t, gammaMirror, lastOpenedBranch(fakeHost))

	// --- step 5: contract version (gamma publishes) ----------------------
	writeContractDescriptorFor(t, gammaMirror, "gamma", "widget", "0.0.0")
	contractCmd := cli.NewContractCommand(nil, funnel, gammaMirror, "fixture-space", "gamma", e2eManifest(), e2eHostConfig("gamma", origin), e2eActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	if code := contractCmd.Run(context.Background(), []string{"publish", "--version", "1.0.0", "XC-gamma-widget"}, io); code != 0 {
		t.Fatalf("contract publish: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	mergeBranchToMain(t, gammaMirror, lastOpenedBranch(fakeHost))

	// --- step 5b: response (gamma responds to axon's question) ----------
	respondCmd := cli.NewRespondCommand(funnel, gammaMirror, "fixture-space", "gamma", e2eManifest(), e2eHostConfig("gamma", origin), e2eActorResolver("agent", "bot"))
	io2, _, errOut2 := newIO()
	if code := respondCmd.Run(context.Background(), []string{"--result", "answered", qID}, io2); code != 0 {
		t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut2.String())
	}
	responseID := latestXSFile(t, gammaMirror)
	mergeBranchToMain(t, gammaMirror, lastOpenedBranch(fakeHost))
	fetchMain(t, axonMirror)

	assertShow(t, gammaMirror, "fixture-space", responseID, "submitted")

	// --- step 6: verify (axon verifies gamma's response; D-024 auto-closes
	// the single-response question) --------------------------------------
	verifyCmd := cli.NewVerifyCommand(funnel, axonMirror, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", origin), e2eActorResolver("agent", "bot"))
	runLifecycleCmd(t, verifyCmd, responseID)
	mergeBranchToMain(t, axonMirror, lastOpenedBranch(fakeHost))

	assertShow(t, axonMirror, "fixture-space", qID, "closed")

	// --- step 7: satisfy (axon folds its ORIGINAL requirement) -----------
	satisfyCmd := cli.NewSatisfyCommand(funnel, axonMirror, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", origin), e2eActorResolver("agent", "bot"))
	io3, _, errOut3 := newIO()
	if code := satisfyCmd.Run(context.Background(), []string{"--refs", "XC-gamma-widget@1.0.0," + responseID, reqID}, io3); code != 0 {
		t.Fatalf("satisfy: code = %d, want 0; stderr=%s", code, errOut3.String())
	}
	mergeBranchToMain(t, axonMirror, lastOpenedBranch(fakeHost))

	assertShow(t, axonMirror, "fixture-space", reqID, "satisfied")

	// --- statusline signal, one system at a time (OP-contract level, via
	// the BUILT binary against each system's own mirror clone) -----------
	// beta: its OWN downstream requirement (XR-beta-e2e1-down, `blocking:
	// true`) is still open (never satisfied within this cascade — see this
	// test's doc comment on the "downstream requirement" step being a
	// real, independent transition, not further threaded into steps 5-7),
	// and axon's now-satisfied XR-axon-e2e1 is still `blocking: true` in
	// its own frontmatter — statusline signals a non-empty, urgent (11)
	// line naming 2 actionable items (which one lands in the single "p1"
	// slot is Go map-iteration order over per-space results, not asserted
	// here to avoid a flaky ordering dependency).
	betaStdout, betaStderr, betaCode := runReadVerbAs(t, betaMirror, "fixture-space", "beta", "statusline")
	if betaCode != 11 {
		t.Fatalf("statusline (beta): exit = %d, want 11; stdout=%s stderr=%s", betaCode, betaStdout, betaStderr)
	}
	if !strings.Contains(betaStdout, "2 new") {
		t.Fatalf("statusline (beta): expected 2 actionable items, got %q", betaStdout)
	}
	// gamma: like beta above, `blocking: true` on the (closed) question and
	// its own published contract keep signaling regardless of terminal
	// fold state (cache's own actionableReasons contract, not this test's
	// invention) — a non-empty, urgent line, never silent.
	gammaStdout, gammaStderr, gammaCode := runReadVerbAs(t, gammaMirror, "fixture-space", "gamma", "statusline")
	if gammaCode != 11 {
		t.Fatalf("statusline (gamma): exit = %d, want 11; stdout=%s stderr=%s", gammaCode, gammaStdout, gammaStderr)
	}
	if !strings.Contains(gammaStdout, "2 new") {
		t.Fatalf("statusline (gamma): expected 2 actionable items, got %q", gammaStdout)
	}
}

// submitArtifact drives a full, real submit round trip (validator + funnel
// + FakeHost) for a fresh requirement/question artifact — the E2E-1
// cascade's own "step 1/4" shape (a fresh OP-205 submit, not a lifecycle
// verb).
func submitArtifact(t *testing.T, mirrorDir, kind, id, from string, to []string, engine *validate.Engine, fakeHost *host.FakeHost) {
	t.Helper()
	stagingDir := t.TempDir()
	var path string
	switch kind {
	case "requirement":
		content := "---\n" +
			"schema: envelope/v1\n" +
			"id: " + id + "\n" +
			"type: requirement\n" +
			"title: t\n" +
			"space: fixture-space\n" +
			"from: " + from + "\n" +
			"to: [" + strings.Join(to, ", ") + "]\n" +
			"actor: {kind: agent, name: bot}\n" +
			"created: 2026-07-21T10:00:00Z\n" +
			"category: new-capability\n" +
			"priority: p3\n" +
			"blocking: true\n" +
			"classification: internal\n" +
			"acceptance_criteria: [\"works\"]\n" +
			"---\nbody\n"
		path = filepath.Join(stagingDir, id+".md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("submitArtifact: write draft: %v", err)
		}
	case "question":
		path = writeQuestionDraft(t, stagingDir, id, from, to[0])
	default:
		t.Fatalf("submitArtifact: unsupported kind %q", kind)
	}

	legality := cli.NewLegalityAdapter(mirrorDir, from, e2eManifest())
	resolver := cli.NewMirrorResolver(mirrorDir, e2eManifest())
	validator := cli.NewSubmitValidatorAdapter(engine, from, resolver, legality)
	funnel := space.NewWriteFunnel(fakeHost, validator, "0.1.0")

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", from, stagingDir, e2eHostConfig(from, mirrorDirOrigin(t, mirrorDir)))
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{path}, io); code != 0 {
		t.Fatalf("submit %s: code = %d, want 0; stdout=%s stderr=%s", id, code, out.String(), errOut.String())
	}
}

// lastOpenedBranch returns the Head branch of the most recently recorded
// OpenPR call — the real, authoritative branch name a write funnel call
// just minted (space.WriteFunnel names it `a2a/<system>/<sorted-ids-
// joined-by-plus>`, an internal cli.go convention this package must not
// reconstruct by guessing), used as mergeBranchToMain's own input.
func lastOpenedBranch(fakeHost *host.FakeHost) string {
	return fakeHost.Opens[len(fakeHost.Opens)-1].Head
}

// mirrorDirOrigin reads a clone's own `origin` remote URL (so
// submitArtifact's SubmitHostConfig.RemoteURL always matches the real
// fixture origin regardless of which system's clone is passed in).
func mirrorDirOrigin(t *testing.T, mirrorDir string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, mirrorDir, "remote", "get-url", "origin"))
}

// runLifecycleCmd runs a lifecycle cli.Command with a single artifact-id
// argument and fails the test loudly on a non-zero exit.
func runLifecycleCmd(t *testing.T, cmd cli.Command, id string) {
	t.Helper()
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
		t.Fatalf("%s %s: code = %d, want 0; stdout=%s stderr=%s", cmd.Name(), id, code, out.String(), errOut.String())
	}
}

// assertShow execs the BUILT binary's `a2a show <id>` against a throwaway
// read-project pointed at mirrorDir (OP-contract-level: the exact string
// cmd_show.go's own format emits), asserting the folded state.
func assertShow(t *testing.T, mirrorDir, spaceID, id, wantState string) {
	t.Helper()
	stdout, stderr, code := runReadVerb(t, mirrorDir, spaceID, "show", id)
	if code != 0 {
		t.Fatalf("show %s: code = %d, want 0; stdout=%s stderr=%s", id, code, stdout, stderr)
	}
	want := fmt.Sprintf("(%s)", wantState)
	if !strings.Contains(stdout, want) {
		t.Fatalf("show %s: expected folded state %q in output, got:\n%s", id, want, stdout)
	}
}

// runReadVerb execs the built a2a binary's read-only verb against a
// throwaway project whose own system id equals spaceID's connected
// system (this cascade always reads as the mirror's OWN owning system —
// see runReadVerbAs for the general form).
func runReadVerb(t *testing.T, mirrorDir, spaceID, verb string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runReadVerbAs(t, mirrorDir, spaceID, filepath.Base(mirrorDir), verb, args...)
}

// runReadVerbAs execs the built a2a binary's read-only verb (`show`,
// `thread`, `inbox`, `outbox`, `statusline`, `search`, `contracts`) against
// a throwaway project configured with ownSystem and a mirror_root/
// mirror_location pair pointing DIRECTLY at mirrorDir — the exact
// directory a write step just mutated, never a fresh clone (a fresh clone
// would need its own fetch and miss any commit still sitting only on a
// local branch that mergeBranchToMain already folded into mirrorDir's own
// main).
func runReadVerbAs(t *testing.T, mirrorDir, spaceID, ownSystem, verb string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	projectDir := t.TempDir()
	homeDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(projectDir, ".a2a"), 0o755); err != nil {
		t.Fatalf("runReadVerbAs: mkdir: %v", err)
	}
	projCfg := fmt.Sprintf("system: %s\nspaces:\n  - id: %s\n    mirror_location: %s\n", ownSystem, spaceID, filepath.Base(mirrorDir))
	if err := os.WriteFile(filepath.Join(projectDir, ".a2a", "config.yaml"), []byte(projCfg), 0o644); err != nil {
		t.Fatalf("runReadVerbAs: write project config: %v", err)
	}
	cfgDir := filepath.Join(homeDir, ".config", "a2a")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("runReadVerbAs: mkdir machine config dir: %v", err)
	}
	machineCfg := fmt.Sprintf("mirror_root: %s\n", filepath.Dir(mirrorDir))
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(machineCfg), 0o644); err != nil {
		t.Fatalf("runReadVerbAs: write machine config: %v", err)
	}

	bin := filepath.Join(binDir, "a2a")
	cmdArgs := append([]string{verb}, args...)
	cmd := exec.Command(bin, cmdArgs...)
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("runReadVerbAs: exec %s %v: %v", bin, cmdArgs, err)
		}
	}
	return out.String(), errBuf.String(), exitCode
}
