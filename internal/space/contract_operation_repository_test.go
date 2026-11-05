package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// TestPublishedContractVersionsAtAttributesABranchPublishToItsLandMerge pins
// the shape a local host lands a write in (internal/host/local.go: a merge
// commit whose first parent is the old tip and whose tree is the branch head's).
// The merge is TREESAME to its second parent, so a walk that follows the
// branch's own commits attributes the publish to a commit main never pointed
// at, and a walk that diffs merges against no parent loses it.
func TestPublishedContractVersionsAtAttributesABranchPublishToItsLandMerge(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	oneFiles := map[string]string{"schema/main.schema.json": "schema one"}
	first := commitContractPublicationAtPath(t, repo, "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M8.yaml", contractV1Descriptor("1.0.0"), oneFiles,
		contractEventV1WithID("01K1A2B3C4D5E6F7G8H9J0K1M8", "1.0.0", legacyContractDigest(t, oneFiles)))

	contractTestGit(t, repo, "checkout", "-q", "-b", "write")
	writeContractCandidateFile(t, repo, "axon/data/note.txt", "not an event\n")
	contractTestGit(t, repo, "add", "--", "axon/data/note.txt")
	contractTestGit(t, repo, "commit", "-q", "-m", "a non-event file the branch adds")
	twoFiles := map[string]string{"schema/main.schema.json": "schema two"}
	writeContractPublicationAtPath(t, repo, "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M9.yaml", contractV1Descriptor("2.0.0"), twoFiles,
		contractEventV1WithID("01K1A2B3C4D5E6F7G8H9J0K1M9", "2.0.0", legacyContractDigest(t, twoFiles)))
	contractTestGit(t, repo, "add", "--", contractTestRoot, "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M9.yaml")
	contractTestGit(t, repo, "commit", "-q", "-m", "publish 2.0.0 on the write branch")
	head := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
	tree := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", head+"^{tree}"))
	merge := strings.TrimSpace(contractTestGitOutput(t, repo, "commit-tree", tree, "-p", first, "-p", head, "-m", "land"))

	got, err := PublishedContractVersionsAt(t.Context(), repo, merge, contractTestID)
	if err != nil {
		t.Fatalf("PublishedContractVersionsAt: %v", err)
	}
	want := []PublishedContractVersion{{Version: "1.0.0", CommitSHA: first}, {Version: "2.0.0", CommitSHA: merge}}
	if !slices.Equal(got, want) {
		t.Fatalf("published versions at the land merge = %+v, want %+v (branch head %s)", got, want, head)
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

// publishedWidgetAdapter is a real repository adapter over a space that already
// carries one published version of contractTestID, plus the candidate reader a
// caller would hand to Preflight or Publish.
func publishedWidgetAdapter(t *testing.T) (*ContractPublicationRepository, *publicationCandidateReader, contract.CandidateSource) {
	t.Helper()
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
	snapshotFiles := []ContractSnapshotFile{publicationSnapshotFile(contract.DescriptorPath, []byte(descriptor))}
	for name, raw := range files {
		snapshotFiles = append(snapshotFiles, publicationSnapshotFile(name, []byte(raw)))
	}
	snapshot := ContractCandidateSnapshot{Source: ContractCandidateSourceMirror, Files: snapshotFiles}
	snapshot.Fingerprint = fingerprintContractFiles(snapshotFiles)
	source := contract.CandidateSource{Kind: contract.CandidateSourceMirror, Location: strings.Repeat("a", 40), Fingerprint: snapshot.Fingerprint}
	return adapter, &publicationCandidateReader{snapshot: snapshot, source: source}, source
}

func TestContractPublicationHistoryIsReadOnceForAPreflight(t *testing.T) {
	t.Parallel()
	adapter, candidate, source := publishedWidgetAdapter(t)
	if _, err := adapter.RefreshContractPublicationMain(t.Context()); err != nil {
		t.Fatal(err)
	}
	service, err := NewContractPreflightService(adapter, publicationCompatibilityChecker{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Preflight(t.Context(), ContractPublicationRequest{
		System: "axon", ContractID: contractTestID, Selector: "auto:patch", Candidate: candidate, CandidateSource: source, AllowEmptyBump: true,
	})
	if err != nil {
		t.Fatalf("Preflight = %+v, %v", result, err)
	}
	if got := adapter.histories.misses.Load(); got != 1 {
		t.Fatalf("history walks during one Preflight = %d, want 1", got)
	}
}

// remoteProbeResolvingInMain behaves like a host that, for every remote head it
// finds, asks main whether that head's target already landed. That is the call
// that used to re-walk the whole history once per head.
type remoteProbeResolvingInMain struct {
	targets []string
	proof   publicationRemoteFake
}

func (f *remoteProbeResolvingInMain) ProbeContractPublicationHeads(ctx context.Context, request ContractPublicationHeadProbeRequest) (ContractPublicationHeadListing, error) {
	for _, target := range f.targets {
		if _, err := request.ResolvedInMain(ctx, request.ContractID, target); err != nil {
			return ContractPublicationHeadListing{}, err
		}
	}
	return f.proof.ProbeContractPublicationHeads(ctx, request)
}

func (f *remoteProbeResolvingInMain) RepairContractPublicationHead(ctx context.Context, proof ContractPublicationHeadProof, runtime SubmissionRuntime) (WriteResult, error) {
	return f.proof.RepairContractPublicationHead(ctx, proof, runtime)
}

// Publish reads history at four sites — explicit-target resolution, intent
// lookup, every remote head's "already in main?" callback, and planning — all
// against one pinned anchor. They must share one walk.
func TestContractPublicationHistoryIsReadOnceAcrossAPublish(t *testing.T) {
	t.Parallel()
	adapter, candidate, source := publishedWidgetAdapter(t)
	remote := &remoteProbeResolvingInMain{targets: []string{"1.0.0", "1.0.0", "2.0.0"}}
	remote.proof.listing = ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true}
	service := &ContractPublicationService{main: adapter, planning: adapter, remote: remote, checker: publicationCompatibilityChecker{}}

	_, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "axon", ContractID: contractTestID, Selector: "explicit:1.1.0", Candidate: candidate, CandidateSource: source,
	})
	// The fakes stop the run after planning (no event builder, no funnel); what
	// matters here is that every history-reading site fired before that.
	t.Logf("Publish stopped with: %v", err)
	if remote.proof.probeCalls != 1 {
		t.Fatalf("remote probe calls = %d, want 1: Publish never reached the head probe, so the test judged nothing", remote.proof.probeCalls)
	}
	// 1 explicit-target resolution + 1 intent lookup + 3 remote-head callbacks
	// + 1 planning read: all six must have asked, and one walk must have answered.
	if got := adapter.histories.requests.Load(); got != 6 {
		t.Fatalf("history reads asked during one Publish = %d, want 6 (a site did not fire, so the walk count below proves less)", got)
	}
	if got := adapter.histories.misses.Load(); got != 1 {
		t.Fatalf("history walks during one Publish = %d, want 1", got)
	}
}

func TestContractPublicationHistoryCacheReturnsIndependentCopiesAndNeverCachesErrors(t *testing.T) {
	t.Parallel()
	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", `{"type":"object"}`)
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))
	anchor := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
	validator := contractHistoryValidator(t)

	var cache contractPublicationHistoryCache
	first, err := cache.read(t.Context(), repo, anchor, contractTestID, validator)
	if err != nil || len(first) != 1 {
		t.Fatalf("first read = %d versions, %v; want 1", len(first), err)
	}
	// Scribble over everything a caller could reach.
	first[0].Version = "9.9.9"
	first[0].PublishEventRaw[0] ^= 0xff
	first[0].Files[0].Raw[0] ^= 0xff
	second, err := cache.read(t.Context(), repo, anchor, contractTestID, validator)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Version != "1.0.0" || second[0].PublishEventRaw[0] == first[0].PublishEventRaw[0] || second[0].Files[0].Raw[0] == first[0].Files[0].Raw[0] {
		t.Fatalf("cache returned state shared with an earlier caller: version %q", second[0].Version)
	}
	// The second read was a hit; scribbling on it must not reach the third.
	second[0].Version = "8.8.8"
	second[0].PublishEventRaw[0] ^= 0xff
	third, err := cache.read(t.Context(), repo, anchor, contractTestID, validator)
	if err != nil {
		t.Fatal(err)
	}
	if third[0].Version != "1.0.0" || third[0].PublishEventRaw[0] == second[0].PublishEventRaw[0] {
		t.Fatalf("a cache hit handed out the stored copy itself: version %q", third[0].Version)
	}
	if got := cache.misses.Load(); got != 1 {
		t.Fatalf("walks = %d, want 1 (the second and third reads are hits)", got)
	}

	// An error is not remembered: a transient failure must not become a verdict.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.read(cancelled, repo, strings.Repeat("0", 40), contractTestID, validator); err == nil {
		t.Fatal("read against an unreachable anchor succeeded")
	}
	if _, err := cache.read(cancelled, repo, strings.Repeat("0", 40), contractTestID, validator); err == nil {
		t.Fatal("second read against an unreachable anchor succeeded")
	}
	if got := cache.misses.Load(); got != 3 {
		t.Fatalf("walks after two failing reads = %d, want 3: errors must be retried, not cached", got)
	}
}

func TestContractPublicationHistoryCacheIsSafeForConcurrentReaders(t *testing.T) {
	t.Parallel()
	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", `{"type":"object"}`)
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))
	anchor := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
	validator := contractHistoryValidator(t)

	var cache contractPublicationHistoryCache
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 4 {
				history, err := cache.read(t.Context(), repo, anchor, contractTestID, validator)
				if err != nil || len(history) != 1 {
					t.Errorf("concurrent read = %d versions, %v", len(history), err)
					return
				}
				if history[0].Version != "1.0.0" {
					t.Errorf("a reader saw another reader's write: version %q", history[0].Version)
					return
				}
				// Each reader owns its copy: writing to it must disturb nobody.
				history[0].PublishEventRaw[0] ^= 0xff
				history[0].Version = "scribbled"
			}
		}()
	}
	group.Wait()
}
