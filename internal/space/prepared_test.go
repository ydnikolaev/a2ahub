package space

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

type recordingPreparedValidator struct {
	candidate [][]FileWrite
	final     [][]FileWrite
	finalErr  error
}

type testNoSubmitValidation struct{}

func (testNoSubmitValidation) ValidateSubmit(context.Context, []FileWrite) error { return nil }

func (v *recordingPreparedValidator) ValidateSubmitCandidate(_ context.Context, files []FileWrite) error {
	v.candidate = append(v.candidate, cloneFiles(files))
	return nil
}

func (v *recordingPreparedValidator) ValidateSubmit(_ context.Context, files []FileWrite) error {
	v.final = append(v.final, cloneFiles(files))
	return v.finalErr
}

func TestPrepareSubmissionFreezesFinalBytesWithoutSideEffects(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	layout, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", layout)
	req.MinBinaryVersion = "0.19.0"
	req.ArtifactIDs = []string{req.ArtifactID}
	fake := host.NewFakeHost()
	var hostReads atomic.Int32
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		hostReads.Add(1)
		return nil, nil
	}
	validator := &recordingPreparedValidator{}
	funnel := NewWriteFunnel(fake, validator, "0.19.0")

	prepared, err := funnel.PrepareSubmission(context.Background(), req, testPreparationContext("0.19.0"))
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	if hostReads.Load() != 0 || len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("preparation made external calls: reads=%d pushes=%d opens=%d", hostReads.Load(), len(fake.Pushes), len(fake.Opens))
	}
	if len(validator.candidate) != 1 || len(validator.final) != 1 {
		t.Fatalf("validator calls candidate/final = %d/%d, want 1/1", len(validator.candidate), len(validator.final))
	}
	if filesContainProducerStamp(validator.candidate[0]) || !filesContainProducerStamp(validator.final[0]) {
		t.Fatalf("candidate/final stamp state = %v/%v, want false/true",
			filesContainProducerStamp(validator.candidate[0]), filesContainProducerStamp(validator.final[0]))
	}
	if filesContainProducerStamp(req.Files) {
		t.Fatal("PrepareSubmission mutated caller-owned candidate bytes")
	}
	if prepared.Digest() == "" || prepared.Branch() != BranchName(req.System, req.Verb, req.ArtifactID) {
		t.Fatalf("prepared identity = %q/%q", prepared.Digest(), prepared.Branch())
	}

	mutations := prepared.Mutations()
	mutations[0].Bytes[0] ^= 0xff
	if reflect.DeepEqual(mutations, prepared.Mutations()) {
		t.Fatal("Mutations accessor exposed mutable prepared storage")
	}
}

func TestPreparedJournalRoundTripSubmitsExactFrozenRequest(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	layout, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", layout)
	req.MinBinaryVersion = "0.19.0"
	req.ArtifactIDs = []string{req.ArtifactID}
	req.PRTitle = "frozen title"
	req.PRBody = "frozen body"
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	raw, err := EncodePreparedSubmissionJournal(prepared)
	if err != nil {
		t.Fatalf("EncodePreparedSubmissionJournal: %v", err)
	}
	decoded, err := DecodePreparedSubmissionJournal(raw)
	if err != nil {
		t.Fatalf("DecodePreparedSubmissionJournal: %v", err)
	}
	if decoded.Digest() != prepared.Digest() || !reflect.DeepEqual(decoded.Mutations(), prepared.Mutations()) || decoded.HostRequest() != prepared.HostRequest() {
		t.Fatalf("decoded prepared submission drifted\n got: %+v\nwant: %+v", decoded.HostRequest(), prepared.HostRequest())
	}

	// Simulate every mutable source changing after persistence. SubmitPrepared
	// receives only the decoded frozen value and a refreshed runtime.
	req.Files[0].Content = []byte("changed template bytes")
	req.PRTitle, req.PRBody, req.ArtifactID = "changed title", "changed body", "XQ-axon-20990101-nope"
	fake := host.NewFakeHost()
	resumeValidator := &recordingPreparedValidator{}
	restarted := NewWriteFunnel(fake, resumeValidator, "9.9.9")
	result, err := restarted.SubmitPrepared(context.Background(), decoded, SubmissionRuntime{
		RepoDir: fx.Clone("axon"), RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/getvisa",
		CurrentSpaceFloor: "0.19.0",
	})
	if err != nil {
		t.Fatalf("SubmitPrepared: %v", err)
	}
	if len(resumeValidator.final) != 0 {
		t.Fatalf("SubmitPrepared re-evaluated frozen files: %+v", resumeValidator.final)
	}
	if len(fake.Opens) != 1 {
		t.Fatalf("OpenPR calls = %d, want 1", len(fake.Opens))
	}
	open := fake.Opens[0]
	if open.Head != decoded.Branch() || open.Title != "frozen title" || open.Body != "frozen body" || open.ExpectedHeadSHA != result.CommitSHA {
		t.Fatalf("OpenPR request = %+v, want frozen request and committed SHA %q", open, result.CommitSHA)
	}
	for _, mutation := range decoded.Mutations() {
		if mutation.Operation != MutationWrite {
			continue
		}
		gotBlob, err := runGitOutput(context.Background(), fx.Clone("axon"), nil, "rev-parse", result.Branch+":"+mutation.Path)
		if err != nil {
			t.Fatalf("git rev-parse %s: %v", mutation.Path, err)
		}
		expectedPath := filepath.Join(t.TempDir(), "expected")
		if err := os.WriteFile(expectedPath, mutation.Bytes, 0o600); err != nil {
			t.Fatalf("write expected blob: %v", err)
		}
		wantBlob, err := runGitOutput(context.Background(), fx.Clone("axon"), nil, "hash-object", expectedPath)
		if err != nil {
			t.Fatalf("git hash-object %s: %v", mutation.Path, err)
		}
		if gotBlob != wantBlob {
			t.Fatalf("committed %s blob = %q, want exact prepared blob %q", mutation.Path, gotBlob, wantBlob)
		}
	}
}

func TestSubmitPreparedRefusesCorruptionTargetAndFloorBeforeHost(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	layout, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", layout)
	req.MinBinaryVersion = "0.19.0"
	req.ArtifactIDs = []string{req.ArtifactID}
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	prior := WriteResult{Branch: prepared.Branch(), CommitSHA: "prior-sha", Stage: WriteStagePushed, RemainingAction: RemainingActionEnsurePR}

	tests := []struct {
		name    string
		mutate  func(*PreparedSubmission, *SubmissionRuntime)
		wantErr error
	}{
		{"corrupt bytes", func(p *PreparedSubmission, _ *SubmissionRuntime) { p.data.mutations[0].Bytes[0] ^= 0xff }, ErrPreparedDigestMismatch},
		{"changed target", func(_ *PreparedSubmission, r *SubmissionRuntime) { r.TargetIdentity = "other/space" }, ErrPreparedTargetStale},
		{"lowered floor", func(_ *PreparedSubmission, r *SubmissionRuntime) { r.CurrentSpaceFloor = "0.18.0" }, ErrPreparedFloorStale},
		{"raised beyond compatibility", func(_ *PreparedSubmission, r *SubmissionRuntime) { r.CurrentSpaceFloor = "0.20.0" }, ErrPreparedFloorStale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			candidate := PreparedSubmission{data: clonePreparedData(prepared.data)}
			runtime := SubmissionRuntime{
				RepoDir: fx.Clone("axon"), RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/getvisa",
				CurrentSpaceFloor: "0.19.0", PriorResult: prior,
			}
			tc.mutate(&candidate, &runtime)
			fake := host.NewFakeHost()
			var hostReads atomic.Int32
			fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
				hostReads.Add(1)
				return nil, nil
			}
			result, err := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0").SubmitPrepared(context.Background(), candidate, runtime)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("SubmitPrepared error = %v, want %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(result, prior) {
				t.Fatalf("result = %+v, want exact prior %+v", result, prior)
			}
			if hostReads.Load() != 0 || len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
				t.Fatalf("refusal made external calls: reads=%d pushes=%d opens=%d", hostReads.Load(), len(fake.Pushes), len(fake.Opens))
			}
		})
	}
}

func TestPrepareSubmissionRejectsCallerStampAndInvalidFinalBytes(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	layout, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", layout)
	req.MinBinaryVersion = "0.19.0"

	t.Run("caller stamp", func(t *testing.T) {
		t.Parallel()
		candidate := req
		candidate.Files = cloneFiles(req.Files)
		candidate.Files[1].Content = appendProducerStamp(candidate.Files[1].Content, "0.19.0")
		if _, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
			context.Background(), candidate, testPreparationContext("0.19.0"),
		); !errors.Is(err, ErrPreparedProducerConflict) {
			t.Fatalf("PrepareSubmission error = %v, want ErrPreparedProducerConflict", err)
		}
	})

	t.Run("final byte validation", func(t *testing.T) {
		t.Parallel()
		finalFailure := errors.New("generated stamp is not accepted by final schema")
		validator := &recordingPreparedValidator{finalErr: finalFailure}
		fake := host.NewFakeHost()
		if _, err := NewWriteFunnel(fake, validator, "0.19.0").PrepareSubmission(
			context.Background(), req, testPreparationContext("0.19.0"),
		); !errors.Is(err, finalFailure) {
			t.Fatalf("PrepareSubmission error = %v, want final validation failure", err)
		}
		if len(validator.final) != 1 || !filesContainProducerStamp(validator.final[0]) {
			t.Fatalf("final validator did not receive producer-stamped bytes: %+v", validator.final)
		}
		if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
			t.Fatalf("failed preparation mutated host: pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
		}
	})
}

func TestPreparedJournalRejectsDeleteAuthorityAndCorruption(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "atlas", layout)
	req.MinBinaryVersion = "0.19.0"
	req.ArtifactIDs = []string{req.ArtifactID}
	funnel := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0")
	prepared, err := funnel.PrepareSubmission(context.Background(), req, PreparationContext{
		TargetIdentity: "acme/getvisa", ObservedSpaceFloor: "0.19.0",
		FeatureFloor: "0.19.0", SchemaFloor: "0.19.0", ProfileFloor: "0.19.0",
		ProducerCompatibility: "0.19.0",
	})
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	raw, err := EncodePreparedSubmissionJournal(prepared)
	if err != nil {
		t.Fatalf("EncodePreparedSubmissionJournal: %v", err)
	}
	if _, err := DecodePreparedSubmissionJournal(append(raw, ' ')); !errors.Is(err, ErrPreparedJournalInvalid) {
		t.Fatalf("noncanonical journal error = %v, want ErrPreparedJournalInvalid", err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	object["unknown"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("json.Marshal unknown: %v", err)
	}
	if _, err := DecodePreparedSubmissionJournal(unknown); !errors.Is(err, ErrPreparedJournalInvalid) {
		t.Fatalf("unknown journal field error = %v, want ErrPreparedJournalInvalid", err)
	}
	corruptDigest := strings.Replace(string(raw), prepared.Digest(), "sha256:"+strings.Repeat("0", 64), 1)
	if _, err := DecodePreparedSubmissionJournal([]byte(corruptDigest)); !errors.Is(err, ErrPreparedDigestMismatch) {
		t.Fatalf("corrupt journal digest error = %v, want ErrPreparedDigestMismatch", err)
	}

	binding := testDeleteBinding("atlas/provides/demo/schema/old.json")
	deleteMutation, err := funnel.mintContractSidecarDelete(binding)
	if err != nil {
		t.Fatalf("mintContractSidecarDelete: %v", err)
	}
	deleteReq := req
	deleteReq.Verb, deleteReq.OperationKey = "contract-publish", binding.OperationKey
	deleteReq.Mutations = []Mutation{deleteMutation}
	deletePrepared, err := funnel.PrepareSubmission(context.Background(), deleteReq, PreparationContext{
		TargetIdentity: "acme/getvisa", ObservedSpaceFloor: "0.19.0",
		ProducerCompatibility: "0.19.0",
	})
	if err != nil {
		t.Fatalf("PrepareSubmission delete: %v", err)
	}
	if _, err := EncodePreparedSubmissionJournal(deletePrepared); !errors.Is(err, ErrPreparedNotJournalable) {
		t.Fatalf("delete journal error = %v, want ErrPreparedNotJournalable", err)
	}
}

func TestPreparedJournalRevalidatesSectionAfterChecksumRewrite(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "atlas", layout)
	req.MinBinaryVersion = "0.19.0"
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	raw, err := EncodePreparedSubmissionJournal(prepared)
	if err != nil {
		t.Fatalf("EncodePreparedSubmissionJournal: %v", err)
	}
	var wire preparedJournalV1
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	wire.Payload.Mutations[0].Path = "other/exchanges/forged.md"
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(wire.Payload); err != nil {
		t.Fatalf("encode forged payload: %v", err)
	}
	digest := sha256.Sum256(bytes.TrimSuffix(payload.Bytes(), []byte("\n")))
	wire.PreparedDigest = "sha256:" + stringHex(digest[:])
	forged, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if _, err := DecodePreparedSubmissionJournal(forged); !errors.Is(err, ErrWrongSection) {
		t.Fatalf("forged journal error = %v, want ErrWrongSection", err)
	}
}

func TestSubmitPreparedDoesNotReevaluateFrozenBytesAgainstMutableValidator(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "atlas", layout)
	req.MinBinaryVersion = "0.19.0"
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	wantErr := errors.New("current validator changed after preparation")
	validator := &recordingPreparedValidator{finalErr: wantErr}
	fake := host.NewFakeHost()
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		return &host.PRInfo{Number: 19, URL: "https://example.invalid/pr/19", State: "open"}, nil
	}
	result, err := NewWriteFunnel(fake, validator, "0.19.0").SubmitPrepared(
		context.Background(), prepared, SubmissionRuntime{
			RepoDir: fx.Clone("atlas"), RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/getvisa",
			CurrentSpaceFloor: "0.19.0",
		},
	)
	if err != nil {
		t.Fatalf("SubmitPrepared re-evaluated frozen bytes: %v", err)
	}
	if result.Stage != WriteStageAutoMergeArmed || len(validator.final) != 0 || len(fake.AutoArms) != 1 {
		t.Fatalf("resume result=%+v validations=%d arms=%d, want exact replay without validation",
			result, len(validator.final), len(fake.AutoArms))
	}
}

func TestOperationalConfidencePreparationFailsClosedWithoutFinalValidator(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "atlas", layout)
	req.MinBinaryVersion = "0.19.0"
	_, err = NewWriteFunnel(host.NewFakeHost(), nil, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if !errors.Is(err, ErrSubmitValidatorRequired) {
		t.Fatalf("PrepareSubmission error = %v, want ErrSubmitValidatorRequired", err)
	}
}

func TestOneShotPreparationIsNotLimitedByPrivateJournalSize(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "atlas")
	layout, err := NewLayout("atlas")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "atlas", layout)
	req.MinBinaryVersion = "0.19.0"
	req.Files = append(req.Files, FileWrite{
		Path: "atlas/exchanges/large-evidence.bin", Content: bytes.Repeat([]byte("x"), preparedJournalMaxBytes),
	})
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("one-shot PrepareSubmission: %v", err)
	}
	if _, err := EncodePreparedSubmissionJournal(prepared); !errors.Is(err, ErrPreparedJournalTooLarge) {
		t.Fatalf("journal error = %v, want ErrPreparedJournalTooLarge", err)
	}
}

func TestSubmitPreparedPreservesPriorStageAndBindsRemote(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	layout, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", layout)
	req.MinBinaryVersion = "0.19.0"
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, testPreparationContext("0.19.0"),
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	prior := WriteResult{Branch: prepared.Branch(), CommitSHA: "known-push", Stage: WriteStagePushed, RemainingAction: RemainingActionEnsurePR}
	runtime := SubmissionRuntime{
		RepoDir: fx.Clone("axon"), RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/getvisa",
		CurrentSpaceFloor: "0.19.0", PriorResult: prior,
	}
	fake := host.NewFakeHost()
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		return nil, errors.New("lookup unavailable")
	}
	result, err := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0").SubmitPrepared(context.Background(), prepared, runtime)
	if err == nil || result.Stage != WriteStagePushed || result.CommitSHA != prior.CommitSHA {
		t.Fatalf("lookup failure result=%+v err=%v, want prior pushed evidence", result, err)
	}

	runtime.RemoteURL = filepath.Join(t.TempDir(), "different.git")
	result, err = NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").SubmitPrepared(context.Background(), prepared, runtime)
	if !errors.Is(err, ErrPreparedTargetStale) || result.Stage != WriteStagePushed {
		t.Fatalf("changed remote result=%+v err=%v, want stale target with prior evidence", result, err)
	}
}

func testPreparationContext(floor string) PreparationContext {
	return PreparationContext{
		TargetIdentity: "acme/getvisa", ObservedSpaceFloor: floor,
		FeatureFloor: floor, SchemaFloor: floor, ProfileFloor: floor,
		ProducerCompatibility: floor,
	}
}

func filesContainProducerStamp(files []FileWrite) bool {
	for _, file := range files {
		if strings.Contains(file.Path, eventPathMarker) && hasProducerStamp(file.Content) {
			return true
		}
	}
	return false
}

func typedDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + stringHex(digest[:])
}

func stringHex(raw []byte) string {
	const alphabet = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, value := range raw {
		out[i*2], out[i*2+1] = alphabet[value>>4], alphabet[value&0x0f]
	}
	return string(out)
}
