package livee2e

import (
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// contractDeprecateOperation names the exact semantic-operation branch the
// product funnel uses. Contract deprecate used to branch on the sorted
// artifact IDs it touched; D6 moved it to an opaque operation key so retries
// across UTC midnight keep one identity. The live harness must call the same
// core derivation rather than keep matching the retired composite grammar.
func contractDeprecateOperation(system, contractID, version, successor, sunset string) (key, branch string) {
	key = operation.ContractDeprecate(system, contractID, version, successor, sunset)
	return key, space.BranchName(system, "contract-deprecate", key)
}

const (
	liveRespondActorKind = "agent"
	liveRespondActorName = "a2a-live-e2e-responder"
)

// respondOperation and respondCommandArgs are one pair by construction: the
// harness supplies an explicit stable actor to the real CLI, then derives the
// branch from those exact canonical inputs. Depending on ambient OS identity
// here would let the command and observer compute different operation keys.
func respondOperation(system, parentID, result string) (key, branch string) {
	key = operation.Respond(
		system,
		liveRespondActorKind,
		liveRespondActorName,
		[]string{parentID},
		result,
		nil,
		nil,
	)
	return key, space.BranchName(system, "respond", key)
}

func respondCommandArgs(parentID, result string) []string {
	return []string{
		"respond",
		"--result", result,
		"--actor-kind", liveRespondActorKind,
		"--actor-name", liveRespondActorName,
		parentID,
	}
}

// operationArtifactID resolves one generated artifact from the same
// funnel-owned PR-body metadata Submit uses for retry recovery. It deliberately
// does not scrape CLI prose or decode the opaque operation key.
func operationArtifactID(body, key, exclude, prefix string) (string, error) {
	gotKey, ids, ok := space.ParseOperationMetadata(body)
	if !ok {
		return "", fmt.Errorf("livee2e: pull request carries no valid a2a operation metadata")
	}
	if gotKey != key {
		return "", fmt.Errorf("livee2e: pull request operation key %q does not match expected %q", gotKey, key)
	}
	for _, id := range ids {
		if id != exclude && strings.HasPrefix(id, prefix) {
			return id, nil
		}
	}
	return "", fmt.Errorf("livee2e: operation %s carries no %s artifact distinct from %s", key, prefix, exclude)
}
