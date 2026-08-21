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
	return respondOperationWithFields(system, parentID, result, nil, nil)
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
func respondOperationWithFields(system, parentID, result string, fields map[string]string, delivers []string) (key, branch string) {
	key = operation.Respond(
		system,
		liveRespondActorKind,
		liveRespondActorName,
		[]string{parentID},
		result,
		fields,
		// refs: still nil, and now for a NARROWER reason than the one this
		// line used to carry until 2026-08-21 — that the catalogue drove
		// none of respond's extra flags at all. judge-the-thing-2026-08
		// P1's declared path
		// (response-delivers-unlanded-package-refused,
		// pathcatalogue_paths.go) DOES drive `a2a respond` with an extra
		// flag through the real binary — `--delivers <DP-id>` — so the
		// refusal it earns (REF-024, internal/space's
		// checkResponseDeliversPossession) is no longer a rule that only a
		// unit test executes, which is the epic's own AC2.
		//
		// `delivers` IS in this derivation as of 2026-08-21. P1 recorded the
		// residue here — "when key.go gains `delivers`, this parameter list
		// gains it too, on the same day" — and the lead closed it that day:
		// two responses on one parent differing only in --delivers derived
		// ONE key, so the second collapsed onto the first's dedup branch and
		// vanished. Today every declared path that drives --delivers drives a
		// REFUSAL, which never reaches a branch, so both live callers pass
		// nil; the parameter exists so that a path driving a SUCCESSFUL
		// --delivers respond derives the branch the CLI derives instead of
		// silently resolving the wrong one.
		//
		// The `--ref` flag itself remains undriven by the catalogue; only
		// `--delivers` gained a path here.
		nil,
		nil,
		// No declared path drives --unmet/--standing/--blocked-by yet either;
		// the zero value encodes to nothing, so this key stays byte-identical
		// to the one every historical run derived.
		operation.RespondIncompleteness{},
		delivers,
	)
	return key, space.BranchName(system, "respond", key)
}

// respondCommandArgs builds the real CLI invocation. extraArgs carries any
// additional respond flags a path declares — `--delivers <DP-id>` is the
// first one a declared path drives (judge-the-thing-2026-08 P1) — and is
// written BEFORE the parent id, matching every other flag here; the command
// parses flags in any order (parseArgsAnyOrder, cmd_lifecycle.go), so the
// position is a readability choice, not a requirement. A caller that passes
// none produces the exact argv every historical run produced.
func respondCommandArgs(parentID, result string, extraArgs ...string) []string {
	args := []string{
		"respond",
		"--result", result,
		"--actor-kind", liveRespondActorKind,
		"--actor-name", liveRespondActorName,
	}
	args = append(args, extraArgs...)
	return append(args, parentID)
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
