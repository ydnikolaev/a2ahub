package space

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

type publicationCandidateReader struct {
	snapshot ContractCandidateSnapshot
	source   contract.CandidateSource
	calls    int
	order    *[]string
}

func (r *publicationCandidateReader) ContractPublicationCandidateSource() contract.CandidateSource {
	return r.source
}

func (r *publicationCandidateReader) ReadContractPublicationCandidate(context.Context) (ContractCandidateSnapshot, error) {
	r.calls++
	appendPublicationOrder(r.order, "candidate")
	return clonePublicationCandidate(r.snapshot), nil
}

type publicationMainFake struct {
	commit     string
	exact      ContractPublicationCompletion
	exactFound bool
	lookup     ContractPublicationIntentLookup
	order      *[]string
	refreshes  int
	exacts     int
	lookups    int
}

func (f *publicationMainFake) RefreshContractPublicationMain(context.Context) (string, error) {
	f.refreshes++
	appendPublicationOrder(f.order, "refresh-main")
	return f.commit, nil
}

func (f *publicationMainFake) ResolveContractPublicationTarget(context.Context, string, string) (ContractPublicationCompletion, bool, error) {
	f.exacts++
	appendPublicationOrder(f.order, "exact-target")
	return f.exact, f.exactFound, nil
}

func (f *publicationMainFake) LookupContractPublicationIntent(context.Context, string, string) (ContractPublicationIntentLookup, error) {
	f.lookups++
	appendPublicationOrder(f.order, "intent-main")
	return f.lookup, nil
}

type publicationPlanningFake struct {
	context ContractPublicationPlanningContext
	calls   int
	request ContractPublicationPlanningRequest
	order   *[]string
}

func (f *publicationPlanningFake) ReadContractPublicationPlanningContext(_ context.Context, request ContractPublicationPlanningRequest) (ContractPublicationPlanningContext, error) {
	f.calls++
	f.request = request
	appendPublicationOrder(f.order, "plan-context")
	return f.context, nil
}

type publicationRemoteFake struct {
	listing     ContractPublicationHeadListing
	probeCalls  int
	repairCalls int
	request     ContractPublicationHeadProbeRequest
	repair      WriteResult
	repairErr   error
	order       *[]string
}

func (f *publicationRemoteFake) ProbeContractPublicationHeads(_ context.Context, request ContractPublicationHeadProbeRequest) (ContractPublicationHeadListing, error) {
	f.probeCalls++
	f.request = request
	appendPublicationOrder(f.order, "remote-heads")
	return f.listing, nil
}

func (f *publicationRemoteFake) RepairContractPublicationHead(context.Context, ContractPublicationHeadProof, SubmissionRuntime) (WriteResult, error) {
	f.repairCalls++
	appendPublicationOrder(f.order, "repair-pr")
	return f.repair, f.repairErr
}

type publicationEventBuilder struct {
	calls int
	order *[]string
}

func (b *publicationEventBuilder) BuildContractPublicationEvent(_ context.Context, plan contract.PublicationPlan) (FileWrite, error) {
	b.calls++
	appendPublicationOrder(b.order, "event")
	publication := ""
	profile := ""
	if plan.EventSchema == "event/v2" {
		profile = "digest_profile: " + string(plan.DigestProfile) + "\n"
		publication = fmt.Sprintf("publication:\n  intent_key: %s\n  candidate_intent_digest: %s\n  version_selector: %s\n  operation_key: %s\n",
			plan.IntentKey, plan.CandidateIntentDigest, plan.VersionSelector, plan.OperationKey)
	}
	return FileWrite{
		Path: "atlas/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M7.yaml",
		Content: []byte(fmt.Sprintf("schema: %s\nevent: 01K1A2B3C4D5E6F7G8H9J0K1M7\nsubject: %s\ntransition: publish\nversion: %s\ndigest: %s\nactor:\n  system: atlas\n%s%s",
			plan.EventSchema, plan.Contract, plan.TargetVersion, plan.AggregateDigest, profile, publication)),
	}, nil
}

type publicationCompatibilityChecker struct{}

func (publicationCompatibilityChecker) CheckCompatibility(contract.CompatibilityCheckInput) contract.CompatibilityResult {
	return contract.CompatibilityResult{Verdict: contract.CompatibilityCompatible, Failures: []contract.CompatibilityFailure{}}
}

type publicationSubmitValidator struct {
	candidateCalls int
	finalCalls     int
}

func (v *publicationSubmitValidator) ValidateSubmitCandidate(context.Context, []FileWrite) error {
	v.candidateCalls++
	return nil
}

func (v *publicationSubmitValidator) ValidateSubmit(context.Context, []FileWrite) error {
	v.finalCalls++
	return nil
}

type publicationRecoveryHost struct {
	*host.FakeHost
	reads int
}

func (h *publicationRecoveryHost) ReadRemoteRecoveryCommit(
	context.Context, string, string, host.Repo, string, host.Credential,
) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error) {
	h.reads++
	return host.Repo{}, "", "", "", "", "", "", nil, false, nil
}

func TestContractPublicationPreflightIsLocalAndUsesTheSharedPlanner(t *testing.T) {
	t.Parallel()

	order := []string{}
	reader, source := publicationDeclaredCandidate(&order, false)
	planning := &publicationPlanningFake{order: &order, context: publicationPlanningContext("", "0.19.0")}
	main := &publicationMainFake{order: &order}
	remote := &publicationRemoteFake{order: &order}
	events := &publicationEventBuilder{order: &order}
	service := mustPublicationService(t, main, planning, remote, events, NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0"))

	result, err := service.Preflight(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major",
		Candidate: reader, CandidateSource: source,
	})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if result.Status != ContractPublicationPlanned || result.Plan.TargetVersion != "1.0.0" || result.Plan.IntentKey == "" {
		t.Fatalf("result = %+v, want first v2 publication plan", result)
	}
	if reader.calls != 1 || planning.calls != 1 || main.refreshes != 0 || main.lookups != 0 || remote.probeCalls != 0 || events.calls != 0 {
		t.Fatalf("calls candidate=%d planning=%d main=%d/%d remote=%d event=%d",
			reader.calls, planning.calls, main.refreshes, main.lookups, remote.probeCalls, events.calls)
	}
	if got := strings.Join(order, ","); got != "candidate,plan-context" {
		t.Fatalf("preflight order = %q", got)
	}
}

func TestContractPublicationFreezingBindsSourceLocationAndAllowsExecutableCandidate(t *testing.T) {
	t.Parallel()
	planning := &publicationPlanningFake{context: publicationPlanningContext("", "0.19.0")}
	service := mustPreflightService(t, planning)
	reader, source := publicationDeclaredCandidate(nil, false)
	reader.source.Location = "different-tree"
	_, err := service.Preflight(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
	})
	if !errors.Is(err, ErrContractPublicationInvalid) || planning.calls != 0 {
		t.Fatalf("source-location mismatch error=%v planning=%d", err, planning.calls)
	}

	reader, source = publicationDeclaredCandidate(nil, false)
	reader.snapshot.Files[1].Mode = "100755"
	reader.snapshot.Fingerprint = fingerprintContractFiles(reader.snapshot.Files)
	reader.source.Fingerprint, source.Fingerprint = reader.snapshot.Fingerprint, reader.snapshot.Fingerprint
	_, err = service.Preflight(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
	})
	if err != nil || planning.calls != 1 {
		t.Fatalf("executable candidate error=%v planning=%d", err, planning.calls)
	}
}

func TestContractPublicationEventBindsPathBasenameAndActorOwner(t *testing.T) {
	t.Parallel()
	planning := &publicationPlanningFake{context: publicationPlanningContext("", "0.19.0")}
	service := mustPreflightService(t, planning)
	reader, source := publicationDeclaredCandidate(nil, false)
	result, err := service.Preflight(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := (&publicationEventBuilder{}).BuildContractPublicationEvent(t.Context(), result.Plan)
	if err != nil {
		t.Fatal(err)
	}
	wrongPath := event
	wrongPath.Path = "atlas/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M8.yaml"
	if _, err := verifyContractPublicationEvent("atlas", result.Plan, wrongPath); !errors.Is(err, ErrContractPublicationInvalid) {
		t.Fatalf("path/event mismatch error = %v", err)
	}
	wrongActor := event
	wrongActor.Content = bytes.Replace(wrongActor.Content, []byte("system: atlas"), []byte("system: checkout"), 1)
	if _, err := verifyContractPublicationEvent("atlas", result.Plan, wrongActor); !errors.Is(err, ErrContractPublicationInvalid) {
		t.Fatalf("actor/owner mismatch error = %v", err)
	}
}

func TestContractPublicationPublishProbesBeforePlanningThenPreparesAndSubmitsOnce(t *testing.T) {
	t.Parallel()

	fixture := spacefixture.New(t, "atlas")
	base, err := runGitOutput(t.Context(), fixture.Clone("atlas"), nil, "rev-parse", "origin/main")
	if err != nil {
		t.Fatalf("resolve fixture main: %v", err)
	}
	order := []string{}
	reader, source := publicationDeclaredCandidate(&order, false)
	main := &publicationMainFake{
		commit: base, order: &order,
		lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
	}
	remote := &publicationRemoteFake{
		order: &order, listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true},
	}
	planning := &publicationPlanningFake{order: &order, context: publicationPlanningContext(base, "0.19.0")}
	events := &publicationEventBuilder{order: &order}
	validator := &publicationSubmitValidator{}
	h := &publicationRecoveryHost{FakeHost: host.NewFakeHost()}
	funnel := NewWriteFunnel(h, validator, "0.19.0")
	service := mustPublicationService(t, main, planning, remote, events, funnel)

	result, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major",
		Candidate: reader, CandidateSource: source,
		SubmitTemplate: SubmitRequest{
			RepoDir: fixture.Clone("atlas"), RemoteURL: fixture.RemoteURL(),
			Repo:             host.Repo{Owner: "acme", Name: "getvisa"},
			MinBinaryVersion: "0.19.0",
			CommitMessage:    "a2a(contract): publish XC-atlas-demo@1.0.0",
			CommitAuthorName: "a2a-atlas", CommitAuthorEmail: "a2a-atlas@a2ahub.invalid",
			PRTitle: "Publish XC-atlas-demo@1.0.0", PRBody: "Exact immutable publication",
		},
		ProducerCompatibility: "0.19.0",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Status != ContractPublicationSubmitted || result.Plan.PlanDigest == "" || result.Write.CommitSHA == "" {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Join(order[:5], ","); got != "candidate,refresh-main,intent-main,remote-heads,plan-context" {
		t.Fatalf("publication order = %q (all=%v)", got, order)
	}
	if reader.calls != 1 || planning.calls != 1 || remote.probeCalls != 1 || events.calls != 1 ||
		validator.candidateCalls != 1 || validator.finalCalls != 1 || h.reads != 1 || len(h.Pushes) != 1 || len(h.Opens) != 1 {
		t.Fatalf("calls reader=%d plan=%d probe=%d event=%d validation=%d/%d recovery=%d push/open=%d/%d",
			reader.calls, planning.calls, remote.probeCalls, events.calls, validator.candidateCalls, validator.finalCalls,
			h.reads, len(h.Pushes), len(h.Opens))
	}
	if remote.request.MaximumHeads != 256 || remote.request.NamespacePrefix != "a2a/atlas/contract-publish/op-v1-" {
		t.Fatalf("remote probe = %+v", remote.request)
	}
}

func TestContractPublicationSubmissionRequiresUnchangedObservedWriteFloor(t *testing.T) {
	t.Parallel()

	_, source := publicationDeclaredCandidate(nil, false)
	record := publicationRecoveryRecord(
		"sha256:"+strings.Repeat("b", 64),
		"op-v1-"+strings.Repeat("c", 64),
		"op-v1-"+strings.Repeat("d", 64),
		"explicit:1.0.0",
		"1.0.0",
	)
	plan := publicationRecoveryPlan(record, source)
	planning := publicationPlanningContext(strings.Repeat("a", 40), "0.19.0")

	for _, test := range []struct {
		name         string
		observed     string
		wantErr      error
		wantFloorOut string
	}{
		{name: "equal", observed: "0.19.0", wantFloorOut: "0.19.0"},
		{name: "changed", observed: "0.18.0", wantErr: ErrPreparedTargetStale},
		{name: "missing", wantErr: ErrPreparedTargetStale},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := ContractPublicationRequest{
				System: "atlas", ContractID: "XC-atlas-demo", ProducerCompatibility: "0.19.0",
				SubmitTemplate: SubmitRequest{
					RepoDir: "/bounded/repo", RemoteURL: "https://example.test/acme/getvisa.git",
					Repo: host.Repo{Owner: "acme", Name: "getvisa"}, PRBody: "Publish exact contract",
					MinBinaryVersion: test.observed,
				},
			}
			submit, preparation, runtime, err := bindContractPublicationSubmission(
				request, plan, planning, nil, "XE-01K1A2B3C4D5E6F7G8H9J0K1M7",
			)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("bind error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil || submit.MinBinaryVersion != test.wantFloorOut ||
				preparation.ObservedSpaceFloor != test.wantFloorOut || runtime.CurrentSpaceFloor != test.wantFloorOut {
				t.Fatalf("submit=%+v preparation=%+v runtime=%+v err=%v", submit, preparation, runtime, err)
			}
		})
	}
}

func TestContractPublicationDurableMainIntentWinsAfterFastMergeAndHeadDeletion(t *testing.T) {
	t.Parallel()

	reader, source := publicationDeclaredCandidate(nil, false)
	preflightPlanning := &publicationPlanningFake{context: publicationPlanningContext("", "0.19.0")}
	service := mustPreflightService(t, preflightPlanning)
	preflight, err := service.Preflight(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := publicationCompletionFromPlan(t, preflight.Plan, source, "auto:major")
	reader, _ = publicationDeclaredCandidate(nil, false)
	main := &publicationMainFake{
		commit: strings.Repeat("a", 40),
		lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{completion}, Exhaustive: true},
	}
	remote := &publicationRemoteFake{listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true}}
	planning := &publicationPlanningFake{context: publicationPlanningContext(strings.Repeat("a", 40), "0.19.0")}
	service = mustPublicationService(t, main, planning, remote, &publicationEventBuilder{}, NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0"))

	result, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
	})
	if err != nil {
		t.Fatalf("Publish recovered main completion: %v", err)
	}
	if result.Status != ContractPublicationAlreadyPublished || result.Plan.VersionSelector != "auto:major" ||
		planning.calls != 0 || remote.probeCalls != 0 {
		t.Fatalf("result=%+v planning=%d remote=%d", result, planning.calls, remote.probeCalls)
	}

	reader, _ = publicationDeclaredCandidate(nil, false)
	result, err = service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
		ExpectPlan: "sha256:" + strings.Repeat("f", 64),
	})
	if err != nil || result.Status != ContractPublicationAlreadyPublished || remote.probeCalls != 0 || planning.calls != 0 {
		t.Fatalf("intent completion expect-plan error=%v planning=%d remote=%d", err, planning.calls, remote.probeCalls)
	}

	completion.Snapshot.Files[0].Raw = append(completion.Snapshot.Files[0].Raw, '\n')
	reader, _ = publicationDeclaredCandidate(nil, false)
	main.lookup.Matches = []ContractPublicationCompletion{completion}
	result, err = service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
	})
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("raw-tree collision error = %v, want ErrOperationConflict", err)
	}
}

func TestContractPublicationExplicitLegacyCompletionSurvivesFloorRaiseAndHeadDeletion(t *testing.T) {
	t.Parallel()

	reader, source := publicationLegacyCandidate(nil)
	preflightPlanning := &publicationPlanningFake{context: publicationPlanningContext("", "0.18.0")}
	service := mustPreflightService(t, preflightPlanning)
	preflight, err := service.Preflight(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0", Candidate: reader, CandidateSource: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := publicationCompletionFromPlan(t, preflight.Plan, source, "explicit:1.0.0")
	reader, _ = publicationLegacyCandidate(nil)
	main := &publicationMainFake{
		commit: strings.Repeat("a", 40), exact: completion, exactFound: true,
		lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
	}
	remote := &publicationRemoteFake{listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true}}
	planning := &publicationPlanningFake{context: publicationPlanningContext(strings.Repeat("a", 40), "0.19.0")}
	service = mustPublicationService(t, main, planning, remote, &publicationEventBuilder{}, NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0"))

	result, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0", Candidate: reader, CandidateSource: source,
	})
	if err != nil || result.Status != ContractPublicationAlreadyPublished {
		t.Fatalf("legacy retry result=%+v error=%v", result, err)
	}
	if main.exacts != 1 || main.lookups != 0 || remote.probeCalls != 0 || planning.calls != 0 {
		t.Fatalf("calls exact=%d intent=%d remote=%d current-planning=%d", main.exacts, main.lookups, remote.probeCalls, planning.calls)
	}

	reader, _ = publicationLegacyCandidate(nil)
	_, err = service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0", Candidate: reader, CandidateSource: source,
		ExpectPlan: "sha256:" + strings.Repeat("f", 64),
	})
	if err != nil || result.Status != ContractPublicationAlreadyPublished || main.lookups != 0 || remote.probeCalls != 0 || planning.calls != 0 {
		t.Fatalf("explicit completion expect-plan error=%v intent=%d remote=%d planning=%d", err, main.lookups, remote.probeCalls, planning.calls)
	}
}

func TestContractPublicationRemoteProofIsExhaustiveAndRecoveryBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		listing ContractPublicationHeadListing
		want    error
	}{
		{name: "continuation", listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: false}, want: ErrContractBoundedResult},
		{name: "257th", listing: ContractPublicationHeadListing{Heads: make([]ContractPublicationHeadProof, 256), Observed: 257, Exhaustive: false}, want: ErrContractBoundedResult},
		{name: "malformed", listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{{Branch: "bad", HeadSHA: strings.Repeat("a", 40), TreeVerified: true}}, Observed: 1, Exhaustive: true}, want: ErrOperationConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader, source := publicationDeclaredCandidate(nil, false)
			main := &publicationMainFake{
				commit: strings.Repeat("a", 40),
				lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
			}
			remote := &publicationRemoteFake{listing: test.listing}
			planning := &publicationPlanningFake{context: publicationPlanningContext(strings.Repeat("a", 40), "0.19.0")}
			service := mustPublicationService(t, main, planning, remote, &publicationEventBuilder{}, NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0"))
			_, err := service.Publish(t.Context(), ContractPublicationRequest{
				System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
			})
			if !errors.Is(err, test.want) || planning.calls != 0 {
				t.Fatalf("Publish error=%v planning=%d, want %v before planning", err, planning.calls, test.want)
			}
		})
	}
}

func TestContractPublicationRepairsOnlyExactRecoveryAndBindsExpectedPlan(t *testing.T) {
	t.Parallel()

	reader, source := publicationDeclaredCandidate(nil, false)
	snapshot := reader.snapshot.CoreSnapshot()
	candidate, issues := contract.BuildCandidateIntent(snapshot)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	selector := "auto:major"
	intentKey := operation.ContractPublishIntent("atlas", "XC-atlas-demo", selector, candidate.Digest())
	target := "1.0.0"
	operationKey := operation.ContractPublish("atlas", "XC-atlas-demo", target)
	record := publicationRecoveryRecord(candidate.Digest(), intentKey, operationKey, selector, target)
	raw, err := EncodeRecoveryV1(record)
	if err != nil {
		t.Fatal(err)
	}
	remote := &publicationRemoteFake{
		listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{{
			Branch: record.HeadBranch, HeadSHA: strings.Repeat("a", 40), RecoveryJSON: raw, TreeVerified: true,
			recoveredPlan: publicationRecoveryPlan(record, source),
		}}, Observed: 1, Exhaustive: true},
		repair: WriteResult{
			Branch: record.HeadBranch, CommitSHA: strings.Repeat("a", 40), PRNumber: 42, PRURL: "https://example.test/pull/42",
			Stage: WriteStagePRCreated, State: WriteStateAlreadyOpen, RemainingAction: RemainingActionArmAutoMerge,
		},
	}
	main := &publicationMainFake{
		commit: strings.Repeat("b", 40), lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
	}
	planning := &publicationPlanningFake{context: publicationPlanningContext(strings.Repeat("b", 40), "0.19.0")}
	service := mustPublicationService(t, main, planning, remote, &publicationEventBuilder{}, NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0"))

	result, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: selector, Candidate: reader, CandidateSource: source,
		ExpectPlan:     record.PlanDigest,
		SubmitTemplate: SubmitRequest{Repo: host.Repo{Owner: "acme", Name: "getvisa"}},
	})
	if err != nil || result.Status != ContractPublicationRepaired || remote.repairCalls != 1 || planning.calls != 0 {
		t.Fatalf("result=%+v err=%v repair=%d planning=%d", result, err, remote.repairCalls, planning.calls)
	}
	if result.Plan.Contract != "XC-atlas-demo" || result.Plan.TargetVersion != target ||
		result.Plan.PlanDigest != record.PlanDigest || result.Plan.IntentKey != record.IntentKey ||
		result.Plan.OperationKey != record.OperationKey || result.Plan.CandidateIntentDigest != record.CandidateIntentDigest ||
		result.Plan.VersionSelector != record.VersionSelector || result.Plan.HeadBranch != record.HeadBranch ||
		result.Plan.CandidateSource != source {
		t.Fatalf("repaired result lost recovered plan identity: %+v", result.Plan)
	}

	reader, _ = publicationDeclaredCandidate(nil, false)
	_, err = service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: selector, Candidate: reader, CandidateSource: source,
		ExpectPlan:     "sha256:" + strings.Repeat("e", 64),
		SubmitTemplate: SubmitRequest{Repo: host.Repo{Owner: "acme", Name: "getvisa"}},
	})
	if !errors.Is(err, ErrContractPublicationPlanChanged) || remote.repairCalls != 1 {
		t.Fatalf("changed expected plan error=%v repair calls=%d", err, remote.repairCalls)
	}
}

func TestContractPublicationRepairPreservesPartialOutcomeAndRequiresObservedPR(t *testing.T) {
	t.Parallel()

	reader, source := publicationDeclaredCandidate(nil, false)
	candidate, issues := contract.BuildCandidateIntent(reader.snapshot.CoreSnapshot())
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	selector := "auto:major"
	intentKey := operation.ContractPublishIntent("atlas", "XC-atlas-demo", selector, candidate.Digest())
	operationKey := operation.ContractPublish("atlas", "XC-atlas-demo", "1.0.0")
	record := publicationRecoveryRecord(candidate.Digest(), intentKey, operationKey, selector, "1.0.0")
	raw, err := EncodeRecoveryV1(record)
	if err != nil {
		t.Fatal(err)
	}
	proof := ContractPublicationHeadProof{
		Branch: record.HeadBranch, HeadSHA: strings.Repeat("a", 40), RecoveryJSON: raw, TreeVerified: true,
		recoveredPlan: publicationRecoveryPlan(record, source),
	}
	repairFailure := errors.New("provider acknowledgement lost")
	tests := []struct {
		name      string
		write     WriteResult
		repairErr error
	}{
		{
			name:      "pushed partial error",
			write:     WriteResult{Branch: proof.Branch, CommitSHA: proof.HeadSHA, Stage: WriteStagePushed, RemainingAction: RemainingActionEnsurePR},
			repairErr: repairFailure,
		},
		{
			name: "post pr partial error",
			write: WriteResult{
				Branch: proof.Branch, CommitSHA: proof.HeadSHA, PRNumber: 42, PRURL: "https://example.test/pull/42",
				Stage: WriteStagePRCreated, State: WriteStateNeedsAttention, RemainingAction: RemainingActionResolvePR, Note: "auto-merge unavailable",
			},
			repairErr: repairFailure,
		},
		{
			name:  "nil error without observed pr",
			write: WriteResult{Branch: proof.Branch, CommitSHA: proof.HeadSHA, Stage: WriteStagePushed, RemainingAction: RemainingActionEnsurePR},
		},
		{
			name: "nil error for different commit",
			write: WriteResult{
				Branch: proof.Branch, CommitSHA: strings.Repeat("b", 40), PRNumber: 42, PRURL: "https://example.test/pull/42",
				Stage: WriteStagePRCreated, State: WriteStateAlreadyOpen, RemainingAction: RemainingActionArmAutoMerge,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidateReader, _ := publicationDeclaredCandidate(nil, false)
			remote := &publicationRemoteFake{
				listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{proof}, Observed: 1, Exhaustive: true},
				repair:  test.write, repairErr: test.repairErr,
			}
			main := &publicationMainFake{
				commit: strings.Repeat("b", 40), lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
			}
			planning := &publicationPlanningFake{context: publicationPlanningContext(strings.Repeat("b", 40), "0.19.0")}
			service := mustPublicationService(t, main, planning, remote, &publicationEventBuilder{}, NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0"))
			result, publishErr := service.Publish(t.Context(), ContractPublicationRequest{
				System: "atlas", ContractID: "XC-atlas-demo", Selector: selector, Candidate: candidateReader, CandidateSource: source,
				ExpectPlan: record.PlanDigest, SubmitTemplate: SubmitRequest{Repo: host.Repo{Owner: "acme", Name: "getvisa"}},
			})
			if !errors.Is(publishErr, ErrOperationConflict) || result.Status == ContractPublicationRepaired ||
				!reflect.DeepEqual(result.Write, test.write) || result.Plan.PlanDigest != record.PlanDigest {
				t.Fatalf("result=%+v error=%v, want preserved partial %+v", result, publishErr, test.write)
			}
		})
	}
}

func TestContractPublicationBelowFloorAutoBumpRefusesBeforeFunnel(t *testing.T) {
	t.Parallel()

	reader, source := publicationLegacyCandidate(nil)
	main := &publicationMainFake{
		commit: strings.Repeat("a", 40), lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
	}
	remote := &publicationRemoteFake{listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true}}
	planning := &publicationPlanningFake{context: publicationPlanningContext(strings.Repeat("a", 40), "0.18.0")}
	events := &publicationEventBuilder{}
	service := mustPublicationService(t, main, planning, remote, events,
		NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0"))

	_, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major", Candidate: reader, CandidateSource: source,
	})
	if !errors.Is(err, ErrContractPublicationExplicitVersion) || events.calls != 0 {
		t.Fatalf("error=%v event calls=%d, want explicit-version-required before funnel", err, events.calls)
	}
}

func TestContractPublicationDeletePermitRequiresAcceptedPlanAndPriorDeclaration(t *testing.T) {
	t.Parallel()

	priorReader, _ := publicationDeclaredCandidate(nil, true)
	priorCore := priorReader.snapshot.CoreSnapshot()
	priorDescriptor, err := contract.ParseDescriptor(priorCore.Descriptor.Raw)
	if err != nil {
		t.Fatal(err)
	}
	priorSet, issues := contract.ValidateCarriedSet(contract.ProfileContractSetV2, priorDescriptor, priorCore)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	planningContext := publicationPlanningContext("", "0.19.0")
	modes := make(map[string]string, len(priorSet.PerFileDigest))
	for name := range priorSet.PerFileDigest {
		modes[name] = "100644"
		planningContext.PriorDeclaredSidecars[name] = MutationPrecondition{
			Digest: priorSet.PerFileDigest[name], Mode: "100644",
		}
	}
	planningContext.Published = []contract.PublishedContract{{
		Version: "0.0.0", DescriptorRaw: append([]byte(nil), priorCore.Descriptor.Raw...), Set: priorSet, Modes: modes,
	}}
	planning := &publicationPlanningFake{context: planningContext}
	reader, source := publicationDeclaredCandidate(nil, false)
	funnel := NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0")
	service := mustPreflightService(t, planning)
	service.funnel = funnel // test-only: exercise the post-plan opaque permit issuer.
	preflight, err := service.Preflight(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0", Candidate: reader, CandidateSource: source,
	})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(preflight.Plan.Mutations) == 0 {
		t.Fatal("plan has no explicit sidecar delete")
	}
	if _, err := service.publicationMutations(preflight.Plan, planningContext, ""); !errors.Is(err, ErrContractPublicationPlanChanged) {
		t.Fatalf("delete without expected plan error = %v", err)
	}
	mutations, err := service.publicationMutations(preflight.Plan, planningContext, preflight.Plan.PlanDigest)
	if err != nil {
		t.Fatalf("accepted delete mutations: %v", err)
	}
	for _, mutation := range mutations {
		if mutation.Operation == MutationWrite && (mutation.Path == "atlas/provides/demo/contract.md" || mutation.Path == "atlas/provides/demo/schema/order.schema.json") {
			if mutation.ExpectedBefore == nil || mutation.ExpectedBefore.Digest == "" || mutation.ExpectedBefore.Mode != "100644" {
				t.Fatalf("replacement lacks exact ExpectedBefore: %+v", mutation)
			}
		}
	}
	if err := funnel.validateMutationSet(preflight.Plan.OperationKey, mutations); err != nil {
		t.Fatalf("issuer rejected its bound delete: %v", err)
	}
	foreign := NewWriteFunnel(host.NewFakeHost(), &publicationSubmitValidator{}, "0.19.0")
	if err := foreign.validateMutationSet(preflight.Plan.OperationKey, mutations); !errors.Is(err, ErrMutationUnauthorized) {
		t.Fatalf("foreign funnel accepted copied permit: %v", err)
	}
}

func TestContractPublicationStagingOverlayUsesRetainedBytesAndRejectsContradictions(t *testing.T) {
	t.Parallel()

	mirror, _ := publicationDeclaredCandidate(nil, true)
	desired, _ := publicationDeclaredCandidate(nil, false)
	stagedFiles := []ContractSnapshotFile{desired.snapshot.file(contract.DescriptorPath), desired.snapshot.file("schema/order.schema.json")}
	stagedFiles[1].Raw = []byte(`{"type":"object","required":["id"]}`)
	for index := range stagedFiles {
		stagedFiles[index].Source = ContractCandidateSourceStaging
	}
	stagingSnapshot := ContractCandidateSnapshot{Source: ContractCandidateSourceStaging, Files: stagedFiles}
	stagingSnapshot.Fingerprint = fingerprintContractFiles(stagingSnapshot.Files)
	stagingSource := contract.CandidateSource{Kind: contract.CandidateSourceStaging, Location: "staging/contracts/demo", Fingerprint: stagingSnapshot.Fingerprint}
	staging := &publicationCandidateReader{snapshot: stagingSnapshot, source: stagingSource}
	overlay, err := NewContractPublicationStagingOverlayReader(mirror, staging, stagingSource.Location)
	if err != nil {
		t.Fatal(err)
	}
	got, err := overlay.ReadContractPublicationCandidate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.file("artifacts/notes.md").Path != "" || got.file("fixtures/valid/order.json").Path == "" ||
		!bytes.Equal(got.file("schema/order.schema.json").Raw, stagedFiles[1].Raw) {
		t.Fatalf("overlay did not apply staged-wins/retained-reuse/removal semantics: %+v", got.Files)
	}

	contradictory := clonePublicationCandidate(stagingSnapshot)
	removed := mirror.snapshot.file("artifacts/notes.md")
	removed.Source = ContractCandidateSourceStaging
	contradictory.Files = append(contradictory.Files, removed)
	contradictory.Fingerprint = fingerprintContractFiles(contradictory.Files)
	if _, err := overlayContractPublicationCandidate(mirror.snapshot, contradictory); !errors.Is(err, ErrContractPublicationInvalid) {
		t.Fatalf("staged+removed contradiction error = %v", err)
	}

	added, _ := publicationDeclaredCandidate(nil, true)
	additionWithoutBytes := ContractCandidateSnapshot{Source: ContractCandidateSourceStaging, Files: []ContractSnapshotFile{added.snapshot.file(contract.DescriptorPath)}}
	additionWithoutBytes.Files[0].Source = ContractCandidateSourceStaging
	additionWithoutBytes.Fingerprint = fingerprintContractFiles(additionWithoutBytes.Files)
	landed, _ := publicationDeclaredCandidate(nil, false)
	if _, err := overlayContractPublicationCandidate(landed.snapshot, additionWithoutBytes); !errors.Is(err, ErrContractPublicationInvalid) {
		t.Fatalf("addition without staged bytes error = %v", err)
	}
}

func publicationDeclaredCandidate(order *[]string, withExtra bool) (*publicationCandidateReader, contract.CandidateSource) {
	artifacts := `  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/order.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
`
	if withExtra {
		artifacts += "  - {path: artifacts/notes.md, role: changelog, normative: false, media_type: text/markdown}\n"
	}
	files := []ContractSnapshotFile{
		publicationSnapshotFile("contract.md", publicationDescriptor("envelope/v2", artifacts)),
		publicationSnapshotFile("schema/order.schema.json", []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)),
		publicationSnapshotFile("fixtures/valid/order.json", []byte(`{}`)),
		publicationSnapshotFile("fixtures/invalid/order.json", []byte(`[]`)),
	}
	if withExtra {
		files = append(files, publicationSnapshotFile("artifacts/notes.md", []byte("notes\n")))
	}
	snapshot := ContractCandidateSnapshot{Source: ContractCandidateSourceMirror, Files: files}
	snapshot.Fingerprint = fingerprintContractFiles(files)
	source := contract.CandidateSource{
		Kind: contract.CandidateSourceMirror, Location: strings.Repeat("a", 40), Fingerprint: snapshot.Fingerprint,
	}
	return &publicationCandidateReader{snapshot: snapshot, source: source, order: order}, source
}

func publicationLegacyCandidate(order *[]string) (*publicationCandidateReader, contract.CandidateSource) {
	snapshot := ContractCandidateSnapshot{
		Source: ContractCandidateSourceMirror,
		Files: []ContractSnapshotFile{
			publicationSnapshotFile("contract.md", publicationDescriptor("envelope/v1", "")),
			publicationSnapshotFile("schema/order.schema.json", []byte(`{"type":"object"}`)),
			publicationSnapshotFile("fixtures/valid/order.json", []byte(`{}`)),
			publicationSnapshotFile("fixtures/invalid/order.json", []byte(`[]`)),
		},
	}
	snapshot.Fingerprint = fingerprintContractFiles(snapshot.Files)
	source := contract.CandidateSource{
		Kind: contract.CandidateSourceMirror, Location: strings.Repeat("a", 40), Fingerprint: snapshot.Fingerprint,
	}
	return &publicationCandidateReader{snapshot: snapshot, source: source, order: order}, source
}

func mustPreflightService(t *testing.T, planning ContractPublicationPlanningReader) *ContractPublicationService {
	t.Helper()
	service, err := NewContractPreflightService(planning, publicationCompatibilityChecker{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func mustPublicationService(t *testing.T, main ContractPublicationMain, planning ContractPublicationPlanningReader, remote ContractPublicationRemoteProof, events ContractPublicationEventBuilder, funnel *WriteFunnel) *ContractPublicationService {
	t.Helper()
	service, err := NewContractPublicationService(main, planning, remote, publicationCompatibilityChecker{}, events, funnel)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func publicationDescriptor(schema, artifacts string) []byte {
	declared := ""
	if artifacts != "" {
		declared = "artifacts:\n" + artifacts
	}
	return []byte(fmt.Sprintf(`---
schema: %s
id: XC-atlas-demo
type: contract
title: Publication binding
version: 0.0.0
schema_format: json-schema-2020-12
compat_policy: default
%s---
# Publication binding
`, schema, declared))
}

func publicationSnapshotFile(name string, raw []byte) ContractSnapshotFile {
	return ContractSnapshotFile{Path: name, Kind: contract.CandidateRegular, Mode: "100644", Source: ContractCandidateSourceMirror, Raw: append([]byte(nil), raw...)}
}

func publicationPlanningContext(base, floor string) ContractPublicationPlanningContext {
	return ContractPublicationPlanningContext{
		AuthoringFloor: floor, ContractRoot: "atlas/provides/demo", BaseCommitSHA: base,
		Published: []contract.PublishedContract{}, PriorDeclaredSidecars: map[string]MutationPrecondition{},
		Warnings: []contract.Finding{}, Violations: []contract.Finding{},
	}
}

func publicationCompletionFromPlan(t *testing.T, plan contract.PublicationPlan, source contract.CandidateSource, selector string) ContractPublicationCompletion {
	t.Helper()
	files := make([]ContractSnapshotFile, 0, len(plan.PlannedBytes()))
	for name, raw := range plan.PlannedBytes() {
		files = append(files, publicationSnapshotFile(name, raw))
	}
	sortContractSnapshotFiles(files)
	event, err := (&publicationEventBuilder{}).BuildContractPublicationEvent(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return ContractPublicationCompletion{Snapshot: HistoricalSnapshot{
		ContractID: plan.Contract, Version: plan.TargetVersion, CommitSHA: strings.Repeat("a", 40),
		DescriptorPath: "atlas/provides/demo/contract.md", PublishEventPath: event.Path,
		PublishEventRaw: event.Content, EventSchema: plan.EventSchema, DigestProfile: plan.DigestProfile,
		PublishedDigest: plan.AggregateDigest, Files: files,
	}}
}

func publicationRecoveryRecord(candidateDigest, intentKey, operationKey, selector, target string) RecoveryV1 {
	branch := BranchName("atlas", "contract-publish", operationKey)
	return RecoveryV1{
		ArtifactIDs: []string{"XC-atlas-demo"}, BaseBranch: "main",
		CandidateIntentDigest: candidateDigest, Flags: RecoveryFlagsV1{}, HeadBranch: branch,
		IntentKey: intentKey, OperationKey: operationKey, PlanDigest: "sha256:" + strings.Repeat("d", 64),
		PRBody: "Publish exact contract", PRTitle: "Publish XC-atlas-demo@" + target,
		PreparedDigest: "sha256:" + strings.Repeat("e", 64), Repository: "acme/getvisa",
		System: "atlas", Target: "XC-atlas-demo@" + target, Verb: "contract-publish", Version: 1,
		VersionSelector: selector,
	}
}

func publicationRecoveryPlan(record RecoveryV1, source contract.CandidateSource) contract.PublicationPlan {
	contractID, target, _ := strings.Cut(record.Target, "@")
	return contract.PublicationPlan{
		Contract: contractID, TargetVersion: target, CandidateSource: source,
		CandidateIntentDigest: record.CandidateIntentDigest, IntentKey: record.IntentKey,
		OperationKey: record.OperationKey, PlanDigest: record.PlanDigest,
		VersionSelector: record.VersionSelector, HeadBranch: record.HeadBranch,
	}
}

func clonePublicationCandidate(input ContractCandidateSnapshot) ContractCandidateSnapshot {
	clone := input
	clone.Files = make([]ContractSnapshotFile, len(input.Files))
	for index := range input.Files {
		clone.Files[index] = input.Files[index]
		clone.Files[index].Raw = append([]byte(nil), input.Files[index].Raw...)
	}
	return clone
}

func sortContractSnapshotFiles(files []ContractSnapshotFile) {
	for left := 0; left < len(files); left++ {
		for right := left + 1; right < len(files); right++ {
			if files[right].Path < files[left].Path {
				files[left], files[right] = files[right], files[left]
			}
		}
	}
}

func appendPublicationOrder(order *[]string, value string) {
	if order != nil {
		*order = append(*order, value)
	}
}

// TestContractPublicationRefusesAViolationBeforeTheFunnel pins the decision the
// pure planner deliberately does not take.
//
// `internal/contract` returns findings and decides no policy, so a publication
// whose declared bump contradicts its own computed compatibility comes back as
// a VALID plan carrying a POL-007 violation — asserted by that package's own
// TestPlanPublicationMaintenanceBaselineAndCompatibility. Deciding is this
// layer's job. Until this refusal existed nothing took it: the violation rode
// along in the result, the publication reached the funnel, and the author got a
// zero exit and an open pull request whose merge gate then went red. Together
// with the planner's test this covers the whole chain: violation computed ->
// violation carried -> publication refused, naming the code and the path.
func TestContractPublicationRefusesAViolationBeforeTheFunnel(t *testing.T) {
	t.Parallel()

	reader, source := publicationDeclaredCandidate(nil, false)
	main := &publicationMainFake{
		commit: strings.Repeat("a", 40),
		lookup: ContractPublicationIntentLookup{Matches: []ContractPublicationCompletion{}, Exhaustive: true},
	}
	remote := &publicationRemoteFake{listing: ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Exhaustive: true}}
	planningContext := publicationPlanningContext(strings.Repeat("a", 40), "0.19.0")
	planningContext.Violations = []contract.Finding{{
		Code: "POL-007", Path: "fixtures/valid/demo.json",
		Message: "declared minor bump contradicts computed compatibility",
	}}
	planning := &publicationPlanningFake{context: planningContext}
	events := &publicationEventBuilder{}
	validator := &publicationSubmitValidator{}
	service := mustPublicationService(t, main, planning, remote, events,
		NewWriteFunnel(host.NewFakeHost(), validator, "0.19.0"))

	result, err := service.Publish(t.Context(), ContractPublicationRequest{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major",
		Candidate: reader, CandidateSource: source,
	})

	var refusal *ContractPublicationViolationError
	if !errors.As(err, &refusal) || !errors.Is(err, ErrContractPublicationInvalid) {
		t.Fatalf("error = %v, want a violation refusal classified as an invalid publication", err)
	}
	// The message alone has to be actionable: a bare count sends the author
	// back to a plan they cannot see from the failed command.
	if !strings.Contains(err.Error(), "POL-007") ||
		!strings.Contains(err.Error(), "fixtures/valid/demo.json") ||
		!strings.Contains(err.Error(), "declared minor bump contradicts computed compatibility") {
		t.Fatalf("refusal = %q, want the code, the path and the reason", err.Error())
	}
	if events.calls != 0 || validator.candidateCalls != 0 || validator.finalCalls != 0 {
		t.Fatalf("refusal reached the write path: events=%d candidate=%d final=%d",
			events.calls, validator.candidateCalls, validator.finalCalls)
	}
	// The plan is still returned, so a caller can render exactly what was
	// refused without re-planning.
	if len(result.Plan.Violations) != 1 || result.Plan.Violations[0].Code != "POL-007" {
		t.Fatalf("refused result carries plan violations = %+v", result.Plan.Violations)
	}
	if result.Status != "" {
		t.Fatalf("refused result status = %q, want no status", result.Status)
	}
}

// TestContractPublicationPlanningErrorNamesEveryIssue pins that a planner
// refusal is actionable from the message alone.
//
// It used to render "refused with 1 issue(s)" — no rule, no path, no detail —
// while carrying all three. A live release run failed on exactly that string
// and the cause could only be found by reading the planner's source.
func TestContractPublicationPlanningErrorNamesEveryIssue(t *testing.T) {
	t.Parallel()

	err := newContractPublicationPlanningError([]contract.Issue{
		{Kind: contract.IssueUndeclaredFile, Path: "schema/order.schema.json", Detail: "staged file is not declared by the descriptor"},
		{Kind: contract.IssueMissingFile, Path: "fixtures/valid/order.json", Detail: "declared entry has no bytes"},
	})
	message := err.Error()
	for _, want := range []string{
		"schema/order.schema.json", "staged file is not declared by the descriptor",
		"fixtures/valid/order.json", "declared entry has no bytes",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("planner refusal = %q, missing %q", message, want)
		}
	}
	if strings.Contains(message, "issue(s)") {
		t.Fatalf("planner refusal still reports a count instead of its issues: %q", message)
	}
	if !errors.Is(err, ErrContractPublicationInvalid) {
		t.Fatalf("planner refusal lost its classification: %v", err)
	}
}
