package livee2e

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/contractroots"
)

// contractroles.go wires universe 1 (answers-that-hold-2026-08 spec 10
// §"The input universes", row 1) to the reader↔reader assertion shape:
// declared roles × the path grammar each role legally takes, DERIVED from
// the descriptor schema itself (testkit/contractroots.Declared), never a
// second hand-written role/root table — exactly the shape spec 10
// §"What the re-verification found" corrected universe 1 to (role is a
// correlated proxy; the causal axis is path shape) for `fb-20260806-c6ad38`,
// `fb-20260808-5c73a9` and `fb-20260820-d1e370`.
//
// Deliberately covers the PATH-GRAMMAR half of universe 1's own row only
// ("roles × the path grammar and media type each role legally takes").
// contractroots.Root — the schema-derived type this wave reads and does not
// edit — carries no media-type rule; adding a second extractor for it would
// touch testkit/contractroots, which internal/contract/roots_gate_test.go
// and internal/e2e/contract_roots_test.go already share and this wave's
// allowlist does not include. Named as a gap rather than stretched to cover
// silently (this phase's own idiom) — see the implementor's deviations note.

// contractRoleRootCells is universe 1's own cross-product. roots and
// declaredRoles are PARAMETERS, never read from the embedded schema by this
// function itself — the production caller passes contractroots.Declared(t)'s
// real answer; a fixture test (AC-10/AC-14) passes a synthetic Root/role
// value the real descriptor schema does not yet carry, to prove this
// function reacts to whatever the schema publishes with no edit to this
// file.
//
// For every role, it resolves the ROOT the schema pins for that role and
// builds a synthetic contract-companion path under it, then asks whether
// internal/space.ContractForPath — the exact site fb-20260806-c6ad38 broke
// ("This function did not know the directory existed", layout.go's own doc
// comment) — agrees the path belongs to the contract. Two independent
// readers of one question ("does this role's own legal directory belong to
// the contract?"): the schema's role-conditional path pattern
// (contractroots), and the space package's own path classifier
// (ContractForPath). A role added to a NEW root arm the schema declares but
// ContractForPath's switch does not yet admit reproduces exactly
// c6ad38's own shape — red, naming both readers — with no change to this
// file: it is the caller's roots/declaredRoles argument that grows.
func contractRoleRootCells(roots []contractroots.Root, declaredRoles []string) (evaluated []string, errs []error) {
	rootForRole := make(map[string]string, len(declaredRoles))
	for _, r := range roots {
		for _, role := range r.Roles {
			rootForRole[role] = r.Root
		}
	}
	for _, role := range declaredRoles {
		evaluated = append(evaluated, role)
		root, ok := rootForRole[role]
		if !ok {
			errs = append(errs, fmt.Errorf(
				"role %q: no root arm constrains it — contractroots.Declared's own completeness "+
					"check should have refused this before returning, so this is unreachable unless "+
					"that check regressed", role))
			continue
		}
		candidate := fmt.Sprintf("systema/provides/example-contract/%s/probe.bin", root)
		if _, _, ok := space.ContractForPath(candidate); !ok {
			errs = append(errs, fmt.Errorf(
				"role %q: the descriptor schema pins root %q, but space.ContractForPath does not "+
					"recognise %q as belonging to the contract — two readers of one question disagree "+
					"(fb-20260806-c6ad38's own shape)", role, root, candidate))
		}
	}
	return evaluated, errs
}
