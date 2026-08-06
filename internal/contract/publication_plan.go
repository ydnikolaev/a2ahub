package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

const (
	// ContractPublicationFloor is the space floor at or above which a contract
	// publishes as contract-set-v2 with a declared `artifacts:` inventory.
	//
	// It ALIASES version.OperationalConfidenceFloor rather than restating
	// "0.19.0", because two gates read these two names for the same decision:
	// internal/validate's V2 carried-set profile check compares against the
	// version constant, while this planner and the V3 descriptor-shape check
	// compare against this one. Two literals that must be equal and are only
	// equal by convention is how a release bumps one and reopens the exact
	// class of bug the pair exists to prevent — V2 saying yes to a shape the
	// planner refuses. That is not hypothetical here: a release script's
	// hand-carried `0.19.0` pattern already drifted from its own derived floor
	// variable and refused a correctly-projected release.
	//
	// ContractPublicationFloor is part of the public package API.
	ContractPublicationFloor = version.OperationalConfidenceFloor
	// ContractSidecarDeleteDomain is part of the public package API.
	ContractSidecarDeleteDomain = "contract-sidecar-v1"
)

// CandidateSourceKind is part of the public package API.
type CandidateSourceKind string

const (
	// CandidateSourceMirror is part of the public package API.
	CandidateSourceMirror CandidateSourceKind = "mirror"
	// CandidateSourceStaging is part of the public package API.
	CandidateSourceStaging CandidateSourceKind = "staging"
)

// CandidateSource is part of the public package API.
type CandidateSource struct {
	Kind        CandidateSourceKind `json:"kind"`
	Location    string              `json:"location"`
	Fingerprint string              `json:"fingerprint"`
}

// MutationAction is part of the public package API.
type MutationAction string

const (
	// MutationWrite is part of the public package API.
	MutationWrite MutationAction = "write"
	// MutationDelete is part of the public package API.
	MutationDelete MutationAction = "delete"
)

// PublicationMutation is part of the public package API.
type PublicationMutation struct {
	Action       MutationAction `json:"action"`
	Path         string         `json:"path"`
	BeforeDigest string         `json:"before_digest"`
	BeforeMode   string         `json:"before_mode"`
	AfterDigest  string         `json:"after_digest"`
	AfterSize    int            `json:"after_size"`
	AfterMode    string         `json:"after_mode"`
	DeleteDomain string         `json:"delete_domain"`
	ContractRoot string         `json:"contract_root"`
	Bytes        []byte         `json:"-"`
}

// PublicationEntry is part of the public package API.
type PublicationEntry struct {
	Path             string `json:"path"`
	Role             string `json:"role"`
	Normative        bool   `json:"normative"`
	MediaType        string `json:"media_type"`
	ConformsTo       string `json:"conforms_to"`
	Size             int    `json:"size"`
	Digest           string `json:"digest"`
	IntegrityCovered bool   `json:"integrity_covered"`
}

// CompatibilityVerdict is part of the public package API.
type CompatibilityVerdict string

const (
	// CompatibilityCompatible is part of the public package API.
	CompatibilityCompatible CompatibilityVerdict = "compatible"
	// CompatibilityBreaking is part of the public package API.
	CompatibilityBreaking CompatibilityVerdict = "breaking"
	// CompatibilityNotEvaluated is part of the public package API.
	CompatibilityNotEvaluated CompatibilityVerdict = "not-evaluated"
	// CompatibilityRefused is part of the public package API.
	CompatibilityRefused CompatibilityVerdict = "refused"
)

// CompatibilityFailure is part of the public package API.
type CompatibilityFailure struct {
	Path   string `json:"path"`
	Schema string `json:"schema"`
	Reason string `json:"reason"`
}

// CompatibilityResult is part of the public package API.
type CompatibilityResult struct {
	Verdict         CompatibilityVerdict   `json:"verdict"`
	Reason          string                 `json:"reason"`
	Failures        []CompatibilityFailure `json:"failures"`
	PolicyViolation *Finding               `json:"-"`
}

// CompatibilityCheckInput is part of the public package API.
type CompatibilityCheckInput struct {
	DeclaredBump    string
	PriorVersion    string
	NewVersion      string
	NewEntries      []Entry
	BaselineEntries []Entry
	NewBytes        map[string][]byte
	BaselineBytes   map[string][]byte
}

// CompatibilityChecker is contract-owned and implemented by a validation
// adapter. The pure core never imports internal/validate or internal/schema.
// CompatibilityChecker is part of the public package API.
type CompatibilityChecker interface {
	CheckCompatibility(CompatibilityCheckInput) CompatibilityResult
}

// InstanceViolation is part of the public package API.
type InstanceViolation struct {
	InstancePointer string `json:"instance_pointer"`
	SchemaPointer   string `json:"schema_pointer"`
	Message         string `json:"message"`
}

// InstanceCheckInput is part of the public package API.
type InstanceCheckInput struct {
	SchemaPath   string
	Schema       []byte
	InstancePath string
	Instance     []byte
}

// InstanceCheckResult is part of the public package API.
type InstanceCheckResult struct {
	Passed     bool                `json:"passed"`
	Violations []InstanceViolation `json:"violations"`
}

// InstanceChecker is the matching one-method consumer seam for later payload
// and suite conformance. It is declared here so schema-library types never
// cross into the contract core.
// InstanceChecker is part of the public package API.
type InstanceChecker interface {
	CheckInstance(InstanceCheckInput) InstanceCheckResult
}

// Finding is part of the public package API.
type Finding struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// CompatibilityExclusion is part of the public package API.
type CompatibilityExclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// PublicationGate is part of the public package API.
type PublicationGate string

const (
	// GateNone is part of the public package API.
	GateNone PublicationGate = "none"
	// GateG1 is part of the public package API.
	GateG1 PublicationGate = "G1"
	// GateG2 is part of the public package API.
	GateG2 PublicationGate = "G2"
)

// PublishedContract is caller-resolved historical input. DescriptorRaw is
// required for legacy snapshots because contract-tree-v1 deliberately excludes
// contract.md from CarriedSet.Bytes.
// PublishedContract is part of the public package API.
type PublishedContract struct {
	Version       string
	DescriptorRaw []byte
	Set           CarriedSet
	// Modes records the exact canonical Git mode observed for every path in
	// DescriptorRaw/Set. A mutation cannot safely bind a replacement or
	// deletion without this prior-tree fact.
	Modes map[string]string
}

// PublicationInput is part of the public package API.
type PublicationInput struct {
	System         string
	ContractID     string
	Selector       string
	AuthoringFloor string
	Candidate      CandidateIntentSnapshot
	// CandidateModes binds every desired regular file to the exact canonical
	// Git mode frozen with the candidate. Nil retains the legacy pure-core test
	// default (new files are 100644; replacements preserve their prior mode).
	CandidateModes map[string]string
	Published      []PublishedContract
	// MutationBaseline is the publication whose tree the write actually lands
	// on — what authoritative main carries right now. It is supplied rather
	// than derived because only the space can know it: Published answers
	// "which versions exist", never "which one's bytes are on main", and the
	// two differ the moment a version is published on anything but the newest
	// line. Zero value means main carries no prior publication (first
	// publish). See the derivation at its use below.
	MutationBaseline      PublishedContract
	CandidateSource       CandidateSource
	ContractRoot          string
	SourceDigestAssertion string
	Warnings              []Finding
	Violations            []Finding
}

// PublicationPlan is immutable-by-construction. Exported fields are a detached
// wire projection; the exact descriptor, desired bytes and canonical record
// used by publish are retained privately and returned only as clones.
// PublicationPlan is part of the public package API.
type PublicationPlan struct {
	Contract                  string                   `json:"contract"`
	CurrentVersion            string                   `json:"current_version"`
	TargetVersion             string                   `json:"target_version"`
	FirstPublish              bool                     `json:"first_publish"`
	BaselineVersion           string                   `json:"baseline_version"`
	AuthoringFloor            string                   `json:"authoring_floor"`
	DescriptorSchema          string                   `json:"descriptor_schema"`
	EventSchema               string                   `json:"event_schema"`
	DigestProfile             DigestProfile            `json:"digest_profile"`
	Entries                   []PublicationEntry       `json:"entries"`
	Mutations                 []PublicationMutation    `json:"mutations"`
	AggregateDigest           string                   `json:"aggregate_digest"`
	SourceDigestProfile       DigestProfile            `json:"source_digest_profile"`
	SourceDigest              string                   `json:"source_digest"`
	Compatibility             CompatibilityResult      `json:"compatibility"`
	ExcludedFromCompatibility []CompatibilityExclusion `json:"excluded_from_compatibility"`
	Gate                      PublicationGate          `json:"gate"`
	Warnings                  []Finding                `json:"warnings"`
	Violations                []Finding                `json:"violations"`
	CandidateSource           CandidateSource          `json:"candidate_source"`
	IntentKey                 string                   `json:"intent_key"`
	CandidateIntentDigest     string                   `json:"candidate_intent_digest"`
	VersionSelector           string                   `json:"version_selector"`
	OperationKey              string                   `json:"operation_key"`
	HeadBranch                string                   `json:"head_branch"`
	PlanDigest                string                   `json:"plan_digest"`

	canonical       []byte
	finalDescriptor []byte
	plannedBytes    map[string][]byte
	wire            []byte
}

// CanonicalBytes is part of the public package API.
func (p PublicationPlan) CanonicalBytes() []byte { return bytes.Clone(p.canonical) }

// FinalDescriptorBytes is part of the public package API.
func (p PublicationPlan) FinalDescriptorBytes() []byte { return bytes.Clone(p.finalDescriptor) }

// PlannedBytes is part of the public package API.
func (p PublicationPlan) PlannedBytes() map[string][]byte {
	return cloneBytesByPath(p.plannedBytes)
}

type publicationPlanWire PublicationPlan

// MarshalJSON always emits the frozen wire projection captured when the plan
// digest was computed. Mutating a detached exported slice cannot create JSON
// that claims the original digest while carrying different semantics.
// MarshalJSON is part of the public package API.
func (p PublicationPlan) MarshalJSON() ([]byte, error) {
	if len(p.wire) != 0 {
		return bytes.Clone(p.wire), nil
	}
	return json.Marshal(publicationPlanWire(p))
}

// Equal is suitable for preflight/publish binding: it compares the frozen
// semantic record and exact desired bytes, not mutable presentation fields.
// Equal is part of the public package API.
func (p PublicationPlan) Equal(other PublicationPlan) bool {
	if p.PlanDigest == "" || p.PlanDigest != other.PlanDigest || !bytes.Equal(p.canonical, other.canonical) {
		return false
	}
	if len(p.plannedBytes) != len(other.plannedBytes) {
		return false
	}
	for path, raw := range p.plannedBytes {
		if !bytes.Equal(raw, other.plannedBytes[path]) {
			return false
		}
	}
	return bytes.Equal(p.finalDescriptor, other.finalDescriptor)
}

type publicationPlanRecord struct {
	Profile                   string                   `json:"profile"`
	Contract                  string                   `json:"contract"`
	CurrentVersion            string                   `json:"current_version"`
	TargetVersion             string                   `json:"target_version"`
	FirstPublish              bool                     `json:"first_publish"`
	BaselineVersion           string                   `json:"baseline_version"`
	AuthoringFloor            string                   `json:"authoring_floor"`
	DescriptorSchema          string                   `json:"descriptor_schema"`
	EventSchema               string                   `json:"event_schema"`
	DigestProfile             DigestProfile            `json:"digest_profile"`
	Entries                   []PublicationEntry       `json:"entries"`
	Mutations                 []PublicationMutation    `json:"mutations"`
	AggregateDigest           string                   `json:"aggregate_digest"`
	SourceDigestProfile       DigestProfile            `json:"source_digest_profile"`
	SourceDigest              string                   `json:"source_digest"`
	Compatibility             CompatibilityResult      `json:"compatibility"`
	ExcludedFromCompatibility []CompatibilityExclusion `json:"excluded_from_compatibility"`
	Gate                      PublicationGate          `json:"gate"`
	Warnings                  []Finding                `json:"warnings"`
	Violations                []Finding                `json:"violations"`
	CandidateSource           CandidateSource          `json:"candidate_source"`
	IntentKey                 string                   `json:"intent_key"`
	CandidateIntentDigest     string                   `json:"candidate_intent_digest"`
	VersionSelector           string                   `json:"version_selector"`
	OperationKey              string                   `json:"operation_key"`
	HeadBranch                string                   `json:"head_branch"`
}

// PlanPublication is the sole pure owner of selector/target/baseline/floor,
// descriptor finalization, carried-set, compatibility, mutation metadata and
// canonical plan semantics. It performs no I/O and never mints delete permits.
// PlanPublication is part of the public package API.
func PlanPublication(input PublicationInput, checker CompatibilityChecker) (PublicationPlan, []Issue) {
	selectorKind, explicitTarget, issues := parsePublicationSelector(input.Selector)
	if len(issues) != 0 {
		return PublicationPlan{}, issues
	}

	issues = validatePublicationInput(input)
	if len(issues) != 0 {
		sortIssues(issues)
		return PublicationPlan{}, issues
	}

	publishedByVersion := make(map[string]PublishedContract, len(input.Published))
	publishedVersions := make([]string, 0, len(input.Published))
	for _, published := range input.Published {
		if _, duplicate := publishedByVersion[published.Version]; duplicate {
			issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "published", Detail: "published version appears more than once"})
			continue
		}
		publishedByVersion[published.Version] = clonePublishedContract(published)
		publishedVersions = append(publishedVersions, published.Version)
	}
	if len(issues) != 0 {
		sortIssues(issues)
		return PublicationPlan{}, issues
	}

	target := explicitTarget
	if selectorKind != "explicit" {
		base := "0.0.0"
		if len(publishedVersions) != 0 {
			base = highestVersion(publishedVersions)
		}
		var err error
		target, err = bumpCanonicalVersion(base, selectorKind)
		if err != nil {
			return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "version_selector", Detail: err.Error()}}
		}
	}
	if _, established := publishedByVersion[target]; established {
		return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "target_version", Detail: "target version is already published; completion must be resolved before planning"}}
	}

	baseline, hasBaseline, err := version.Baseline(publishedVersions, target)
	if err != nil {
		return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "baseline_version", Detail: "cannot select a canonical published baseline"}}
	}
	firstPublish := len(publishedVersions) == 0

	belowFloor, err := version.OlderThan(input.AuthoringFloor, ContractPublicationFloor)
	if err != nil {
		return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "authoring_floor", Detail: "cannot compare authoring floor"}}
	}
	descriptorSchema, eventSchema := "envelope/v2", "event/v2"
	profile := ProfileContractSetV2
	if belowFloor {
		descriptorSchema, eventSchema, profile = "envelope/v1", "event/v1", ProfileContractTreeV1
	}
	// Both refusals below name the concrete descriptor shape and the command
	// that produces it, never the internal mode name alone.
	//
	// Found on 2026-08-06 by an external report: the message was "authoring
	// floor requires declared-v2 candidate inventory", and the strings
	// `declared-v2`, `candidate inventory` and `authoring floor` appeared in
	// zero lines of the shipped skill manual. A refusal that names a
	// precondition with no reachable means of satisfaction leaves the operator
	// with a merged contract and no next command — which is exactly what
	// happened. A refusal is only actionable if it names its own remedy.
	if profile == ProfileContractSetV2 && input.Candidate.InventoryMode() != InventoryDeclaredV2 {
		return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "artifacts", Detail: fmt.Sprintf(
			"this space's authoring floor is %s, at or above %s, so a contract publishes as %s: its %s must be `schema: envelope/v2` and declare every carried file exactly once under a top-level `artifacts:` key. This candidate declares none, so it can never publish as authored. `a2a template show contract` prints the exact shape. Correct the descriptor in your own staged candidate and re-run with `--staging <project-relative-dir>`; that overlays the corrected descriptor onto the landed one, so a contract that already merged is fixed without a second submit.",
			input.AuthoringFloor, ContractPublicationFloor, ProfileContractSetV2, DescriptorPath)}}
	}
	if profile == ProfileContractTreeV1 && input.Candidate.InventoryMode() != InventoryLegacyFixedV1 {
		return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "artifacts", Detail: fmt.Sprintf(
			"this space's authoring floor is %s, below %s, so a contract publishes as %s over the fixed schema/** + fixtures/** tree and its %s must NOT carry a top-level `artifacts:` key. Remove it, or raise the space's min_binary_version to %s to publish the declared set instead. Run `a2a template show contract --envelope-schema envelope/v1` for the shape this floor expects.",
			input.AuthoringFloor, ContractPublicationFloor, ProfileContractTreeV1, DescriptorPath, ContractPublicationFloor)}}
	}

	sourceProfile, sourceDigest, sourceIssues := publicationSourceDigest(input.Candidate, input.SourceDigestAssertion)
	if len(sourceIssues) != 0 {
		return PublicationPlan{}, sourceIssues
	}
	finalRaw, finalizeIssues := finalizePublicationDescriptor(input.Candidate, descriptorSchema, target, sourceDigest)
	if len(finalizeIssues) != 0 {
		return PublicationPlan{}, finalizeIssues
	}
	validateText(DescriptorPath, finalRaw, &finalizeIssues)
	if len(finalizeIssues) != 0 {
		sortIssues(finalizeIssues)
		return PublicationPlan{}, finalizeIssues
	}
	finalDescriptor, err := ParseDescriptor(finalRaw)
	if err != nil {
		return PublicationPlan{}, []Issue{{Kind: IssueDescriptorMismatch, Path: DescriptorPath, Detail: "final descriptor cannot be parsed"}}
	}
	frozenCandidate := input.Candidate.Snapshot()
	frozenCandidate.Descriptor.Raw = bytes.Clone(finalRaw)
	set, setIssues := ValidateCarriedSet(profile, finalDescriptor, frozenCandidate)
	if len(setIssues) != 0 {
		return PublicationPlan{}, setIssues
	}

	entries := publicationEntries(profile, set, finalRaw)
	exclusions := compatibilityExclusions(set)
	compatibility := CompatibilityResult{Verdict: CompatibilityNotEvaluated, Reason: "no published baseline", Failures: make([]CompatibilityFailure, 0)}
	if hasBaseline {
		if finalDescriptor.SchemaFormat != "json-schema-2020-12" {
			compatibility.Reason = fmt.Sprintf("schema format %s has no computed compatibility adapter", finalDescriptor.SchemaFormat)
		} else {
			if checker == nil {
				return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "compatibility", Detail: "JSON Schema publication requires a compatibility checker"}}
			}
			baselinePublished := publishedByVersion[baseline]
			compatibility = checker.CheckCompatibility(compatibilityInput(set, baselinePublished.Set, baseline, target))
			var compatibilityIssues []Issue
			compatibility, compatibilityIssues = normalizeCompatibility(compatibility)
			if len(compatibilityIssues) != 0 {
				return PublicationPlan{}, compatibilityIssues
			}
		}
	}

	gate := GateNone
	if !hasBaseline {
		gate = GateG1
	} else if majorOf(baseline) != majorOf(target) || compatibility.Verdict == CompatibilityBreaking {
		gate = GateG2
	}

	// The mutation baseline answers "what am I about to overwrite", which is a
	// question about the TREE ON MAIN — never about which version is the
	// compatibility baseline, and never about which version the candidate was
	// materialized from. This core cannot see main, so the space supplies it.
	//
	// It used to be derived here, as the semver-highest published version
	// unless one matched the candidate's own current version. Both answers are
	// right only while every publication is on the newest line. Live run
	// 2026-08-05 found the case where it is not: with 1.0.0, 1.1.0 and 2.0.0
	// published in that order, main holds 2.0.0's tree, but publishing the
	// maintenance version 1.2.0 from a candidate materialized at 1.1.0
	// selected 1.1.0's snapshot — so the write set was computed against bytes
	// that are not there and the precondition named a digest main does not
	// have. The funnel refused every attempt:
	//   "mutation precondition does not match the current file:
	//    alpha/provides/<contract>/contract.md"
	// A maintenance release on an older line was therefore impossible once a
	// newer major existed.
	mutationBaseline := clonePublishedContract(input.MutationBaseline)
	desiredBytes := cloneBytesByPath(set.Bytes)
	desiredBytes[DescriptorPath] = bytes.Clone(finalRaw)
	mutations, mutationIssues := publicationMutations(profile, desiredBytes, input.CandidateModes, mutationBaseline, input.ContractRoot)
	if len(mutationIssues) != 0 {
		return PublicationPlan{}, mutationIssues
	}

	intentKey := operation.ContractPublishIntent(input.System, input.ContractID, input.Selector, input.Candidate.Digest())
	operationKey := operation.ContractPublish(input.System, input.ContractID, target)
	warnings := cloneFindings(input.Warnings)
	violations := cloneFindings(input.Violations)
	if compatibility.PolicyViolation != nil {
		violations = cloneFindings(append(violations, *compatibility.PolicyViolation))
	}
	compatibility.PolicyViolation = nil
	plan := PublicationPlan{
		Contract: input.ContractID, CurrentVersion: input.Candidate.CurrentVersion(), TargetVersion: target,
		FirstPublish: firstPublish, BaselineVersion: baseline, AuthoringFloor: input.AuthoringFloor,
		DescriptorSchema: descriptorSchema, EventSchema: eventSchema, DigestProfile: profile,
		Entries: entries, Mutations: mutations, AggregateDigest: set.AggregateDigest,
		SourceDigestProfile: sourceProfile, SourceDigest: sourceDigest,
		Compatibility: compatibility, ExcludedFromCompatibility: exclusions, Gate: gate,
		Warnings: warnings, Violations: violations, CandidateSource: input.CandidateSource,
		IntentKey: intentKey, CandidateIntentDigest: input.Candidate.Digest(), VersionSelector: input.Selector,
		OperationKey:    operationKey,
		HeadBranch:      operation.BranchName(input.System, "contract-publish", operationKey),
		finalDescriptor: bytes.Clone(finalRaw), plannedBytes: cloneBytesByPath(desiredBytes),
	}
	record := publicationPlanRecord{
		Profile:  "contract-publication-plan-v1",
		Contract: plan.Contract, CurrentVersion: plan.CurrentVersion, TargetVersion: plan.TargetVersion,
		FirstPublish: plan.FirstPublish, BaselineVersion: plan.BaselineVersion, AuthoringFloor: plan.AuthoringFloor,
		DescriptorSchema: plan.DescriptorSchema, EventSchema: plan.EventSchema, DigestProfile: plan.DigestProfile,
		Entries: plan.Entries, Mutations: plan.Mutations, AggregateDigest: plan.AggregateDigest,
		SourceDigestProfile: plan.SourceDigestProfile, SourceDigest: plan.SourceDigest,
		Compatibility: plan.Compatibility, ExcludedFromCompatibility: plan.ExcludedFromCompatibility,
		Gate: plan.Gate, Warnings: plan.Warnings, Violations: plan.Violations,
		CandidateSource: plan.CandidateSource, IntentKey: plan.IntentKey,
		CandidateIntentDigest: plan.CandidateIntentDigest, VersionSelector: plan.VersionSelector,
		OperationKey: plan.OperationKey, HeadBranch: plan.HeadBranch,
	}
	canonical, err := json.Marshal(record)
	if err != nil {
		return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "plan_digest", Detail: fmt.Sprintf("cannot encode canonical publication plan: %v", err)}}
	}
	plan.canonical = bytes.Clone(canonical)
	plan.PlanDigest = artifact.Digest(canonical)
	wire, err := json.Marshal(publicationPlanWire(plan))
	if err != nil {
		return PublicationPlan{}, []Issue{{Kind: IssueInvalidPublication, Path: "plan", Detail: fmt.Sprintf("cannot encode publication plan: %v", err)}}
	}
	plan.wire = bytes.Clone(wire)
	return plan, nil
}

func parsePublicationSelector(selector string) (kind, target string, issues []Issue) {
	if !versionSelectorPattern.MatchString(selector) {
		return "", "", []Issue{{Kind: IssueInvalidPublication, Path: "version_selector", Detail: "must be auto:patch|minor|major or explicit:<canonical-semver>"}}
	}
	kind, value, _ := strings.Cut(selector, ":")
	if kind == "explicit" {
		if !strictCanonicalVersion(value) {
			return "", "", []Issue{{Kind: IssueInvalidPublication, Path: "version_selector", Detail: "explicit target must be canonical major.minor.patch"}}
		}
		return kind, value, nil
	}
	return value, "", nil
}

func validatePublicationInput(input PublicationInput) []Issue {
	issues := make([]Issue, 0)
	parsedID, idErr := artifact.ParseID(input.ContractID)
	if idErr != nil || parsedID.Prefix != "XC" || parsedID.Class != artifact.ClassStanding || parsedID.System != input.System || input.ContractID != input.Candidate.ContractID() {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "contract", Detail: "contract ID must match the frozen XC candidate"})
	}
	if !sha256DigestPattern.MatchString(input.Candidate.Digest()) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "candidate_intent_digest", Detail: "candidate intent must have a canonical digest"})
	}
	if input.Candidate.CurrentVersion() != "" && !strictCanonicalVersion(input.Candidate.CurrentVersion()) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "current_version", Detail: "descriptor version must be canonical major.minor.patch"})
	}
	if !strictCanonicalVersion(input.AuthoringFloor) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "authoring_floor", Detail: "authoring floor must be canonical major.minor.patch"})
	}
	for _, published := range input.Published {
		if !strictCanonicalVersion(published.Version) {
			issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "published", Detail: "every published version must be canonical major.minor.patch"})
		}
	}
	if input.CandidateSource.Kind != CandidateSourceMirror && input.CandidateSource.Kind != CandidateSourceStaging {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "candidate_source.kind", Detail: "candidate source must be mirror or staging"})
	}
	if input.CandidateSource.Location == "" || !sha256DigestPattern.MatchString(input.CandidateSource.Fingerprint) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "candidate_source", Detail: "candidate source location and subtree fingerprint are required"})
	}
	if input.CandidateSource.Kind == CandidateSourceStaging && !cleanPortablePath(input.CandidateSource.Location) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "candidate_source.location", Detail: "staging source must be a clean project-relative path"})
	}
	if input.CandidateSource.Kind == CandidateSourceMirror && strings.ContainsAny(input.CandidateSource.Location, "/\\\x00\r\n\t ") {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "candidate_source.location", Detail: "mirror source must be an opaque subtree object identity, not a path"})
	}
	if !cleanPortablePath(input.ContractRoot) {
		issues = append(issues, Issue{Kind: IssueInvalidPublication, Path: "contract_root", Detail: "contract root must be a clean relative path"})
	}
	return issues
}

func strictCanonicalVersion(value string) bool {
	canonical, err := version.Canonical(value)
	return err == nil && canonical == value
}

func highestVersion(versions []string) string {
	highest := versions[0]
	for _, candidate := range versions[1:] {
		older, _ := version.OlderThan(highest, candidate)
		if older {
			highest = candidate
		}
	}
	return highest
}

func bumpCanonicalVersion(base, kind string) (string, error) {
	parts := strings.Split(base, ".")
	values := [3]int{}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return "", fmt.Errorf("base version is not canonical")
		}
		values[i] = value
	}
	switch kind {
	case "major":
		if values[0] == math.MaxInt {
			return "", fmt.Errorf("major version cannot be incremented")
		}
		values = [3]int{values[0] + 1, 0, 0}
	case "minor":
		if values[1] == math.MaxInt {
			return "", fmt.Errorf("minor version cannot be incremented")
		}
		values = [3]int{values[0], values[1] + 1, 0}
	case "patch":
		if values[2] == math.MaxInt {
			return "", fmt.Errorf("patch version cannot be incremented")
		}
		values[2]++
	default:
		return "", fmt.Errorf("unsupported bump %q", kind)
	}
	return fmt.Sprintf("%d.%d.%d", values[0], values[1], values[2]), nil
}

func publicationSourceDigest(candidate CandidateIntentSnapshot, assertion string) (DigestProfile, string, []Issue) {
	descriptor := candidate.Descriptor()
	if descriptor.GeneratedFrom == nil {
		if assertion != "" {
			return "", "", []Issue{{Kind: IssueDigestMismatch, Path: "generated_from.source_digest", Detail: "source digest assertion requires generated_from.tool"}}
		}
		return "", "", nil
	}
	digests := make(map[string]string)
	snapshot := candidate.Snapshot()
	byPath := make(map[string][]byte, len(snapshot.Files))
	for _, file := range snapshot.Files {
		byPath[file.Path] = file.Raw
	}
	if candidate.InventoryMode() == InventoryDeclaredV2 {
		for _, entry := range descriptor.Artifacts {
			if entry.Role == RoleSchema || fixtureRole(entry.Role) {
				digests[entry.Path] = artifact.Digest(byPath[entry.Path])
			}
		}
	} else {
		for path, raw := range byPath {
			if insideLegacyRoot(path) {
				digests[path] = artifact.Digest(raw)
			}
		}
	}
	digest := artifact.CombineDigestPairs(digests)
	if assertion != "" {
		if !sha256DigestPattern.MatchString(assertion) || assertion != digest {
			return "", "", []Issue{{Kind: IssueDigestMismatch, Path: "generated_from.source_digest", Detail: fmt.Sprintf("asserted %s, computed %s using %s", assertion, digest, ProfileExportSourceV1)}}
		}
	}
	return ProfileExportSourceV1, digest, nil
}

func finalizePublicationDescriptor(candidate CandidateIntentSnapshot, schema, target, sourceDigest string) ([]byte, []Issue) {
	snapshot := candidate.Snapshot()
	fm, err := artifact.ParseFrontmatter(snapshot.Descriptor.Raw)
	if err != nil {
		return nil, []Issue{{Kind: IssueDescriptorMismatch, Path: DescriptorPath, Detail: "candidate descriptor cannot be finalized"}}
	}
	var document yaml.Node
	if err := yaml.Unmarshal(fm.YAML, &document); err != nil || len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, []Issue{{Kind: IssueDescriptorMismatch, Path: DescriptorPath, Detail: "candidate descriptor frontmatter is not a mapping"}}
	}
	root := document.Content[0]
	setYAMLScalar(root, "schema", schema)
	setYAMLScalar(root, "version", target)
	if candidate.Descriptor().GeneratedFrom != nil {
		generated := yamlMappingValue(root, "generated_from")
		if generated == nil || generated.Kind != yaml.MappingNode {
			return nil, []Issue{{Kind: IssueDescriptorMismatch, Path: "generated_from", Detail: "generated_from must be a mapping"}}
		}
		setYAMLScalar(generated, "source_digest", sourceDigest)
	}
	encoded, err := yaml.Marshal(root)
	if err != nil {
		return nil, []Issue{{Kind: IssueDescriptorMismatch, Path: DescriptorPath, Detail: "final descriptor cannot be serialized"}}
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: encoded, Body: bytes.Clone(fm.Body)}), nil
}

func yamlMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func setYAMLScalar(mapping *yaml.Node, key, value string) {
	if existing := yamlMappingValue(mapping, key); existing != nil {
		existing.Kind = yaml.ScalarNode
		existing.Tag = "!!str"
		existing.Value = value
		existing.Content = nil
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func publicationEntries(profile DigestProfile, set CarriedSet, finalRaw []byte) []PublicationEntry {
	entries := make([]PublicationEntry, 0, len(set.Entries)+1)
	entries = append(entries, PublicationEntry{
		Path: DescriptorPath, Role: "descriptor", Normative: true, MediaType: "text/markdown",
		Size: len(finalRaw), Digest: artifact.Digest(finalRaw), IntegrityCovered: profile == ProfileContractSetV2,
	})
	for _, entry := range set.Entries {
		role, normative, mediaType, conformsTo := string(entry.Role), entry.Normative, entry.MediaType, entry.ConformsTo
		if profile == ProfileContractTreeV1 {
			role, normative = legacyPlanRole(entry.Path), true
		}
		entries = append(entries, PublicationEntry{
			Path: entry.Path, Role: role, Normative: normative, MediaType: mediaType, ConformsTo: conformsTo,
			Size: len(set.Bytes[entry.Path]), Digest: set.PerFileDigest[entry.Path], IntegrityCovered: true,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func legacyPlanRole(path string) string {
	switch {
	case strings.HasPrefix(path, "schema/"):
		return string(RoleSchema)
	case strings.HasPrefix(path, "fixtures/valid/"):
		return string(RoleValidFixture)
	case strings.HasPrefix(path, "fixtures/invalid/"):
		return string(RoleInvalidFixture)
	default:
		return "fixture"
	}
}

func compatibilityExclusions(set CarriedSet) []CompatibilityExclusion {
	exclusions := make([]CompatibilityExclusion, 0)
	for _, entry := range set.Entries {
		if !entry.Normative {
			exclusions = append(exclusions, CompatibilityExclusion{Path: entry.Path, Reason: "entry is advisory (normative=false)"})
		}
	}
	sort.Slice(exclusions, func(i, j int) bool { return exclusions[i].Path < exclusions[j].Path })
	return exclusions
}

func compatibilityInput(current, baseline CarriedSet, priorVersion, newVersion string) CompatibilityCheckInput {
	return CompatibilityCheckInput{
		DeclaredBump: inferBump(priorVersion, newVersion), PriorVersion: priorVersion, NewVersion: newVersion,
		NewEntries: normativeEntries(current), BaselineEntries: normativeEntries(baseline),
		NewBytes: cloneBytesByPath(current.Bytes), BaselineBytes: cloneBytesByPath(baseline.Bytes),
	}
}

func normativeEntries(set CarriedSet) []Entry {
	entries := make([]Entry, 0, len(set.Entries))
	for _, entry := range set.Entries {
		if set.Profile == ProfileContractTreeV1 {
			entry.Role = Role(legacyPlanRole(entry.Path))
			entry.Normative = true
		}
		if entry.Normative {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func inferBump(prior, target string) string {
	p := versionParts(prior)
	t := versionParts(target)
	switch {
	case p[0] != t[0]:
		return "major"
	case p[1] != t[1]:
		return "minor"
	default:
		return "patch"
	}
}

func majorOf(value string) int { return versionParts(value)[0] }

func versionParts(value string) [3]int {
	parts := strings.Split(value, ".")
	out := [3]int{}
	for i := range out {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
}

func normalizeCompatibility(result CompatibilityResult) (CompatibilityResult, []Issue) {
	switch result.Verdict {
	case CompatibilityCompatible, CompatibilityBreaking, CompatibilityNotEvaluated, CompatibilityRefused:
	default:
		return CompatibilityResult{}, []Issue{{Kind: IssueInvalidPublication, Path: "compatibility.verdict", Detail: "checker returned an unknown compatibility verdict"}}
	}
	result.Failures = append([]CompatibilityFailure(nil), result.Failures...)
	if result.Failures == nil {
		result.Failures = make([]CompatibilityFailure, 0)
	}
	sort.Slice(result.Failures, func(i, j int) bool {
		if result.Failures[i].Path != result.Failures[j].Path {
			return result.Failures[i].Path < result.Failures[j].Path
		}
		if result.Failures[i].Schema != result.Failures[j].Schema {
			return result.Failures[i].Schema < result.Failures[j].Schema
		}
		return result.Failures[i].Reason < result.Failures[j].Reason
	})
	requiresPolicyViolation := result.Verdict == CompatibilityBreaking || result.Verdict == CompatibilityRefused
	if requiresPolicyViolation && (result.PolicyViolation == nil || result.PolicyViolation.Code == "" || result.PolicyViolation.Message == "") {
		return CompatibilityResult{}, []Issue{{
			Kind: IssueInvalidPublication, Path: "compatibility.policy_violation",
			Detail: "breaking/refused compatibility must carry the canonical policy violation",
		}}
	}
	if !requiresPolicyViolation && result.PolicyViolation != nil {
		return CompatibilityResult{}, []Issue{{
			Kind: IssueInvalidPublication, Path: "compatibility.policy_violation",
			Detail: "compatible/not-evaluated compatibility cannot carry a policy violation",
		}}
	}
	if result.PolicyViolation != nil {
		expectedCode := "POL-007"
		if result.Verdict == CompatibilityRefused {
			expectedCode = "POL-008"
		}
		if result.PolicyViolation.Code != expectedCode {
			return CompatibilityResult{}, []Issue{{
				Kind: IssueInvalidPublication, Path: "compatibility.policy_violation.code",
				Detail: fmt.Sprintf("%s compatibility must carry %s", result.Verdict, expectedCode),
			}}
		}
	}
	if result.PolicyViolation != nil {
		cloned := *result.PolicyViolation
		result.PolicyViolation = &cloned
	}
	return result, nil
}

func publicationMutations(profile DigestProfile, desired map[string][]byte, desiredModes map[string]string, prior PublishedContract, contractRoot string) ([]PublicationMutation, []Issue) {
	priorBytes := cloneBytesByPath(prior.Set.Bytes)
	if len(prior.DescriptorRaw) != 0 {
		priorBytes[DescriptorPath] = bytes.Clone(prior.DescriptorRaw)
	}
	mutations := make([]PublicationMutation, 0)
	for path, raw := range desired {
		beforeDigest, beforeMode, afterMode := "", "", "100644"
		if priorRaw, exists := priorBytes[path]; exists {
			mode, ok := canonicalPublicationGitMode(prior.Modes[path])
			if !ok {
				return nil, []Issue{{Kind: IssueInvalidPublication, Path: path, Detail: "replacement lacks an exact canonical prior Git mode"}}
			}
			beforeDigest, beforeMode, afterMode = artifact.Digest(priorRaw), mode, mode
		}
		if desiredModes != nil {
			mode, ok := canonicalPublicationGitMode(desiredModes[path])
			if !ok {
				return nil, []Issue{{Kind: IssueInvalidPublication, Path: path, Detail: "candidate lacks an exact canonical Git mode"}}
			}
			afterMode = mode
		}
		if bytes.Equal(raw, priorBytes[path]) && (beforeMode == "" || beforeMode == afterMode) {
			continue
		}
		mutations = append(mutations, PublicationMutation{
			Action: MutationWrite, Path: path, BeforeDigest: beforeDigest, BeforeMode: beforeMode,
			AfterDigest: artifact.Digest(raw), AfterSize: len(raw), AfterMode: afterMode,
		})
	}
	if profile == ProfileContractSetV2 && prior.Set.Profile == ProfileContractSetV2 {
		for _, entry := range prior.Set.Entries {
			if _, retained := desired[entry.Path]; retained {
				continue
			}
			raw, exists := priorBytes[entry.Path]
			if !exists || !insideCarriedRoot(entry.Path) || entry.Path == DescriptorPath {
				return nil, []Issue{{Kind: IssueInvalidPublication, Path: entry.Path, Detail: "delete candidate is not an exact prior-declared contract sidecar"}}
			}
			mode, ok := canonicalPublicationGitMode(prior.Modes[entry.Path])
			if !ok {
				return nil, []Issue{{Kind: IssueInvalidPublication, Path: entry.Path, Detail: "delete candidate lacks an exact canonical prior Git mode"}}
			}
			mutations = append(mutations, PublicationMutation{
				Action: MutationDelete, Path: entry.Path, BeforeDigest: artifact.Digest(raw), BeforeMode: mode,
				DeleteDomain: ContractSidecarDeleteDomain, ContractRoot: contractRoot,
			})
		}
	}
	sort.Slice(mutations, func(i, j int) bool {
		if mutations[i].Path != mutations[j].Path {
			return mutations[i].Path < mutations[j].Path
		}
		return mutations[i].Action < mutations[j].Action
	})
	if mutations == nil {
		mutations = make([]PublicationMutation, 0)
	}
	return mutations, nil
}

func canonicalPublicationGitMode(mode string) (string, bool) {
	switch mode {
	case "100644", "100755":
		return mode, true
	default:
		return "", false
	}
}

func clonePublishedContract(input PublishedContract) PublishedContract {
	clone := PublishedContract{
		Version: input.Version, DescriptorRaw: bytes.Clone(input.DescriptorRaw), Set: cloneCarriedSet(input.Set),
		Modes: make(map[string]string, len(input.Modes)),
	}
	for path, mode := range input.Modes {
		clone.Modes[path] = mode
	}
	return clone
}

func cloneCarriedSet(input CarriedSet) CarriedSet {
	clone := input
	clone.Descriptor = cloneDescriptor(input.Descriptor)
	clone.Entries = append([]Entry(nil), input.Entries...)
	if clone.Entries == nil {
		clone.Entries = make([]Entry, 0)
	}
	clone.PerFileDigest = make(map[string]string, len(input.PerFileDigest))
	for path, digest := range input.PerFileDigest {
		clone.PerFileDigest[path] = digest
	}
	clone.Bytes = cloneBytesByPath(input.Bytes)
	return clone
}

func cloneBytesByPath(input map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(input))
	for path, raw := range input {
		clone[path] = bytes.Clone(raw)
	}
	return clone
}

func cloneFindings(input []Finding) []Finding {
	clone := append([]Finding(nil), input...)
	if clone == nil {
		clone = make([]Finding, 0)
	}
	sort.Slice(clone, func(i, j int) bool {
		if clone[i].Code != clone[j].Code {
			return clone[i].Code < clone[j].Code
		}
		if clone[i].Path != clone[j].Path {
			return clone[i].Path < clone[j].Path
		}
		return clone[i].Message < clone[j].Message
	})
	return clone
}
