package contract

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

func TestPlanPublicationFloorAndFirstPublishMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		floor             string
		candidate         CandidateIntentSnapshot
		wantDescriptor    string
		wantEvent         string
		wantProfile       DigestProfile
		descriptorCovered bool
	}{
		{
			name:           "below floor",
			floor:          "0.18.9",
			candidate:      mustCandidateIntent(t, plannerLegacySnapshot("XC-atlas-demo", "0.0.0", "json-schema-2020-12")),
			wantDescriptor: "envelope/v1", wantEvent: "event/v1", wantProfile: ProfileContractTreeV1,
		},
		{
			name:           "at floor",
			floor:          "0.19.0",
			candidate:      mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "0.0.0", "json-schema-2020-12", nil)),
			wantDescriptor: "envelope/v2", wantEvent: "event/v2", wantProfile: ProfileContractSetV2,
			descriptorCovered: true,
		},
		{
			name:           "above floor",
			floor:          "2.0.0",
			candidate:      mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "0.0.0", "json-schema-2020-12", nil)),
			wantDescriptor: "envelope/v2", wantEvent: "event/v2", wantProfile: ProfileContractSetV2,
			descriptorCovered: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checker := &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}}
			plan, issues := PlanPublication(PublicationInput{
				System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0",
				AuthoringFloor: test.floor, Candidate: test.candidate,
				CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree:demo", Fingerprint: "sha256:" + strings.Repeat("1", 64)},
				ContractRoot:    "contracts/atlas/demo",
			}, checker)
			assertNoIssues(t, issues)
			if checker.calls != 0 {
				t.Fatalf("first publish called compatibility checker %d times", checker.calls)
			}
			if !plan.FirstPublish || plan.TargetVersion != "1.0.0" || plan.BaselineVersion != "" {
				t.Fatalf("first-publish version selection = %+v", plan)
			}
			if plan.DescriptorSchema != test.wantDescriptor || plan.EventSchema != test.wantEvent || plan.DigestProfile != test.wantProfile {
				t.Fatalf("floor selection = %s/%s/%s", plan.DescriptorSchema, plan.EventSchema, plan.DigestProfile)
			}
			if plan.Compatibility.Verdict != CompatibilityNotEvaluated || plan.Compatibility.Reason != "no published baseline" {
				t.Fatalf("first-publish compatibility = %+v", plan.Compatibility)
			}
			if plan.Gate != GateG1 {
				t.Fatalf("first-publish gate = %q, want %q", plan.Gate, GateG1)
			}
			descriptor := findPlanEntry(t, plan.Entries, DescriptorPath)
			if descriptor.IntegrityCovered != test.descriptorCovered {
				t.Fatalf("descriptor integrity_covered = %v, want %v", descriptor.IntegrityCovered, test.descriptorCovered)
			}
			if plan.Entries == nil || plan.Mutations == nil || plan.Warnings == nil || plan.Violations == nil {
				t.Fatal("plan collections must be non-nil")
			}
			for _, mutation := range plan.Mutations {
				if mutation.Action == MutationDelete {
					t.Fatal("first/legacy publish planned a delete")
				}
			}
		})
	}
}

func TestPlanPublicationStrictSemverBeforeBaselineOrChecker(t *testing.T) {
	t.Parallel()

	validCandidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "0.0.0", "json-schema-2020-12", nil))
	tests := []struct {
		name      string
		floor     string
		selector  string
		candidate CandidateIntentSnapshot
		published []PublishedContract
	}{
		{name: "invalid selector", floor: "0.19.0", selector: "explicit:01.2.3", candidate: validCandidate},
		{name: "overflowing explicit selector", floor: "0.19.0", selector: "explicit:999999999999999999999999999.2.3", candidate: validCandidate},
		{name: "short floor", floor: "0.19", selector: "explicit:1.0.0", candidate: validCandidate},
		{name: "prefixed floor", floor: "v0.19.0", selector: "explicit:1.0.0", candidate: validCandidate},
		{name: "leading-zero floor", floor: "00.19.0", selector: "explicit:1.0.0", candidate: validCandidate},
		{
			name: "noncanonical current descriptor", floor: "0.19.0", selector: "explicit:1.0.0",
			candidate: mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "01.0.0", "json-schema-2020-12", nil)),
		},
		{
			name: "noncanonical published version", floor: "0.19.0", selector: "explicit:1.0.0", candidate: validCandidate,
			published: []PublishedContract{{Version: "00.9.0"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checker := &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}}
			plan, issues := PlanPublication(PublicationInput{
				System: "atlas", ContractID: "XC-atlas-demo", Selector: test.selector,
				AuthoringFloor: test.floor, Candidate: test.candidate, Published: test.published,
				CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree:demo", Fingerprint: "sha256:" + strings.Repeat("1", 64)},
				ContractRoot:    "contracts/atlas/demo",
			}, checker)
			if len(issues) == 0 {
				t.Fatalf("invalid semver/selector produced plan %+v", plan)
			}
			if checker.calls != 0 {
				t.Fatalf("invalid semver/selector called checker %d times", checker.calls)
			}
			if plan.PlanDigest != "" {
				t.Fatalf("invalid input produced plan digest %q", plan.PlanDigest)
			}
		})
	}
}

func TestPlanPublicationMaintenanceBaselineAndCompatibility(t *testing.T) {
	t.Parallel()

	candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "2.0.0", "json-schema-2020-12", nil))
	oneOne := publishedDeclared(t, "1.1.0", plannerDeclaredSnapshot("XC-atlas-demo", "1.1.0", "json-schema-2020-12", nil))
	twoZero := publishedDeclared(t, "2.0.0", plannerDeclaredSnapshot("XC-atlas-demo", "2.0.0", "json-schema-2020-12", nil))
	checker := &recordingCompatibilityChecker{result: CompatibilityResult{
		Verdict:  CompatibilityCompatible,
		Failures: []CompatibilityFailure{},
	}}

	plan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.2.0",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		Published:       []PublishedContract{twoZero, oneOne},
		CandidateSource: CandidateSource{Kind: CandidateSourceStaging, Location: "staging/contracts/atlas/demo", Fingerprint: "sha256:" + strings.Repeat("2", 64)},
		ContractRoot:    "contracts/atlas/demo",
	}, checker)
	assertNoIssues(t, issues)
	if plan.FirstPublish || plan.BaselineVersion != "1.1.0" || plan.TargetVersion != "1.2.0" {
		t.Fatalf("maintenance plan selected %+v", plan)
	}
	if checker.calls != 1 || checker.input.PriorVersion != "1.1.0" || checker.input.NewVersion != "1.2.0" || checker.input.DeclaredBump != "minor" {
		t.Fatalf("compatibility input = %+v (calls %d)", checker.input, checker.calls)
	}
	if plan.Compatibility.Verdict != CompatibilityCompatible || plan.Gate != GateNone {
		t.Fatalf("maintenance compatibility/gate = %+v/%q", plan.Compatibility, plan.Gate)
	}

	breakingChecker := &recordingCompatibilityChecker{result: CompatibilityResult{
		Verdict:         CompatibilityBreaking,
		Reason:          "prior valid fixture no longer validates",
		Failures:        []CompatibilityFailure{{Path: "fixtures/valid/order.json", Schema: "schema/order.json", Reason: "required field removed"}},
		PolicyViolation: &Finding{Code: "POL-007", Path: "fixtures/valid/order.json", Message: "declared minor bump contradicts computed compatibility"},
	}}
	breakingPlan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.2.0",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		Published:       []PublishedContract{twoZero, oneOne},
		CandidateSource: CandidateSource{Kind: CandidateSourceStaging, Location: "staging/contracts/atlas/demo", Fingerprint: "sha256:" + strings.Repeat("2", 64)},
		ContractRoot:    "contracts/atlas/demo",
	}, breakingChecker)
	assertNoIssues(t, issues)
	if breakingPlan.Compatibility.Verdict != CompatibilityBreaking || breakingPlan.Gate != GateG2 {
		t.Fatalf("same-major breaking compatibility/gate = %+v/%q, want breaking/G2", breakingPlan.Compatibility, breakingPlan.Gate)
	}
	if len(breakingPlan.Violations) != 1 || breakingPlan.Violations[0].Code != "POL-007" {
		t.Fatalf("same-major breaking plan violations = %+v, want canonical POL-007 refusal", breakingPlan.Violations)
	}

	refusedChecker := &recordingCompatibilityChecker{result: CompatibilityResult{
		Verdict:         CompatibilityRefused,
		Reason:          "published baseline cannot be evaluated",
		PolicyViolation: &Finding{Code: "POL-008", Path: "fixtures/valid/order.json", Message: "computed compatibility could not evaluate the published baseline"},
	}}
	refusedPlan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.2.0",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		Published:       []PublishedContract{twoZero, oneOne},
		CandidateSource: CandidateSource{Kind: CandidateSourceStaging, Location: "staging/contracts/atlas/demo", Fingerprint: "sha256:" + strings.Repeat("2", 64)},
		ContractRoot:    "contracts/atlas/demo",
	}, refusedChecker)
	assertNoIssues(t, issues)
	if refusedPlan.Compatibility.Verdict != CompatibilityRefused || refusedPlan.Gate != GateNone ||
		len(refusedPlan.Violations) != 1 || refusedPlan.Violations[0].Code != "POL-008" {
		t.Fatalf("refused compatibility plan = compatibility %+v gate %q violations %+v", refusedPlan.Compatibility, refusedPlan.Gate, refusedPlan.Violations)
	}

	_, issues = PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.2.0",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		Published:       []PublishedContract{twoZero, oneOne},
		CandidateSource: CandidateSource{Kind: CandidateSourceStaging, Location: "staging/contracts/atlas/demo", Fingerprint: "sha256:" + strings.Repeat("2", 64)},
		ContractRoot:    "contracts/atlas/demo",
	}, &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityBreaking}})
	if len(issues) == 0 {
		t.Fatal("breaking compatibility without canonical policy violation produced a plan")
	}
	_, issues = PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.2.0",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		Published:       []PublishedContract{twoZero, oneOne},
		CandidateSource: CandidateSource{Kind: CandidateSourceStaging, Location: "staging/contracts/atlas/demo", Fingerprint: "sha256:" + strings.Repeat("2", 64)},
		ContractRoot:    "contracts/atlas/demo",
	}, &recordingCompatibilityChecker{result: CompatibilityResult{
		Verdict:         CompatibilityBreaking,
		PolicyViolation: &Finding{Code: "POL-008", Message: "wrong policy code"},
	}})
	if len(issues) == 0 {
		t.Fatal("breaking compatibility accepted the baseline-refusal code")
	}
}

func TestPlanPublicationAutomaticTargetMatrix(t *testing.T) {
	t.Parallel()

	baseline := publishedDeclared(t, "1.2.3", plannerDeclaredSnapshot("XC-atlas-demo", "1.2.3", "json-schema-2020-12", nil))
	tests := []struct {
		selector string
		want     string
		wantGate PublicationGate
	}{
		{selector: "auto:patch", want: "1.2.4", wantGate: GateNone},
		{selector: "auto:minor", want: "1.3.0", wantGate: GateNone},
		{selector: "auto:major", want: "2.0.0", wantGate: GateG2},
	}
	for _, test := range tests {
		t.Run(test.selector, func(t *testing.T) {
			t.Parallel()
			candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "1.2.3", "json-schema-2020-12", nil))
			plan, issues := PlanPublication(PublicationInput{
				System: "atlas", ContractID: "XC-atlas-demo", Selector: test.selector,
				AuthoringFloor: "0.19.0", Candidate: candidate, Published: []PublishedContract{baseline},
				CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree:demo", Fingerprint: "sha256:" + strings.Repeat("a", 64)},
				ContractRoot:    "contracts/atlas/demo",
			}, &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}})
			assertNoIssues(t, issues)
			if plan.TargetVersion != test.want || plan.BaselineVersion != "1.2.3" {
				t.Fatalf("%s selected target/baseline %s/%s, want %s/1.2.3", test.selector, plan.TargetVersion, plan.BaselineVersion, test.want)
			}
			if plan.Gate != test.wantGate {
				t.Fatalf("%s gate = %q, want %q", test.selector, plan.Gate, test.wantGate)
			}
		})
	}
}

func TestPlanPublicationUnsupportedFormatMakesZeroCheckerCalls(t *testing.T) {
	t.Parallel()

	candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "openapi-3.x", nil))
	baseline := publishedDeclared(t, "1.0.0", plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "openapi-3.x", nil))
	checker := &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}}
	plan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.1.0",
		AuthoringFloor: "0.19.0", Candidate: candidate, Published: []PublishedContract{baseline},
		CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree:demo", Fingerprint: "sha256:" + strings.Repeat("3", 64)},
		ContractRoot:    "contracts/atlas/demo",
	}, checker)
	assertNoIssues(t, issues)
	if checker.calls != 0 {
		t.Fatalf("unsupported format called checker %d times", checker.calls)
	}
	if plan.Compatibility.Verdict != CompatibilityNotEvaluated || !strings.Contains(plan.Compatibility.Reason, "openapi-3.x") {
		t.Fatalf("unsupported compatibility = %+v", plan.Compatibility)
	}
}

func TestPlanPublicationFinalizesDescriptorBeforeEveryDigest(t *testing.T) {
	t.Parallel()

	generated := &GeneratedFrom{Tool: "exporter/2", SourceDigest: "sha256:" + strings.Repeat("a", 64)}
	candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "json-schema-2020-12", generated))
	plan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:2.0.0",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree:demo", Fingerprint: "sha256:" + strings.Repeat("4", 64)},
		ContractRoot:    "contracts/atlas/demo",
	}, nil)
	assertNoIssues(t, issues)

	finalRaw := plan.FinalDescriptorBytes()
	if !bytes.Contains(finalRaw, []byte("version: 2.0.0")) || !bytes.Contains(finalRaw, []byte(plan.SourceDigest)) {
		t.Fatalf("final descriptor lacks finalized version/source digest:\n%s", finalRaw)
	}
	descriptor, err := ParseDescriptor(finalRaw)
	if err != nil {
		t.Fatal(err)
	}
	set, setIssues := ValidateCarriedSet(ProfileContractSetV2, descriptor, CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: finalRaw},
		Files:      candidate.Snapshot().Files,
	})
	assertNoIssues(t, setIssues)
	if set.AggregateDigest != plan.AggregateDigest || set.PerFileDigest[DescriptorPath] != findPlanEntry(t, plan.Entries, DescriptorPath).Digest {
		t.Fatalf("plan digests were not computed from final descriptor: plan=%s rebuilt=%s", plan.AggregateDigest, set.AggregateDigest)
	}
	projection, err := set.ExportSource()
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceDigestProfile != ProfileExportSourceV1 || plan.SourceDigest != projection.Digest || plan.SourceDigest == plan.AggregateDigest {
		t.Fatalf("source/event digest profiles collapsed: plan=%+v projection=%+v", plan, projection)
	}

	mutated := plan.FinalDescriptorBytes()
	mutated[0] = 'x'
	if plan.FinalDescriptorBytes()[0] != '-' {
		t.Fatal("returned final descriptor mutation changed frozen plan bytes")
	}
}

func TestPlanPublicationEmitsSortedWriteDeleteMetadataOnly(t *testing.T) {
	t.Parallel()

	priorSnapshot := plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "json-schema-2020-12", nil)
	priorSnapshot.Files = append(priorSnapshot.Files, regular("artifacts/changelog.md", "old notes\n"))
	priorSnapshot.Descriptor.Raw = bytes.Replace(priorSnapshot.Descriptor.Raw,
		[]byte("artifacts:\n"),
		[]byte("artifacts:\n  - {path: artifacts/changelog.md, role: changelog, normative: false, media_type: text/markdown}\n"), 1)
	prior := publishedDeclared(t, "1.0.0", priorSnapshot)

	desiredSnapshot := plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "json-schema-2020-12", nil)
	desiredSnapshot.Files[0].Raw = []byte(`{"type":"string"}`)
	desiredSnapshot.Files = append(desiredSnapshot.Files, regular("artifacts/errors.yaml", "ERR: failure\n"))
	desiredSnapshot.Descriptor.Raw = bytes.Replace(desiredSnapshot.Descriptor.Raw,
		[]byte("artifacts:\n"),
		[]byte("artifacts:\n  - {path: artifacts/errors.yaml, role: errors, normative: true, media_type: application/yaml}\n"), 1)
	desired := mustCandidateIntent(t, desiredSnapshot)

	plan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.1.0",
		AuthoringFloor: "0.19.0", Candidate: desired, Published: []PublishedContract{prior},
		MutationBaseline: prior,
		CandidateSource:  CandidateSource{Kind: CandidateSourceStaging, Location: "staging/demo", Fingerprint: "sha256:" + strings.Repeat("5", 64)},
		ContractRoot:     "contracts/atlas/demo",
	}, &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}})
	assertNoIssues(t, issues)

	want := []string{
		"delete:artifacts/changelog.md",
		"write:artifacts/errors.yaml",
		"write:contract.md",
		"write:schema/order.schema.json",
	}
	got := make([]string, len(plan.Mutations))
	for i, mutation := range plan.Mutations {
		got[i] = string(mutation.Action) + ":" + mutation.Path
		if mutation.Bytes != nil {
			t.Fatalf("public mutation leaked raw bytes: %+v", mutation)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("mutations = %v, want %v", got, want)
	}
	deletion := plan.Mutations[0]
	if deletion.BeforeDigest == "" || deletion.BeforeMode != "100644" || deletion.DeleteDomain != ContractSidecarDeleteDomain || deletion.ContractRoot != "contracts/atlas/demo" {
		t.Fatalf("delete metadata is incomplete: %+v", deletion)
	}
	if deletion.AfterDigest != "" || deletion.AfterSize != 0 {
		t.Fatalf("delete carries write metadata: %+v", deletion)
	}
	for _, mutation := range plan.Mutations {
		switch mutation.Path {
		case "artifacts/errors.yaml":
			if mutation.BeforeDigest != "" || mutation.BeforeMode != "" || mutation.AfterMode != "100644" {
				t.Fatalf("new write carries invalid tree metadata: %+v", mutation)
			}
		case DescriptorPath, "schema/order.schema.json":
			if mutation.BeforeDigest == "" || mutation.BeforeMode != "100644" || mutation.AfterMode != "100644" {
				t.Fatalf("replacement lacks exact prior tree metadata: %+v", mutation)
			}
		}
	}
}

func TestPlanPublicationPreservesExecutableModeAndRefusesUnknownPriorMode(t *testing.T) {
	t.Parallel()

	priorSnapshot := plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "json-schema-2020-12", nil)
	prior := publishedDeclared(t, "1.0.0", priorSnapshot)
	prior.Modes["schema/order.schema.json"] = "100755"
	desiredSnapshot := plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "json-schema-2020-12", nil)
	desiredSnapshot.Files[0].Raw = []byte(`{"type":"string"}`)
	desired := mustCandidateIntent(t, desiredSnapshot)
	input := PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.1.0", AuthoringFloor: "0.19.0",
		Candidate: desired, Published: []PublishedContract{prior}, ContractRoot: "contracts/atlas/demo",
		MutationBaseline: prior,
		CandidateSource:  CandidateSource{Kind: CandidateSourceMirror, Location: "tree", Fingerprint: "sha256:" + strings.Repeat("7", 64)},
	}
	plan, issues := PlanPublication(input, &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}})
	assertNoIssues(t, issues)
	mutation := findPublicationMutation(t, plan.Mutations, "schema/order.schema.json")
	if mutation.BeforeMode != "100755" || mutation.AfterMode != "100755" || mutation.BeforeDigest == "" {
		t.Fatalf("executable replacement metadata = %+v", mutation)
	}
	input.CandidateModes = map[string]string{
		DescriptorPath: "100644", "schema/order.schema.json": "100644",
		"fixtures/valid/order.json": "100644", "fixtures/invalid/missing-id.json": "100644",
	}
	plan, issues = PlanPublication(input, &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}})
	assertNoIssues(t, issues)
	mutation = findPublicationMutation(t, plan.Mutations, "schema/order.schema.json")
	if mutation.BeforeMode != "100755" || mutation.AfterMode != "100644" {
		t.Fatalf("intentional executable-to-regular transition = %+v", mutation)
	}

	prior.Modes["schema/order.schema.json"] = ""
	input.Published = []PublishedContract{prior}
	_, issues = PlanPublication(input, &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}})
	if len(issues) == 0 || issues[0].Path != "schema/order.schema.json" {
		t.Fatalf("unknown prior mode issues = %+v", issues)
	}
}

func TestPlanPublicationByteIdenticalRerunAndCanonicalGolden(t *testing.T) {
	t.Parallel()

	candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "0.0.0", "json-schema-2020-12", nil))
	input := PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "auto:major",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree:demo", Fingerprint: "sha256:" + strings.Repeat("6", 64)},
		ContractRoot:    "contracts/atlas/demo",
		Warnings:        []Finding{}, Violations: []Finding{},
	}
	first, firstIssues := PlanPublication(input, nil)
	second, secondIssues := PlanPublication(input, nil)
	assertNoIssues(t, firstIssues)
	assertNoIssues(t, secondIssues)
	if !first.Equal(second) || first.PlanDigest != second.PlanDigest || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) || !bytes.Equal(first.FinalDescriptorBytes(), second.FinalDescriptorBytes()) {
		t.Fatalf("byte-identical rerun diverged:\n%+v\n%+v", first, second)
	}
	const wantPlanDigest = "sha256:3d80eb79ee84ab1aa23160152842bdd3b199ec46a0a4192ed51edf79cd1c94e3"
	if first.PlanDigest != wantPlanDigest {
		t.Fatalf("plan digest = %q, want golden %q\ncanonical=%s", first.PlanDigest, wantPlanDigest, first.CanonicalBytes())
	}
	frozenWire, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}

	input.Warnings = append(input.Warnings, Finding{Code: "W", Message: "caller mutation"})
	first.Entries[0].Path = "caller-mutated"
	first.Mutations[0].AfterDigest = "caller-mutated"
	first.Warnings = append(first.Warnings, Finding{Code: "W2"})
	mutatedWire, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frozenWire, mutatedWire) {
		t.Fatal("public projection mutation changed frozen plan JSON")
	}
	if second.Entries[0].Path == "caller-mutated" || second.Mutations[0].AfterDigest == "caller-mutated" || len(second.Warnings) != 0 {
		t.Fatal("plan/input mutation leaked into an independent rerun")
	}
	if equalByteMaps(first.PlannedBytes(), second.PlannedBytes()) {
		// They should still be equal: public projection mutation must not reach
		// the private immutable bytes used by publish.
	} else {
		t.Fatal("public plan mutation changed frozen planned bytes")
	}
}

func equalByteMaps(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for path, raw := range left {
		if !bytes.Equal(raw, right[path]) {
			return false
		}
	}
	return true
}

type recordingCompatibilityChecker struct {
	calls  int
	input  CompatibilityCheckInput
	result CompatibilityResult
}

func (c *recordingCompatibilityChecker) CheckCompatibility(input CompatibilityCheckInput) CompatibilityResult {
	c.calls++
	c.input = cloneCompatibilityCheckInput(input)
	return c.result
}

type inertInstanceChecker struct{}

func (inertInstanceChecker) CheckInstance(InstanceCheckInput) InstanceCheckResult {
	return InstanceCheckResult{Violations: []InstanceViolation{}}
}

var _ CompatibilityChecker = (*recordingCompatibilityChecker)(nil)
var _ InstanceChecker = inertInstanceChecker{}

func mustCandidateIntent(t *testing.T, snapshot CandidateSnapshot) CandidateIntentSnapshot {
	t.Helper()
	intent, issues := BuildCandidateIntent(snapshot)
	assertNoIssues(t, issues)
	return intent
}

func plannerDeclaredSnapshot(id, version, schemaFormat string, generated *GeneratedFrom) CandidateSnapshot {
	generatedYAML := ""
	if generated != nil {
		generatedYAML = "generated_from:\n  tool: " + generated.Tool + "\n  source_digest: " + generated.SourceDigest + "\n"
	}
	raw := declaredIntentDescriptor(`schema: envelope/v2
id: `+id+`
type: contract
title: Planner contract
version: `+version+`
schema_format: `+schemaFormat+`
compat_policy: default
`+generatedYAML+`artifacts:
  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/missing-id.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
`, "# Planner contract\n")
	return CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: raw},
		Files:      cloneCandidates(validCandidates()[:3]),
	}
}

func plannerLegacySnapshot(id, version, schemaFormat string) CandidateSnapshot {
	raw := declaredIntentDescriptor(`schema: envelope/v1
id: `+id+`
type: contract
title: Planner contract
version: `+version+`
schema_format: `+schemaFormat+`
compat_policy: default
`, "# Planner contract\n")
	return CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: raw},
		Files:      cloneCandidates(validCandidates()[:3]),
	}
}

func publishedDeclared(t *testing.T, version string, snapshot CandidateSnapshot) PublishedContract {
	t.Helper()
	descriptor, err := ParseDescriptor(snapshot.Descriptor.Raw)
	if err != nil {
		t.Fatal(err)
	}
	set, issues := ValidateCarriedSet(ProfileContractSetV2, descriptor, snapshot)
	assertNoIssues(t, issues)
	modes := map[string]string{DescriptorPath: "100644"}
	for path := range set.Bytes {
		modes[path] = "100644"
	}
	return PublishedContract{Version: version, DescriptorRaw: bytes.Clone(snapshot.Descriptor.Raw), Set: set, Modes: modes}
}

func findPublicationMutation(t *testing.T, mutations []PublicationMutation, path string) PublicationMutation {
	t.Helper()
	for _, mutation := range mutations {
		if mutation.Path == path {
			return mutation
		}
	}
	t.Fatalf("plan has no mutation %q: %+v", path, mutations)
	return PublicationMutation{}
}

func findPlanEntry(t *testing.T, entries []PublicationEntry, path string) PublicationEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Path == path {
			return entry
		}
	}
	t.Fatalf("plan has no entry %q: %+v", path, entries)
	return PublicationEntry{}
}

func cloneCompatibilityCheckInput(input CompatibilityCheckInput) CompatibilityCheckInput {
	clone := input
	clone.NewEntries = slices.Clone(input.NewEntries)
	clone.BaselineEntries = slices.Clone(input.BaselineEntries)
	clone.NewBytes = cloneByteMap(input.NewBytes)
	clone.BaselineBytes = cloneByteMap(input.BaselineBytes)
	return clone
}

func cloneByteMap(input map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(input))
	for path, raw := range input {
		clone[path] = bytes.Clone(raw)
	}
	return clone
}

func TestPlanPublicationSourceDigestIsCanonicalArtifactDigest(t *testing.T) {
	t.Parallel()

	candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "0.0.0", "json-schema-2020-12", &GeneratedFrom{
		Tool: "exporter", SourceDigest: artifact.Digest([]byte("old")),
	}))
	plan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0", AuthoringFloor: "0.19.0",
		Candidate: candidate, CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree", Fingerprint: artifact.Digest([]byte("tree"))},
		ContractRoot: "contracts/atlas/demo",
	}, nil)
	assertNoIssues(t, issues)
	if !sha256DigestPattern.MatchString(plan.SourceDigest) {
		t.Fatalf("source digest is not canonical: %q", plan.SourceDigest)
	}
}

func TestPlanPublicationRejectsMismatchedSourceDigestAssertion(t *testing.T) {
	t.Parallel()

	candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "0.0.0", "json-schema-2020-12", &GeneratedFrom{
		Tool: "exporter", SourceDigest: artifact.Digest([]byte("old")),
	}))
	plan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.0.0", AuthoringFloor: "0.19.0",
		Candidate: candidate, CandidateSource: CandidateSource{Kind: CandidateSourceMirror, Location: "tree", Fingerprint: artifact.Digest([]byte("tree"))},
		ContractRoot: "contracts/atlas/demo", SourceDigestAssertion: artifact.Digest([]byte("wrong assertion")),
	}, nil)
	if plan.PlanDigest != "" {
		t.Fatalf("mismatched source assertion produced plan %q", plan.PlanDigest)
	}
	assertIssue(t, issues, IssueDigestMismatch)
}

// TestPlanPublicationOverwritesTheTreeOnMainNotTheCompatibilityBaseline pins
// the two questions apart. "Which published version do I check compatibility
// against" and "whose bytes am I about to overwrite" used to share one answer,
// derived here as the semver-highest published version unless one matched the
// candidate's own current version.
//
// Live run 2026-08-05 found the shape where the two diverge: 1.0.0, 1.1.0 and
// 2.0.0 published in that order, so main carries 2.0.0's tree, and a
// maintenance 1.2.0 prepared from a candidate materialized at 1.1.0. The old
// derivation picked 1.1.0's snapshot, so the write set was computed against
// bytes that are not on main and the precondition named a digest main does not
// have. The funnel refused it every time — a maintenance release on an older
// line was impossible once a newer major existed.
//
// The baseline is now supplied by the space, which is the only layer that can
// see main. The compatibility baseline stays 1.1.0, and this test asserts both
// so the pair cannot silently collapse back into one value.
func TestPlanPublicationOverwritesTheTreeOnMainNotTheCompatibilityBaseline(t *testing.T) {
	t.Parallel()

	candidate := mustCandidateIntent(t, plannerDeclaredSnapshot("XC-atlas-demo", "1.1.0", "json-schema-2020-12", nil))
	oneOne := publishedDeclared(t, "1.1.0", plannerDeclaredSnapshot("XC-atlas-demo", "1.1.0", "json-schema-2020-12", nil))
	twoZero := publishedDeclared(t, "2.0.0", plannerDeclaredSnapshot("XC-atlas-demo", "2.0.0", "json-schema-2020-12", nil))

	plan, issues := PlanPublication(PublicationInput{
		System: "atlas", ContractID: "XC-atlas-demo", Selector: "explicit:1.2.0",
		AuthoringFloor: "0.19.0", Candidate: candidate,
		Published:        []PublishedContract{oneOne, twoZero},
		MutationBaseline: twoZero,
		CandidateSource:  CandidateSource{Kind: CandidateSourceStaging, Location: "staging/contracts/atlas/demo", Fingerprint: "sha256:" + strings.Repeat("3", 64)},
		ContractRoot:     "contracts/atlas/demo",
	}, &recordingCompatibilityChecker{result: CompatibilityResult{Verdict: CompatibilityCompatible}})
	assertNoIssues(t, issues)

	if plan.BaselineVersion != "1.1.0" {
		t.Fatalf("compatibility baseline = %q, want the highest published version BELOW the target", plan.BaselineVersion)
	}

	wantBefore := artifact.Digest(twoZero.DescriptorRaw)
	notWant := artifact.Digest(oneOne.DescriptorRaw)
	if wantBefore == notWant {
		t.Fatal("fixture is degenerate: the two prior descriptors must differ for this test to mean anything")
	}
	found := false
	for _, mutation := range plan.Mutations {
		if mutation.Path != DescriptorPath {
			continue
		}
		found = true
		if mutation.BeforeDigest == notWant {
			t.Fatalf("descriptor precondition names the COMPATIBILITY baseline (1.1.0); main carries 2.0.0's tree, so the funnel would refuse this write")
		}
		if mutation.BeforeDigest != wantBefore {
			t.Fatalf("descriptor precondition = %q, want main's current descriptor %q", mutation.BeforeDigest, wantBefore)
		}
	}
	if !found {
		t.Fatal("plan carries no descriptor mutation")
	}
}
