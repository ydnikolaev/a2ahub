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
	SchemaFile      string
	ValidFixtureKey string
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
	return liveContractPaths{
		SchemaFile:      path.Join(layout.ProvidesSchemaDir(parsed.Slug), parsed.Slug+".schema.json"),
		ValidFixtureKey: path.Join("fixtures", "valid", parsed.Slug+".json"),
	}, nil
}
