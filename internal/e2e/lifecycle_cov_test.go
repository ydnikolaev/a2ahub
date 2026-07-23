package e2e

// This file is wave 14c's own addition to the T3 Lifecycle family (spec
// 26-e2e-full-coverage.md §2's "Lifecycle" row): every OP-211 generic
// lifecycle verb + the OP-212 approve/reject pair, each proven with (a) a
// legal transition whose folded state is read back via the BUILT binary's
// `a2a show` (e2e1_test.go's own assertShow, this package's sanctioned
// read-surface helper — never a second fold-reading implementation) and
// (b) one illegal transition refused locally with the owning LFC-###
// machine code (fold/legality.go's own two-code set: LFC-001 illegal-
// transition, LFC-002 unauthorized-actor). Priors are seeded RAW via
// helpers_test.go's writeXArtifact/writeLifecycleEvent (untracked files on
// the mirror's working tree, verified by this file's own spike to survive
// every git plumbing step assertShow's own read path needs: checkout,
// `reset --hard`, `merge --no-ff` — none of which touch untracked paths).
//
// Real space.WriteFunnel + host.NewFakeHost throughout (this package's own
// direct-construction idiom, spec §11 amendment: exec cannot reach a host).
// validator is nil everywhere here, matching e2e1_test.go's own
// TestE2E1Cascade precedent ("lifecycle verbs: nil validator — the local
// legality gate precedes the funnel").

import (
	"context"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// newVerbFixture builds a fresh 3-system spacefixture, clones it as
// cloneSystem, and returns a real FakeHost + WriteFunnel pair. When
// fixManifest is true, origin's space.yaml is rewritten list-shaped
// (helpers_test.go's properManifestYAML) and this clone fast-forwarded
// onto it — required before any assertShow (exec'd-binary) read; skipped
// for illegal-transition-only fixtures that are refused locally and never
// reach the funnel or the built binary.
func newVerbFixture(t *testing.T, cloneSystem string, fixManifest bool) (mirrorDir, remote string, fakeHost *host.FakeHost, funnel *space.WriteFunnel) {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	remote = fx.RemoteURL()
	mirrorDir = fx.Clone(cloneSystem)
	if fixManifest {
		fixOriginManifest(t, remote, "fixture-space", mirrorDir)
	}
	fakeHost = host.NewFakeHost()
	funnel = space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	return mirrorDir, remote, fakeHost, funnel
}

// mustRunLegal runs cmd and fails the test loudly on a non-zero exit —
// the "legal transition succeeded" half of every verb's pair.
func mustRunLegal(t *testing.T, verb string, cmd cli.Command, args []string) {
	t.Helper()
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), args, io); code != 0 {
		t.Fatalf("%s: code = %d, want 0; stdout=%s stderr=%s", verb, code, out.String(), errOut.String())
	}
}

// assertIllegalRetry runs cmd and asserts: non-zero exit, stderr names
// wantCode (the LFC-### transition-matrix teeth this wave's brief calls
// for), and the write funnel is NOT reached again (Pushes/Opens counts
// unchanged from immediately before this call) — the refusal happens at
// the LOCAL legality gate, before space.WriteFunnel.Submit, exactly as
// fold.CheckLegality's callers (cmd_lifecycle.go) are specified to behave.
func assertIllegalRetry(t *testing.T, verb string, cmd cli.Command, args []string, wantCode string, fakeHost *host.FakeHost) {
	t.Helper()
	pushesBefore, opensBefore := len(fakeHost.Pushes), len(fakeHost.Opens)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), args, io)
	if code == 0 {
		t.Fatalf("%s: expected a non-zero exit on the illegal retry; got 0", verb)
	}
	if !strings.Contains(errOut.String(), wantCode) {
		t.Fatalf("%s: expected the refusal to name %s; got %q", verb, wantCode, errOut.String())
	}
	if len(fakeHost.Pushes) != pushesBefore || len(fakeHost.Opens) != opensBefore {
		t.Fatalf("%s: expected the write funnel NOT to be reached again on the illegal retry; pushes %d->%d opens %d->%d",
			verb, pushesBefore, len(fakeHost.Pushes), opensBefore, len(fakeHost.Opens))
	}
}

// TestT3LifecycleTransitionCoverage is wave 14c's own coverage test: one
// subtest per OP-211 verb (+ approve/reject), each asserting the legal
// transition's folded state (via assertShow) and the illegal transition's
// LFC-### refusal (transition-matrix teeth, fold/legality.go).
func TestT3LifecycleTransitionCoverage(t *testing.T) {
	t.Parallel()

	t.Run("ack", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XQ-axon-20260721-b001"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

		cmd := cli.NewAckCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "ack", cmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "acknowledged")

		// illegal: ack again from `acknowledged` (submitted-only fromState) -> LFC-001
		assertIllegalRetry(t, "ack", cmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("accept", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XQ-axon-20260721-b002"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")

		cmd := cli.NewAcceptCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "accept", cmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "accepted")

		// illegal: accept again from `accepted` (acknowledged-only fromState) -> LFC-001
		assertIllegalRetry(t, "accept", cmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("start", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XQ-axon-20260721-b003"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")

		cmd := cli.NewStartCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "start", cmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "in_progress")

		// illegal: start again from `in_progress` (accepted-only fromState) -> LFC-001
		assertIllegalRetry(t, "start", cmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("decline", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XQ-axon-20260721-b004"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

		cmd := cli.NewDeclineCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "decline", cmd, []string{"--reason", "not needed", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "declined")

		// illegal: decline again from `declined` (no row) -> LFC-001 (transition-matrix teeth)
		assertIllegalRetry(t, "decline", cmd, []string{"--reason", "still not needed", id}, "LFC-001", fakeHost)
	})

	t.Run("cancel", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "axon", true)
		id := "XQ-axon-20260721-b005"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

		cmd := cli.NewCancelCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "cancel", cmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "cancelled")

		// illegal: cancel again from `cancelled` (no row) -> LFC-001
		assertIllegalRetry(t, "cancel", cmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("withdraw", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "axon", true)
		id := "XR-axon-withdraw-lc"
		writeRequirementArtifact(t, mirrorDir, id, "axon", "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")

		cmd := cli.NewWithdrawCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "withdraw", cmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "withdrawn")

		// illegal: withdraw again from `withdrawn` (draft/published/acknowledged-only) -> LFC-001
		assertIllegalRetry(t, "withdraw", cmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("supersede", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "axon", true)
		id := "XR-axon-supersede-lc"
		writeRequirementArtifact(t, mirrorDir, id, "axon", "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")

		cmd := cli.NewSupersedeCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "supersede", cmd, []string{"--refs", "XR-axon-supersede-lc-v2", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "superseded")

		// illegal: supersede again from `superseded` (no row) -> LFC-001
		assertIllegalRetry(t, "supersede", cmd, []string{"--refs", "XR-axon-supersede-lc-v3", id}, "LFC-001", fakeHost)
	})

	t.Run("satisfy", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "axon", true)
		id := "XR-axon-satisfy-lc"
		writeRequirementArtifact(t, mirrorDir, id, "axon", "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")

		cmd := cli.NewSatisfyCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "satisfy", cmd, []string{"--refs", "XC-axon-widget@1.0.0,XS-beta-20260721-p1p1", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "satisfied")

		// illegal: satisfy again from `satisfied` (acknowledged-only) -> LFC-001
		assertIllegalRetry(t, "satisfy", cmd, []string{"--refs", "XC-axon-widget@1.0.0,XS-beta-20260721-p1p1", id}, "LFC-001", fakeHost)
	})

	t.Run("verify_pass", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XH-axon-20260721-b101"
		writeHandoffArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")

		cmd := cli.NewVerifyPassCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "verify_pass", cmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "accepted")

		// illegal: verify-pass again from `accepted` (acknowledged-only) -> LFC-001
		assertIllegalRetry(t, "verify_pass", cmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("verify_fail", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XH-axon-20260721-b102"
		writeHandoffArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")

		cmd := cli.NewVerifyFailCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "verify_fail", cmd, []string{"--findings", "did not meet spec", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "rejected")

		// illegal: verify-fail again from `rejected` (acknowledged-only) -> LFC-001
		assertIllegalRetry(t, "verify_fail", cmd, []string{"--findings", "still failing", id}, "LFC-001", fakeHost)
	})

	t.Run("respond", func(t *testing.T) {
		t.Parallel()
		// legal: accepted -> responded (parent), submitted (response) —
		// exchangeRows' `respond` row.
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		parentID := "XQ-axon-20260721-b201"
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

		cmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "respond", cmd, []string{"--result", "answered", parentID})
		// latestXSFile reads the LATEST commit's own file list — it must
		// run BEFORE mergeBranchToMain, whose own `merge --no-ff` commit
		// carries no file list of its own.
		responseID := latestXSFile(t, mirrorDir)
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", parentID, "responded")
		assertShow(t, mirrorDir, "fixture-space", responseID, "submitted")

		// illegal: respond on a freshly-submitted (not yet acknowledged)
		// parent -> LFC-001 (respond's fromState list excludes `submitted`).
		mirrorDir2, remote2, fakeHost2, funnel2 := newVerbFixture(t, "beta", false)
		badParentID := "XQ-axon-20260721-b202"
		writeQuestionArtifact(t, mirrorDir2, badParentID, "beta")
		writeLifecycleEvent(t, mirrorDir2, "axon", 0, badParentID, "submit", "axon")
		cmd2 := cli.NewRespondCommand(funnel2, mirrorDir2, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote2), e2eActorResolver("agent", "bot"))
		assertIllegalRetry(t, "respond", cmd2, []string{"--result", "answered", badParentID}, "LFC-001", fakeHost2)
	})

	t.Run("verify", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		parentID := "XQ-axon-20260721-b301"
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

		respondCmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "respond (setup)", respondCmd, []string{"--result", "answered", parentID})
		responseID := latestXSFile(t, mirrorDir)
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

		verifyCmd := cli.NewVerifyCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "verify", verifyCmd, []string{responseID})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", responseID, "verified")
		// D-024: single-response verify auto-closes the parent in the same PR.
		assertShow(t, mirrorDir, "fixture-space", parentID, "closed")

		// illegal: verify again from `verified` (submitted-only) -> LFC-001
		assertIllegalRetry(t, "verify", verifyCmd, []string{responseID}, "LFC-001", fakeHost)
	})

	t.Run("dispute", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		parentID := "XQ-axon-20260721-b302"
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

		respondCmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "respond (setup)", respondCmd, []string{"--result", "answered", parentID})
		responseID := latestXSFile(t, mirrorDir)
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))

		disputeCmd := cli.NewDisputeCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "dispute", disputeCmd, []string{"--reason", "wrong answer", "--reason-code", "out-of-scope", responseID})
		// --reason-code is exercised for real (spec §2's "dispute --reason-code"
		// callout): assert the committed event actually carries it, while
		// still on the ephemeral branch (before the merge below).
		diff := gitOutput(t, mirrorDir, "show", "HEAD")
		if !strings.Contains(diff, "reason_code: out-of-scope") {
			t.Fatalf("dispute: expected the committed event to carry reason_code: out-of-scope; got:\n%s", diff)
		}
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", responseID, "disputed")
		// dispute's fold side-effect reopens the parent responded -> in_progress.
		assertShow(t, mirrorDir, "fixture-space", parentID, "in_progress")

		// illegal: dispute again from `disputed` (submitted-only) -> LFC-001
		assertIllegalRetry(t, "dispute", disputeCmd, []string{"--reason", "again", responseID}, "LFC-001", fakeHost)
	})

	t.Run("close", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XQ-axon-20260721-b303"
		seedAcceptedQuestion(t, mirrorDir, id, "beta")

		respondCmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "respond (setup)", respondCmd, []string{"--result", "answered", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "responded")

		closeCmd := cli.NewCloseCommand(funnel, mirrorDir, "fixture-space", "axon", e2eManifest(), e2eHostConfig("axon", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "close", closeCmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "closed")

		// illegal: close again from `closed` (responded-only) -> LFC-001
		assertIllegalRetry(t, "close", closeCmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("block", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XQ-axon-20260721-b401"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")

		cmd := cli.NewBlockCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "block", cmd, []string{"--refs", "XQ-axon-20260721-b401k", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "blocked")

		// illegal: block again from `blocked` (ack/accept/in_progress-only) -> LFC-001
		assertIllegalRetry(t, "block", cmd, []string{"--refs", "XQ-axon-20260721-b401m", id}, "LFC-001", fakeHost)
	})

	t.Run("unblock", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		// block and unblock share the SAME deterministic branch
		// `a2a/beta/<id>` (WriteFunnel keys idempotency off artifact id,
		// not verb — TestT3BlockUnblock's own documented finding). The
		// default FakeHost.FindPRByHeadBranch (state=all, matching the
		// real GitHubHost) would find block's still-"open" PR and
		// short-circuit unblock's OWN Submit call before it ever commits
		// unblock's event — this override models the real-world "auto-
		// delete head branches" norm E2E1's own TestE2E1Cascade already
		// relies on for the identical reason, so unblock's event actually
		// lands and its recovered folded state is genuinely observable.
		fakeHost.FindPRFunc = func(_ context.Context, _ host.FindPRRequest) (*host.PRInfo, error) { return nil, nil }

		id := "XQ-axon-20260721-b402"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")

		blockCmd := cli.NewBlockCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "block (setup)", blockCmd, []string{"--refs", "XQ-axon-20260721-b402k", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "blocked")

		unblockCmd := cli.NewUnblockCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "unblock", unblockCmd, []string{id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		// unblock recovers the pre-block fold state (accepted, per this
		// seed's own accept event) — the StateDynamic row's own resolution.
		assertShow(t, mirrorDir, "fixture-space", id, "accepted")

		// illegal: unblock again from `accepted` (not blocked) -> LFC-001
		assertIllegalRetry(t, "unblock", unblockCmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("approve", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XD-axon-20260721-b501"
		// single required approver -> one approve reaches quorum in one
		// shot, so the resulting folded state is a plain, assertable
		// "approved" rather than an internal partial-quorum sub-state.
		writeDecisionArtifact(t, mirrorDir, id, []string{"beta"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "propose", "axon")

		cmd := cli.NewApproveCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("human", "owner"))
		mustRunLegal(t, "approve", cmd, []string{id})
		// P8-3: approve ALWAYS carries the advisory G3 gate marker.
		if len(fakeHost.Opens) == 0 || !strings.Contains(fakeHost.Opens[len(fakeHost.Opens)-1].Body, "G3") {
			t.Fatalf("approve: expected an advisory G3 gate marker in the PR body; got %+v", fakeHost.Opens)
		}
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "approved")

		// illegal: approve again from `approved` (proposed-only) -> LFC-001
		assertIllegalRetry(t, "approve", cmd, []string{id}, "LFC-001", fakeHost)
	})

	t.Run("reject", func(t *testing.T) {
		t.Parallel()
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
		id := "XD-axon-20260721-b502"
		writeDecisionArtifact(t, mirrorDir, id, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "propose", "axon")

		cmd := cli.NewRejectCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("human", "owner"))
		mustRunLegal(t, "reject", cmd, []string{"--reason", "scope creep", id})
		// P8-3: reject ALWAYS carries the advisory G3 gate marker too.
		if len(fakeHost.Opens) == 0 || !strings.Contains(fakeHost.Opens[len(fakeHost.Opens)-1].Body, "G3") {
			t.Fatalf("reject: expected an advisory G3 gate marker in the PR body; got %+v", fakeHost.Opens)
		}
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "rejected")

		// illegal: reject again from `rejected` (proposed-only) -> LFC-001
		assertIllegalRetry(t, "reject", cmd, []string{"--reason", "still no", id}, "LFC-001", fakeHost)
	})

	t.Run("note", func(t *testing.T) {
		t.Parallel()
		// note is transition-free (D-025): it authors an annotation event
		// but never touches fold state and carries NO legality check
		// (internal/cli's own TestNoteSkipsLegalityCheck, cmd_lifecycle.go's
		// NoteCommand.Run — no fold.CheckLegality call at all). There is
		// consequently no illegal-transition half to assert here: forcing
		// one would encode behavior the product does not have (DEFECT
		// POLICY's "never encode broken behavior" cuts both ways — it also
		// forbids fabricating a refusal that doesn't exist). This subtest's
		// own positive proof IS that folded state stays UNCHANGED across a
		// note, on a bystander system's own actor (any party, per this
		// spec's own Open Q1).
		mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "gamma", true)
		id := "XQ-axon-20260721-b601"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

		cmd := cli.NewNoteCommand(funnel, mirrorDir, "fixture-space", "gamma", e2eManifest(), e2eHostConfig("gamma", remote), e2eActorResolver("agent", "bot"))
		mustRunLegal(t, "note", cmd, []string{"--note", "reminder: please respond", id})
		mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
		assertShow(t, mirrorDir, "fixture-space", id, "submitted")
	})
}

// TestT3LifecycleMultiIDBatch is spec §2's "multi-id forms" callout (P8-1:
// N artifact IDs on one lifecycle verb produce exactly one commit/one PR)
// exercised at the e2e level (real WriteFunnel + FakeHost, not the fake
// funnel internal/cli's own unit test uses) with folded-state proof for
// every one of the N ids, not just the commit/PR count.
func TestT3LifecycleMultiIDBatch(t *testing.T) {
	t.Parallel()
	mirrorDir, remote, fakeHost, funnel := newVerbFixture(t, "beta", true)
	ids := []string{"XQ-axon-20260721-c001", "XQ-axon-20260721-c002", "XQ-axon-20260721-c003"}
	for i, id := range ids {
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", i, id, "submit", "axon")
	}

	cmd := cli.NewAckCommand(funnel, mirrorDir, "fixture-space", "beta", e2eManifest(), e2eHostConfig("beta", remote), e2eActorResolver("agent", "bot"))
	mustRunLegal(t, "ack (batch)", cmd, ids)
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("ack (batch): expected exactly ONE commit/PR for a 3-id batch, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
	mergeBranchToMain(t, mirrorDir, lastOpenedBranch(fakeHost))
	for _, id := range ids {
		assertShow(t, mirrorDir, "fixture-space", id, "acknowledged")
	}
}
