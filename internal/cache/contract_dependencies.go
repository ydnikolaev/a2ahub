package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

const (
	DependencyDriftCurrent    = "current"
	DependencyDriftBehind     = "behind"
	DependencyDriftDeprecated = "deprecated"
	DependencyDriftRetired    = "retired"
	DependencyDriftMissing    = "missing"
	DependencyDriftDangling   = "dangling"

	DependencyDegradationUnreadableRegistry   = "unreadable-registry"
	DependencyDegradationMalformedRegistry    = "malformed-registry"
	DependencyDegradationIncompleteRegistry   = "incomplete-registry"
	DependencyDegradationIncompleteDependency = "incomplete-dependency"
)

// ContractDependencyEdge is one neutral consumer-to-provider contract line.
// It contains cache-owned version/drift facts and no presentation language;
// P7 maps it mechanically into the dashboard model.
type ContractDependencyEdge struct {
	From            string
	To              string
	Space           string
	Contract        string
	PinnedMajor     int
	PinnedVersion   string
	PinnedState     string
	ProviderVersion string
	State           string
	AvailableMajors []int
	Drift           string
	Sunset          string
	Successor       string
	Description     string
}

// ContractDependencyDegradation names one registry input that could not be
// included honestly. Code is stable machine vocabulary; Reason retains the
// bounded/strict reader's diagnostic detail.
type ContractDependencyDegradation struct {
	Space  string
	System string
	Path   string
	Code   string
	Reason string
}

// ContractDependencyProjection is the complete readable edge set plus proof
// of whether it is a complete scan. Missing consumes.yaml is the registry
// contract's normal empty declaration. Any unreadable, malformed, identity-
// mismatched, path-unsafe, or incomplete present input sets Complete false and
// appears in Degradations instead of disappearing silently.
type ContractDependencyProjection struct {
	Edges        []ContractDependencyEdge
	Complete     bool
	Degradations []ContractDependencyDegradation
}

// ContractDependencies rebuilds dependency drift from the current mirror and
// contract projections. Registry errors degrade the returned projection; an
// error is reserved for failure to build cache's canonical contract index.
func (s *Store) ContractDependencies(ctx context.Context) (ContractDependencyProjection, error) {
	contracts, err := s.Contracts(ctx, "")
	if err != nil {
		return ContractDependencyProjection{}, fmt.Errorf("cache: contract dependencies: contracts: %w", err)
	}
	return projectContractDependencies(s.spaceMirrorsSnapshot(), contracts), nil
}

type dependencyContractKey struct {
	space string
	id    string
}

func projectContractDependencies(mirrors []SpaceMirror, contracts []ContractInfo) ContractDependencyProjection {
	projection := ContractDependencyProjection{
		Edges:        []ContractDependencyEdge{},
		Degradations: []ContractDependencyDegradation{},
	}
	contractByID := make(map[dependencyContractKey]ContractInfo, len(contracts))
	for _, contract := range contracts {
		contractByID[dependencyContractKey{space: contract.Space, id: contract.ID}] = contract
	}

	orderedMirrors := append([]SpaceMirror(nil), mirrors...)
	sort.Slice(orderedMirrors, func(i, j int) bool {
		if orderedMirrors[i].SpaceID != orderedMirrors[j].SpaceID {
			return orderedMirrors[i].SpaceID < orderedMirrors[j].SpaceID
		}
		return orderedMirrors[i].Dir < orderedMirrors[j].Dir
	})
	for _, mirror := range orderedMirrors {
		statuses := make(map[string]string, len(mirror.Manifest.Participants))
		participants := append([]space.Participant(nil), mirror.Manifest.Participants...)
		for _, participant := range participants {
			statuses[participant.System] = participant.Status
		}
		sort.Slice(participants, func(i, j int) bool {
			if participants[i].System != participants[j].System {
				return participants[i].System < participants[j].System
			}
			return participants[i].Section < participants[j].Section
		})

		for _, participant := range participants {
			section, valid := safeDependencySection(participant.Section)
			rel := filepath.ToSlash(filepath.Join(participant.Section, "consumes.yaml"))
			if !valid {
				projection.Degradations = append(projection.Degradations, ContractDependencyDegradation{
					Space: mirror.SpaceID, System: participant.System, Path: rel,
					Code:   DependencyDegradationIncompleteRegistry,
					Reason: "participant section is empty, absolute, or escapes the mirror",
				})
				continue
			}
			path := filepath.Join(mirror.Dir, section, "consumes.yaml")
			raw, err := readBounded(path, maxCacheReadBytes)
			if err != nil {
				if os.IsNotExist(err) {
					continue // consumes.yaml is optional: absence declares no dependencies
				}
				projection.Degradations = append(projection.Degradations, ContractDependencyDegradation{
					Space: mirror.SpaceID, System: participant.System, Path: rel,
					Code: DependencyDegradationUnreadableRegistry, Reason: err.Error(),
				})
				continue
			}
			registry, err := parseConsumesStrict(raw, rel)
			if err != nil {
				projection.Degradations = append(projection.Degradations, ContractDependencyDegradation{
					Space: mirror.SpaceID, System: participant.System, Path: rel,
					Code: DependencyDegradationMalformedRegistry, Reason: err.Error(),
				})
				continue
			}
			if registry.System != participant.System {
				projection.Degradations = append(projection.Degradations, ContractDependencyDegradation{
					Space: mirror.SpaceID, System: participant.System, Path: rel,
					Code:   DependencyDegradationIncompleteRegistry,
					Reason: fmt.Sprintf("registry system %q does not match participant %q", registry.System, participant.System),
				})
				continue
			}

			for index, dependency := range registry.Dependencies {
				provider, valid := dependencyProvider(dependency.Contract)
				if !valid || dependency.Major <= 0 {
					projection.Degradations = append(projection.Degradations, ContractDependencyDegradation{
						Space: mirror.SpaceID, System: participant.System, Path: rel,
						Code:   DependencyDegradationIncompleteDependency,
						Reason: fmt.Sprintf("dependency[%d] needs an XC-<provider>-<slug> contract id and a positive major", index),
					})
					continue
				}
				edge := ContractDependencyEdge{
					From: participant.System, To: provider, Space: mirror.SpaceID,
					Contract: dependency.Contract, PinnedMajor: dependency.Major,
					AvailableMajors: []int{},
				}
				contract, found := contractByID[dependencyContractKey{space: mirror.SpaceID, id: dependency.Contract}]
				if !found || statuses[provider] == "left" {
					edge.Drift = DependencyDriftDangling
				} else {
					edge.ProviderVersion = contract.Version
					edge.State = contract.State
					edge.Description = contract.Description
					edge.PinnedVersion, edge.PinnedState, edge.Sunset, edge.Successor,
						edge.AvailableMajors, edge.Drift = contractDependencyFacts(contract, dependency.Major)
				}
				projection.Edges = append(projection.Edges, edge)
			}
		}
	}

	sort.Slice(projection.Edges, func(i, j int) bool {
		left, right := projection.Edges[i], projection.Edges[j]
		if left.Space != right.Space {
			return left.Space < right.Space
		}
		if left.Contract != right.Contract {
			return left.Contract < right.Contract
		}
		if left.From != right.From {
			return left.From < right.From
		}
		return left.PinnedMajor < right.PinnedMajor
	})
	sort.Slice(projection.Degradations, func(i, j int) bool {
		left, right := projection.Degradations[i], projection.Degradations[j]
		if left.Space != right.Space {
			return left.Space < right.Space
		}
		if left.System != right.System {
			return left.System < right.System
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Reason < right.Reason
	})
	projection.Complete = len(projection.Degradations) == 0
	return projection
}

func safeDependencySection(section string) (string, bool) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(section), "/")
	clean := filepath.Clean(filepath.FromSlash(trimmed))
	if trimmed == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

func dependencyProvider(contractID string) (string, bool) {
	parts := strings.SplitN(contractID, "-", 3)
	if len(parts) != 3 || parts[0] != "XC" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	return parts[1], true
}

func contractDependencyFacts(contract ContractInfo, pinnedMajor int) (
	pinnedVersion, pinnedState, sunset, successor string,
	availableMajors []int, drift string,
) {
	seenMajors := map[int]bool{}
	var pinned *ContractVersion
	for index := range contract.Versions {
		candidate := &contract.Versions[index]
		major, err := version.Major(candidate.Version)
		if err != nil {
			continue
		}
		if !seenMajors[major] {
			seenMajors[major] = true
			availableMajors = append(availableMajors, major)
		}
		if major == pinnedMajor {
			pinned = candidate
		}
	}
	sort.Ints(availableMajors)
	if pinned == nil {
		if len(contract.Versions) == 0 {
			return "", contract.State, "", "", availableMajors,
				contractDependencyDrift(contract.State, pinnedMajor, contract.Version)
		}
		return "", DependencyDriftMissing, "", "", availableMajors, DependencyDriftMissing
	}

	pinnedVersion, pinnedState = pinned.Version, pinned.State
	sunset, successor = pinned.Sunset, pinned.Successor
	switch pinned.State {
	case DependencyDriftRetired:
		drift = DependencyDriftRetired
	case DependencyDriftDeprecated:
		drift = DependencyDriftDeprecated
	default:
		drift = contractDependencyDrift(pinned.State, pinnedMajor, contract.Version)
	}
	return pinnedVersion, pinnedState, sunset, successor, availableMajors, drift
}

func contractDependencyDrift(state string, pinnedMajor int, providerVersion string) string {
	switch state {
	case DependencyDriftRetired:
		return DependencyDriftRetired
	case DependencyDriftDeprecated:
		return DependencyDriftDeprecated
	}
	providerMajor, err := version.Major(providerVersion)
	if err == nil && providerMajor > pinnedMajor {
		return DependencyDriftBehind
	}
	return DependencyDriftCurrent
}
