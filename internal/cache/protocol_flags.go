package cache

import (
	"context"
	"fmt"
	"sort"
)

// ProtocolFlags returns every unique non-fatal fold violation found while
// rebuilding the connected spaces. These are committed-history facts, not
// V1/V2/V3 validation results: the offending event stays in history and fold
// reports why it did not apply cleanly.
func (s *Store) ProtocolFlags(ctx context.Context) ([]ProtocolFlagInfo, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return nil, err
	}

	var out []ProtocolFlagInfo
	seen := map[string]bool{}
	for spaceID, artifacts := range idx {
		for _, fa := range artifacts {
			for _, flag := range fa.Result.Flags {
				key := spaceID + "|" + string(flag.Kind) + "|" + flag.Subject + "|" + flag.EventULID
				if seen[key] {
					continue
				}
				seen[key] = true
				system := fa.Env.From
				for _, event := range fa.Events {
					if event.ULID == flag.EventULID {
						system = event.Actor.System
						break
					}
				}
				out = append(out, ProtocolFlagInfo{
					Space: spaceID, System: system, Artifact: flag.Subject,
					Code: string(flag.Kind), EventULID: flag.EventULID,
					Message: fmt.Sprintf(
						"committed event %s on %s was folded with %s",
						flag.EventULID, flag.Subject, flag.Kind,
					),
					Severity: "attention",
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Space != out[j].Space {
			return out[i].Space < out[j].Space
		}
		if out[i].Artifact != out[j].Artifact {
			return out[i].Artifact < out[j].Artifact
		}
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].EventULID < out[j].EventULID
	})
	if out == nil {
		out = []ProtocolFlagInfo{}
	}
	return out, nil
}
