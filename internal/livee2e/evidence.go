package livee2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// EvidenceManifestVersion identifies the closed publishable evidence format.
	EvidenceManifestVersion = "live-e2e-evidence/v1"
	// ScenarioLogVersion identifies the canonical per-scenario evidence format.
	ScenarioLogVersion = "live-e2e-scenario-log/v1"
	// OperationalFloor is the exact first-party write floor proven by P7.
	OperationalFloor = requiredCandidateFloor
	// OperationalReleaseTag is the immutable release tag expected in candidate evidence.
	OperationalReleaseTag     = "v" + OperationalFloor
	maxEvidenceBytes          = 1 << 20
	maxCandidateCheckLogBytes = 16 << 20
	maxScenarioLogBytes       = 1 << 20
)

// EvidenceManifest binds a candidate and its scenario evidence for publication.
type EvidenceManifest struct {
	ManifestVersion string             `json:"manifest_version"`
	Candidate       CandidateEvidence  `json:"candidate"`
	CandidateCheck  CandidateCheck     `json:"candidate_check"`
	Scenarios       []ScenarioEvidence `json:"scenarios"`
}

// CandidateEvidence identifies the source and binary exercised by a run.
type CandidateEvidence struct {
	SHA                   string           `json:"sha"`
	Tree                  string           `json:"tree"`
	Version               string           `json:"version"`
	VerificationCheckout  CheckoutEvidence `json:"verification_checkout"`
	ExecutionCheckout     CheckoutEvidence `json:"execution_checkout"`
	BuiltSourceSHA        string           `json:"built_source_sha"`
	Binary                BinaryEvidence   `json:"binary"`
	EmbeddedTemplateFloor string           `json:"embedded_template_floor"`
}

// CheckoutEvidence records a checked source checkout's immutable identity.
type CheckoutEvidence struct {
	ID             string `json:"id"`
	SHA            string `json:"sha"`
	Tree           string `json:"tree"`
	Detached       *bool  `json:"detached"`
	IndexClean     *bool  `json:"index_clean"`
	WorktreeClean  *bool  `json:"worktree_clean"`
	UntrackedClean *bool  `json:"untracked_clean"`
}

// BinaryEvidence records the hash and build metadata for the executed binary.
type BinaryEvidence struct {
	SHA256    string          `json:"sha256"`
	Version   string          `json:"version"`
	BuildInfo BinaryBuildInfo `json:"build_info"`
}

// BinaryBuildInfo is the subset of Go build information required for attestation.
type BinaryBuildInfo struct {
	Revision string `json:"revision"`
	Modified *bool  `json:"modified"`
}

// CandidateCheck binds the retained candidate quality-gate log.
type CandidateCheck struct {
	LogPath   string `json:"log_path"`
	LogSHA256 string `json:"log_sha256"`
	Passed    *bool  `json:"passed"`
}

// ScenarioEvidence records one bounded, provider-safe scenario outcome.
type ScenarioEvidence struct {
	ScenarioID         string              `json:"scenario_id"`
	Branch             string              `json:"branch"`
	StartedAt          string              `json:"started_at"`
	EndedAt            string              `json:"ended_at"`
	SurfaceIDs         []string            `json:"surface_ids"`
	RepositoryID       string              `json:"repository_id"`
	CommandFamily      string              `json:"command_family"`
	Outcome            string              `json:"outcome"`
	ArtifactRefs       []string            `json:"artifact_refs"`
	PRRefs             []string            `json:"pr_refs"`
	CleanupStatus      string              `json:"cleanup_status"`
	LogPath            string              `json:"log_path"`
	LogSHA256          string              `json:"log_sha256"`
	Limitations        []string            `json:"limitations"`
	LiveManifestFloor  string              `json:"live_manifest_floor"`
	LiveManifestCommit string              `json:"live_manifest_commit"`
	Assertions         []AssertionEvidence `json:"assertions"`
}

// AssertionEvidence records the verdict for one scenario assertion.
type AssertionEvidence struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
}

// scenarioLogEvidence is the canonical, bounded machine record persisted at
// each scenario's log_path. It intentionally contains no provider payloads:
// it binds the assertion verdicts to the exact candidate and scenario run
// while the publishable manifest carries only safe references.
type scenarioLogEvidence struct {
	EvidenceVersion string              `json:"evidence_version"`
	CandidateSHA    string              `json:"candidate_sha"`
	CandidateTree   string              `json:"candidate_tree"`
	BinarySHA256    string              `json:"binary_sha256"`
	ScenarioID      string              `json:"scenario_id"`
	Branch          string              `json:"branch"`
	StartedAt       string              `json:"started_at"`
	EndedAt         string              `json:"ended_at"`
	Outcome         string              `json:"outcome"`
	Assertions      []AssertionEvidence `json:"assertions"`
}

// EvidenceValidationError groups all validation defects in an evidence manifest.
type EvidenceValidationError struct {
	Issues []string
}

func (e *EvidenceValidationError) Error() string {
	return "invalid live evidence: " + strings.Join(e.Issues, "; ")
}

var (
	gitObjectPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	safeIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	repositoryPattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commandFamilyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*( [a-z0-9][a-z0-9-]*){0,4}$`)
	secretPatterns       = []*regexp.Regexp{
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`(?i)bearer[[:space:]]+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`://[^/@[:space:]]+:[^/@[:space:]]+@`),
		regexp.MustCompile(`(?i)[?&](access_token|token|password|secret)=[^&[:space:]]+`),
	}
)

var requiredAssertions = map[string][]string{
	"LE-OC-01/baseline":             {"receipt", "provenance", "fresh-fold", "no-mismatch-flag"},
	"LE-OC-02/baseline":             {"announcement-v2", "durable-checkpoints", "cross-clone-projection", "local-heartbeat-local-only"},
	"LE-OC-03/baseline":             {"snapshot-revision-equal", "operational-rows-equal", "sse-revision-only", "conditional-snapshot"},
	"LE-OC-04/provider-setting":     {"same-pr-retry"},
	"LE-OC-05/baseline":             {"visibility-public", "visibility-higher-classification"},
	"LE-OC-06/baseline":             {"preflight-write-free", "contract-v2", "event-v2", "publication-intent", "contract-set-v2", "plan-publish-equal"},
	"LE-OC-07/baseline":             {"historical-bytes-equal", "historical-digest-equal", "idempotent-rerun", "divergent-refusal"},
	"LE-OC-08/baseline":             {"self-suite", "known-valid-pass", "known-invalid-stable-locations"},
	"LE-OC-08/unsupported-provider": {"unsupported-reported"},
}

// liveRequiredRows is spec 09 D-7's own release-contract rule, recomputed
// from the catalogue at validation time rather than hand-copied: a row's
// evidence-bundle entry must carry a genuine LIVE pass exactly when GitHub
// itself must decide part of its verdict — every TierProvider row, and every
// TierLogic row carrying a ProviderAssertions carve-out (plan D-2's
// "progression is not judgement": LE-OC-03/baseline's "sse-revision-only"
// and LE-OC-05/baseline's "visibility-public" are each GitHub's own fact
// inside an otherwise-logic row). Every OTHER operational-confidence row —
// LE-OC-01/02/06/07/08-baseline — is logic-tier territory: this function no
// longer names it as bundle-required at all, because its truth is proven
// elsewhere now (logicTierRowKeys below, checked against the candidate check
// transcript's own marker), not asserted here. A row that appears anyway
// (belt-and-suspenders, per tier.go's own TierLogic doc) is still validated
// structurally by validateScenario; it is simply no longer load-bearing for
// release.
//
// Scoped to FamilyOperationalConfidence because that is the only family the
// evidence bundle's own ScenarioEvidence.ScenarioID contract accepts
// (validateScenario checks membership in requiredAssertions, whose keys are
// exactly the operational-confidence rows) — every other Catalogue() family
// is a different release gate's concern, not this manifest's.
func liveRequiredRows(scenarios []Scenario) map[string]Scenario {
	rows := make(map[string]Scenario)
	for _, s := range scenarios {
		if s.Family != FamilyOperationalConfidence {
			continue
		}
		if s.Tier == TierProvider || len(s.ProviderAssertions) > 0 {
			rows[scenarioRowKey(s)] = s
		}
	}
	return rows
}

// logicTierRowKeys is the exact set the logic-tier marker
// (LOGIC_TIER_ROWS_SHA256, candidate.go's candidateCheckAttestation) must
// claim: every row across the WHOLE catalogue that TierLogic may judge —
// not only the operational-confidence rows the evidence bundle itself
// carries. LE-OC-03/baseline and LE-OC-05/baseline belong here too: their
// ProviderAssertions carve-out narrows which ASSERTION needs a live pass,
// not which tier judges the row's own verdict — report.go's own
// VerdictSkippedProvider guard refuses that verdict on a TierLogic row for
// exactly this reason. TestLogicMatrix drives the full catalogue (spec 09
// §5a), so a row silently dropped from its own dispatch table — the spec 46
// postmortem defect this marker exists to catch — is caught only by
// comparing against this FULL set, never a narrower one.
func logicTierRowKeys(scenarios []Scenario) []string {
	keys := make([]string, 0, len(scenarios))
	for _, s := range scenarios {
		if s.Tier == TierLogic {
			keys = append(keys, scenarioRowKey(s))
		}
	}
	sort.Strings(keys)
	return keys
}

// logicTierRowsDigest canonicalizes a row-key set into the exact value the
// LOGIC_TIER_ROWS_SHA256 marker carries: the sha256 of the sorted keys, one
// per line, with a trailing newline. Both the logic lane — which must
// compute it from the rows it actually drove to a passing verdict this run,
// never from a static Catalogue() read, or the marker would prove nothing —
// and verifyCandidateCheckLog below — which recomputes it from Catalogue()
// itself, independent of any run — call this exact function, so the two can
// only disagree over an actual coverage gap, never over formatting.
func logicTierRowsDigest(keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	joined := strings.Join(sorted, "\n")
	if joined != "" {
		joined += "\n"
	}
	return runtimeDigest([]byte(joined))
}

// DecodeEvidenceManifest strictly decodes and validates a persisted P7 live
// evidence manifest. The caller owns the input-size bound.
func DecodeEvidenceManifest(raw []byte) (EvidenceManifest, error) {
	var generic any
	genericDecoder := json.NewDecoder(bytes.NewReader(raw))
	if err := genericDecoder.Decode(&generic); err != nil {
		return EvidenceManifest{}, fmt.Errorf("decode JSON: %w", err)
	}
	if err := requireJSONEOF(genericDecoder); err != nil {
		return EvidenceManifest{}, fmt.Errorf("decode JSON: %w", err)
	}
	var safetyIssues []string
	scanSafeEvidence(generic, "$", &safetyIssues)
	if len(safetyIssues) != 0 {
		sort.Strings(safetyIssues)
		return EvidenceManifest{}, &EvidenceValidationError{Issues: safetyIssues}
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest EvidenceManifest
	if err := decoder.Decode(&manifest); err != nil {
		return EvidenceManifest{}, fmt.Errorf("decode closed manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return EvidenceManifest{}, fmt.Errorf("decode closed manifest: %w", err)
	}
	if err := ValidateEvidenceManifest(manifest); err != nil {
		return EvidenceManifest{}, err
	}
	return manifest, nil
}

// ValidateEvidenceFile validates the closed manifest and its SHA-bound
// candidate make-check log and every canonical scenario log. Log paths are always resolved beneath the
// evidence file's directory; neither absolute paths nor symlink escapes are
// accepted.
func ValidateEvidenceFile(evidencePath string) (EvidenceManifest, error) {
	raw, err := readBoundedRuntimeFile(evidencePath, maxEvidenceBytes)
	if err != nil {
		return EvidenceManifest{}, fmt.Errorf("read evidence file: %w", err)
	}
	manifest, err := DecodeEvidenceManifest(raw)
	if err != nil {
		return EvidenceManifest{}, err
	}
	if err := verifyCandidateCheckLog(filepath.Dir(evidencePath), manifest.CandidateCheck, manifest.Candidate); err != nil {
		return EvidenceManifest{}, err
	}
	for index, scenario := range manifest.Scenarios {
		if err := verifyScenarioLog(filepath.Dir(evidencePath), manifest.Candidate, scenario); err != nil {
			return EvidenceManifest{}, fmt.Errorf("scenarios[%d]: %w", index, err)
		}
	}
	return manifest, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read trailing content: %w", err)
	}
	return fmt.Errorf("trailing JSON value")
}

func verifyCandidateCheckLog(evidenceDir string, check CandidateCheck, candidate CandidateEvidence) error {
	raw, got, err := readEvidenceLog(evidenceDir, "candidate_check.log_path", check.LogPath, maxCandidateCheckLogBytes)
	if err != nil {
		return err
	}
	if got != check.LogSHA256 {
		return fmt.Errorf("candidate_check.log_sha256 does not match candidate_check.log_path content")
	}
	attestation, err := candidateCheckAttestation(raw, check.LogPath, CandidateExpectation{
		SHA: candidate.SHA, Tree: candidate.Tree, Tag: candidate.Version, Floor: candidate.EmbeddedTemplateFloor,
	})
	if err != nil {
		return fmt.Errorf("candidate_check.log_path does not attest the exact verification checkout: %w", err)
	}
	if checkoutEvidenceID(attestation.SourceRoot) != candidate.VerificationCheckout.ID {
		return fmt.Errorf("candidate_check.log_path CHECKOUT_ROOT does not bind candidate.verification_checkout.id")
	}
	// spec 09 D-7: recompute, never trust. The logic lane's own claim (the
	// transcript's LOGIC_TIER_ROWS_SHA256 marker) is checked against the
	// catalogue's CURRENT logic-row set, not against whatever the marker
	// happened to say — a row silently dropped from the logic lane's
	// dispatch table changes this digest and reds the release gate, exactly
	// the property the whole marker exists to buy.
	wantLogicKeys := logicTierRowKeys(Catalogue())
	wantLogicDigest := logicTierRowsDigest(wantLogicKeys)
	if attestation.LogicTierRowsSHA256 != wantLogicDigest {
		return fmt.Errorf(
			"candidate_check.log_path LOGIC_TIER_ROWS_SHA256=%s does not match the catalogue's own logic-tier row set (want %s, covering: %s)",
			attestation.LogicTierRowsSHA256, wantLogicDigest, strings.Join(wantLogicKeys, ", "),
		)
	}
	return nil
}

func verifyScenarioLog(evidenceDir string, candidate CandidateEvidence, scenario ScenarioEvidence) error {
	raw, got, err := readEvidenceLog(evidenceDir, "log_path", scenario.LogPath, maxScenarioLogBytes)
	if err != nil {
		return err
	}
	if got != scenario.LogSHA256 {
		return fmt.Errorf("log_sha256 does not match log_path content")
	}

	want := scenarioLogEvidence{
		EvidenceVersion: ScenarioLogVersion,
		CandidateSHA:    candidate.SHA,
		CandidateTree:   candidate.Tree,
		BinarySHA256:    candidate.Binary.SHA256,
		ScenarioID:      scenario.ScenarioID,
		Branch:          scenario.Branch,
		StartedAt:       scenario.StartedAt,
		EndedAt:         scenario.EndedAt,
		Outcome:         scenario.Outcome,
		Assertions:      scenario.Assertions,
	}
	wantRaw, err := json.Marshal(want)
	if err != nil {
		return fmt.Errorf("marshal canonical scenario log: %w", err)
	}
	wantRaw = append(wantRaw, '\n')
	if !bytes.Equal(raw, wantRaw) {
		return fmt.Errorf("log_path content is not the canonical candidate-bound scenario record")
	}
	return nil
}

func readEvidenceLog(evidenceDir, field, logPath string, limit int64) ([]byte, string, error) {
	base, err := filepath.EvalSymlinks(evidenceDir)
	if err != nil {
		return nil, "", fmt.Errorf("%s resolve evidence directory: %w", field, err)
	}
	joined := filepath.Join(base, filepath.FromSlash(logPath))
	// #nosec G703 -- safeRelativePath validated logPath and joined is checked beneath the resolved evidence root below.
	directInfo, err := os.Lstat(joined)
	if err != nil {
		return nil, "", fmt.Errorf("%s stat: %w", field, err)
	}
	if !directInfo.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%s must name a regular file, not a symlink or special file", field)
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return nil, "", fmt.Errorf("%s resolve: %w", field, err)
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("%s must resolve beneath the evidence directory", field)
	}
	raw, err := readBoundedRuntimeFile(resolved, limit)
	if err != nil {
		return nil, "", fmt.Errorf("%s read: %w", field, err)
	}
	return raw, runtimeDigest(raw), nil
}

// ValidateEvidenceManifest checks the closed, publishable evidence contract.
func ValidateEvidenceManifest(manifest EvidenceManifest) error {
	var issues []string
	add := func(format string, args ...any) { issues = append(issues, fmt.Sprintf(format, args...)) }

	if manifest.ManifestVersion != EvidenceManifestVersion {
		add("manifest_version must be %q", EvidenceManifestVersion)
	}
	candidate := manifest.Candidate
	requireGitObject(&issues, "candidate.sha", candidate.SHA)
	requireGitObject(&issues, "candidate.tree", candidate.Tree)
	if candidate.Version != OperationalReleaseTag {
		add("candidate.version must be %q", OperationalReleaseTag)
	}
	if candidate.EmbeddedTemplateFloor != OperationalFloor {
		add("candidate.embedded_template_floor must be %q", OperationalFloor)
	}
	validateCheckout(&issues, "candidate.verification_checkout", candidate.VerificationCheckout, candidate.SHA, candidate.Tree)
	validateCheckout(&issues, "candidate.execution_checkout", candidate.ExecutionCheckout, candidate.SHA, candidate.Tree)
	if candidate.VerificationCheckout.ID != "" && candidate.VerificationCheckout.ID == candidate.ExecutionCheckout.ID {
		add("verification and execution checkout ids must differ; make-check input cannot be reused for build/live")
	}
	if candidate.BuiltSourceSHA != candidate.SHA {
		add("candidate.built_source_sha must equal candidate.sha")
	}
	if !digestPattern.MatchString(candidate.Binary.SHA256) {
		add("candidate.binary.sha256 must be a full lowercase sha256 digest")
	}
	if candidate.Binary.Version != OperationalFloor {
		add("candidate.binary.version must equal the candidate floor %q", OperationalFloor)
	}
	if candidate.Binary.BuildInfo.Revision != candidate.SHA {
		add("candidate.binary.build_info.revision must equal candidate.sha")
	}
	if candidate.Binary.BuildInfo.Modified == nil {
		add("candidate.binary.build_info.modified attestation is required")
	} else if *candidate.Binary.BuildInfo.Modified {
		add("candidate.binary.build_info.modified must be false")
	}

	if err := safeRelativePath(manifest.CandidateCheck.LogPath); err != nil {
		add("candidate_check.log_path %v", err)
	}
	if !digestPattern.MatchString(manifest.CandidateCheck.LogSHA256) {
		add("candidate_check.log_sha256 must be a full lowercase sha256 digest")
	}
	if manifest.CandidateCheck.Passed == nil {
		add("candidate_check.passed attestation is required")
	} else if !*manifest.CandidateCheck.Passed {
		add("candidate_check.passed must be true for release evidence")
	}

	liveRequired := liveRequiredRows(Catalogue())
	seen := make(map[string]bool)
	seenLogPaths := make(map[string]bool)
	for index, scenario := range manifest.Scenarios {
		validateScenario(&issues, index, scenario, manifest.CandidateCheck.LogPath, liveRequired, seen, seenLogPaths)
	}
	// D-7: presence is required for every live-required row regardless of
	// Optional — only the OUTCOME leniency (pass vs explicitly-unverified)
	// differs, and that is validateScenario's own call via
	// operationalOptionalBranch (report.go). A row this loop does not find
	// in liveRequired (every other operational-confidence row) is proven by
	// the logic-tier marker instead (verifyCandidateCheckLog), not asserted
	// here at all.
	for key, row := range liveRequired {
		if seen[key] {
			continue
		}
		if row.Optional {
			add("scenarios must include %s as pass or explicitly unverified", key)
		} else {
			add("scenarios must include mandatory PASS row %s", key)
		}
	}

	if len(issues) != 0 {
		sort.Strings(issues)
		return &EvidenceValidationError{Issues: issues}
	}
	return nil
}

func validateCheckout(issues *[]string, field string, checkout CheckoutEvidence, sha, tree string) {
	if !safeIDPattern.MatchString(checkout.ID) {
		*issues = append(*issues, field+".id must be a bounded publish-safe identifier")
	}
	if checkout.SHA != sha {
		*issues = append(*issues, field+".sha must equal candidate.sha")
	}
	if checkout.Tree != tree {
		*issues = append(*issues, field+".tree must equal candidate.tree")
	}
	requireTrue := func(name string, value *bool) {
		if value == nil {
			*issues = append(*issues, field+"."+name+" attestation is required")
		} else if !*value {
			*issues = append(*issues, field+"."+name+" must be true")
		}
	}
	requireTrue("detached", checkout.Detached)
	requireTrue("index_clean", checkout.IndexClean)
	requireTrue("worktree_clean", checkout.WorktreeClean)
	requireTrue("untracked_clean", checkout.UntrackedClean)
}

func validateScenario(issues *[]string, index int, scenario ScenarioEvidence, candidateCheckLogPath string, liveRequired map[string]Scenario, seen, seenLogPaths map[string]bool) {
	field := fmt.Sprintf("scenarios[%d]", index)
	key := scenario.ScenarioID + "/" + scenario.Branch
	// requiredAssertions' keys ARE the declared operational-confidence rows
	// (TestOperationalConfidenceCatalogueMatchesEvidenceContract,
	// operational_runtime_test.go, cross-checks this against
	// OperationalConfidenceCatalogue() itself) — reusing that membership
	// here instead of a second hand-typed ID/branch switch means a row
	// renamed or removed from the catalogue cannot silently leave a stale
	// literal behind.
	if _, ok := requiredAssertions[key]; !ok {
		*issues = append(*issues, field+".scenario_id/branch is not a declared operational-confidence row")
	}
	if seen[key] {
		*issues = append(*issues, field+" duplicates "+key)
	}
	seen[key] = true

	// operationalOptionalBranch (report.go) is the SAME fact report.go's own
	// verdict recording already uses for "may this row read unverified" —
	// reused here rather than restated, so the two can never drift apart.
	optionalUnverified := operationalOptionalBranch(scenario.ScenarioID, scenario.Branch)
	switch scenario.Outcome {
	case "pass":
	case "unverified":
		if !optionalUnverified {
			*issues = append(*issues, field+".outcome unverified is allowed only for LE-OC-04/provider-setting or LE-OC-08/unsupported-provider")
		}
		if len(scenario.Limitations) == 0 {
			*issues = append(*issues, field+".limitations must explain an unverified optional branch")
		}
	case "fail":
		*issues = append(*issues, field+".outcome fail cannot satisfy the release gate")
	default:
		*issues = append(*issues, field+".outcome must be pass, fail, or unverified")
	}
	if row, ok := liveRequired[key]; ok && !row.Optional && scenario.Outcome != "pass" {
		*issues = append(*issues, field+" is mandatory and must have outcome pass")
	}

	started, startErr := time.Parse(time.RFC3339, scenario.StartedAt)
	ended, endErr := time.Parse(time.RFC3339, scenario.EndedAt)
	if startErr != nil {
		*issues = append(*issues, field+".started_at must be RFC3339")
	}
	if endErr != nil {
		*issues = append(*issues, field+".ended_at must be RFC3339")
	}
	if startErr == nil && endErr == nil && ended.Before(started) {
		*issues = append(*issues, field+".ended_at must not precede started_at")
	}
	if len(scenario.SurfaceIDs) == 0 {
		*issues = append(*issues, field+".surface_ids must not be empty")
	}
	for _, surface := range scenario.SurfaceIDs {
		if !safeIDPattern.MatchString(surface) {
			*issues = append(*issues, field+".surface_ids contains a non publish-safe identifier")
		}
	}
	if !repositoryPattern.MatchString(scenario.RepositoryID) {
		*issues = append(*issues, field+".repository_id must be a publish-safe owner/repository identifier")
	}
	if !commandFamilyPattern.MatchString(scenario.CommandFamily) {
		*issues = append(*issues, field+".command_family must be a bounded command family without arguments or values")
	}
	if scenario.ArtifactRefs == nil {
		*issues = append(*issues, field+".artifact_refs is required (use [] when none)")
	}
	for _, ref := range scenario.ArtifactRefs {
		if !safeIDPattern.MatchString(ref) {
			*issues = append(*issues, field+".artifact_refs contains a non publish-safe reference")
		}
	}
	if scenario.PRRefs == nil {
		*issues = append(*issues, field+".pr_refs is required (use [] when none)")
	}
	for _, ref := range scenario.PRRefs {
		if !safePullRequestRef(ref) {
			*issues = append(*issues, field+".pr_refs contains a non publish-safe GitHub pull-request URL")
		}
	}
	switch scenario.CleanupStatus {
	case "":
		*issues = append(*issues, field+".cleanup_status is required")
	case "complete", "not-required":
	case "incomplete":
		if scenario.Outcome == "pass" {
			*issues = append(*issues, field+".cleanup_status cannot be incomplete for a passing release row")
		}
	default:
		*issues = append(*issues, field+".cleanup_status must be complete, not-required, or incomplete")
	}
	if err := safeRelativePath(scenario.LogPath); err != nil {
		*issues = append(*issues, field+".log_path "+err.Error())
	}
	if scenario.LogPath == candidateCheckLogPath {
		*issues = append(*issues, field+".log_path must not reuse candidate_check.log_path")
	}
	if seenLogPaths[scenario.LogPath] {
		*issues = append(*issues, field+".log_path duplicates another scenario log path")
	}
	seenLogPaths[scenario.LogPath] = true
	if !digestPattern.MatchString(scenario.LogSHA256) {
		*issues = append(*issues, field+".log_sha256 must be a full lowercase sha256 digest")
	}
	if scenario.Limitations == nil {
		*issues = append(*issues, field+".limitations is required (use [] when none)")
	}
	for _, limitation := range scenario.Limitations {
		if strings.TrimSpace(limitation) == "" || len(limitation) > 500 || hasSecret(limitation) {
			*issues = append(*issues, field+".limitations contains an empty, over-bound, or secret-bearing value")
		}
	}
	if scenario.LiveManifestFloor != OperationalFloor {
		*issues = append(*issues, field+".live_manifest_floor must equal candidate embedded floor "+OperationalFloor)
	}
	requireGitObject(issues, field+".live_manifest_commit", scenario.LiveManifestCommit)
	if scenario.Assertions == nil {
		*issues = append(*issues, field+".assertions is required")
	}
	assertions := make(map[string]bool, len(scenario.Assertions))
	for _, assertion := range scenario.Assertions {
		if !safeIDPattern.MatchString(assertion.ID) {
			*issues = append(*issues, field+".assertions contains a non publish-safe identifier")
		}
		if assertions[assertion.ID] {
			*issues = append(*issues, field+".assertions contains duplicate id "+assertion.ID)
		}
		assertions[assertion.ID] = true
		switch assertion.Outcome {
		case "pass", "fail", "unverified":
		default:
			*issues = append(*issues, field+".assertions["+assertion.ID+"].outcome must be pass, fail, or unverified")
		}
		if scenario.Outcome == "pass" && assertion.Outcome != "pass" {
			*issues = append(*issues, field+".assertions["+assertion.ID+"] must pass to prove a passing scenario")
		}
	}
	for _, required := range requiredAssertions[key] {
		if !assertions[required] {
			*issues = append(*issues, field+".assertions must include "+required)
		}
	}
}

func requireGitObject(issues *[]string, field, value string) {
	if !gitObjectPattern.MatchString(value) {
		*issues = append(*issues, field+" must be a full lowercase 40-hex Git object id")
	}
}

func safeRelativePath(value string) error {
	if value == "" {
		return fmt.Errorf("is required")
	}
	if strings.ContainsAny(value, "\x00\r\n\\") || path.IsAbs(value) || path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("must be a clean repository-relative path")
	}
	if strings.Contains(value, "://") || hasSecret(value) {
		return fmt.Errorf("must not contain a URL or secret")
	}
	return nil
}

func safePullRequestRef(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	return len(parts) == 4 && repositoryPattern.MatchString(parts[0]+"/"+parts[1]) && parts[2] == "pull" && regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(parts[3])
}

func scanSafeEvidence(value any, field string, issues *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") || strings.Contains(lower, "authorization") || strings.Contains(lower, "payload") || strings.Contains(lower, "private") {
				*issues = append(*issues, field+"."+key+" is forbidden: manifests contain safe references, never secrets or private payloads")
			}
			scanSafeEvidence(child, field+"."+key, issues)
		}
	case []any:
		for index, child := range typed {
			scanSafeEvidence(child, fmt.Sprintf("%s[%d]", field, index), issues)
		}
	case string:
		if hasSecret(typed) {
			*issues = append(*issues, field+" contains secret-shaped content")
		}
	}
}

func hasSecret(value string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}
