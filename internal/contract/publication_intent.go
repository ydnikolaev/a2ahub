package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

// InventoryMode freezes which candidate-intent record shape was used before
// authoritative floor and publication-profile selection.
// InventoryMode is part of the public package API.
type InventoryMode string

const (
	// InventoryDeclaredV2 is part of the public package API.
	InventoryDeclaredV2 InventoryMode = "declared-v2"
	// InventoryLegacyFixedV1 is part of the public package API.
	InventoryLegacyFixedV1 InventoryMode = "legacy-fixed-v1"
)

// CandidateIntentSnapshot is a detached immutable candidate. Its digest is a
// semantic lookup identity, never an integrity or completion proof. Accessors
// return clones so a collector, checker, or transport cannot change the bytes
// later consumed by publication planning.
// CandidateIntentSnapshot is part of the public package API.
type CandidateIntentSnapshot struct {
	digest          string
	canonical       []byte
	inventoryMode   InventoryMode
	contractID      string
	currentVersion  string
	descriptor      Descriptor
	snapshot        CandidateSnapshot
	exportSource    DigestProjection
	hasExportSource bool
}

// Digest is part of the public package API.
func (s CandidateIntentSnapshot) Digest() string { return s.digest }

// InventoryMode is part of the public package API.
func (s CandidateIntentSnapshot) InventoryMode() InventoryMode { return s.inventoryMode }

// ContractID is part of the public package API.
func (s CandidateIntentSnapshot) ContractID() string { return s.contractID }

// CurrentVersion is part of the public package API.
func (s CandidateIntentSnapshot) CurrentVersion() string { return s.currentVersion }

// Descriptor is part of the public package API.
func (s CandidateIntentSnapshot) Descriptor() Descriptor { return cloneDescriptor(s.descriptor) }

// CanonicalBytes is part of the public package API.
func (s CandidateIntentSnapshot) CanonicalBytes() []byte { return bytes.Clone(s.canonical) }

// Snapshot is part of the public package API.
func (s CandidateIntentSnapshot) Snapshot() CandidateSnapshot {
	return cloneCandidateSnapshot(s.snapshot)
}

// ExportSource returns the export-source-v1 projection this intent carries,
// computed once here from the CANDIDATE-side declared-v2 carried set at
// intent-build time — before target version, floor, or finalized
// source_digest exist. This is spec §10's required shape: computing the
// projection from the FINALIZED descriptor is circular (the digest would
// feed the finalized descriptor, and the finalized descriptor feeds the
// set), so the planner reads this carried value instead of re-deriving one
// from the descriptor's inventory and raw bytes a second time. The second
// return is false for a legacy (non-declared-v2) candidate, which has no
// export-source-v1 projection to carry.
// ExportSource is part of the public package API.
func (s CandidateIntentSnapshot) ExportSource() (DigestProjection, bool) {
	return s.exportSource, s.hasExportSource
}

type candidateIntentRecord struct {
	Profile       string                 `json:"profile"`
	InventoryMode InventoryMode          `json:"inventory_mode"`
	Descriptor    map[string]any         `json:"descriptor"`
	MarkdownBody  string                 `json:"markdown_body"`
	Entries       []candidateIntentEntry `json:"entries"`
}

type candidateIntentEntry struct {
	Path     string                         `json:"path"`
	Declared *candidateIntentDeclaredFields `json:"declared,omitempty"`
	Digest   string                         `json:"digest"`
}

type candidateIntentDeclaredFields struct {
	Role       Role   `json:"role"`
	Normative  bool   `json:"normative"`
	MediaType  string `json:"media_type"`
	ConformsTo string `json:"conforms_to"`
}

// BuildCandidateIntent validates and freezes one bounded candidate before any
// baseline, floor, schema/profile, target, template, actor, or clock lookup.
// YAML map presentation, publish-generated version/source digest, actor/clock,
// and source location are deliberately absent from the canonical record.
// Markdown line endings and terminal blank lines are normalized, while every
// other body byte remains semantic. Declared artifacts retain descriptor order.
// BuildCandidateIntent is part of the public package API.
func BuildCandidateIntent(input CandidateSnapshot) (CandidateIntentSnapshot, []Issue) {
	snapshot := cloneCandidateSnapshot(input)
	issues := validateDescriptorCandidate(snapshot.Descriptor)
	validateText(DescriptorPath, snapshot.Descriptor.Raw, &issues)
	if len(issues) != 0 {
		sortIssues(issues)
		return CandidateIntentSnapshot{}, issues
	}

	fm, err := artifact.ParseFrontmatter(snapshot.Descriptor.Raw)
	if err != nil {
		return CandidateIntentSnapshot{}, []Issue{{
			Kind: IssueDescriptorMismatch, Path: DescriptorPath,
			Detail: "candidate descriptor frontmatter cannot be parsed",
		}}
	}
	var semantic map[string]any
	if err := yaml.Unmarshal(fm.YAML, &semantic); err != nil || semantic == nil {
		return CandidateIntentSnapshot{}, []Issue{{
			Kind: IssueDescriptorMismatch, Path: DescriptorPath,
			Detail: "candidate descriptor frontmatter must decode to an object",
		}}
	}

	contractID, _ := semantic["id"].(string)
	currentVersion, _ := semantic["version"].(string)
	// The mode predicate reads semantic BEFORE the deletes below remove the
	// `artifacts` key it inspects.
	mode := inventoryModeFromSemantic(semantic)
	delete(semantic, "version")
	delete(semantic, "artifacts")
	delete(semantic, "actor")
	delete(semantic, "created")
	removeGeneratedSourceDigest(semantic)

	descriptor, err := ParseDescriptor(snapshot.Descriptor.Raw)
	if err != nil {
		return CandidateIntentSnapshot{}, []Issue{{
			Kind: IssueDescriptorMismatch, Path: DescriptorPath,
			Detail: "candidate descriptor cannot be decoded into the contract projection",
		}}
	}

	profile := SelectDigestProfile(mode)
	var set CarriedSet
	if mode == InventoryDeclaredV2 {
		set, issues = BuildCarriedSet(profile, snapshot.Descriptor.Raw, descriptor, snapshot.Files)
	} else {
		set, issues = BuildCarriedSet(profile, nil, Descriptor{}, snapshot.Files)
	}
	if len(issues) != 0 {
		return CandidateIntentSnapshot{}, issues
	}

	var exportSource DigestProjection
	hasExportSource := false
	if mode == InventoryDeclaredV2 {
		projection, exportErr := set.ExportSource()
		if exportErr != nil {
			return CandidateIntentSnapshot{}, []Issue{{
				Kind: IssueUnsupportedProfile, Path: "generated_from.source_digest",
				Detail: exportErr.Error(),
			}}
		}
		exportSource = projection
		hasExportSource = true
	}

	entries := make([]candidateIntentEntry, 0, len(set.Entries))
	if mode == InventoryDeclaredV2 {
		for _, entry := range descriptor.Artifacts {
			entries = append(entries, candidateIntentEntry{
				Path: entry.Path,
				Declared: &candidateIntentDeclaredFields{
					Role: entry.Role, Normative: entry.Normative,
					MediaType: entry.MediaType, ConformsTo: entry.ConformsTo,
				},
				Digest: set.PerFileDigest[entry.Path],
			})
		}
	} else {
		for _, entry := range set.Entries {
			entries = append(entries, candidateIntentEntry{
				Path: entry.Path, Digest: set.PerFileDigest[entry.Path],
			})
		}
	}
	if entries == nil {
		entries = make([]candidateIntentEntry, 0)
	}

	record := candidateIntentRecord{
		Profile:       "contract-candidate-intent-v1",
		InventoryMode: mode,
		Descriptor:    semantic,
		MarkdownBody:  normalizeMarkdownBody(fm.Body),
		Entries:       entries,
	}
	canonical, err := json.Marshal(record)
	if err != nil {
		return CandidateIntentSnapshot{}, []Issue{{
			Kind: IssueDescriptorMismatch, Path: DescriptorPath,
			Detail: fmt.Sprintf("candidate descriptor semantics cannot be canonically encoded: %v", err),
		}}
	}

	frozenFiles := make([]CandidateFile, 0, len(set.Bytes))
	paths := make([]string, 0, len(set.Bytes))
	for path := range set.Bytes {
		if path != DescriptorPath {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		frozenFiles = append(frozenFiles, CandidateFile{
			Path: path, Kind: CandidateRegular, Raw: bytes.Clone(set.Bytes[path]),
		})
	}

	return CandidateIntentSnapshot{
		digest:          artifact.Digest(canonical),
		canonical:       bytes.Clone(canonical),
		inventoryMode:   mode,
		contractID:      contractID,
		currentVersion:  currentVersion,
		descriptor:      cloneDescriptor(descriptor),
		exportSource:    exportSource,
		hasExportSource: hasExportSource,
		snapshot: CandidateSnapshot{
			Descriptor: CandidateFile{
				Path: DescriptorPath, Kind: CandidateRegular,
				Raw: bytes.Clone(snapshot.Descriptor.Raw),
			},
			Files: frozenFiles,
		},
	}, nil
}

// inventoryModeFromSemantic is the ONE predicate deciding whether a
// candidate descriptor is declared-v2 or legacy-fixed-v1: whether the raw
// frontmatter's `artifacts` KEY is present, never whether its decoded value
// is non-empty. An explicit `artifacts: []` is still a declared-v2
// candidate — BuildCarriedSet's own IssueTooFewEntries catches an empty
// inventory as a content defect, which is a different question from which
// inventory MODE this is.
func inventoryModeFromSemantic(semantic map[string]any) InventoryMode {
	if _, declared := semantic["artifacts"]; declared {
		return InventoryDeclaredV2
	}
	return InventoryLegacyFixedV1
}

// DetectInventoryMode applies the SAME predicate BuildCandidateIntent uses
// internally to a caller that holds only raw descriptor bytes and no
// CandidateIntentSnapshot of its own — criterion 9's second caller
// (verify-export's local-candidate check has no InventoryMode to read,
// unlike the publisher, which always builds one through
// BuildCandidateIntent). This is the one place that predicate lives; a
// caller re-deriving "declared" some other way (e.g. `len(Artifacts) > 0`,
// which diverges on an explicit `artifacts: []`) would be a second,
// possibly-drifted definition of the same question — the exact class this
// epic exists to close, reintroduced inside its own fix.
// DetectInventoryMode is part of the public package API.
func DetectInventoryMode(descriptorRaw []byte) (InventoryMode, error) {
	fm, err := artifact.ParseFrontmatter(descriptorRaw)
	if err != nil {
		return "", fmt.Errorf("contract: descriptor frontmatter cannot be parsed: %w", err)
	}
	var semantic map[string]any
	if err := yaml.Unmarshal(fm.YAML, &semantic); err != nil || semantic == nil {
		return "", fmt.Errorf("contract: descriptor frontmatter must decode to an object")
	}
	return inventoryModeFromSemantic(semantic), nil
}

func removeGeneratedSourceDigest(descriptor map[string]any) {
	generated, ok := descriptor["generated_from"].(map[string]any)
	if !ok {
		return
	}
	delete(generated, "source_digest")
}

func normalizeMarkdownBody(raw []byte) string {
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.TrimRight(normalized, "\n")
	if normalized != "" {
		normalized += "\n"
	}
	return normalized
}

func cloneCandidateSnapshot(snapshot CandidateSnapshot) CandidateSnapshot {
	clone := CandidateSnapshot{
		Descriptor: CandidateFile{
			Path: snapshot.Descriptor.Path,
			Kind: snapshot.Descriptor.Kind,
			Raw:  bytes.Clone(snapshot.Descriptor.Raw),
		},
		Files: make([]CandidateFile, len(snapshot.Files)),
	}
	for i, file := range snapshot.Files {
		clone.Files[i] = CandidateFile{Path: file.Path, Kind: file.Kind, Raw: bytes.Clone(file.Raw)}
	}
	if clone.Files == nil {
		clone.Files = make([]CandidateFile, 0)
	}
	return clone
}
