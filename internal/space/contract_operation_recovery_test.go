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

// TestPublicationRecomputeConflictReason unit-tests the pure classifier
// wired into verifyRecomputedPublication's conflict path in isolation from
// the git/host fixtures a full probe/repair test needs. Argument order is
// (mintedByVersion, runningVersion) — deliberately named in the assertions
// below, because the two are easy to swap silently and a swap reports the
// version boundary backwards without any test here catching it structurally;
// only the argument names (and the deliberately asymmetric versions in the
// boundary case) make a swap visible as a wrong resulting string.
func TestPublicationRecomputeConflictReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		mintedByVersion string
		runningVersion  string
		want            string
	}{
		{"same version: unchanged legacy reason", "0.19.0", "0.19.0", "publication-plan-recompute"},
		{"absent minted version: predates version recording", "", "0.19.0", "publication-plan-recompute-unversioned-head"},
		{"different versions: names both, minted first", "0.18.0", "0.19.0", "publication-plan-recompute-version-boundary minted-by=0.18.0 running=0.19.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := publicationRecomputeConflictReason(tc.mintedByVersion, tc.runningVersion); got != tc.want {
				t.Fatalf("publicationRecomputeConflictReason(%q, %q) = %q, want %q", tc.mintedByVersion, tc.runningVersion, got, tc.want)
			}
		})
	}
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
	recovery, err := NewContractPublicationRecovery(recoveryHost, probe, fx.RemoteURL(), host.Repo{Owner: "acme", Name: "space"}, host.Credential{}, "0.19.0", ContractPublicationRecoveryValidation{
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

// contractPublicationHeadFixture pushes one remote publication head for
// contractTestID@1.0.0 whose tree, descriptor, carried set and event are all
// genuinely valid and round-trip through verifyPublicationTree, but whose own
// PlanDigest trailer is deliberately wrong — so a real recompute (the SAME
// verifyRecomputedPublication step production hits) disagrees with what the
// branch's own trailer recorded and refuses with exactly the ground truth's
// failing reason, "publication-plan-recompute".
//
// This is a DELIBERATE, MANUFACTURED disagreement, not an organic
// reproduction of whatever the live e2e run actually hit: an equivalent
// fixture built here WITHOUT the tamper (a clean, unmodified, merged-but-
// unpruned branch, otherwise identical) was empirically observed to recompute
// CLEANLY — Observed:1, no error — contradicting a literal read of "reproduced
// in the repo already" for that exact minimal shape. The real trigger inside
// the live e2e's cmd/a2a-wired flow (squash-merge commit rewrite, a second
// order-dependent effect of validateContractPublicationHeadListing's own
// inProgress branch, or something else) is NOT established here; only that
// SOME disagreement between a still-present branch's trailer and a fresh
// recompute is reachable, and that a resolved-in-main head must not abort the
// probe over it once it is. See this test file's package doc / the
// implementation worker's own report for the full empirical trail.
//
// When mergeToMain is true, the SAME commit is also fast-forwarded onto
// origin's main branch — modelling a merged-but-unpruned branch whose target
// version (1.0.0) is genuinely resolvable in main's own published history.
// When false, main never advances past the pre-publication floor-raise
// commit, modelling a still-open (or otherwise never-landed) branch whose
// target is NOT resolvable in main — the case that must still refuse.
//
// mintedByVersion stamps the manufactured RecoveryV1's own MintedByVersion.
// Pass the SAME value as the funnel's own binaryVersion below ("0.19.0") to
// keep manufacturing the genuine same-binary disagreement the doc comment
// above describes; pass a different (or empty) value to manufacture a
// cross-version or version-absent disagreement instead.
func contractPublicationHeadFixture(t *testing.T, fx *spacefixture.Fixture, source string, mergeToMain bool, mintedByVersion string) (RecoveryV1, string) {
	t.Helper()
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
	// actor carries the full {kind, name, system} shape event/v2's own schema
	// requires: this event is intentionally readable as REAL published
	// history (mergeToMain schema-validates it via contractHistoryValidator),
	// unlike a minimal actor shape that only ever satisfied a check the
	// original recovery test never actually reached.
	event := fmt.Sprintf("schema: event/v2\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: publish\nactor: {kind: agent, name: publisher, system: axon}\nat: 2026-08-03T12:00:00Z\nversion: 1.0.0\ndigest: %s\ndigest_profile: contract-set-v2\npublication:\n  intent_key: %s\n  candidate_intent_digest: %s\n  version_selector: %s\n  operation_key: %s\n", eventID, contractTestID, plan.AggregateDigest, intentKey, candidate.Digest(), selector, opKey)
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
		// PlanDigest is deliberately NOT plan.PlanDigest: see the doc comment
		// above. Every other field is the real, correctly-recomputable value.
		Recovery: &RecoveryV1{CandidateIntentDigest: candidate.Digest(), IntentKey: intentKey, MintedByVersion: mintedByVersion, PlanDigest: "sha256:" + strings.Repeat("7", 64), Target: target, VersionSelector: selector},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := DecodeRecoveryV1(prepared.data.recoveryJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range prepared.data.mutations {
		if mutation.Operation != MutationWrite {
			t.Fatal("unexpected delete")
		}
		writeRecoveryFixtureFile(t, source, mutation.Path, string(mutation.Bytes))
	}
	contractTestGit(t, source, "add", "--", contractTestRoot, eventPath)
	contractTestGit(t, source, "commit", "-q", "--author", "a2a-axon <a2a-axon@a2ahub.invalid>", "-m", prepared.data.commitMessage)
	headSHA := strings.TrimSpace(contractTestGitOutput(t, source, "rev-parse", "HEAD"))
	contractTestGit(t, source, "push", "-q", "origin", "HEAD:refs/heads/"+record.HeadBranch)
	if mergeToMain {
		// Fast-forward merge, same commit, branch left in place — exactly
		// the shape a solo-commit PR merge with "delete branch on merge"
		// turned OFF produces.
		contractTestGit(t, source, "push", "-q", "origin", "HEAD:refs/heads/main")
	}
	return record, headSHA
}

func resolvedInMainFrom(t *testing.T, fx *spacefixture.Fixture) func(context.Context, string, string) (bool, error) {
	t.Helper()
	mainReader, err := NewContractPublicationRepository(t.TempDir(), fx.RemoteURL(), host.Credential{}, acceptingPublicationManifestValidator{}, contractHistoryValidator(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mainReader.RefreshContractPublicationMain(t.Context()); err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, contractID, target string) (bool, error) {
		_, found, err := mainReader.ResolveContractPublicationTarget(ctx, contractID, target)
		return found, err
	}
}

// TestContractPublicationRecoveryProbeSkipsHeadResolvedInMain drives the
// exact WALL FOUND by internal/e2e/contract_compat_offline_test.go's own doc
// comment: a prior publish's own now-merged, unpruned branch must not abort
// the whole probe. Reproduced offline, without the fake-prune workaround that
// file's own e2e test needs.
func TestContractPublicationRecoveryProbeSkipsHeadResolvedInMain(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	source := fx.Clone("axon")
	probe := fx.Clone("beta")
	record, headSHA := contractPublicationHeadFixture(t, fx, source, true, "0.19.0")

	fakePR := host.NewFakeHost()
	recoveryHost := contractRecoveryTestHost{git: host.NewGitHubHost(nil, ""), pr: fakePR}
	recovery, err := NewContractPublicationRecovery(recoveryHost, probe, fx.RemoteURL(), host.Repo{Owner: "acme", Name: "space"}, host.Credential{}, "0.19.0", ContractPublicationRecoveryValidation{
		ManifestValidator: acceptingPublicationManifestValidator{}, HistoryValidator: contractHistoryValidator(t), Compatibility: publicationCompatibilityChecker{},
	})
	if err != nil {
		t.Fatal(err)
	}

	// WITHOUT the resolved-in-main lookup (ResolvedInMain nil, the
	// pre-existing behaviour every caller that does not wire it still gets),
	// the probe aborts the WHOLE listing over this one already-merged,
	// unpruned branch — the ground truth's exact failing string. The fixture
	// mints with the SAME version the recovery adapter below runs as
	// ("0.19.0"), so this is the genuine same-binary disagreement: the
	// reason must be the EXACT unsuffixed legacy string, not the
	// -unversioned-head or -version-boundary variant a HasSuffix (rather
	// than Contains) check would catch a swap into.
	_, err = recovery.ProbeContractPublicationHeads(t.Context(), ContractPublicationHeadProbeRequest{
		System: "axon", ContractID: contractTestID, NamespacePrefix: "a2a/axon/contract-publish/op-v1-", MaximumHeads: 256,
	})
	if !errors.Is(err, ErrOperationConflict) || !strings.HasSuffix(err.Error(), "publication-plan-recompute") {
		t.Fatalf("probe without ResolvedInMain = %v, want an operation-conflict publication-plan-recompute refusal", err)
	}

	// WITH it wired to main's own ResolveContractPublicationTarget — the
	// SAME lookup Publish's explicit-target fast path already trusts one
	// call earlier — the resolved head is skipped, not fed through recompute
	// at all, and the probe succeeds with an empty, exhaustive listing.
	resolvedInMain := resolvedInMainFrom(t, fx)
	listing, err := recovery.ProbeContractPublicationHeads(t.Context(), ContractPublicationHeadProbeRequest{
		System: "axon", ContractID: contractTestID, NamespacePrefix: "a2a/axon/contract-publish/op-v1-", MaximumHeads: 256,
		ResolvedInMain: resolvedInMain,
	})
	if err != nil || !listing.Exhaustive || listing.Observed != 0 || len(listing.Heads) != 0 {
		t.Fatalf("listing=%+v err=%v, want the resolved head skipped with an empty, exhaustive listing", listing, err)
	}

	// A skipped head was never verified, so it must remain unrepairable: no
	// proof was ever minted for it. This is the exact-intent repair path's
	// own gate (RepairContractPublicationHead's "publication-proof-not-
	// minted" check) — asserted here to prove the skip does not also make a
	// forged proof for this head pass repair.
	raw, err := EncodeRecoveryV1(record)
	if err != nil {
		t.Fatal(err)
	}
	skipped := ContractPublicationHeadProof{Branch: record.HeadBranch, HeadSHA: headSHA, RecoveryJSON: raw, TreeVerified: true}
	if _, err := recovery.RepairContractPublicationHead(t.Context(), skipped, SubmissionRuntime{
		RepoDir: probe, RemoteURL: fx.RemoteURL(), TargetIdentity: "acme/space", CurrentSpaceFloor: "0.19.0",
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("repair of a skipped (never-verified) head = %v, want operation conflict", err)
	}
}

// TestContractPublicationRecoveryProbeStillFailsUnresolvedConflictingHead
// proves the ResolvedInMain mechanism is narrow: a head whose target does NOT
// resolve in main — including a genuinely conflicting or tampered one — must
// still abort the probe exactly as before, even when the mechanism is wired.
func TestContractPublicationRecoveryProbeStillFailsUnresolvedConflictingHead(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	source := fx.Clone("axon")
	probe := fx.Clone("beta")
	contractPublicationHeadFixture(t, fx, source, false, "0.19.0")

	fakePR := host.NewFakeHost()
	recoveryHost := contractRecoveryTestHost{git: host.NewGitHubHost(nil, ""), pr: fakePR}
	recovery, err := NewContractPublicationRecovery(recoveryHost, probe, fx.RemoteURL(), host.Repo{Owner: "acme", Name: "space"}, host.Credential{}, "0.19.0", ContractPublicationRecoveryValidation{
		ManifestValidator: acceptingPublicationManifestValidator{}, HistoryValidator: contractHistoryValidator(t), Compatibility: publicationCompatibilityChecker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedInMain := resolvedInMainFrom(t, fx)
	// Same-version fixture, as above: the reason must be the exact
	// unsuffixed legacy string.
	_, err = recovery.ProbeContractPublicationHeads(t.Context(), ContractPublicationHeadProbeRequest{
		System: "axon", ContractID: contractTestID, NamespacePrefix: "a2a/axon/contract-publish/op-v1-", MaximumHeads: 256,
		ResolvedInMain: resolvedInMain,
	})
	if !errors.Is(err, ErrOperationConflict) || !strings.HasSuffix(err.Error(), "publication-plan-recompute") {
		t.Fatalf("probe of an unresolved conflicting head with ResolvedInMain wired = %v, want an operation-conflict publication-plan-recompute refusal", err)
	}
}

// TestContractPublicationRecoveryProbeUnversionedHeadNamesCompatibilityNotTampering
// proves the SECOND verifier branch: a recovered head whose own RecoveryV1
// record carries no MintedByVersion at all (every head written before this
// work) must refuse with a reason distinct from, not folded into, the
// same-binary tampering reason — this is the exact compatibility guarantee
// TRAP 1 exists to protect, proven here at the verifier, not just at decode.
func TestContractPublicationRecoveryProbeUnversionedHeadNamesCompatibilityNotTampering(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	source := fx.Clone("axon")
	probe := fx.Clone("beta")
	contractPublicationHeadFixture(t, fx, source, false, "")

	fakePR := host.NewFakeHost()
	recoveryHost := contractRecoveryTestHost{git: host.NewGitHubHost(nil, ""), pr: fakePR}
	recovery, err := NewContractPublicationRecovery(recoveryHost, probe, fx.RemoteURL(), host.Repo{Owner: "acme", Name: "space"}, host.Credential{}, "0.19.0", ContractPublicationRecoveryValidation{
		ManifestValidator: acceptingPublicationManifestValidator{}, HistoryValidator: contractHistoryValidator(t), Compatibility: publicationCompatibilityChecker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedInMain := resolvedInMainFrom(t, fx)
	_, err = recovery.ProbeContractPublicationHeads(t.Context(), ContractPublicationHeadProbeRequest{
		System: "axon", ContractID: contractTestID, NamespacePrefix: "a2a/axon/contract-publish/op-v1-", MaximumHeads: 256,
		ResolvedInMain: resolvedInMain,
	})
	if !errors.Is(err, ErrOperationConflict) || !strings.HasSuffix(err.Error(), "publication-plan-recompute-unversioned-head") {
		t.Fatalf("probe of an unversioned head = %v, want an operation-conflict publication-plan-recompute-unversioned-head refusal", err)
	}
}

// TestContractPublicationRecoveryProbeVersionBoundaryHeadNamesBothVersions
// proves the THIRD verifier branch: a recovered head minted by a DIFFERENT
// binary version than the one running the recompute must refuse with a
// reason naming BOTH versions — an unprovable head is still refused, but its
// reason must not read as tampering.
func TestContractPublicationRecoveryProbeVersionBoundaryHeadNamesBothVersions(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	source := fx.Clone("axon")
	probe := fx.Clone("beta")
	contractPublicationHeadFixture(t, fx, source, false, "0.27.4")

	fakePR := host.NewFakeHost()
	recoveryHost := contractRecoveryTestHost{git: host.NewGitHubHost(nil, ""), pr: fakePR}
	recovery, err := NewContractPublicationRecovery(recoveryHost, probe, fx.RemoteURL(), host.Repo{Owner: "acme", Name: "space"}, host.Credential{}, "0.19.0", ContractPublicationRecoveryValidation{
		ManifestValidator: acceptingPublicationManifestValidator{}, HistoryValidator: contractHistoryValidator(t), Compatibility: publicationCompatibilityChecker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedInMain := resolvedInMainFrom(t, fx)
	_, err = recovery.ProbeContractPublicationHeads(t.Context(), ContractPublicationHeadProbeRequest{
		System: "axon", ContractID: contractTestID, NamespacePrefix: "a2a/axon/contract-publish/op-v1-", MaximumHeads: 256,
		ResolvedInMain: resolvedInMain,
	})
	wantSuffix := "publication-plan-recompute-version-boundary minted-by=0.27.4 running=0.19.0"
	if !errors.Is(err, ErrOperationConflict) || !strings.HasSuffix(err.Error(), wantSuffix) {
		t.Fatalf("probe across a version boundary = %v, want a refusal ending %q", err, wantSuffix)
	}
	if strings.Contains(err.Error(), "unversioned-head") {
		t.Fatalf("probe across a version boundary = %v, must not read as the unversioned-head case", err)
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
