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
type InventoryMode string

const (
	InventoryDeclaredV2    InventoryMode = "declared-v2"
	InventoryLegacyFixedV1 InventoryMode = "legacy-fixed-v1"
)

// CandidateIntentSnapshot is a detached immutable candidate. Its digest is a
// semantic lookup identity, never an integrity or completion proof. Accessors
// return clones so a collector, checker, or transport cannot change the bytes
// later consumed by publication planning.
type CandidateIntentSnapshot struct {
	digest         string
	canonical      []byte
	inventoryMode  InventoryMode
	contractID     string
	currentVersion string
	descriptor     Descriptor
	snapshot       CandidateSnapshot
}

func (s CandidateIntentSnapshot) Digest() string               { return s.digest }
func (s CandidateIntentSnapshot) InventoryMode() InventoryMode { return s.inventoryMode }
func (s CandidateIntentSnapshot) ContractID() string           { return s.contractID }
func (s CandidateIntentSnapshot) CurrentVersion() string       { return s.currentVersion }
func (s CandidateIntentSnapshot) Descriptor() Descriptor       { return cloneDescriptor(s.descriptor) }
func (s CandidateIntentSnapshot) CanonicalBytes() []byte       { return bytes.Clone(s.canonical) }
func (s CandidateIntentSnapshot) Snapshot() CandidateSnapshot {
	return cloneCandidateSnapshot(s.snapshot)
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
	_, declared := semantic["artifacts"]
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

	mode := InventoryLegacyFixedV1
	var set CarriedSet
	if declared {
		mode = InventoryDeclaredV2
		set, issues = BuildCarriedSet(ProfileContractSetV2, snapshot.Descriptor.Raw, descriptor, snapshot.Files)
	} else {
		set, issues = BuildCarriedSet(ProfileContractTreeV1, nil, Descriptor{}, snapshot.Files)
	}
	if len(issues) != 0 {
		return CandidateIntentSnapshot{}, issues
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
		digest:         artifact.Digest(canonical),
		canonical:      bytes.Clone(canonical),
		inventoryMode:  mode,
		contractID:     contractID,
		currentVersion: currentVersion,
		descriptor:     cloneDescriptor(descriptor),
		snapshot: CandidateSnapshot{
			Descriptor: CandidateFile{
				Path: DescriptorPath, Kind: CandidateRegular,
				Raw: bytes.Clone(snapshot.Descriptor.Raw),
			},
			Files: frozenFiles,
		},
	}, nil
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
