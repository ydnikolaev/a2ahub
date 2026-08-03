package space

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

type acceptingPublicationManifestValidator struct{}

func (acceptingPublicationManifestValidator) ValidateManifest(context.Context, []byte) error {
	return nil
}

func TestContractPublicationRepositoryPinsMainAndServesPlanningAndCompletion(t *testing.T) {
	t.Parallel()
	repo := newContractHistoryRepo(t)
	manifest := "schema: space/v1\nspace: test\nmin_binary_version: \"0.19.0\"\nparticipants:\n" +
		"  - system: axon\n    org: fixture\n    section: axon\n    owners: [axon-bot]\n    status: active\n    joined: \"2026-01-01\"\n"
	if err := os.WriteFile(filepath.Join(repo, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	contractTestGit(t, repo, "add", "space.yaml")
	contractTestGit(t, repo, "commit", "-q", "-m", "seed manifest")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
	descriptor, files, digest := contractV2Publication(t, "1.0.0", `{"type":"object"}`)
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))

	remote := strings.TrimSpace(contractTestGitOutput(t, repo, "remote", "get-url", "origin"))
	adapter, err := NewContractPublicationRepository(repo, remote, acceptingPublicationManifestValidator{}, contractHistoryValidator(t))
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := adapter.RefreshContractPublicationMain(t.Context())
	if err != nil || anchor == "" {
		t.Fatalf("RefreshContractPublicationMain = %q, %v", anchor, err)
	}
	completion, found, err := adapter.ResolveContractPublicationTarget(t.Context(), contractTestID, "1.0.0")
	if err != nil || !found || completion.Snapshot.CommitSHA == "" || len(completion.PublishedBefore) != 0 {
		t.Fatalf("completion=%+v found=%v err=%v", completion, found, err)
	}
	lookup, err := adapter.LookupContractPublicationIntent(t.Context(), contractTestID, "op-v1-"+strings.Repeat("a", 64))
	if err != nil || !lookup.Exhaustive || len(lookup.Matches) != 1 {
		t.Fatalf("lookup=%+v err=%v", lookup, err)
	}

	candidateFiles := make([]contract.CandidateFile, 0, len(files))
	for name, raw := range files {
		candidateFiles = append(candidateFiles, contract.CandidateFile{Path: name, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	candidate, issues := contract.BuildCandidateIntent(contract.CandidateSnapshot{
		Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: []byte(descriptor)},
		Files:      candidateFiles,
	})
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	planning, err := adapter.ReadContractPublicationPlanningContext(t.Context(), ContractPublicationPlanningRequest{
		System: "axon", ContractID: contractTestID, Selector: "auto:patch", AuthoritativeCommit: anchor,
		Candidate: candidate, CandidateSource: contract.CandidateSource{Kind: contract.CandidateSourceMirror, Fingerprint: "sha256:" + strings.Repeat("f", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if planning.BaseCommitSHA != anchor || planning.AuthoringFloor != "0.19.0" || len(planning.Published) != 1 || planning.ContractRoot != contractTestRoot {
		t.Fatalf("planning=%+v", planning)
	}
	if got := planning.PriorDeclaredSidecars[contract.DescriptorPath]; got.Digest == "" || !validContractPublicationGitMode(got.Mode) {
		t.Fatalf("descriptor precondition = %+v, want exact digest/mode", got)
	}
}

func TestContractPublicationRepositoryRefusesAnchorChange(t *testing.T) {
	t.Parallel()
	repo := newContractHistoryRepo(t)
	contractTestGit(t, repo, "commit", "--allow-empty", "-q", "-m", "seed")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
	remote := strings.TrimSpace(contractTestGitOutput(t, repo, "remote", "get-url", "origin"))
	adapter, err := NewContractPublicationRepository(repo, remote, acceptingPublicationManifestValidator{}, contractHistoryValidator(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.RefreshContractPublicationMain(t.Context()); err != nil {
		t.Fatal(err)
	}
	contractTestGit(t, repo, "commit", "--allow-empty", "-q", "-m", "move main")
	contractTestGit(t, repo, "push", "-q", "origin", "main")
	if _, _, err := adapter.ResolveContractPublicationTarget(t.Context(), contractTestID, "1.0.0"); err == nil {
		t.Fatal("moved authoritative main was accepted")
	}
}

func TestContractPublicationHistoryIgnoresVersionlessLifecyclePublish(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	lifecycleEventPath := "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M8.yaml"
	lifecycleEvent := "schema: event/v1\nevent: 01K1A2B3C4D5E6F7G8H9J0K1M8\n" +
		"space: test\nsubject: " + contractTestID + "\ntransition: publish\n" +
		"actor: {kind: agent, name: submitter, system: axon}\nat: 2026-08-03T11:00:00Z\n"
	writeContractCandidateFile(t, repo, lifecycleEventPath, lifecycleEvent)
	contractTestGit(t, repo, "add", "--", lifecycleEventPath)
	contractTestGit(t, repo, "commit", "-q", "-m", "submit contract lifecycle event")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")

	descriptor, files, digest := contractV2Publication(t, "1.0.0", `{"type":"object"}`)
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))
	remote := strings.TrimSpace(contractTestGitOutput(t, repo, "remote", "get-url", "origin"))
	adapter, err := NewContractPublicationRepository(repo, remote, acceptingPublicationManifestValidator{}, contractHistoryValidator(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.RefreshContractPublicationMain(t.Context()); err != nil {
		t.Fatal(err)
	}
	completion, found, err := adapter.ResolveContractPublicationTarget(t.Context(), contractTestID, "1.0.0")
	if err != nil || !found || completion.Snapshot.Version != "1.0.0" {
		t.Fatalf("versioned completion after lifecycle publish = %+v, found=%t err=%v", completion, found, err)
	}
}
