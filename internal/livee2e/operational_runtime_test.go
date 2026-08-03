package livee2e

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOperationalConfidenceCatalogueMatchesEvidenceContract(t *testing.T) {
	t.Parallel()

	catalogue := OperationalConfidenceCatalogue()
	if len(catalogue) != len(requiredAssertions) {
		t.Fatalf("operational catalogue has %d rows, evidence contract has %d", len(catalogue), len(requiredAssertions))
	}
	seen := make(map[string]bool, len(catalogue))
	for _, scenario := range catalogue {
		key := scenario.Name + "/" + scenario.Branch
		if seen[key] {
			t.Errorf("duplicate operational row %s", key)
		}
		seen[key] = true
		if scenario.Family != FamilyOperationalConfidence {
			t.Errorf("%s family=%q, want %q", key, scenario.Family, FamilyOperationalConfidence)
		}
		if !reflect.DeepEqual(scenario.Systems, []string{SystemA}) || !reflect.DeepEqual(scenario.Surfaces, []Surface{SurfaceCLI}) {
			t.Errorf("%s applicability=%v/%v, want A/cli", key, scenario.Systems, scenario.Surfaces)
		}
		if want, ok := requiredAssertions[key]; !ok {
			t.Errorf("%s is not an evidence-contract row", key)
		} else if !reflect.DeepEqual(scenario.Assertions, want) {
			t.Errorf("%s assertions=%v, want %v", key, scenario.Assertions, want)
		}
		wantOptional := operationalOptionalBranch(scenario.Name, scenario.Branch)
		if scenario.Optional != wantOptional {
			t.Errorf("%s optional=%t, want %t", key, scenario.Optional, wantOptional)
		}
	}
	for key := range requiredAssertions {
		if !seen[key] {
			t.Errorf("evidence-contract row %s is not dispatched", key)
		}
	}
}

func TestOperationalRowsKeepLEOC08BranchesDistinct(t *testing.T) {
	t.Parallel()

	run := NewRunFor("owner", "repo", OperationalConfidenceCatalogue())
	var branches []string
	for _, result := range run.Report().Results {
		if result.Scenario == "LE-OC-08" {
			branches = append(branches, result.Branch)
		}
	}
	want := []string{"baseline", "unsupported-provider"}
	if !reflect.DeepEqual(branches, want) {
		t.Fatalf("LE-OC-08 branches=%v, want %v", branches, want)
	}
}

func TestRecordAllowsUnverifiedOnlyOnExactOptionalBranches(t *testing.T) {
	t.Parallel()

	run := NewRunFor("owner", "repo", OperationalConfidenceCatalogue())
	mandatory := Result{Scenario: "LE-OC-01", Branch: "baseline", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictUnverified}
	if err := run.Record(mandatory); err == nil || !strings.Contains(err.Error(), "mandatory") {
		t.Fatalf("mandatory UNVERIFIED refusal=%v", err)
	}
	for _, optional := range []Result{
		{Scenario: "LE-OC-04", Branch: "provider-setting", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictUnverified},
		{Scenario: "LE-OC-08", Branch: "unsupported-provider", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictUnverified},
	} {
		if err := run.Record(optional); err != nil {
			t.Errorf("optional UNVERIFIED %s/%s: %v", optional.Scenario, optional.Branch, err)
		}
	}
}

func TestOperationalOmittedVerdictFailsClosed(t *testing.T) {
	t.Parallel()

	verdict, err := operationalRequireVerdict(VerdictNotRun, nil)
	if verdict != VerdictFail || err == nil || !strings.Contains(err.Error(), "omitted") {
		t.Fatalf("omitted verdict normalized to %s/%v, want fail with diagnostic", verdict, err)
	}
}

func TestOptionalUnverifiedRowsDoNotFailAnOtherwiseCompleteRelease(t *testing.T) {
	t.Parallel()

	scenarios := []Scenario{
		operationalScenario("LE-OC-01", "baseline", false, "receipt"),
		operationalScenario("LE-OC-04", "provider-setting", true, "same-pr-retry"),
		operationalScenario("LE-OC-08", "unsupported-provider", true, "unsupported-reported"),
	}
	run := NewRunFor("owner", "repo", scenarios)
	for _, result := range []Result{
		{Scenario: "LE-OC-01", Branch: "baseline", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass},
		{Scenario: "LE-OC-04", Branch: "provider-setting", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictUnverified},
		{Scenario: "LE-OC-08", Branch: "unsupported-provider", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictUnverified},
	} {
		if err := run.Record(result); err != nil {
			t.Fatalf("Record(%s/%s): %v", result.Scenario, result.Branch, err)
		}
	}
	if code := run.Report().ExitCode(); code != 0 {
		t.Fatalf("ExitCode=%d, want 0 for a complete run with only allowed optional UNVERIFIED rows", code)
	}
}

func TestDefaultEvidencePath(t *testing.T) {
	t.Parallel()

	temp := t.TempDir()
	checkLog := filepath.Join(temp, "candidate-check.log")
	first, err := DefaultEvidencePath(checkLog)
	if err != nil {
		t.Fatalf("first DefaultEvidencePath: %v", err)
	}
	second, err := DefaultEvidencePath(checkLog)
	if err != nil {
		t.Fatalf("second DefaultEvidencePath: %v", err)
	}
	if first == second || filepath.Base(first) != "manifest.json" || filepath.Base(second) != "manifest.json" {
		t.Fatalf("retained paths first=%q second=%q, want distinct run directories and manifest.json", first, second)
	}
}

func TestWriteEvidenceBundleProducesCandidateBoundCanonicalLogs(t *testing.T) {
	t.Parallel()

	temp := t.TempDir()
	verificationRoot := filepath.Join(temp, "verification")
	executionRoot := filepath.Join(temp, "execution")
	checkLog := filepath.Join(temp, "retained-check.log")
	sha := strings.Repeat("a", 40)
	tree := strings.Repeat("b", 40)
	if err := os.WriteFile(checkLog, candidateCheckLog(verificationRoot, sha, tree, OperationalReleaseTag, "0"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := NewRunFor("example-owner", "example-repo", OperationalConfidenceCatalogue())
	run.VerificationCandidate = CandidateAttestation{
		SourceRoot: verificationRoot, SourceSHA: sha, SourceTree: tree,
		SourceDetached: true, IndexClean: true, WorktreeClean: true, UntrackedClean: true,
		IntendedTag: OperationalReleaseTag, TemplateFloor: OperationalFloor, CheckLog: checkLog,
	}
	run.ExecutionCandidate = CandidateAttestation{
		SourceRoot: executionRoot, SourceSHA: sha, SourceTree: tree,
		SourceDetached: true, IndexClean: true, WorktreeClean: true, UntrackedClean: true,
		IntendedTag: OperationalReleaseTag, TemplateFloor: OperationalFloor,
		BinarySHA256: strings.Repeat("c", 64), BuildRevision: sha, BuildModified: false,
	}
	started := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for index, scenario := range OperationalConfidenceCatalogue() {
		verdict := VerdictPass
		assertionOutcome := "pass"
		limitations := []string{}
		if scenario.Optional {
			verdict = VerdictUnverified
			assertionOutcome = "unverified"
			limitations = []string{"The optional provider branch was unavailable in this deterministic writer test."}
		}
		assertions := make([]AssertionEvidence, 0, len(scenario.Assertions))
		for _, id := range scenario.Assertions {
			assertions = append(assertions, AssertionEvidence{ID: id, Outcome: assertionOutcome})
		}
		result := Result{
			Scenario: scenario.Name, Branch: scenario.Branch, System: SystemA, Surface: SurfaceCLI, Verdict: verdict,
			OperationalEvidence: &OperationalEvidence{
				StartedAt: started.Add(time.Duration(index) * time.Minute), EndedAt: started.Add(time.Duration(index)*time.Minute + time.Second),
				Assertions: assertions, SurfaceIDs: []string{"cli", "github"}, CommandFamily: "a2a live",
				ArtifactRefs: []string{}, PRRefs: []string{}, CleanupStatus: "complete", Limitations: limitations,
				LiveManifestFloor: OperationalFloor, LiveManifestCommit: strings.Repeat("d", 40),
			},
		}
		if err := run.Record(result); err != nil {
			t.Fatalf("Record(%s/%s): %v", scenario.Name, scenario.Branch, err)
		}
	}

	evidencePath := filepath.Join(temp, "bundle", "evidence.json")
	manifest, err := run.WriteEvidenceBundle(evidencePath)
	if err != nil {
		t.Fatalf("WriteEvidenceBundle: %v", err)
	}
	if len(manifest.Scenarios) != len(requiredAssertions) {
		t.Fatalf("manifest rows=%d, want %d", len(manifest.Scenarios), len(requiredAssertions))
	}
	validated, err := ValidateEvidenceFile(evidencePath)
	if err != nil {
		t.Fatalf("ValidateEvidenceFile: %v", err)
	}
	if !reflect.DeepEqual(validated, manifest) {
		t.Fatal("persisted evidence differs from the writer's returned manifest")
	}
	for _, row := range manifest.Scenarios {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(evidencePath), filepath.FromSlash(row.LogPath)))
		if err != nil {
			t.Fatalf("read %s: %v", row.LogPath, err)
		}
		var log scenarioLogEvidence
		if err := jsonUnmarshalClosed(raw, &log); err != nil {
			t.Fatalf("decode %s: %v", row.LogPath, err)
		}
		if log.CandidateSHA != run.ExecutionCandidate.SourceSHA || log.CandidateTree != run.ExecutionCandidate.SourceTree || log.BinarySHA256 != "sha256:"+run.ExecutionCandidate.BinarySHA256 {
			t.Errorf("%s is not bound to the exact candidate: %+v", row.LogPath, log)
		}
	}
	if _, err := run.WriteEvidenceBundle(evidencePath); err == nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("create-only rewrite refusal=%v, want os.ErrExist", err)
	}
	var retained []string
	for range 2 {
		path, err := DefaultEvidencePath(checkLog)
		if err != nil {
			t.Fatalf("reserve retained evidence run: %v", err)
		}
		if _, err := run.WriteEvidenceBundle(path); err != nil {
			t.Fatalf("write retained evidence run %s: %v", path, err)
		}
		retained = append(retained, path)
	}
	if retained[0] == retained[1] {
		t.Fatalf("two retained default runs reused %q", retained[0])
	}
}

func TestWriteEvidenceBundleRejectsSingleCheckoutReuse(t *testing.T) {
	t.Parallel()

	run := NewRunFor("example-owner", "example-repo", OperationalConfidenceCatalogue())
	shared := CandidateAttestation{
		SourceRoot: filepath.Join(t.TempDir(), "shared"), SourceSHA: strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40),
		SourceDetached: true, IndexClean: true, WorktreeClean: true, UntrackedClean: true,
		IntendedTag: OperationalReleaseTag, TemplateFloor: OperationalFloor, CheckLog: filepath.Join(t.TempDir(), "check.log"),
	}
	run.VerificationCandidate = shared
	run.ExecutionCandidate = shared
	if _, err := run.WriteEvidenceBundle(filepath.Join(t.TempDir(), "manifest.json")); err == nil || !strings.Contains(err.Error(), "roots must differ") {
		t.Fatalf("single-checkout reuse refusal=%v", err)
	}
}

func jsonUnmarshalClosed(raw []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}
