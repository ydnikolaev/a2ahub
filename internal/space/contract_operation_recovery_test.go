package space

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

type contractRecoveryTestHost struct {
	git *host.GitHubHost
	pr  *host.FakeHost
}

func (h contractRecoveryTestHost) ListContractPublishHeads(ctx context.Context, repoDir, remoteURL, prefix string, credential host.Credential) (host.RemoteHeadListing, error) {
	return h.git.ListContractPublishHeads(ctx, repoDir, remoteURL, prefix, credential)
}
func (h contractRecoveryTestHost) ReadRemoteRecoveryCommit(ctx context.Context, repoDir, remoteURL string, repository host.Repo, branch string, credential host.Credential) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error) {
	return h.git.ReadRemoteRecoveryCommit(ctx, repoDir, remoteURL, repository, branch, credential)
}
func (h contractRecoveryTestHost) ReadRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commit string, paths []string) (map[string]host.RemoteTreeFile, error) {
	return h.git.ReadRemoteRecoveryTreeFiles(ctx, repoDir, commit, paths)
}
func (h contractRecoveryTestHost) ListRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commit string, prefixes []string) (map[string]host.RemoteTreeFile, error) {
	return h.git.ListRemoteRecoveryTreeFiles(ctx, repoDir, commit, prefixes)
}
func (h contractRecoveryTestHost) FindPRByHeadBranch(ctx context.Context, request host.FindPRRequest) (*host.PRInfo, error) {
	return h.pr.FindPRByHeadBranch(ctx, request)
}
func (h contractRecoveryTestHost) OpenPR(ctx context.Context, request host.OpenPRRequest) (host.PRInfo, error) {
	return h.pr.OpenPR(ctx, request)
}
func (h contractRecoveryTestHost) EnableAutoMerge(ctx context.Context, request host.EnableAutoMergeRequest) (host.MergeMethod, error) {
	return h.pr.EnableAutoMerge(ctx, request)
}

func TestContractPublicationRecoveryProvesRemoteTreeAndRepairsOnlyPR(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	source := fx.Clone("axon")
	probe := fx.Clone("beta")
	manifestPath := filepath.Join(source, "space.yaml")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(strings.Replace(string(manifestRaw), `min_binary_version: "0.0.0"`, `min_binary_version: "0.19.0"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	contractTestGit(t, source, "add", "space.yaml")
	contractTestGit(t, source, "commit", "-q", "-m", "raise floor")
	contractTestGit(t, source, "push", "-q", "origin", "main")
	base := strings.TrimSpace(contractTestGitOutput(t, source, "rev-parse", "HEAD"))
	descriptor, files, _ := contractV2Publication(t, "1.0.0", `{"type":"object"}`)
	candidateFiles := make([]contract.CandidateFile, 0, len(files))
	for name, raw := range files {
		candidateFiles = append(candidateFiles, contract.CandidateFile{Path: name, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	candidate, issues := contract.BuildCandidateIntent(contract.CandidateSnapshot{
		Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: []byte(descriptor)}, Files: candidateFiles,
	})
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	target := contractTestID + "@1.0.0"
	selector := "explicit:1.0.0"
	sourceIdentity := contract.CandidateSource{Kind: contract.CandidateSourceMirror, Location: "tree:fixture", Fingerprint: artifact.Digest(candidate.CanonicalBytes())}
	intentKey := operation.ContractPublishIntent("axon", contractTestID, selector, candidate.Digest())
	opKey := operation.ContractPublish("axon", contractTestID, "1.0.0")
	eventID := "01K1A2B3C4D5E6F7G8H9J0K1M7"
	plan, planIssues := contract.PlanPublication(contract.PublicationInput{
		System: "axon", ContractID: contractTestID, Selector: selector, AuthoringFloor: "0.19.0",
		Candidate: candidate, Published: []contract.PublishedContract{}, CandidateSource: sourceIdentity,
		ContractRoot: contractTestRoot, Warnings: []contract.Finding{}, Violations: []contract.Finding{},
	}, publicationCompatibilityChecker{})
	if len(planIssues) != 0 {
		t.Fatal(planIssues)
	}
	eventPath := filepath.Join("axon", "events", "2026", eventID+".yaml")
	event := fmt.Sprintf("schema: event/v2\nevent: %s\nsubject: %s\ntransition: publish\nversion: 1.0.0\ndigest: %s\ndigest_profile: contract-set-v2\nactor:\n  system: axon\npublication:\n  intent_key: %s\n  candidate_intent_digest: %s\n  version_selector: %s\n  operation_key: %s\n", eventID, contractTestID, plan.AggregateDigest, intentKey, candidate.Digest(), selector, opKey)
	mutations := make([]Mutation, 0, len(plan.Mutations)+1)
	planned := plan.PlannedBytes()
	for _, mutation := range plan.Mutations {
		if mutation.Action != contract.MutationWrite {
			t.Fatal("first publication unexpectedly deletes")
		}
		mutations = append(mutations, Mutation{Path: filepath.ToSlash(filepath.Join(contractTestRoot, mutation.Path)), Operation: MutationWrite, Bytes: planned[mutation.Path]})
	}
	mutations = append(mutations, Mutation{Path: filepath.ToSlash(eventPath), Operation: MutationWrite, Bytes: []byte(event)})
	funnel := NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0")
	boundPRBody, err := bindContractPublicationCandidateSource("frozen body", sourceIdentity)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := funnel.PrepareSubmission(t.Context(), SubmitRequest{
		RepoDir: source, System: "axon", Verb: "contract-publish", ArtifactID: contractTestID,
		ArtifactIDs: []string{contractTestID, "XE-" + eventID}, OperationKey: opKey, Mutations: mutations,
		CommitMessage: "publish contract", CommitAuthorName: "a2a-axon", CommitAuthorEmail: "a2a-axon@a2ahub.invalid",
		RemoteURL: fx.RemoteURL(), Repo: host.Repo{Owner: "acme", Name: "space"}, BaseBranch: "main",
		PRTitle: "frozen title", PRBody: boundPRBody, MinBinaryVersion: "0.19.0",
	}, PreparationContext{
		TargetIdentity: "acme/space", BaseCommitSHA: base, ObservedSpaceFloor: "0.19.0",
		FeatureFloor: "0.19.0", SchemaFloor: "0.19.0", ProfileFloor: "0.19.0", ProducerCompatibility: "0.19.0",
		Recovery: &RecoveryV1{CandidateIntentDigest: candidate.Digest(), IntentKey: intentKey, PlanDigest: plan.PlanDigest, Target: target, VersionSelector: selector},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := DecodeRecoveryV1(prepared.data.recoveryJSON)
	if err != nil {
		t.Fatal(err)
	}
	if persistedSource, ok := contractPublicationCandidateSourceFromPRBody(record.PRBody); !ok || persistedSource != sourceIdentity {
		t.Fatalf("persisted candidate source = %+v ok=%v body=%q", persistedSource, ok, record.PRBody)
	}
	for _, mutation := range prepared.data.mutations {
		if mutation.Operation != MutationWrite {
			t.Fatal("unexpected delete")
		}
		writeRecoveryFixtureFile(t, source, mutation.Path, string(mutation.Bytes))
	}
	contractTestGit(t, source, "add", "--", contractTestRoot, eventPath)
	contractTestGit(t, source, "commit", "-q", "--author", "a2a-axon <a2a-axon@a2ahub.invalid>", "-m", prepared.data.commitMessage)
	contractTestGit(t, source, "push", "-q", "origin", "HEAD:refs/heads/"+record.HeadBranch)

	// A valid recovery-shaped head for another contract is inside the same
	// system namespace, but must not be reconstructed with this retry's
	// candidate source or block this contract's recovery.
	unrelated := record
	unrelatedContract := "XC-axon-unrelated"
	unrelated.ArtifactIDs = append([]string(nil), record.ArtifactIDs...)
	unrelated.ArtifactIDs[0] = unrelatedContract
	unrelated.Target = unrelatedContract + "@1.0.0"
	unrelated.OperationKey = operation.ContractPublish("axon", unrelatedContract, "1.0.0")
	unrelated.IntentKey = operation.ContractPublishIntent("axon", unrelatedContract, selector, candidate.Digest())
	unrelated.HeadBranch = BranchName("axon", "contract-publish", unrelated.OperationKey)
	unrelated.PRTitle = "Publish unrelated contract"
	encoded, digest, err := EncodeRecoveryV1Trailer(unrelated)
	if err != nil {
		t.Fatal(err)
	}
	contractTestGit(t, source, "checkout", "-q", "-b", "unrelated-recovery", base)
	contractTestGit(t, source, "commit", "-q", "--allow-empty", "--author", "a2a-axon <a2a-axon@a2ahub.invalid>", "-m", appendRecoveryTrailers("unrelated recovery", unrelated, encoded, digest))
	contractTestGit(t, source, "push", "-q", "origin", "HEAD:refs/heads/"+unrelated.HeadBranch)
	contractTestGit(t, source, "checkout", "-q", "main")

	fakePR := host.NewFakeHost()
	recoveryHost := contractRecoveryTestHost{git: host.NewGitHubHost(nil, ""), pr: fakePR}
	recovery, err := NewContractPublicationRecovery(recoveryHost, probe, fx.RemoteURL(), host.Repo{Owner: "acme", Name: "space"}, host.Credential{}, ContractPublicationRecoveryValidation{
		ManifestValidator: acceptingPublicationManifestValidator{}, HistoryValidator: contractHistoryValidator(t), Compatibility: publicationCompatibilityChecker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	listing, err := recovery.ProbeContractPublicationHeads(t.Context(), ContractPublicationHeadProbeRequest{
		System: "axon", ContractID: contractTestID, NamespacePrefix: "a2a/axon/contract-publish/op-v1-", MaximumHeads: 256,
	})
	if err != nil || !listing.Exhaustive || listing.Observed != 1 || len(listing.Heads) != 1 || !listing.Heads[0].TreeVerified {
		t.Fatalf("listing=%+v err=%v", listing, err)
	}
	if !listing.Heads[0].recoveredPlan.Equal(plan) {
		t.Fatal("remote proof did not retain the exact recomputed publication plan")
	}
	fakePR.OpenPRFunc = func(_ context.Context, request host.OpenPRRequest) (host.PRInfo, error) {
		return host.PRInfo{Number: 41, URL: "https://example.invalid/wrong", State: "open", Title: "changed title", Body: request.Body, BaseBranch: request.Base, HeadSHA: request.ExpectedHeadSHA}, nil
	}
	if _, err := recovery.RepairContractPublicationHead(t.Context(), listing.Heads[0], SubmissionRuntime{
		RepoDir: probe, RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/space", CurrentSpaceFloor: "0.19.0",
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed provider title error = %v, want operation conflict", err)
	}
	fakePR.OpenPRFunc = nil
	result, err := recovery.RepairContractPublicationHead(t.Context(), listing.Heads[0], SubmissionRuntime{
		RepoDir: probe, RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/space", CurrentSpaceFloor: "0.19.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PRNumber <= 0 || result.CommitSHA != listing.Heads[0].HeadSHA || len(fakePR.Opens) != 2 {
		t.Fatalf("result=%+v opens=%d", result, len(fakePR.Opens))
	}

	fakePR.FindPRFunc = func(_ context.Context, request host.FindPRRequest) (*host.PRInfo, error) {
		return &host.PRInfo{
			Number: 52, URL: "https://example.invalid/pr/52", State: "open",
			Title: record.PRTitle, Body: record.PRBody, BaseBranch: record.BaseBranch,
			HeadSHA: listing.Heads[0].HeadSHA, AutoMergeArmed: false,
		}, nil
	}
	rearmed, err := recovery.RepairContractPublicationHead(t.Context(), listing.Heads[0], SubmissionRuntime{
		RepoDir: probe, RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/space", CurrentSpaceFloor: "0.19.0",
	})
	if err != nil {
		t.Fatalf("re-arm existing publication PR: %v", err)
	}
	if rearmed.Stage != WriteStageAutoMergeArmed || rearmed.State != WriteStatePendingMerge || len(fakePR.AutoArms) != 1 ||
		fakePR.AutoArms[0].PRNumber != 52 || fakePR.AutoArms[0].ExpectedHeadSHA != listing.Heads[0].HeadSHA {
		t.Fatalf("rearmed=%+v auto-arms=%+v", rearmed, fakePR.AutoArms)
	}
	fakePR.FindPRFunc = nil

	for _, test := range []struct {
		name   string
		mutate func(*RecoveryV1)
	}{
		{name: "missing stable candidate source", mutate: func(record *RecoveryV1) { record.PRBody = "legacy body without source binding" }},
		{name: "echoed plan digest", mutate: func(record *RecoveryV1) { record.PlanDigest = "sha256:" + strings.Repeat("9", 64) }},
		{name: "echoed prepared digest", mutate: func(record *RecoveryV1) { record.PreparedDigest = "sha256:" + strings.Repeat("8", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			tampered := record
			tampered.ArtifactIDs = append([]string(nil), record.ArtifactIDs...)
			test.mutate(&tampered)
			encoded, digest, err := EncodeRecoveryV1Trailer(tampered)
			if err != nil {
				t.Fatal(err)
			}
			message := appendRecoveryTrailers(prepared.data.commitMessageBase, tampered, encoded, digest)
			contractTestGit(t, source, "commit", "--amend", "-q", "-m", message)
			contractTestGit(t, source, "push", "-q", "--force", "origin", "HEAD:refs/heads/"+record.HeadBranch)
			_, err = recovery.ProbeContractPublicationHeads(t.Context(), ContractPublicationHeadProbeRequest{
				System: "axon", ContractID: contractTestID, NamespacePrefix: "a2a/axon/contract-publish/op-v1-", MaximumHeads: 256,
			})
			if !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("Probe tampered recovery error = %v, want operation conflict", err)
			}
		})
	}
}

func writeRecoveryFixtureFile(t *testing.T, root, relative, raw string) {
	t.Helper()
	full := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}
