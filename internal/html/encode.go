package html

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// CanonicalViewModelJSON returns the compact, deterministic dashboard JSON
// used by both the static shell and the local dashboard route. encoding/json's
// default HTML escaping is intentional: the same bytes are safe to splice into
// the trusted script region of the shell.
func CanonicalViewModelJSON(data Data) ([]byte, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("html: marshal canonical view model: %w", err)
	}
	return encoded, nil
}

// ContentFingerprint identifies stable, non-operational dashboard facts.
// Operational facts are identified by Snapshot.Revision at the retaining
// transport. Display-only ages and observation times are cleared on an
// independent projection so every other field participates by default.
func ContentFingerprint(data Data) (string, error) {
	projection := stableFingerprintProjection(data)
	encoded, err := CanonicalViewModelJSON(projection)
	if err != nil {
		return "", fmt.Errorf("html: fingerprint view model: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

func stableFingerprintProjection(data Data) Data {
	projection := data
	projection.Operational = Data{}.Operational
	projection.GeneratedAt = Data{}.GeneratedAt
	projection.Tooling.CacheAge = ""

	projection.Spaces = append([]SpaceHealth(nil), data.Spaces...)
	for i := range projection.Spaces {
		projection.Spaces[i].SyncAge = ""
	}
	projection.Threads = append([]Thread(nil), data.Threads...)
	for i := range projection.Threads {
		projection.Threads[i].LastActivity = ""
	}
	projection.Inbox = stableFingerprintItems(data.Inbox)
	projection.Outbox = stableFingerprintItems(data.Outbox)
	projection.Archive = stableFingerprintItems(data.Archive)
	projection.Aggregates.NeedYou.Items = stableFingerprintItems(data.Aggregates.NeedYou.Items)
	projection.Aggregates.NoHumanMove.Items = stableFingerprintItems(data.Aggregates.NoHumanMove.Items)
	projection.Aggregates.YouAwait.Items = stableFingerprintItems(data.Aggregates.YouAwait.Items)
	projection.ExchangeEdges = append([]ExchangeEdge(nil), data.ExchangeEdges...)
	for i := range projection.ExchangeEdges {
		projection.ExchangeEdges[i].MaxStale = ""
	}
	projection.ArtifactDetails = append([]ArtifactDetail(nil), data.ArtifactDetails...)
	for i := range projection.ArtifactDetails {
		projection.ArtifactDetails[i].SyncAge = ""
	}
	return projection
}

func stableFingerprintItems(items []Item) []Item {
	projection := append([]Item(nil), items...)
	for i := range projection {
		projection[i].Age = ""
	}
	return projection
}
