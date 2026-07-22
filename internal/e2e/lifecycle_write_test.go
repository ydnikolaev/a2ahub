package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestT3LifecycleVerbs is AC-1's direct-construction half (see
// txtar_test.go's doc comment for why the write path cannot be exec'd):
// every OP-211 lifecycle verb, driven against a REAL space.WriteFunnel +
// host.NewFakeHost + testkit/spacefixture clone — the plan's binding
// mechanism for the write path (cmd_submit_test.go:200-245 /
// cmd_lifecycle_test.go:496-522 idiom). Each subtest seeds the minimal
// legal prior state the verb's own transition requires (mirroring
// internal/cli's own P8 test fixtures), then asserts: exit 0, exactly one
// real PushBranch + one real OpenPR call recorded on the FakeHost (proving
// the commit actually landed via git, not just that a fake funnel recorded
// a call), and no leaked state onto another subtest's fixture (each
// subtest builds its own spacefixture.New + t.Parallel()).
func TestT3LifecycleVerbs(t *testing.T) {
	t.Parallel()

	type tc struct {
		name  string
		seed  func(t *testing.T, mirrorDir string) (ownSystem string, args []string)
		build func(funnel *space.WriteFunnel, mirrorDir, ownSystem string, hostCfg cli.SubmitHostConfig) cli.Command
	}

	cases := []tc{
		{
			name: "ack",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XQ-axon-20260721-a001"
				writeQuestionArtifact(t, dir, id, "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "submit", "axon")
				return "beta", []string{id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewAckCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "accept",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XQ-axon-20260721-a002"
				writeQuestionArtifact(t, dir, id, "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "submit", "axon")
				writeLifecycleEvent(t, dir, "beta", 1, id, "acknowledge", "beta")
				return "beta", []string{id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewAcceptCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "decline",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XQ-axon-20260721-a003"
				writeQuestionArtifact(t, dir, id, "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "submit", "axon")
				return "beta", []string{"--reason", "not needed", id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewDeclineCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "start",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XQ-axon-20260721-a004"
				writeQuestionArtifact(t, dir, id, "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "submit", "axon")
				writeLifecycleEvent(t, dir, "beta", 1, id, "acknowledge", "beta")
				writeLifecycleEvent(t, dir, "beta", 2, id, "accept", "beta")
				return "beta", []string{id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewStartCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "cancel",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XQ-axon-20260721-a005"
				writeQuestionArtifact(t, dir, id, "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "submit", "axon")
				return "axon", []string{id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewCancelCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "withdraw",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XR-axon-widget"
				writeRequirementArtifact(t, dir, id, "axon", "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "publish", "axon")
				return "axon", []string{id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewWithdrawCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "supersede",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XR-axon-legacy"
				writeRequirementArtifact(t, dir, id, "axon", "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "publish", "axon")
				return "axon", []string{"--refs", "XR-axon-legacy-v2", id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewSupersedeCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "satisfy",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XR-axon-satisfiable"
				writeRequirementArtifact(t, dir, id, "axon", "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "publish", "axon")
				writeLifecycleEvent(t, dir, "beta", 1, id, "acknowledge", "beta")
				return "axon", []string{"--refs", "XC-axon-widget@1.0.0,XS-beta-20260721-p1p1", id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewSatisfyCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "verify_pass",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XH-axon-20260721-n001"
				writeHandoffArtifact(t, dir, id, "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "submit", "axon")
				writeLifecycleEvent(t, dir, "beta", 1, id, "acknowledge", "beta")
				return "beta", []string{id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewVerifyPassCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "verify_fail",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XH-axon-20260721-n002"
				writeHandoffArtifact(t, dir, id, "beta")
				writeLifecycleEvent(t, dir, "axon", 0, id, "submit", "axon")
				writeLifecycleEvent(t, dir, "beta", 1, id, "acknowledge", "beta")
				return "beta", []string{"--findings", "did not meet spec", id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewVerifyFailCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "note",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XQ-axon-20260721-a006"
				writeQuestionArtifact(t, dir, id, "beta")
				return "gamma", []string{"--note", "reminder: please respond", id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewNoteCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("agent", "bot"))
			},
		},
		{
			name: "approve",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XD-axon-20260721-e001"
				writeDecisionArtifact(t, dir, id, []string{"beta", "gamma"})
				writeLifecycleEvent(t, dir, "axon", 0, id, "propose", "axon")
				return "beta", []string{id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewApproveCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("human", "owner"))
			},
		},
		{
			name: "reject",
			seed: func(t *testing.T, dir string) (string, []string) {
				id := "XD-axon-20260721-e002"
				writeDecisionArtifact(t, dir, id, []string{"beta", "gamma"})
				writeLifecycleEvent(t, dir, "axon", 0, id, "propose", "axon")
				return "beta", []string{"--reason", "scope creep", id}
			},
			build: func(f *space.WriteFunnel, dir, sys string, hc cli.SubmitHostConfig) cli.Command {
				return cli.NewRejectCommand(f, dir, "fixture-space", sys, e2eManifest(), hc, e2eActorResolver("human", "owner"))
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := spacefixture.New(t, "axon", "beta", "gamma")
			mirrorDir := fx.Clone("beta")
			ownSystem, args := c.seed(t, mirrorDir)

			fakeHost := host.NewFakeHost()
			funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
			hostCfg := e2eHostConfig(ownSystem, fx.RemoteURL())

			cmd := c.build(funnel, mirrorDir, ownSystem, hostCfg)
			io, out, errOut := newIO()
			code := cmd.Run(context.Background(), args, io)
			if code != 0 {
				t.Fatalf("%s: code = %d, want 0; stdout=%s stderr=%s", c.name, code, out.String(), errOut.String())
			}
			if len(fakeHost.Pushes) != 1 {
				t.Fatalf("%s: expected exactly one real PushBranch call, got %d", c.name, len(fakeHost.Pushes))
			}
			if len(fakeHost.Opens) != 1 {
				t.Fatalf("%s: expected exactly one real OpenPR call, got %d", c.name, len(fakeHost.Opens))
			}
		})
	}
}

// TestT3BlockUnblock is the block/unblock pair (block leaves a "recover
// prior state" marker that unblock reads back — a genuine two-step
// round-trip against the SAME mirror).
func TestT3BlockUnblock(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("beta")
	id := "XQ-axon-20260721-a007"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := e2eHostConfig("beta", fx.RemoteURL())

	blockCmd := cli.NewBlockCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), hostCfg, e2eActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := blockCmd.Run(context.Background(), []string{"--refs", "XQ-axon-20260721-blocker", id}, io); code != 0 {
		t.Fatalf("block: code = %d, want 0; stderr=%s", code, errOut.String())
	}

	unblockCmd := cli.NewUnblockCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), hostCfg, e2eActorResolver("agent", "bot"))
	io2, _, errOut2 := newIO()
	if code := unblockCmd.Run(context.Background(), []string{id}, io2); code != 0 {
		t.Fatalf("unblock: code = %d, want 0; stderr=%s", code, errOut2.String())
	}
	// block and unblock share the SAME deterministic branch (a2a/beta/<id>
	// — the write funnel keys idempotency off artifact id, not verb), so
	// unblock's own funnel call short-circuits to WriteStateAlreadyOpen
	// against block's still-open PR — exactly ONE real OpenPR, not two.
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call (block+unblock share a branch, dedup), got %d", len(fakeHost.Opens))
	}
}

// TestT3RespondVerifyDispute chains respond -> verify (auto-closes a
// single-response parent) and, on a second parent, respond -> dispute —
// real funnel + FakeHost throughout, proving state committed by one verb
// is legible to the NEXT verb against the same real git mirror (no fake
// funnel materialization step needed, unlike internal/cli's own unit
// tests: a REAL commit is really on disk).
func TestT3RespondVerifyDispute(t *testing.T) {
	t.Parallel()

	t.Run("verify_auto_closes", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon", "beta", "gamma")
		mirrorDir := fx.Clone("beta")
		parentID := "XQ-axon-20260721-f001"
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

		fakeHost := host.NewFakeHost()
		funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")

		respondCmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := respondCmd.Run(context.Background(), []string{"--result", "answered", parentID}, io); code != 0 {
			t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
		}
		responseID := latestXSFile(t, mirrorDir)

		verifyCmd := cli.NewVerifyCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
		io2, _, errOut2 := newIO()
		if code := verifyCmd.Run(context.Background(), []string{responseID}, io2); code != 0 {
			t.Fatalf("verify: code = %d, want 0; stderr=%s", code, errOut2.String())
		}
	})

	t.Run("dispute", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon", "beta", "gamma")
		mirrorDir := fx.Clone("beta")
		parentID := "XQ-axon-20260721-f002"
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

		fakeHost := host.NewFakeHost()
		funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")

		respondCmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := respondCmd.Run(context.Background(), []string{"--result", "answered", parentID}, io); code != 0 {
			t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
		}
		responseID := latestXSFile(t, mirrorDir)

		disputeCmd := cli.NewDisputeCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
		io2, _, errOut2 := newIO()
		if code := disputeCmd.Run(context.Background(), []string{"--reason", "wrong answer", responseID}, io2); code != 0 {
			t.Fatalf("dispute: code = %d, want 0; stderr=%s", code, errOut2.String())
		}
	})
}

// TestT3CloseFromResponded is the close-after-respond chain.
func TestT3CloseFromResponded(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("beta")
	id := "XQ-axon-20260721-f003"
	seedAcceptedQuestion(t, mirrorDir, id, "beta")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")

	respondCmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := respondCmd.Run(context.Background(), []string{"--result", "answered", id}, io); code != 0 {
		t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
	}

	closeCmd := cli.NewCloseCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", fx.RemoteURL()), e2eActorResolver("agent", "bot"))
	io2, _, errOut2 := newIO()
	if code := closeCmd.Run(context.Background(), []string{id}, io2); code != 0 {
		t.Fatalf("close: code = %d, want 0; stderr=%s", code, errOut2.String())
	}
}

// TestT3RespondIdempotentRetryReturnsAlreadyOpen is AC-301.1's idempotent-
// retry proof against the REAL funnel + FakeHost (cmd_lifecycle_test.go's
// own TestRespondIdempotentRetryReturnsAlreadyOpen idiom, this package's
// copy): a retried `respond` with IDENTICAL content and a FIXED clock lands
// on the SAME deterministic branch, short-circuiting to
// space.WriteStateAlreadyOpen — no second PR.
func TestT3RespondIdempotentRetryReturnsAlreadyOpen(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	mirrorDir := fx.Clone("beta")
	parentID := "XQ-axon-20260721-r003"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := e2eHostConfig("beta", fx.RemoteURL())
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }

	cmd1 := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), hostCfg, e2eActorResolver("agent", "bot"))
	cmd1.SetClockForTest(fixedNow)
	io1, _, errOut1 := newIO()
	if code := cmd1.Run(context.Background(), []string{"--result", "answered", parentID}, io1); code != 0 {
		t.Fatalf("respond (1st): code = %d, want 0; stderr=%s", code, errOut1.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call after the 1st respond, got %d", len(fakeHost.Opens))
	}

	cmd2 := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), hostCfg, e2eActorResolver("agent", "bot"))
	cmd2.SetClockForTest(fixedNow)
	io2, out2, errOut2 := newIO()
	if code := cmd2.Run(context.Background(), []string{"--result", "answered", parentID}, io2); code != 0 {
		t.Fatalf("respond (retry): code = %d, want 0; stderr=%s", code, errOut2.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly one OpenPR call after the retry (dedup), got %d", len(fakeHost.Opens))
	}
	if !strings.Contains(out2.String(), "already submitted") {
		t.Fatalf("expected the retry's stdout to report the already-submitted idempotent path, got %q", out2.String())
	}
}

// latestXSFile finds the most-recently-written XS-*.md response file under
// mirrorDir (the real funnel commits it directly to disk — no
// materialization step needed, unlike internal/cli's fake-funnel tests).
func latestXSFile(t *testing.T, mirrorDir string) string {
	t.Helper()
	out := gitOutput(t, mirrorDir, "log", "--name-only", "--pretty=format:", "-1")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "/exchanges/XS-") {
			base := line[strings.LastIndex(line, "/")+1:]
			return strings.TrimSuffix(base, ".md")
		}
	}
	t.Fatalf("latestXSFile: no XS- response file found in the latest commit under %s", mirrorDir)
	return ""
}
