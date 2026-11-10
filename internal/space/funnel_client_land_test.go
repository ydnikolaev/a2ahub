package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

const (
	clientLandHeadSHA  = "2222222222222222222222222222222222222222"
	clientLandMergeSHA = "3333333333333333333333333333333333333333"
)

func clientLandCaps() *host.Capabilities {
	return &host.Capabilities{NeedsCredential: true, LandsSynchronously: true}
}

func clientLandRequest(t *testing.T) (SubmitRequest, string) {
	t.Helper()
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(spacefixture.New(t, "axon"), "axon", l)
	return req, BranchName(req.System, req.Verb, req.ArtifactID)
}

func clientLandFake(existing *host.PRInfo) *host.FakeHost {
	fake := host.NewFakeHost()
	fake.Caps = clientLandCaps()
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		if existing == nil {
			return nil, nil
		}
		found := *existing
		return &found, nil
	}
	return fake
}

func clientLandOpenPR() *host.PRInfo {
	return &host.PRInfo{Number: 7, URL: "https://example.invalid/pr/7", State: "open", HeadSHA: clientLandHeadSHA, BaseBranch: "main", Title: "t", Body: "b"}
}

func TestFunnelResumesAnOpenPROnAHostThatLandsSynchronously(t *testing.T) {
	t.Parallel()
	req, branch := clientLandRequest(t)
	fake := clientLandFake(clientLandOpenPR())
	fake.OpenPRFunc = func(_ context.Context, got host.OpenPRRequest) (host.PRInfo, error) {
		return host.PRInfo{Number: 7, URL: "https://example.invalid/pr/7", State: "merged", HeadSHA: got.ExpectedHeadSHA, MergeSHA: clientLandMergeSHA}, nil
	}

	result, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStateMerged || result.Stage != WriteStageMerged || result.PRNumber != 7 {
		t.Fatalf("result = %+v, want merged at PR 7", result)
	}
	if len(fake.Opens) != 1 || fake.Opens[0].ExpectedHeadSHA != clientLandHeadSHA || fake.Opens[0].Head != branch || fake.Opens[0].Base != "main" {
		t.Fatalf("opens = %+v, want one resume of the found head", fake.Opens)
	}
	if len(fake.AutoArms) != 0 {
		t.Errorf("auto-merge armed %d times on a host that lands inside OpenPR", len(fake.AutoArms))
	}
	if len(fake.Pushes) != 0 {
		t.Errorf("pushes = %d, a resume must not push the head again", len(fake.Pushes))
	}
}

// TestFunnelTreatsAnAlreadyLandedOpenPRAsANewWrite is `ack` then `accept` inside
// the seconds GitHub still reads the first pull request as open: reporting the
// first one's result would silently lose the second.
func TestFunnelTreatsAnAlreadyLandedOpenPRAsANewWrite(t *testing.T) {
	t.Parallel()
	req, _ := clientLandRequest(t)
	fake := clientLandFake(clientLandOpenPR())
	fake.ReadRemoteBranchFunc = func(context.Context, host.RemoteBranchRequest) (host.RemoteBranchHead, error) {
		return host.RemoteBranchHead{SHA: clientLandHeadSHA, Exists: true}, nil
	}
	fake.OpenPRFunc = func(_ context.Context, got host.OpenPRRequest) (host.PRInfo, error) {
		info := host.PRInfo{Number: 8, URL: "https://example.invalid/pr/8", State: "merged", HeadSHA: got.ExpectedHeadSHA}
		if got.ExpectedHeadSHA != clientLandHeadSHA {
			info.MergeSHA = clientLandMergeSHA
		}
		return info, nil
	}

	result, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(fake.Pushes) != 1 {
		t.Fatalf("pushes = %d, want the new write pushed", len(fake.Pushes))
	}
	if got := fake.Pushes[0].ForceWithLeaseSHA; got != clientLandHeadSHA {
		t.Errorf("ForceWithLeaseSHA = %q, want the kept head %q", got, clientLandHeadSHA)
	}
	if len(fake.Opens) != 2 || fake.Opens[1].ExpectedHeadSHA == clientLandHeadSHA {
		t.Fatalf("opens = %+v, want the found head and then the new one", fake.Opens)
	}
	if result.State != WriteStateMerged {
		t.Errorf("State = %v, want merged", result.State)
	}
}

func TestFunnelReplaysAnOperationKeyedWriteAlreadyLanded(t *testing.T) {
	t.Parallel()
	req, _ := clientLandRequest(t)
	req.ArtifactIDs = []string{req.ArtifactID}
	req.OperationKey = operation.Respond("axon", "agent", "bot", []string{req.ArtifactID}, "answered", nil, nil, nil, operation.RespondIncompleteness{}, nil)
	existing := clientLandOpenPR()
	existing.Body = appendOperationMetadata("b", req.OperationKey, req.ArtifactIDs)
	fake := clientLandFake(existing)
	fake.OpenPRFunc = func(_ context.Context, got host.OpenPRRequest) (host.PRInfo, error) {
		return host.PRInfo{Number: 7, URL: "https://example.invalid/pr/7", State: "merged", HeadSHA: got.ExpectedHeadSHA}, nil
	}

	result, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStateAlreadyMerged || len(fake.Pushes) != 0 {
		t.Fatalf("state = %v, pushes = %d; want an already-merged replay that pushes nothing", result.State, len(fake.Pushes))
	}
}

func TestFunnelResumeRefusalKeepsThePRAndNamesTheLand(t *testing.T) {
	t.Parallel()
	req, _ := clientLandRequest(t)
	fake := clientLandFake(clientLandOpenPR())
	refusal := &AheadError{SpaceVersion: "0.28.0", BinaryVersion: "0.27.1"}
	fake.OpenPRFunc = func(context.Context, host.OpenPRRequest) (host.PRInfo, error) {
		return host.PRInfo{}, refusal
	}

	result, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
	if !errors.Is(err, ErrStaleBinaryVersion) {
		t.Fatalf("err = %v, want the stale-binary family", err)
	}
	if result.State != WriteStateNeedsAttention || result.Note != "PR exists, but the write did not land" {
		t.Errorf("state = %v, note = %q", result.State, result.Note)
	}
	if result.PRNumber != 7 {
		t.Errorf("PRNumber = %d, want the existing PR", result.PRNumber)
	}
	if len(fake.Pushes) != 0 {
		t.Errorf("pushes = %d after a refusal", len(fake.Pushes))
	}
}

func TestFunnelResumeRefusesAHeadThatCannotBeProved(t *testing.T) {
	t.Parallel()
	req, _ := clientLandRequest(t)
	existing := clientLandOpenPR()
	existing.HeadSHA = ""
	fake := clientLandFake(existing)

	if _, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req); !errors.Is(err, ErrExpectedHeadUnavailable) {
		t.Fatalf("err = %v, want ErrExpectedHeadUnavailable", err)
	}
	if len(fake.Opens) != 0 {
		t.Errorf("opens = %d, a head that cannot be read must not be landed", len(fake.Opens))
	}
}

// TestFunnelRetryAfterARefusalReplacesTheRefusedHead is the retry after `a2a
// update`: the refused write left its head on the remote and no pull request, and
// a plain write's new commit is not a fast-forward of it.
func TestFunnelRetryAfterARefusalReplacesTheRefusedHead(t *testing.T) {
	t.Parallel()
	const orphan = "1111111111111111111111111111111111111111"
	tests := []struct {
		name      string
		caps      *host.Capabilities
		wantLease string
	}{
		{name: "a host that lands from the client replaces it", caps: clientLandCaps(), wantLease: orphan},
		{name: "a GitHub space is unchanged", caps: nil, wantLease: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, _ := clientLandRequest(t)
			fake := host.NewFakeHost()
			fake.Caps = tt.caps
			fake.ReadRemoteBranchFunc = func(context.Context, host.RemoteBranchRequest) (host.RemoteBranchHead, error) {
				return host.RemoteBranchHead{SHA: orphan, Exists: true}, nil
			}

			if _, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			if len(fake.Pushes) != 1 {
				t.Fatalf("pushes = %d, want 1", len(fake.Pushes))
			}
			if got := fake.Pushes[0].ForceWithLeaseSHA; got != tt.wantLease {
				t.Errorf("ForceWithLeaseSHA = %q, want %q", got, tt.wantLease)
			}
		})
	}
}

// TestFunnelNewWriteRefusedByTheGuardNamesTheUpdate is the common shape of the
// refusal: a fresh write, pushed, and refused when it tries to land.
func TestFunnelNewWriteRefusedByTheGuardNamesTheUpdate(t *testing.T) {
	t.Parallel()
	req, _ := clientLandRequest(t)
	fake := host.NewFakeHost()
	fake.Caps = clientLandCaps()
	fake.OpenPRFunc = func(context.Context, host.OpenPRRequest) (host.PRInfo, error) {
		return host.PRInfo{}, &AheadError{SpaceVersion: "0.28.0", BinaryVersion: "0.27.1"}
	}

	_, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
	if !errors.Is(err, ErrStaleBinaryVersion) {
		t.Fatalf("err = %v, want the stale-binary family", err)
	}
	const want = "the space is ahead of this binary: space has 0.28.0, this is 0.27.1; run `a2a update` and retry"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err, want)
	}
}

func TestFunnelPartialOpenNotesFollowTheHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		caps *host.Capabilities
		want string
	}{
		{name: "lands from the client", caps: clientLandCaps(), want: "PR exists, but the write did not land"},
		{name: "a GitHub space is unchanged", caps: nil, want: "PR was created, but auto-merge could not be armed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, _ := clientLandRequest(t)
			fake := host.NewFakeHost()
			fake.Caps = tt.caps
			fake.OpenPRFunc = func(context.Context, host.OpenPRRequest) (host.PRInfo, error) {
				return host.PRInfo{Number: 9, URL: "https://example.invalid/pr/9"}, errors.New("refused")
			}

			result, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
			if err == nil {
				t.Fatal("Submit: want the host's error")
			}
			if result.Note != tt.want {
				t.Errorf("Note = %q, want %q", result.Note, tt.want)
			}
		})
	}
}

// TestFunnelOpenPROnAGitHubSpaceStillArmsAutoMerge is the regression half of the
// resume: a space without `validation: local` is unchanged.
func TestFunnelOpenPROnAGitHubSpaceStillArmsAutoMerge(t *testing.T) {
	t.Parallel()
	req, _ := clientLandRequest(t)
	fake := clientLandFake(clientLandOpenPR())
	fake.Caps = nil
	fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{HeadSHA: clientLandHeadSHA}, nil
	}

	result, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStateAlreadyOpen || len(fake.AutoArms) != 1 || len(fake.Opens) != 0 {
		t.Fatalf("state = %v, arms = %d, opens = %d; want already-open, armed once, not re-opened", result.State, len(fake.AutoArms), len(fake.Opens))
	}
}
