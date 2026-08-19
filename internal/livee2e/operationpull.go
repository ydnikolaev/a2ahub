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
	return respondOperationWithFields(system, parentID, result, nil)
}

// respondOperationWithFields generalizes respondOperation with an explicit
// `fields` map — RespondCommand.Run's own operationKey (cmd_lifecycle.go)
// threads `--field` overrides into operation.Respond's own key derivation,
// so a harness call driving `a2a respond --field <k>=<v> ...` must mirror
// the SAME fields here or resolve the wrong branch entirely. Needed for the
// multi-response reconciliation paths (Family 14, pathcatalogue_paths.go):
// a plain `--result`-only SECOND respond call on the same parent would mint
// the IDENTICAL content-derived responseID as the first (RespondCommand.
// Run's own HIGH-1 fix-wave doc comment) and collapse onto its dedup
// branch instead of authoring a genuinely second response.
func respondOperationWithFields(system, parentID, result string, fields map[string]string) (key, branch string) {
	key = operation.Respond(
		system,
		liveRespondActorKind,
		liveRespondActorName,
		[]string{parentID},
		result,
		fields,
		nil, // refs: no declared path drives `respond --ref` yet
		nil,
		// No declared path drives --unmet/--standing/--blocked-by yet either;
		// the zero value encodes to nothing, so this key stays byte-identical
		// to the one every historical run derived.
		operation.RespondIncompleteness{},
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
