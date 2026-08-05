package space

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func TestP7IT05PostPRFailuresRetainAndRepairTheExactPR(t *testing.T) {
	t.Parallel()

	postPRFailure := errors.New("injected post-PR failure")
	tests := []struct {
		name      string
		configure func(*host.FakeHost, *bool, *host.PRInfo)
	}{
		{
			name: "arm auto-merge",
			configure: func(fake *host.FakeHost, created *bool, known *host.PRInfo) {
				fake.OpenPRFunc = func(_ context.Context, request host.OpenPRRequest) (host.PRInfo, error) {
					*created = true
					known.HeadSHA = request.ExpectedHeadSHA
					return *known, &host.PartialWriteError{
						Stage: host.PartialWriteStagePRCreated, Operation: host.PartialWriteOperationEnableAutoMerge,
						Err: postPRFailure,
					}
				}
			},
		},
		{
			name: "check status before direct merge",
			configure: func(fake *host.FakeHost, created *bool, known *host.PRInfo) {
				fake.OpenPRFunc = func(_ context.Context, request host.OpenPRRequest) (host.PRInfo, error) {
					*created = true
					known.HeadSHA = request.ExpectedHeadSHA
					known.AutoMergeNote = host.AutoMergeAlreadyCleanNote
					return *known, nil
				}
				first := true
				fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
					if first {
						first = false
						return host.CheckStatusResult{}, postPRFailure
					}
					return host.CheckStatusResult{State: "completed", Conclusion: "success", HeadSHA: known.HeadSHA}, nil
				}
			},
		},
		{
			name: "direct merge",
			configure: func(fake *host.FakeHost, created *bool, known *host.PRInfo) {
				fake.OpenPRFunc = func(_ context.Context, request host.OpenPRRequest) (host.PRInfo, error) {
					*created = true
					known.HeadSHA = request.ExpectedHeadSHA
					known.AutoMergeNote = host.AutoMergeAlreadyCleanNote
					return *known, nil
				}
				fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
					return host.CheckStatusResult{State: "completed", Conclusion: "success", HeadSHA: known.HeadSHA}, nil
				}
				first := true
				fake.MergePRFunc = func(context.Context, host.MergePRRequest) (host.MergeMethod, error) {
					if first {
						first = false
						return host.MergeMethodSquash, postPRFailure
					}
					return host.MergeMethodSquash, nil
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := spacefixture.New(t, "axon")
			layout, err := NewLayout("axon")
			if err != nil {
				t.Fatal(err)
			}
			request := newTestSubmitRequest(fixture, "axon", layout)
			fake := host.NewFakeHost()
			created := false
			known := host.PRInfo{Number: 73, URL: "https://example.invalid/pr/73", State: "open", BaseBranch: "main"}
			test.configure(fake, &created, &known)
			if fake.CheckStatusFunc == nil {
				fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
					return host.CheckStatusResult{State: "completed", Conclusion: "success", HeadSHA: known.HeadSHA}, nil
				}
			}
			fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
				if !created {
					return nil, nil
				}
				copy := known
				return &copy, nil
			}

			funnel := NewWriteFunnel(fake, nil, "0.1.0")
			first, err := funnel.Submit(t.Context(), request)
			if !errors.Is(err, postPRFailure) || first.PRNumber != known.Number || first.PRURL != known.URL ||
				first.Stage != WriteStagePRCreated || first.State != WriteStateNeedsAttention {
				t.Fatalf("first result=%+v err=%v, want exact known PR at failed post-PR boundary", first, err)
			}
			firstURL, firstBranch, firstHead := first.PRURL, first.Branch, first.CommitSHA
			fake.OpenPRFunc = nil
			fake.EnableAutoMergeFunc = nil

			repaired, err := funnel.Submit(t.Context(), request)
			if err != nil {
				t.Fatalf("retry: %v", err)
			}
			if repaired.PRNumber != known.Number || repaired.PRURL != firstURL || repaired.Branch != firstBranch ||
				repaired.Stage != WriteStageAutoMergeArmed || repaired.State != WriteStateAlreadyOpen ||
				len(fake.Opens) != 1 || len(fake.Pushes) != 1 || len(fake.AutoArms) != 1 ||
				fake.AutoArms[0].ExpectedHeadSHA != firstHead {
				t.Fatalf("repair=%+v opens=%d pushes=%d arms=%+v; want same PR/head and no duplicate OpenPR", repaired, len(fake.Opens), len(fake.Pushes), fake.AutoArms)
			}
		})
	}
}

func TestP7IT06MixedHistoryAndLegacyLostACKAfterFloorRise(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	legacyFiles := map[string]string{
		"schema/main.schema.json":  `{"type":"object"}`,
		"fixtures/valid/main.json": "{}\n",
	}
	legacyDigest := legacyContractDigest(t, legacyFiles)
	legacyCommit := commitContractPublication(t, repo, contractV1Descriptor("1.0.0"), legacyFiles, contractEventV1("1.0.0", legacyDigest))
	v2Descriptor, v2Files, _ := contractV2Publication(t, "2.0.0", `{"type":"object","required":["id"]}`)
	v2Descriptor = strings.Replace(v2Descriptor, "artifacts:\n", "generated_from:\n  tool: exporter/2\n  source_digest: sha256:"+strings.Repeat("d", 64)+"\nartifacts:\n", 1)
	v2Parsed, err := contract.ParseDescriptor([]byte(v2Descriptor))
	if err != nil {
		t.Fatal(err)
	}
	v2Candidates := make([]contract.CandidateFile, 0, len(v2Files))
	for path, raw := range v2Files {
		v2Candidates = append(v2Candidates, contract.CandidateFile{Path: path, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	v2Set, v2Issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, []byte(v2Descriptor), v2Parsed, v2Candidates)
	if len(v2Issues) != 0 {
		t.Fatal(v2Issues)
	}
	v2Digest := v2Set.AggregateDigest
	for _, obsolete := range []string{"schema/main.schema.json", "fixtures/valid/main.json"} {
		if err := os.Remove(filepath.Join(repo, contractTestRoot, filepath.FromSlash(obsolete))); err != nil {
			t.Fatal(err)
		}
	}
	v2EventID := "01K1A2B3C4D5E6F7G8H9J0K1M8"
	v2Event := strings.Replace(contractEventV2("2.0.0", v2Digest), "01K1A2B3C4D5E6F7G8H9J0K1M7", v2EventID, 1)
	commitContractPublicationAtPath(t, repo, "axon/events/2026/"+v2EventID+".yaml", v2Descriptor, v2Files, v2Event)

	validator := contractHistoryValidator(t)
	legacy, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", validator)
	if err != nil {
		t.Fatalf("resolve legacy: %v", err)
	}
	current, err := ResolveContractVersion(t.Context(), repo, contractTestID, "2.0.0", validator)
	if err != nil {
		t.Fatalf("resolve v2: %v", err)
	}
	if legacy.CommitSHA != legacyCommit || legacy.DigestProfile != contract.ProfileContractTreeV1 ||
		legacy.AggregateVerification != ContractVerificationEventDigest ||
		legacy.DescriptorVerification != ContractVerificationGitObjectAtPublishCommit ||
		legacy.TreeObjectID == "" || legacy.TreeObjectID == legacy.CommitSHA {
		t.Fatalf("legacy provenance/profile = %+v", legacy)
	}
	if current.DigestProfile != contract.ProfileContractSetV2 ||
		current.DescriptorVerification != ContractVerificationEventDigest ||
		current.AggregateVerification != ContractVerificationEventDigest || current.CommitSHA == legacy.CommitSHA ||
		current.Descriptor.GeneratedFrom == nil || current.Descriptor.GeneratedFrom.SourceDigest == current.PublishedDigest {
		t.Fatalf("v2 provenance/profile = %+v", current)
	}

	reader, source := publicationReaderFromHistorical(legacy)
	main := &publicationMainFake{
		commit: strings.Repeat("f", 40), exact: ContractPublicationCompletion{Snapshot: legacy}, exactFound: true,
		lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
	}
	remote := &publicationRemoteFake{listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true}}
	planning := &publicationPlanningFake{context: publicationPlanningContext(strings.Repeat("f", 40), "0.19.0")}
	events := &publicationEventBuilder{}
	writeHost := &publicationRecoveryHost{FakeHost: host.NewFakeHost()}
	service := mustPublicationService(t, main, planning, remote, events, NewWriteFunnel(writeHost, &publicationSubmitValidator{}, "0.19.0"))
	result, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "axon", ContractID: contractTestID, Selector: "explicit:1.0.0", Candidate: reader, CandidateSource: source,
	})
	if err != nil {
		t.Fatalf("legacy lost-ACK retry after floor rise: %v", err)
	}
	if result.Status != ContractPublicationAlreadyPublished || result.Plan.DigestProfile != contract.ProfileContractTreeV1 ||
		planning.calls != 0 || remote.probeCalls != 0 || events.calls != 0 || writeHost.reads != 0 ||
		len(writeHost.Pushes) != 0 || len(writeHost.Opens) != 0 {
		t.Fatalf("result=%+v plan=%d probe=%d event=%d recovery=%d push/open=%d/%d", result, planning.calls, remote.probeCalls, events.calls, writeHost.reads, len(writeHost.Pushes), len(writeHost.Opens))
	}
}

func TestP7IT07PreflightRenameAndV2LostACKCollision(t *testing.T) {
	t.Parallel()

	fixture := spacefixture.New(t, "atlas")
	repo := fixture.Clone("atlas")
	priorReader, _ := publicationDeclaredCandidate(nil, true)
	for _, file := range priorReader.snapshot.Files {
		writeContractCandidateFile(t, repo, "atlas/provides/demo/"+file.Path, string(file.Raw))
	}
	contractTestGit(t, repo, "add", "--", "atlas/provides/demo")
	contractTestGit(t, repo, "commit", "-q", "-m", "seed prior contract")
	contractTestGit(t, repo, "push", "-q", "origin", "main")
	base := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))

	planningContext := publicationPlanningContext(base, "0.19.0")
	priorCore := priorReader.snapshot.CoreSnapshot()
	priorDescriptor, err := contract.ParseDescriptor(priorCore.Descriptor.Raw)
	if err != nil {
		t.Fatal(err)
	}
	priorSet, issues := contract.ValidateCarriedSet(contract.ProfileContractSetV2, priorDescriptor, priorCore)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	priorModes := make(map[string]string, len(priorSet.PerFileDigest))
	for name, digest := range priorSet.PerFileDigest {
		priorModes[name] = "100644"
		planningContext.PriorDeclaredSidecars[name] = MutationPrecondition{Digest: digest, Mode: "100644"}
	}
	planningContext.Published = []contract.PublishedContract{{
		Version: "0.0.0", DescriptorRaw: bytes.Clone(priorCore.Descriptor.Raw), Set: priorSet, Modes: priorModes,
	}}
	// The tree this publication overwrites — supplied, not derived; see
	// PublicationInput.MutationBaseline.
	planningContext.MutationBaseline = planningContext.Published[0]

	renameReader, renameSource := publicationRenamedSidecarCandidate()
	main := &publicationMainFake{
		commit: base, lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
	}
	remote := &publicationRemoteFake{listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true}}
	planning := &publicationPlanningFake{context: planningContext}
	events := &publicationEventBuilder{}
	writeHost := &publicationRecoveryHost{FakeHost: host.NewFakeHost()}
	service := mustPublicationService(t, main, planning, remote, events, NewWriteFunnel(writeHost, &publicationSubmitValidator{}, "0.19.0"))
	request := ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0", Candidate: renameReader, CandidateSource: renameSource,
		SubmitTemplate: SubmitRequest{
			RepoDir: repo, RemoteURL: fixture.RemoteURL(), Repo: host.Repo{Owner: "acme", Name: "space"}, BaseBranch: "main",
			MinBinaryVersion: "0.19.0", CommitMessage: "publish reviewed sidecar rename",
			CommitAuthorName: "a2a-atlas", CommitAuthorEmail: "a2a-atlas@a2ahub.invalid",
			PRTitle: "Publish XC-atlas-demo@1.0.0", PRBody: "Reviewed exact-set sidecar rename",
		},
		ProducerCompatibility: "0.19.0",
	}
	preflight, err := service.Preflight(t.Context(), request)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if main.refreshes != 0 || main.lookups != 0 || remote.probeCalls != 0 || writeHost.reads != 0 || len(writeHost.Pushes) != 0 || len(writeHost.Opens) != 0 {
		t.Fatalf("preflight crossed host boundary: main=%d/%d remote=%d recovery=%d push/open=%d/%d", main.refreshes, main.lookups, remote.probeCalls, writeHost.reads, len(writeHost.Pushes), len(writeHost.Opens))
	}
	request.ExpectPlan = preflight.Plan.PlanDigest
	published, err := service.Publish(t.Context(), request)
	if err != nil {
		t.Fatalf("unchanged publish: %v", err)
	}
	if published.Status != ContractPublicationSubmitted || !reflect.DeepEqual(preflight.Plan, published.Plan) {
		t.Fatalf("preflight/publish plan drift:\npreflight=%+v\npublish=%+v", preflight.Plan, published.Plan)
	}
	assertP7PublicationPlanFinalBytes(t, published.Plan)
	changed := strings.Fields(contractTestGitOutput(t, repo, "diff", "--name-status", base, published.Write.CommitSHA))
	if !containsAdjacent(changed, "D", "atlas/provides/demo/artifacts/notes.md") ||
		!containsAdjacent(changed, "A", "atlas/provides/demo/artifacts/changelog.md") {
		t.Fatalf("sidecar rename diff = %v", changed)
	}

	completion := publicationCompletionFromPlan(t, published.Plan, renameSource, request.Selector)
	exactReader, exactSource := publicationRenamedSidecarCandidate()
	main.lookup = ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{completion}, Exhaustive: true}
	beforePlanning := planning.calls
	exact, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: request.Selector, Candidate: exactReader, CandidateSource: exactSource,
	})
	if err != nil || exact.Status != ContractPublicationAlreadyPublished || planning.calls != beforePlanning {
		t.Fatalf("exact v2 lost-ACK retry=%+v err=%v planning=%d/%d", exact, err, planning.calls, beforePlanning)
	}

	cosmeticReader, cosmeticSource := publicationRenamedSidecarCandidate()
	for index := range cosmeticReader.snapshot.Files {
		if cosmeticReader.snapshot.Files[index].Path == contract.DescriptorPath {
			cosmeticReader.snapshot.Files[index].Raw = bytes.Replace(cosmeticReader.snapshot.Files[index].Raw,
				[]byte("title: Publication binding"), []byte(`title: "Publication binding"`), 1)
		}
	}
	cosmeticReader.snapshot.Fingerprint = fingerprintContractFiles(cosmeticReader.snapshot.Files)
	cosmeticReader.source.Fingerprint = cosmeticReader.snapshot.Fingerprint
	cosmeticSource.Fingerprint = cosmeticReader.snapshot.Fingerprint
	exactIntent, exactIssues := contract.BuildCandidateIntent(exactReader.snapshot.CoreSnapshot())
	cosmeticIntent, cosmeticIssues := contract.BuildCandidateIntent(cosmeticReader.snapshot.CoreSnapshot())
	if len(exactIssues) != 0 || len(cosmeticIssues) != 0 || exactIntent.Digest() != cosmeticIntent.Digest() {
		t.Fatalf("cosmetic descriptors did not share intent: exact=%v cosmetic=%v %s/%s", exactIssues, cosmeticIssues, exactIntent.Digest(), cosmeticIntent.Digest())
	}
	_, err = service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: request.Selector, Candidate: cosmeticReader, CandidateSource: cosmeticSource,
	})
	if !errors.Is(err, ErrOperationConflict) || planning.calls != beforePlanning || len(writeHost.Opens) != 1 {
		t.Fatalf("semantic/raw collision error=%v planning=%d/%d opens=%d", err, planning.calls, beforePlanning, len(writeHost.Opens))
	}

	reordered := clonePublicationCandidate(exactReader.snapshot)
	first := []byte("  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}\n")
	second := []byte("  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}\n")
	for index := range reordered.Files {
		if reordered.Files[index].Path == contract.DescriptorPath {
			raw := bytes.Replace(reordered.Files[index].Raw, first, []byte("__FIRST__\n"), 1)
			raw = bytes.Replace(raw, second, first, 1)
			reordered.Files[index].Raw = bytes.Replace(raw, []byte("__FIRST__\n"), second, 1)
		}
	}
	reorderedIntent, reorderedIssues := contract.BuildCandidateIntent(reordered.CoreSnapshot())
	if len(reorderedIssues) != 0 || reorderedIntent.Digest() == exactIntent.Digest() {
		t.Fatalf("artifact order was not semantic: issues=%v digest=%s", reorderedIssues, reorderedIntent.Digest())
	}
}

func TestP7IT08HistoricalResolverMaterializerAndConformance(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, _ := contractV2Publication(t, "1.2.3", `{"type":"object","required":["id"]}`)
	files["fixtures/valid/order.json"] = "{\"id\":\"1\"}\n"
	parsedDescriptor, err := contract.ParseDescriptor([]byte(descriptor))
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]contract.CandidateFile, 0, len(files))
	for path, raw := range files {
		candidates = append(candidates, contract.CandidateFile{Path: path, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, []byte(descriptor), parsedDescriptor, candidates)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	digest := set.AggregateDigest
	commit := commitContractPublication(t, repo, descriptor, files, contractEventV2("1.2.3", digest))
	snapshot, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.2.3", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("resolve historical version: %v", err)
	}
	if snapshot.CommitSHA != commit || snapshot.PublishedDigest != digest {
		t.Fatalf("resolved snapshot = %+v", snapshot)
	}

	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewContractMaterializer(project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = materializer.Close() }() // reason: assertions remain primary
	materialized, err := materializer.Materialize(t.Context(), snapshot, "vendor/widget")
	if !contractMaterializeNoReplaceSupported {
		if !errors.Is(err, ErrContractAtomicNoReplaceUnsupported) {
			t.Fatalf("unsupported no-replace error = %v", err)
		}
		return
	}
	if err != nil || materialized.Outcome != ContractMaterialized || materialized.SourceCommit != commit {
		t.Fatalf("materialize=%+v err=%v", materialized, err)
	}
	assertMaterializedSnapshot(t, filepath.Join(project, "vendor", "widget"), snapshot)
	again, err := materializer.Materialize(t.Context(), snapshot, "vendor/widget")
	if err != nil || again.Outcome != ContractAlreadyPresent {
		t.Fatalf("idempotent materialize=%+v err=%v", again, err)
	}
	if err := os.WriteFile(filepath.Join(project, "vendor", "widget", "schema", "order.schema.json"), []byte("diverged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := filesystemProjection(t, filepath.Join(project, "vendor", "widget"))
	if _, err := materializer.Materialize(t.Context(), snapshot, "vendor/widget"); !errors.Is(err, ErrContractDestinationDiverged) {
		t.Fatalf("divergent rerun error = %v", err)
	}
	if after := filesystemProjection(t, filepath.Join(project, "vendor", "widget")); !reflect.DeepEqual(before, after) {
		t.Fatalf("divergence was overwritten: before=%v after=%v", before, after)
	}

	raceProject := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(raceProject, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSnapshotUnrooted(t, filepath.Join(raceProject, "vendor", "widget"), snapshot)
	raceMaterializer, err := NewContractMaterializer(raceProject)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raceMaterializer.Close() }() // reason: assertions remain primary
	raceMaterializer.hooks.afterCompareLeafLstat = func(relative string) {
		if relative != "schema/order.schema.json" {
			return
		}
		leaf := filepath.Join(raceProject, "vendor", "widget", filepath.FromSlash(relative))
		if err := os.Remove(leaf); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, leaf); err != nil {
			t.Fatal(err)
		}
		raceMaterializer.hooks.afterCompareLeafLstat = nil
	}
	if _, err := raceMaterializer.Materialize(t.Context(), snapshot, "vendor/widget"); !errors.Is(err, ErrContractUnsafePath) {
		t.Fatalf("symlink-swap error = %v", err)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "outside\n" {
		t.Fatalf("symlink-swap escaped root: raw=%q err=%v", raw, err)
	}

	suite := contract.CheckConformance(contract.ConformanceInput{
		ContractID: snapshot.ContractID, Version: snapshot.Version, Commit: snapshot.CommitSHA,
		SchemaFormat: snapshot.Descriptor.SchemaFormat, PublishedDigest: snapshot.PublishedDigest,
		Set: snapshot.CarriedSet, Mode: contract.ConformanceModeSuite,
	}, validate.ContractInstanceAdapter{})
	if !suite.Passed || suite.Outcome != contract.ConformanceSuiteConsistent || len(suite.Results) != 2 {
		t.Fatalf("historical suite = %+v", suite)
	}
	payloadInput := contract.ConformanceInput{
		ContractID: snapshot.ContractID, Version: snapshot.Version, Commit: snapshot.CommitSHA,
		SchemaFormat: snapshot.Descriptor.SchemaFormat, PublishedDigest: snapshot.PublishedDigest,
		Set: snapshot.CarriedSet, Mode: contract.ConformanceModePayload,
		PayloadPath: "payloads/invalid.json", Payload: []byte("null\n"),
	}
	left := contract.CheckConformance(payloadInput, validate.ContractInstanceAdapter{})
	right := contract.CheckConformance(payloadInput, validate.ContractInstanceAdapter{})
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil || !bytes.Equal(leftJSON, rightJSON) || left.Passed ||
		left.Outcome != contract.ConformanceNonconformant || len(left.Results) != 1 || len(left.Results[0].Violations) == 0 ||
		left.Results[0].Violations[0].InstancePointer != "" || left.Results[0].Violations[0].Message == "" {
		t.Fatalf("stable structured violation left=%s right=%s errors=%v/%v", leftJSON, rightJSON, leftErr, rightErr)
	}
}

func publicationReaderFromHistorical(snapshot HistoricalSnapshot) (*publicationCandidateReader, contract.CandidateSource) {
	files := make([]ContractSnapshotFile, len(snapshot.Files))
	for index, file := range snapshot.Files {
		files[index] = cloneContractSnapshotFile(file)
		files[index].Source = ContractCandidateSourceMirror
	}
	candidate := ContractCandidateSnapshot{Source: ContractCandidateSourceMirror, Files: files}
	candidate.Fingerprint = fingerprintContractFiles(candidate.Files)
	source := contract.CandidateSource{Kind: contract.CandidateSourceMirror, Location: snapshot.CommitSHA, Fingerprint: candidate.Fingerprint}
	return &publicationCandidateReader{snapshot: candidate, source: source}, source
}

func publicationRenamedSidecarCandidate() (*publicationCandidateReader, contract.CandidateSource) {
	reader, _ := publicationDeclaredCandidate(nil, true)
	reader.snapshot.Files[0].Raw = bytes.Replace(reader.snapshot.Files[0].Raw,
		[]byte("artifacts/notes.md"), []byte("artifacts/changelog.md"), 1)
	for index := range reader.snapshot.Files {
		if reader.snapshot.Files[index].Path == "artifacts/notes.md" {
			reader.snapshot.Files[index].Path = "artifacts/changelog.md"
			reader.snapshot.Files[index].Raw = []byte("reviewed change log\n")
		}
	}
	sortContractSnapshotFiles(reader.snapshot.Files)
	reader.snapshot.Fingerprint = fingerprintContractFiles(reader.snapshot.Files)
	reader.source.Fingerprint = reader.snapshot.Fingerprint
	return reader, reader.source
}

func assertP7PublicationPlanFinalBytes(t *testing.T, plan contract.PublicationPlan) {
	t.Helper()
	planned := plan.PlannedBytes()
	descriptorRaw := planned[contract.DescriptorPath]
	descriptor, err := contract.ParseDescriptor(descriptorRaw)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]contract.CandidateFile, 0, len(planned)-1)
	for path, raw := range planned {
		if path != contract.DescriptorPath {
			files = append(files, contract.CandidateFile{Path: path, Kind: contract.CandidateRegular, Raw: raw})
		}
	}
	set, issues := contract.BuildCarriedSet(plan.DigestProfile, descriptorRaw, descriptor, files)
	entryDigests := make(map[string]string, len(plan.Entries))
	for _, entry := range plan.Entries {
		entryDigests[entry.Path] = entry.Digest
	}
	setDigests := make(map[string]string, len(set.PerFileDigest)+1)
	for path, digest := range set.PerFileDigest {
		setDigests[path] = digest
	}
	setDigests[contract.DescriptorPath] = artifact.Digest(descriptorRaw)
	versionFinalized := bytes.Contains(descriptorRaw, []byte("version: "+plan.TargetVersion+"\n")) ||
		bytes.Contains(descriptorRaw, []byte("version: \""+plan.TargetVersion+"\""))
	if len(issues) != 0 || set.AggregateDigest != plan.AggregateDigest ||
		!reflect.DeepEqual(setDigests, entryDigests) || !versionFinalized {
		t.Fatalf("plan digests were not derived from the final descriptor: issues=%v set=%+v plan=%+v", issues, set, plan)
	}
}

func containsAdjacent(values []string, left, right string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == left && values[index+1] == right {
			return true
		}
	}
	return false
}
