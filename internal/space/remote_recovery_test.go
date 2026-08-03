package space

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

type fakeRemoteRecoveryReader struct {
	repository host.Repo
	branch     string
	headSHA    string
	parentSHA  string
	message    string
	changes    map[string]host.RemoteRecoveryChange
	exists     bool
	err        error

	wantRepository host.Repo
	wantBranch     string
	wantRepoDir    string
	wantRemoteURL  string
	wantCredential host.Credential
	calls          int
	authorName     string
	authorEmail    string
}

type recoveryCapableFakeHost struct {
	*host.FakeHost
	reader *fakeRemoteRecoveryReader
}

func (f *recoveryCapableFakeHost) ReadRemoteRecoveryCommit(
	ctx context.Context,
	repoDir string,
	remoteURL string,
	repository host.Repo,
	branch string,
	credential host.Credential,
) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error) {
	return f.reader.ReadRemoteRecoveryCommit(ctx, repoDir, remoteURL, repository, branch, credential)
}

func (f *fakeRemoteRecoveryReader) ReadRemoteRecoveryCommit(
	_ context.Context,
	repoDir string,
	remoteURL string,
	repository host.Repo,
	branch string,
	credential host.Credential,
) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error) {
	f.calls++
	f.wantRepository = repository
	f.wantBranch = branch
	f.wantRepoDir = repoDir
	f.wantRemoteURL = remoteURL
	f.wantCredential = credential
	return f.repository, f.branch, f.headSHA, f.parentSHA, f.message, f.authorName, f.authorEmail, cloneRecoveryChanges(f.changes), f.exists, f.err
}

func TestProbePreparedRemoteRecoveryRepairsOnlyThePRAfterLostAck(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	reader := matchingRemoteRecoveryReader(prepared)
	credential := host.Credential{Token: "invocation-only"}

	runtime := testRemoteRecoveryRuntime()
	runtime.Credential = credential
	repair, found, err := probePreparedRemoteRecovery(context.Background(), reader, prepared, runtime)
	if err != nil {
		t.Fatalf("probePreparedRemoteRecovery: %v", err)
	}
	if !found {
		t.Fatal("probePreparedRemoteRecovery found = false, want true")
	}
	wantHost := prepared.HostRequest()
	wantOpen := host.OpenPRRequest{
		Repo: wantHost.Repository, Head: wantHost.HeadBranch, Base: wantHost.BaseBranch,
		Title: wantHost.PRTitle, Body: wantHost.PRBody, ExpectedHeadSHA: strings.Repeat("a", 40),
		Credential: credential,
	}
	if repair.HeadSHA != strings.Repeat("a", 40) || repair.OpenPR != wantOpen {
		t.Fatalf("repair = %+v, want head/open %+v", repair, wantOpen)
	}
	if reader.calls != 1 || reader.wantRepository != wantHost.Repository || reader.wantBranch != wantHost.HeadBranch ||
		reader.wantRepoDir != runtime.RepoDir || reader.wantRemoteURL != runtime.RemoteURL || reader.wantCredential != credential {
		t.Fatalf("reader request = repo=%+v branch=%q repo_dir=%q remote=%q credential=%+v calls=%d",
			reader.wantRepository, reader.wantBranch, reader.wantRepoDir, reader.wantRemoteURL, reader.wantCredential, reader.calls)
	}
	if got := prepared.Mutations()[0].Bytes; string(got) != "{\"type\":\"object\"}\n" {
		t.Fatalf("prepared mutation changed during recovery: %q", got)
	}
}

func TestPrepareRecoverableSubmissionRequiresFrozenBaseCommit(t *testing.T) {
	t.Parallel()

	operationKey := "op-v1-" + strings.Repeat("a", 64)
	_, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), SubmitRequest{
			System: "atlas", Verb: "contract-publish", ArtifactID: "XC-atlas-demo",
			ArtifactIDs: []string{"XC-atlas-demo"}, OperationKey: operationKey,
			Mutations:     []Mutation{{Path: "atlas/provides/demo/schema/contract.json", Operation: MutationWrite, Bytes: []byte("{}\n")}},
			CommitMessage: "publish", RemoteURL: "https://github.com/acme/space.git",
			Repo: host.Repo{Owner: "acme", Name: "space"}, BaseBranch: "main", PRTitle: "publish",
		}, PreparationContext{
			TargetIdentity: "acme/space", ProducerCompatibility: "0.19.0",
			Recovery: &RecoveryV1{
				CandidateIntentDigest: "sha256:" + strings.Repeat("c", 64),
				IntentKey:             "op-v1-" + strings.Repeat("b", 64), PlanDigest: testRecoveryPlanDigest(),
				Target: "XC-atlas-demo@2.0.0", VersionSelector: "auto:major",
			},
		},
	)
	if !errors.Is(err, ErrPreparedInvalid) {
		t.Fatalf("PrepareSubmission error = %v, want ErrPreparedInvalid", err)
	}
}

func TestSubmitPreparedRepairsLostPushAcknowledgementWithoutSecondCommitOrPush(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	fake := &recoveryCapableFakeHost{
		FakeHost: host.NewFakeHost(),
		reader:   matchingRemoteRecoveryReader(prepared),
	}
	result, err := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0").SubmitPrepared(
		context.Background(),
		prepared,
		SubmissionRuntime{
			RepoDir: "/cache/acme-space", RemoteURL: "https://github.com/acme/space.git",
			TargetIdentity: "acme/space", CurrentSpaceFloor: "0.19.0", Credential: host.Credential{Token: "invocation-only"},
			PriorResult: WriteResult{
				Branch: prepared.Branch(), Stage: WriteStageCommitted,
				RemainingAction: RemainingActionRetryPush,
			},
		},
	)
	if err != nil {
		t.Fatalf("SubmitPrepared: %v", err)
	}
	if len(fake.Pushes) != 0 {
		t.Fatalf("PushBranch calls = %d, want zero during PR-only repair", len(fake.Pushes))
	}
	if len(fake.Opens) != 1 {
		t.Fatalf("OpenPR calls = %d, want one", len(fake.Opens))
	}
	if result.CommitSHA != strings.Repeat("a", 40) || result.Stage != WriteStageAutoMergeArmed {
		t.Fatalf("result = %+v, want recovered commit and armed stage", result)
	}
	if got := fake.Opens[0]; got.ExpectedHeadSHA != result.CommitSHA || got.Head != prepared.Branch() {
		t.Fatalf("OpenPR = %+v, want exact recovered head", got)
	}
}

func TestSubmitPreparedNeverReplacesAPriorProvenPR(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	fake := &recoveryCapableFakeHost{
		FakeHost: host.NewFakeHost(),
		reader:   matchingRemoteRecoveryReader(prepared),
	}
	prior := WriteResult{
		Branch: prepared.Branch(), PRNumber: 41, PRURL: "https://example.invalid/pr/41",
		Stage: WriteStagePRCreated, State: WriteStateOutcomeUnknown,
		ArtifactIDs:     prepared.ArtifactIDs(),
		RemainingAction: RemainingActionObserveProviderOutcome,
	}
	result, err := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0").SubmitPrepared(
		context.Background(), prepared, SubmissionRuntime{
			RepoDir: "/cache/acme-space", RemoteURL: "https://github.com/acme/space.git",
			TargetIdentity: "acme/space", CurrentSpaceFloor: "0.19.0", PriorResult: prior,
		},
	)
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("SubmitPrepared error = %v, want ErrOperationConflict", err)
	}
	if !reflect.DeepEqual(result, prior) {
		t.Fatalf("result = %+v, want exact prior %+v", result, prior)
	}
	if fake.reader.calls != 0 || len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("prior PR absence crossed recovery/write boundary: reads=%d pushes=%d opens=%d",
			fake.reader.calls, len(fake.Pushes), len(fake.Opens))
	}
}

func TestSubmitPreparedBindsPriorPRToExactProviderIdentity(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	hostRequest := prepared.HostRequest()
	prior := WriteResult{
		Branch: prepared.Branch(), PRNumber: 41, PRURL: "https://example.invalid/pr/41",
		CommitSHA: strings.Repeat("a", 40), Stage: WriteStagePRCreated,
		State: WriteStateOutcomeUnknown, ArtifactIDs: prepared.ArtifactIDs(),
		RemainingAction: RemainingActionObserveProviderOutcome,
	}
	tests := []struct {
		name       string
		mutatePR   func(*host.PRInfo)
		statusHead string
	}{
		{name: "different number", mutatePR: func(pr *host.PRInfo) { pr.Number++ }, statusHead: prior.CommitSHA},
		{name: "different base", mutatePR: func(pr *host.PRInfo) { pr.BaseBranch = "release" }, statusHead: prior.CommitSHA},
		{name: "different lookup head", mutatePR: func(pr *host.PRInfo) { pr.HeadSHA = strings.Repeat("b", 40) }, statusHead: prior.CommitSHA},
		{name: "head changed after lookup", mutatePR: func(*host.PRInfo) {}, statusHead: strings.Repeat("c", 40)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := host.NewFakeHost()
			observed := host.PRInfo{
				Number: prior.PRNumber, URL: prior.PRURL, State: "open", Body: hostRequest.PRBody,
				BaseBranch: hostRequest.BaseBranch, HeadSHA: prior.CommitSHA,
			}
			tt.mutatePR(&observed)
			fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
				copy := observed
				return &copy, nil
			}
			fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
				return host.CheckStatusResult{State: "queued", HeadSHA: tt.statusHead}, nil
			}
			result, err := NewWriteFunnel(fake, testNoSubmitValidation{}, "0.19.0").SubmitPrepared(
				context.Background(), prepared, SubmissionRuntime{
					RepoDir: "/cache/acme-space", RemoteURL: "https://github.com/acme/space.git",
					TargetIdentity: "acme/space", CurrentSpaceFloor: "0.19.0", PriorResult: prior,
				},
			)
			if !errors.Is(err, ErrOperationConflict) || result.PRNumber != prior.PRNumber || len(fake.AutoArms) != 0 {
				t.Fatalf("result=%+v err=%v arms=%d, want exact prior/conflict/no mutation", result, err, len(fake.AutoArms))
			}
		})
	}
}

func TestProbePreparedRemoteRecoveryReturnsCleanAbsence(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	reader := matchingRemoteRecoveryReader(prepared)
	reader.exists = false
	reader.repository, reader.branch, reader.headSHA = host.Repo{}, "", ""
	reader.message, reader.changes = "", nil

	repair, found, err := probePreparedRemoteRecovery(context.Background(), reader, prepared, testRemoteRecoveryRuntime())
	if err != nil || found || repair != (remoteRecoveryRepair{}) {
		t.Fatalf("absence = repair=%+v found=%v err=%v, want zero/false/nil", repair, found, err)
	}
}

func TestProbePreparedRemoteRecoveryRejectsMalformedDuplicateAndMissingTrailers(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	canonical := prepared.data.commitMessage
	tests := []struct {
		name    string
		message string
	}{
		{name: "malformed", message: strings.Replace(canonical, "A2A-Recovery-Version: 1", "A2A-Recovery-Version=1", 1)},
		{name: "duplicate", message: "A2A-Target: XC-atlas-demo@2.0.0\n" + canonical},
		{name: "missing", message: strings.Replace(canonical, "A2A-Plan-Digest: "+testRecoveryPlanDigest()+"\n", "", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := matchingRemoteRecoveryReader(prepared)
			reader.message = tt.message
			_, found, err := probePreparedRemoteRecovery(context.Background(), reader, prepared, testRemoteRecoveryRuntime())
			if found || !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("probe = found=%v err=%v, want false/ErrOperationConflict", found, err)
			}
		})
	}
}

func TestProbePreparedRemoteRecoveryRejectsNonExactChangedPathSet(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	tests := []struct {
		name   string
		mutate func(map[string]host.RemoteRecoveryChange)
	}{
		{name: "digest mismatch", mutate: func(changes map[string]host.RemoteRecoveryChange) {
			change := changes["atlas/provides/demo/schema/contract.json"]
			change.AfterDigest = remoteRecoveryFileDigest([]byte("different\n"))
			changes["atlas/provides/demo/schema/contract.json"] = change
		}},
		{name: "missing planned path", mutate: func(changes map[string]host.RemoteRecoveryChange) {
			delete(changes, "atlas/provides/demo/schema/contract.json")
		}},
		{name: "extra remote path", mutate: func(changes map[string]host.RemoteRecoveryChange) {
			changes["atlas/provides/demo/schema/unplanned.json"] = host.RemoteRecoveryChange{
				AfterDigest: remoteRecoveryFileDigest([]byte("hidden\n")), AfterMode: "100644",
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := matchingRemoteRecoveryReader(prepared)
			tt.mutate(reader.changes)
			_, found, err := probePreparedRemoteRecovery(context.Background(), reader, prepared, testRemoteRecoveryRuntime())
			if found || !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("tree mismatch = found=%v err=%v, want false/ErrOperationConflict", found, err)
			}
		})
	}
}

func TestVerifyRemoteRecoveryTreeRequiresAnExplicitDeleteChange(t *testing.T) {
	t.Parallel()

	writePath := "atlas/provides/demo/schema/current.json"
	deletePath := "atlas/provides/demo/schema/old.json"
	mutations := []Mutation{
		{Path: writePath, Operation: MutationWrite, Bytes: []byte("current\n")},
		{Path: deletePath, Operation: MutationDelete, ExpectedBefore: &MutationPrecondition{
			Digest: "sha256:" + strings.Repeat("e", 64), Mode: "100644",
		}},
	}
	matching := map[string]host.RemoteRecoveryChange{
		writePath: {AfterDigest: remoteRecoveryFileDigest([]byte("current\n")), AfterMode: "100644"},
		deletePath: {
			BeforeDigest: "sha256:" + strings.Repeat("e", 64), BeforeMode: "100644",
		},
	}
	if err := verifyRemoteRecoveryTree(mutations, matching); err != nil {
		t.Fatalf("matching write+delete tree: %v", err)
	}

	missingDelete := cloneRecoveryChanges(matching)
	delete(missingDelete, deletePath)
	if err := verifyRemoteRecoveryTree(mutations, missingDelete); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("missing delete error = %v, want ErrOperationConflict", err)
	}
	presentAfterDelete := cloneRecoveryChanges(matching)
	presentAfterDelete[deletePath] = host.RemoteRecoveryChange{
		BeforeDigest: "sha256:" + strings.Repeat("e", 64), BeforeMode: "100644",
		AfterDigest: remoteRecoveryFileDigest([]byte("still present\n")), AfterMode: "100644",
	}
	if err := verifyRemoteRecoveryTree(mutations, presentAfterDelete); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("present deleted path error = %v, want ErrOperationConflict", err)
	}
}

func TestProbePreparedRemoteRecoveryRejectsObservedIdentityMismatch(t *testing.T) {
	t.Parallel()

	prepared := testRemoteRecoveryPrepared(t)
	tests := []struct {
		name   string
		mutate func(*fakeRemoteRecoveryReader)
	}{
		{name: "repository", mutate: func(r *fakeRemoteRecoveryReader) { r.repository.Name = "other" }},
		{name: "branch", mutate: func(r *fakeRemoteRecoveryReader) { r.branch += "-other" }},
		{name: "base commit", mutate: func(r *fakeRemoteRecoveryReader) { r.parentSHA = strings.Repeat("e", 40) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := matchingRemoteRecoveryReader(prepared)
			tt.mutate(reader)
			_, found, err := probePreparedRemoteRecovery(context.Background(), reader, prepared, testRemoteRecoveryRuntime())
			if found || !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("identity mismatch = found=%v err=%v, want false/ErrOperationConflict", found, err)
			}
		})
	}
}

func testRemoteRecoveryPrepared(t *testing.T) PreparedSubmission {
	t.Helper()

	operationKey := "op-v1-" + strings.Repeat("a", 64)
	recovery := &RecoveryV1{
		CandidateIntentDigest: "sha256:" + strings.Repeat("c", 64),
		IntentKey:             "op-v1-" + strings.Repeat("b", 64),
		PlanDigest:            testRecoveryPlanDigest(),
		Target:                "XC-atlas-demo@2.0.0",
		VersionSelector:       "auto:major",
	}
	req := SubmitRequest{
		System: "atlas", Verb: "contract-publish", ArtifactID: "XC-atlas-demo",
		ArtifactIDs: []string{"XC-atlas-demo"}, OperationKey: operationKey,
		Mutations: []Mutation{{
			Path: "atlas/provides/demo/schema/contract.json", Operation: MutationWrite,
			Bytes: []byte("{\"type\":\"object\"}\n"),
		}},
		CommitMessage:    "a2a(contract): publish XC-atlas-demo@2.0.0",
		CommitAuthorName: "a2a-atlas", CommitAuthorEmail: "a2a-atlas@a2ahub.invalid",
		RemoteURL: "https://github.com/acme/space.git",
		Repo:      host.Repo{Owner: "acme", Name: "space"}, BaseBranch: "main",
		PRTitle: "Publish XC-atlas-demo@2.0.0", PRBody: "Publish XC-atlas-demo@2.0.0",
	}
	prepared, err := NewWriteFunnel(host.NewFakeHost(), testNoSubmitValidation{}, "0.19.0").PrepareSubmission(
		context.Background(), req, PreparationContext{
			TargetIdentity: "acme/space", ObservedSpaceFloor: "0.19.0", FeatureFloor: "0.19.0",
			SchemaFloor: "0.19.0", ProfileFloor: "0.19.0", ProducerCompatibility: "0.19.0",
			BaseCommitSHA: strings.Repeat("f", 40), Recovery: recovery,
		},
	)
	if err != nil {
		t.Fatalf("PrepareSubmission: %v", err)
	}
	return prepared
}

func matchingRemoteRecoveryReader(prepared PreparedSubmission) *fakeRemoteRecoveryReader {
	hostRequest := prepared.HostRequest()
	changes := make(map[string]host.RemoteRecoveryChange)
	for _, mutation := range prepared.Mutations() {
		if mutation.Operation == MutationWrite {
			change := host.RemoteRecoveryChange{
				AfterDigest: remoteRecoveryFileDigest(mutation.Bytes), AfterMode: "100644",
			}
			if mutation.ExpectedBefore != nil {
				change.BeforeDigest, change.BeforeMode = mutation.ExpectedBefore.Digest, mutation.ExpectedBefore.Mode
				change.AfterMode = mutation.ExpectedBefore.Mode
			}
			changes[mutation.Path] = change
		} else {
			changes[mutation.Path] = host.RemoteRecoveryChange{
				BeforeDigest: mutation.ExpectedBefore.Digest, BeforeMode: mutation.ExpectedBefore.Mode,
			}
		}
	}
	return &fakeRemoteRecoveryReader{
		repository: hostRequest.Repository, branch: hostRequest.HeadBranch,
		headSHA: strings.Repeat("a", 40), parentSHA: prepared.data.baseCommitSHA,
		message: prepared.data.commitMessage + "\n",
		changes: changes, exists: true,
	}
}

func testRecoveryPlanDigest() string { return "sha256:" + strings.Repeat("d", 64) }

func testRemoteRecoveryRuntime() remoteRecoveryRuntime {
	return remoteRecoveryRuntime{
		RepoDir: "/cache/acme-space", RemoteURL: "https://github.com/acme/space.git",
	}
}

func cloneRecoveryChanges(in map[string]host.RemoteRecoveryChange) map[string]host.RemoteRecoveryChange {
	out := make(map[string]host.RemoteRecoveryChange, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
