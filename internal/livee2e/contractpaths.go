package livee2e

import (
	"fmt"
	"path"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// liveContractPaths derives every contract-baseline path from the contract ID
// returned by the product. Live scenarios scope standing slugs to the current
// destructive-space generation, so recomputing paths from a scenario's base
// slug can silently address a previous generation.
type liveContractPaths struct {
	ContractRoot    string
	StagingRoot     string
	SchemaFile      string
	ValidFixtureKey string
}

// contractPublishVersionArgs keeps the two honest publication sources
// explicit. A non-empty path selects a complete materialized candidate
// (including its own contract.md), never contract-new's transient sidecar
// directory by accident.
//
// An empty staging path publishes the already-landed contract — but ONLY
// where no staging tree exists for that contract. Since answers-that-hold
// P2, a publication that finds one while no --staging was given is REFUSED
// (internal/contractwiring/candidate.go) instead of silently falling back to
// the mirror, which is how the previous version's bytes shipped under a new
// number. `a2a new contract` writes such a tree and nothing removes it, so
// the empty-path branch is reachable only after the tree is gone.
func contractPublishVersionArgs(contractID, version, staging string) []string {
	args := []string{"contract", "publish", contractID, "--version", version}
	if staging != "" {
		args = append(args, "--staging", staging)
	}
	return args
}

func contractPathsForID(contractID string) (liveContractPaths, error) {
	parsed, err := artifact.ParseID(contractID)
	if err != nil {
		return liveContractPaths{}, fmt.Errorf("livee2e: parse contract id %q: %w", contractID, err)
	}
	if parsed.Class != artifact.ClassStanding || parsed.Prefix != "XC" {
		return liveContractPaths{}, fmt.Errorf("livee2e: %q is not a standing contract id", contractID)
	}
	layout, err := space.NewLayout(parsed.System)
	if err != nil {
		return liveContractPaths{}, fmt.Errorf("livee2e: contract layout for %q: %w", contractID, err)
	}
	contractRoot := path.Dir(layout.ProvidesContract(parsed.Slug))
	return liveContractPaths{
		ContractRoot:    contractRoot,
		StagingRoot:     path.Join(".a2a", "staging", contractRoot),
		SchemaFile:      path.Join(layout.ProvidesSchemaDir(parsed.Slug), parsed.Slug+".schema.json"),
		ValidFixtureKey: path.Join("fixtures", "valid", parsed.Slug+".json"),
	}, nil
}
