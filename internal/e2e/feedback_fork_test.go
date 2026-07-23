package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/feedback"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestFeedbackSubmitFromANonCollaborator is P28's product-level proof
// (US-904): the verb the skill tells every consumer agent to use works for
// an agent WITHOUT write access to the product repo. Same direct-
// construction idiom as TestFeedbackSubmitWrite (the t3 exec harness
// cannot reach a host), with the fake refusing the direct push exactly the
// way GitHub refuses a non-collaborator.
func TestFeedbackSubmitFromANonCollaborator(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")

	projectRoot := t.TempDir()
	draft := writeFeedbackDraft(t, t.TempDir())
	ledgerPath := filepath.Join(projectRoot, ".a2a", "feedback", "ledger.yaml")
	slug := "a2ahub-feedback-fixture"

	fakeHost := host.NewFakeHost()
	fakeHost.ForkLogin = "seomatrix"
	fakeHost.PushBranchFunc = func(_ context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
		if req.RemoteURL == fx.RemoteURL() {
			return host.PushBranchResult{}, &host.Error{
				Op: "PushBranch", Input: req.Branch,
				Err: fmt.Errorf("%w: %w: remote: Permission to ydnikolaev/a2ahub.git denied to seomatrix",
					host.ErrPushRejected, host.ErrPushForbidden),
			}
		}
		return host.PushBranchResult{Branch: req.Branch}, nil
	}

	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	submitter := feedback.NewSubmitter(funnel, ledgerPath, projectRoot, slug, feedback.SubmitConfig{
		RemoteURL:         fx.RemoteURL(),
		Repo:              host.Repo{Owner: "ydnikolaev", Name: "a2ahub"},
		BaseBranch:        "main",
		CommitAuthorName:  "a2a-feedback",
		CommitAuthorEmail: "a2a-feedback@a2a.local",
	})
	cmd := cli.NewFeedbackCommand(nil, submitter, ledgerPath, "", nil)

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"submit", draft}, io); code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "opened PR") {
		t.Fatalf("expected an 'opened PR' message; got %q", out.String())
	}

	branch := "a2a/feedback/submit/" + feedbackSubmitTestID
	if len(fakeHost.Forks) != 1 {
		t.Fatalf("EnsureFork calls = %d, want 1", len(fakeHost.Forks))
	}
	if len(fakeHost.Pushes) != 2 || len(fakeHost.Opens) != 1 {
		t.Fatalf("pushes/opens = %d/%d, want 2/1 (refused, then the fork; one PR)",
			len(fakeHost.Pushes), len(fakeHost.Opens))
	}
	if head := fakeHost.Opens[0].Head; head != "seomatrix:"+branch {
		t.Fatalf("PR head = %q, want seomatrix:%s", head, branch)
	}
	if repo := fakeHost.Opens[0].Repo; repo != (host.Repo{Owner: "ydnikolaev", Name: "a2ahub"}) {
		t.Fatalf("PR opened against %+v, want the product repo", repo)
	}

	// The report still lands in the ledger under its own id — the fork is
	// a transport detail, not part of the report's identity.
	item, err := feedback.FindLedgerItem(ledgerPath, feedbackSubmitTestID)
	if err != nil || item == nil {
		t.Fatalf("FindLedgerItem = %+v, %v; want the row", item, err)
	}

	// AC-904.3 at product level: the re-run an agent performs after a
	// dropped connection finds the fork PR and opens no second one.
	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"submit", draft}, io2); code != 0 {
		t.Fatalf("retry: code = %d, want 0; stdout=%s", code, out2.String())
	}
	if !strings.Contains(out2.String(), "already submitted") {
		t.Fatalf("expected the retry to report the already-submitted path, got %q", out2.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("opens = %d after the retry, want STILL 1", len(fakeHost.Opens))
	}
}
