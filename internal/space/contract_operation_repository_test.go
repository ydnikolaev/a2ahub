package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"

	"github.com/ydnikolaev/a2ahub/internal/host"
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
	adapter, err := NewContractPublicationRepository(repo, remote, host.Credential{}, acceptingPublicationManifestValidator{}, contractHistoryValidator(t))
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
	adapter, err := NewContractPublicationRepository(repo, remote, host.Credential{}, acceptingPublicationManifestValidator{}, contractHistoryValidator(t))
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
	adapter, err := NewContractPublicationRepository(repo, remote, host.Credential{}, acceptingPublicationManifestValidator{}, contractHistoryValidator(t))
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

// TestPublishedContractVersionsAtListsTheEventsHistoryAdded pins the one
// version lister both the publication planner (through publicationHistoryAt)
// and the local land check read: the versions of the publish events the
// first-parent history added up to the anchor, oldest first, each with the
// commit that added it. An event the tree later dropped still counts, and a
// subject spelled with a YAML escape is the same contract.
func TestPublishedContractVersionsAtListsTheEventsHistoryAdded(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	lifecycleEventPath := "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M6.yaml"
	writeContractCandidateFile(t, repo, lifecycleEventPath, "schema: event/v1\nevent: 01K1A2B3C4D5E6F7G8H9J0K1M6\n"+
		"space: test\nsubject: "+contractTestID+"\ntransition: publish\n"+
		"actor: {kind: agent, name: submitter, system: axon}\nat: 2026-08-03T11:00:00Z\n")
	contractTestGit(t, repo, "add", "--", lifecycleEventPath)
	contractTestGit(t, repo, "commit", "-q", "-m", "versionless lifecycle publish")
	oneFiles := map[string]string{"schema/main.schema.json": "schema one"}
	first := commitContractPublicationAtPath(t, repo, "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M8.yaml", contractV1Descriptor("1.0.0"), oneFiles,
		contractEventV1WithID("01K1A2B3C4D5E6F7G8H9J0K1M8", "1.0.0", legacyContractDigest(t, oneFiles)))

	escapedPath := "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M9.yaml"
	files := map[string]string{"schema/main.schema.json": "schema two"}
	escaped := strings.Replace(
		contractEventV1WithID("01K1A2B3C4D5E6F7G8H9J0K1M9", "2.0.0", legacyContractDigest(t, files)),
		"subject: "+contractTestID, `subject: "XC-axon-\u0077idget"`, 1)
	if strings.Contains(escaped, contractTestID) {
		t.Fatalf("the escaped fixture still spells %s literally:\n%s", contractTestID, escaped)
	}
	second := commitContractPublicationAtPath(t, repo, escapedPath, contractV1Descriptor("2.0.0"), files, escaped)

	contractTestGit(t, repo, "rm", "-q", "--", escapedPath)
	contractTestGit(t, repo, "commit", "-q", "-m", "drop the 2.0.0 publish event from the tree")
	tip := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))

	got, err := PublishedContractVersionsAt(t.Context(), repo, tip, contractTestID)
	if err != nil {
		t.Fatalf("PublishedContractVersionsAt: %v", err)
	}
	want := []PublishedContractVersion{{Version: "1.0.0", CommitSHA: first}, {Version: "2.0.0", CommitSHA: second}}
	if !slices.Equal(got, want) {
		t.Fatalf("published versions = %+v, want %+v", got, want)
	}

	atFirst, err := PublishedContractVersionsAt(t.Context(), repo, first, contractTestID)
	if err != nil || !slices.Equal(atFirst, want[:1]) {
		t.Fatalf("published versions at the first publication = %+v (err %v), want %+v", atFirst, err, want[:1])
	}

	history, err := publicationHistoryAt(t.Context(), repo, second, contractTestID, contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("publicationHistoryAt: %v", err)
	}
	listed, err := PublishedContractVersionsAt(t.Context(), repo, second, contractTestID)
	if err != nil {
		t.Fatalf("PublishedContractVersionsAt at the planner's anchor: %v", err)
	}
	if len(history) != len(listed) {
		t.Fatalf("planner history has %d versions, the lister %d", len(history), len(listed))
	}
	for i := range history {
		if history[i].Version != listed[i].Version || history[i].CommitSHA != listed[i].CommitSHA {
			t.Fatalf("planner history[%d] = %s@%s, lister = %+v", i, history[i].Version, history[i].CommitSHA, listed[i])
		}
	}
}

func TestPublishedContractVersionsAtRefusals(t *testing.T) {
	t.Parallel()

	duplicate := newContractHistoryRepo(t)
	commitLegacyContractVersion(t, duplicate, "1.0.0", "schema one")
	commitLegacyContractVersion(t, duplicate, "1.0.0", "schema duplicate")

	nonCanonical := newContractHistoryRepo(t)
	files := map[string]string{"schema/main.schema.json": "schema"}
	commitContractPublication(t, nonCanonical, contractV1Descriptor("1.0.0"), files,
		contractEventV1("01.0.0", legacyContractDigest(t, files)))

	tests := []struct {
		name   string
		repo   string
		anchor string
		id     string
		want   error
	}{
		{name: "duplicate version", repo: duplicate, anchor: "HEAD", id: contractTestID, want: ErrContractHistoryInvalid},
		{name: "non-canonical version", repo: nonCanonical, anchor: "HEAD", id: contractTestID, want: ErrContractHistoryInvalid},
		{name: "not a contract id", repo: duplicate, anchor: "HEAD", id: "XM-axon-widget", want: ErrContractInvalidReference},
		{name: "anchor is not a commit", repo: duplicate, anchor: strings.Repeat("0", 40), id: contractTestID, want: ErrContractObjectUnavailable},
		{name: "anchor is an option", repo: duplicate, anchor: "--all", id: contractTestID, want: ErrContractObjectUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := PublishedContractVersionsAt(t.Context(), tt.repo, tt.anchor, tt.id)
			if !errors.Is(err, tt.want) {
				t.Fatalf("PublishedContractVersionsAt = %+v, %v; want an error wrapping %v", got, err, tt.want)
			}
		})
	}
}
