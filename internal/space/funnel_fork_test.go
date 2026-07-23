package space

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// forbidPushTo makes fake refuse every push to remoteURL the way GitHub
// refuses a non-collaborator, and accept every other target — the exact
// shape of the P28 report (`Permission to ydnikolaev/a2ahub.git denied`).
func forbidPushTo(fake *host.FakeHost, remoteURL string) {
	fake.PushBranchFunc = func(_ context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
		if req.RemoteURL == remoteURL {
			return host.PushBranchResult{}, &host.Error{
				Op: "PushBranch", Input: req.Branch,
				Err: fmt.Errorf("%w: %w: remote: Permission denied", host.ErrPushRejected, host.ErrPushForbidden),
			}
		}
		return host.PushBranchResult{Branch: req.Branch}, nil
	}
}

// forkFallbackRequest is a submit that opts into the fork path (what
// `a2a feedback submit` does).
func forkFallbackRequest(t *testing.T, fx *spacefixture.Fixture) SubmitRequest {
	t.Helper()
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)
	req.AllowForkFallback = true
	return req
}

// TestFunnelFallsBackToAFork is AC-904.1: a push refused for lack of write
// access ensures the submitter's fork, pushes there, and opens the PR with
// an owner-qualified head into the ORIGINAL repo's base branch.
func TestFunnelFallsBackToAFork(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := forkFallbackRequest(t, fx)

	fake := host.NewFakeHost()
	fake.ForkLogin = "consumer"
	forbidPushTo(fake, fx.RemoteURL())

	result, err := NewWriteFunnel(fake, nil, "0.1.0").Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}

	branch := "a2a/axon/submit/" + req.ArtifactID
	if len(fake.Forks) != 1 || fake.Forks[0].Repo != req.Repo {
		t.Fatalf("EnsureFork calls = %+v, want exactly one for %+v", fake.Forks, req.Repo)
	}
	if len(fake.Pushes) != 2 {
		t.Fatalf("pushes = %d, want 2 (the refused one, then the fork)", len(fake.Pushes))
	}
	if got := fake.Pushes[1].RemoteURL; !strings.Contains(got, "consumer") {
		t.Errorf("second push went to %q, want the fork remote", got)
	}
	if len(fake.Opens) != 1 {
		t.Fatalf("opens = %d, want 1", len(fake.Opens))
	}
	open := fake.Opens[0]
	if open.Head != "consumer:"+branch {
		t.Errorf("PR head = %q, want consumer:%s", open.Head, branch)
	}
	if open.Repo != req.Repo || open.Base != req.BaseBranch {
		t.Errorf("PR targets %+v@%s, want %+v@%s", open.Repo, open.Base, req.Repo, req.BaseBranch)
	}
	if result.Branch != branch {
		t.Errorf("result branch = %q, want the unqualified %q", result.Branch, branch)
	}
}

// TestFunnelForkFallbackIsIdempotent is AC-904.3 and the sharp edge of the
// whole phase: step 0's lookup cannot see a cross-fork PR (it does not yet
// know the fork's owner), so a re-run walks all the way to the push — over
// a mirror whose branch ALREADY carries this commit. It must find the fork
// PR and open no second one.
func TestFunnelForkFallbackIsIdempotent(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := forkFallbackRequest(t, fx)

	fake := host.NewFakeHost()
	fake.ForkLogin = "consumer"
	forbidPushTo(fake, fx.RemoteURL())
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	first, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	// Same request, same mirror directory — the re-run an agent performs
	// after a dropped connection.
	second, err := funnel.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	if second.State != WriteStateAlreadyOpen {
		t.Fatalf("State = %v, want %v", second.State, WriteStateAlreadyOpen)
	}
	if second.PRNumber != first.PRNumber || second.PRURL != first.PRURL {
		t.Errorf("re-run returned PR %d/%s, want the first one %d/%s",
			second.PRNumber, second.PRURL, first.PRNumber, first.PRURL)
	}
	if len(fake.Opens) != 1 {
		t.Fatalf("opens = %d, want 1 — the re-run must not open a second PR", len(fake.Opens))
	}
}

// TestFunnelWithoutForkFallbackReportsTheRefusal is AC-904.2: a space
// write (the default) never forks. A refused push is a credential fault to
// report, not to route around, and the host is never asked for a fork.
func TestFunnelWithoutForkFallbackReportsTheRefusal(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := forkFallbackRequest(t, fx)
	req.AllowForkFallback = false

	fake := host.NewFakeHost()
	forbidPushTo(fake, fx.RemoteURL())

	_, err := NewWriteFunnel(fake, nil, "0.1.0").Submit(context.Background(), req)
	if !errors.Is(err, host.ErrPushRejected) {
		t.Fatalf("err = %v, want ErrPushRejected", err)
	}
	if len(fake.Forks) != 0 {
		t.Errorf("EnsureFork called %d time(s) for a write that did not opt in", len(fake.Forks))
	}
	if len(fake.Opens) != 0 {
		t.Errorf("opens = %d, want 0", len(fake.Opens))
	}
}

// hostWithoutFork is a Host that does NOT satisfy the optional Forker
// capability — the GitLab/Gitea-profile case ADR-003 keeps possible.
type hostWithoutFork struct{ inner host.Host }

func (h hostWithoutFork) PushBranch(ctx context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
	return h.inner.PushBranch(ctx, req)
}

func (h hostWithoutFork) OpenPR(ctx context.Context, req host.OpenPRRequest) (host.PRInfo, error) {
	return h.inner.OpenPR(ctx, req)
}

func (h hostWithoutFork) CheckStatus(ctx context.Context, req host.StatusRequest) (host.CheckStatusResult, error) {
	return h.inner.CheckStatus(ctx, req)
}

func (h hostWithoutFork) ReviewStatus(ctx context.Context, req host.StatusRequest) (host.ReviewStatusResult, error) {
	return h.inner.ReviewStatus(ctx, req)
}

func (h hostWithoutFork) FindPRByHeadBranch(ctx context.Context, req host.FindPRRequest) (*host.PRInfo, error) {
	return h.inner.FindPRByHeadBranch(ctx, req)
}

// TestFunnelForkFallbackUnavailable is AC-904.4: when no fork can be used
// — the host profile has none, or creating it failed — the error names the
// manual fork+PR path, which always works.
func TestFunnelForkFallbackUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("host has no fork capability", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := forkFallbackRequest(t, fx)

		fake := host.NewFakeHost()
		forbidPushTo(fake, fx.RemoteURL())

		_, err := NewWriteFunnel(hostWithoutFork{inner: fake}, nil, "0.1.0").Submit(context.Background(), req)
		assertManualForkAdvice(t, err)
	})

	t.Run("fork cannot be created", func(t *testing.T) {
		t.Parallel()
		fx := spacefixture.New(t, "axon")
		req := forkFallbackRequest(t, fx)

		fake := host.NewFakeHost()
		forbidPushTo(fake, fx.RemoteURL())
		fake.EnsureForkFunc = func(context.Context, host.EnsureForkRequest) (host.ForkInfo, error) {
			return host.ForkInfo{}, &host.Error{Op: "EnsureFork", Err: host.ErrForkUnavailable}
		}

		_, err := NewWriteFunnel(fake, nil, "0.1.0").Submit(context.Background(), req)
		assertManualForkAdvice(t, err)
		if !errors.Is(err, host.ErrForkUnavailable) {
			t.Errorf("err = %v, want the host cause preserved", err)
		}
	})
}

func assertManualForkAdvice(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrForkFallbackUnavailable) {
		t.Fatalf("err = %v, want ErrForkFallbackUnavailable", err)
	}
	if !strings.Contains(err.Error(), "by hand") {
		t.Errorf("err = %q, want it to name the manual fork+PR path", err)
	}
}
