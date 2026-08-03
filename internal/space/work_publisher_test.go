package space

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

type fakePreparedWorkSubmitter struct {
	calls   int
	got     PreparedSubmission
	runtime SubmissionRuntime
	result  WriteResult
	err     error
}

func (f *fakePreparedWorkSubmitter) SubmitPrepared(_ context.Context, prepared PreparedSubmission, runtime SubmissionRuntime) (WriteResult, error) {
	f.calls++
	f.got = prepared
	f.runtime = runtime
	return f.result, f.err
}

type fakeWorkRuntimeProvider struct {
	calls   int
	runtime SubmissionRuntime
	err     error
}

func (f *fakeWorkRuntimeProvider) SubmissionRuntime(context.Context) (SubmissionRuntime, error) {
	f.calls++
	return f.runtime, f.err
}

func TestWorkPublisherResumesExactPreparedAndPriorResult(t *testing.T) {
	t.Parallel()
	prepared, journal := testWorkPreparedJournal(t)
	prior := WriteResult{
		ArtifactIDs: prepared.ArtifactIDs(), Branch: prepared.Branch(), CommitSHA: strings.Repeat("a", 40),
		Stage: WriteStageAutoMergeArmed, State: WriteStateAlreadyOpen,
		RemainingAction: RemainingActionWaitForGates, PRNumber: 42, PRURL: "https://example.invalid/pr/42",
	}
	priorRaw, err := EncodeWriteResultJournal(prior)
	if err != nil {
		t.Fatal(err)
	}
	priorJournal, err := workreport.NewWriteResultJournal(priorRaw)
	if err != nil {
		t.Fatal(err)
	}
	submitter := &fakePreparedWorkSubmitter{result: prior}
	runtime := &fakeWorkRuntimeProvider{runtime: SubmissionRuntime{
		RepoDir: "/fresh/mirror", RemoteURL: "https://example.invalid/o/r.git",
		TargetIdentity: "o/r", CurrentSpaceFloor: "0.19.0", Credential: host.Credential{Token: "fresh"},
	}}
	publisher, err := NewWorkPublisher(submitter, runtime)
	if err != nil {
		t.Fatal(err)
	}

	attempt, err := publisher.SubmitPrepared(context.Background(), journal, priorJournal)
	if err != nil {
		t.Fatalf("SubmitPrepared: %v", err)
	}
	if submitter.calls != 1 || runtime.calls != 1 || submitter.got.Digest() != prepared.Digest() {
		t.Fatalf("calls/digest = %d/%d/%q, want 1/1/%q", submitter.calls, runtime.calls, submitter.got.Digest(), prepared.Digest())
	}
	if !reflect.DeepEqual(submitter.runtime.PriorResult, prior) || submitter.runtime.RepoDir != "/fresh/mirror" || submitter.runtime.Credential.Token != "fresh" {
		t.Fatalf("runtime = %+v; exact prior or refreshed facts were lost", submitter.runtime)
	}
	if attempt.Convergence != workreport.ConvergenceAccepted || attempt.ErrorCode != "" {
		t.Fatalf("attempt = %+v, want accepted without error", attempt)
	}
	gotRaw := attempt.WriteResult.Bytes()
	if !bytes.Equal(gotRaw, priorRaw) {
		t.Fatalf("result journal drifted\n got: %s\nwant: %s", gotRaw, priorRaw)
	}
}

func TestWorkPublisherConvergenceMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		stage WriteStage
		state WriteState
		err   error
		want  workreport.Convergence
	}{
		{"merged", WriteStageMerged, WriteStateMerged, nil, workreport.ConvergenceTerminal},
		{"already merged", WriteStageMerged, WriteStateAlreadyMerged, nil, workreport.ConvergenceTerminal},
		{"pending merge", WriteStageAutoMergeArmed, WriteStatePendingMerge, nil, workreport.ConvergenceAccepted},
		{"armed already open", WriteStageAutoMergeArmed, WriteStateAlreadyOpen, nil, workreport.ConvergenceAccepted},
		{"unarmed already open", WriteStagePRCreated, WriteStateAlreadyOpen, nil, workreport.ConvergenceResumable},
		{"needs attention", WriteStagePRCreated, WriteStateNeedsAttention, nil, workreport.ConvergenceResumable},
		{"unknown without transport error", WriteStagePushed, WriteStateOutcomeUnknown, nil, workreport.ConvergenceResumable},
		{"commit awaiting push", WriteStageCommitted, "", nil, workreport.ConvergenceResumable},
		{"provider ambiguity", WriteStagePushed, WriteStateOutcomeUnknown, errors.New("timeout"), workreport.ConvergenceResumable},
		{"error overrides accepted", WriteStageAutoMergeArmed, WriteStatePendingMerge, errors.New("late failure"), workreport.ConvergenceResumable},
		{"error overrides terminal", WriteStageMerged, WriteStateMerged, errors.New("late failure"), workreport.ConvergenceResumable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := convergenceForWorkWrite(WriteResult{Stage: tc.stage, State: tc.state}, tc.err); got != tc.want {
				t.Fatalf("convergence = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkPublisherPreservesPartialResultAndErrorIdentity(t *testing.T) {
	t.Parallel()
	_, prepared := testWorkPreparedJournal(t)
	providerFailure := errors.New("provider timed out after opening PR")
	partial := WriteResult{
		ArtifactIDs: []string{"XA-partial"}, Branch: "a2a/atlas/publish/op-v1-partial",
		CommitSHA: strings.Repeat("b", 40), PRNumber: 73, PRURL: "https://example.invalid/pr/73",
		Stage: WriteStagePRCreated, State: WriteStateNeedsAttention,
		RemainingAction: RemainingActionResolvePR, Note: "auto-merge was not armed",
	}
	partial.AutoMergeNote = partial.Note
	submitter := &fakePreparedWorkSubmitter{result: partial, err: providerFailure}
	publisher, err := NewWorkPublisher(submitter, &fakeWorkRuntimeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := publisher.SubmitPrepared(context.Background(), prepared, workreport.WriteResultJournal{})
	if !errors.Is(err, providerFailure) {
		t.Fatalf("error = %v, want provider identity", err)
	}
	if attempt.Convergence != workreport.ConvergenceResumable || attempt.ErrorCode != workPublishErrorShared {
		t.Fatalf("attempt = %+v", attempt)
	}
	got, decodeErr := DecodeWriteResultJournal(attempt.WriteResult.Bytes())
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !reflect.DeepEqual(got, partial) || got.PRNumber != 73 || got.PRURL == "" {
		t.Fatalf("partial result drifted: %+v", got)
	}
}

func TestWorkPublisherTurnsNilErrorResumableOutcomeIntoRefusal(t *testing.T) {
	t.Parallel()
	_, prepared := testWorkPreparedJournal(t)
	incomplete := WriteResult{
		ArtifactIDs: []string{"XA-incomplete"}, Branch: "a2a/atlas/publish/op-v1-incomplete",
		CommitSHA: strings.Repeat("c", 40), PRNumber: 74, PRURL: "https://example.invalid/pr/74",
		Stage: WriteStagePRCreated, State: WriteStateNeedsAttention,
		RemainingAction: RemainingActionResolvePR, Note: "operator action required",
	}
	publisher, err := NewWorkPublisher(&fakePreparedWorkSubmitter{result: incomplete}, &fakeWorkRuntimeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := publisher.SubmitPrepared(context.Background(), prepared, workreport.WriteResultJournal{})
	if !errors.Is(err, ErrWorkPublishIncomplete) {
		t.Fatalf("error = %v, want ErrWorkPublishIncomplete", err)
	}
	if attempt.ErrorCode != workPublishErrorPending || attempt.Convergence != workreport.ConvergenceResumable {
		t.Fatalf("attempt = %+v", attempt)
	}
	got, decodeErr := DecodeWriteResultJournal(attempt.WriteResult.Bytes())
	if decodeErr != nil || got.PRURL != incomplete.PRURL {
		t.Fatalf("result/error = %+v/%v", got, decodeErr)
	}
}

func TestWorkPublisherRejectsInvalidFunnelOutcomeAndKeepsPrior(t *testing.T) {
	t.Parallel()
	preparedValue, prepared := testWorkPreparedJournal(t)
	invalid := WriteResult{
		ArtifactIDs: preparedValue.ArtifactIDs(), Branch: preparedValue.Branch(),
		Stage: WriteStageMerged, State: WriteStatePendingMerge,
		RemainingAction: RemainingActionWaitForGates,
	}
	publisher, err := NewWorkPublisher(&fakePreparedWorkSubmitter{result: invalid}, &fakeWorkRuntimeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := publisher.SubmitPrepared(context.Background(), prepared, workreport.WriteResultJournal{})
	if !errors.Is(err, ErrWriteResultJournalInvalid) {
		t.Fatalf("error = %v, want ErrWriteResultJournalInvalid", err)
	}
	if attempt.ErrorCode != workPublishErrorResult || attempt.Convergence != workreport.ConvergenceResumable {
		t.Fatalf("attempt = %+v", attempt)
	}
	got, decodeErr := DecodeWriteResultJournal(attempt.WriteResult.Bytes())
	if decodeErr != nil || got.Stage != WriteStageNone || got.Branch != preparedValue.Branch() {
		t.Fatalf("fallback result/error = %+v/%v", got, decodeErr)
	}
}

func TestWorkPublisherFailuresRemainStructuredAndDoNotSubmit(t *testing.T) {
	t.Parallel()
	_, validPrepared := testWorkPreparedJournal(t)
	preparedBytes := validPrepared.Bytes()
	preparedBytes = bytes.Replace(
		preparedBytes,
		[]byte(`"prepared_digest":"sha256:`),
		[]byte(`"prepared_digest":"sha256:0`),
		1,
	)
	corruptPrepared, err := workreport.NewPreparedJournal(preparedBytes)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		prepared   workreport.PreparedJournal
		runtimeErr error
		wantCode   string
	}{
		{"corrupt prepared journal", corruptPrepared, nil, workPublishErrorPrepared},
		{"runtime unavailable", validPrepared, errors.New("credential unavailable"), workPublishErrorRuntime},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			submitter := &fakePreparedWorkSubmitter{}
			provider := &fakeWorkRuntimeProvider{err: tc.runtimeErr}
			publisher, err := NewWorkPublisher(submitter, provider)
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := publisher.SubmitPrepared(context.Background(), tc.prepared, workreport.WriteResultJournal{})
			if err == nil {
				t.Fatal("SubmitPrepared succeeded, want structured refusal")
			}
			if !attempt.Attempted || attempt.Convergence != workreport.ConvergenceResumable || attempt.ErrorCode != tc.wantCode || attempt.WriteResult.Len() == 0 {
				t.Fatalf("attempt = %+v", attempt)
			}
			if submitter.calls != 0 {
				t.Fatalf("submitter calls = %d, want zero", submitter.calls)
			}
		})
	}
}

func TestNewWorkPublisherRequiresDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewWorkPublisher(nil, &fakeWorkRuntimeProvider{}); err == nil {
		t.Fatal("nil submitter accepted")
	}
	if _, err := NewWorkPublisher(&fakePreparedWorkSubmitter{}, nil); err == nil {
		t.Fatal("nil runtime provider accepted")
	}
}

func testWorkPreparedJournal(t *testing.T) (PreparedSubmission, workreport.PreparedJournal) {
	t.Helper()
	fx := spacefixture.New(t, "axon")
	layout, err := NewLayout("axon")
	if err != nil {
		t.Fatal(err)
	}
	req := newTestSubmitRequest(fx, "axon", layout)
	req.MinBinaryVersion = "0.19.0"
	req.ArtifactIDs = []string{req.ArtifactID}
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodePreparedSubmissionJournal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := workreport.NewPreparedJournal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return prepared, journal
}
