package livee2e

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/contractroots"
)

// TestContractRoleRootCellsAgreeOnHEAD is universe 1's own live cell,
// production shape: the REAL descriptor schema's own roles/roots
// (contractroots.Declared, never a second hand-written table here) crossed
// against space.ContractForPath. Green on HEAD: c6ad38 is fixed
// (layout.go's `artifacts` arm), so every declared role's own root is one
// ContractForPath already recognises.
func TestContractRoleRootCellsAgreeOnHEAD(t *testing.T) {
	roots, declaredRoles := contractroots.Declared(t)
	if len(declaredRoles) == 0 {
		t.Fatal("contractroots.Declared(t) returned no roles — nothing to cross")
	}
	evaluated, errs := contractRoleRootCells(roots, declaredRoles)
	if len(evaluated) != len(declaredRoles) {
		t.Fatalf("evaluated %d roles, want %d (one cell per declared role)", len(evaluated), len(declaredRoles))
	}
	if len(errs) != 0 {
		t.Fatalf("contractRoleRootCells disagreed on HEAD: %v", errs)
	}
	t.Logf("crossed %d declared roles against space.ContractForPath, all agree", len(evaluated))
}

// TestContractRoleRootCellsAreDerivedFromSchema is AC-10/AC-14 at the
// mechanism level: contractRoleRootCells reacts to whatever roots/
// declaredRoles it is given — a FIXTURE value here, never the real
// descriptor schema edited, and never a second role list maintained inside
// internal/livee2e — proving the cross-product is driven by its PARAMETERS,
// not a literal this file carries.
//
// The fixture role's root ("a-root-the-real-schema-does-not-declare-yet")
// is deliberately one space.ContractForPath cannot recognise — this proves
// the CELL is constructed (evaluated grows) with no Go edit, and that an
// unrecognised root reds naming the new role, exactly c6ad38's own shape
// reproduced for a value the schema does not yet publish.
func TestContractRoleRootCellsAreDerivedFromSchema(t *testing.T) {
	fixtureRoots := []contractroots.Root{
		{Root: "schema", Roles: []string{"schema"}},
		{Root: "a-root-the-real-schema-does-not-declare-yet", Roles: []string{"a-role-the-real-schema-does-not-declare-yet"}},
	}
	fixtureRoles := []string{"schema", "a-role-the-real-schema-does-not-declare-yet"}

	evaluated, errs := contractRoleRootCells(fixtureRoots, fixtureRoles)
	if len(evaluated) != 2 {
		t.Fatalf("evaluated %v, want 2 cells (one per fixture role)", evaluated)
	}
	if !verbAgreementContainsString(evaluated, "a-role-the-real-schema-does-not-declare-yet") {
		t.Fatalf("evaluated %v does not include the fixture role — the cross-product is not reacting to its own parameter", evaluated)
	}
	if len(errs) == 0 {
		t.Fatal("expected the fixture role's unrecognised root to red — a cell that always passes is a cell nothing drove")
	}
	t.Logf("fixture role reds as expected (schema grew, no Go edit): %v", errs)
}

// TestContractRoleRootCellsRefusesAnUnresolvedRole guards
// contractRoleRootCells' own defensive branch: a role named in declaredRoles
// with no entry in roots (contractroots.Declared's own completeness check
// should make this unreachable in production, but this function's own
// contract must still hold for a caller that hands it an inconsistent pair).
func TestContractRoleRootCellsRefusesAnUnresolvedRole(t *testing.T) {
	evaluated, errs := contractRoleRootCells(nil, []string{"orphan-role"})
	if len(evaluated) != 1 {
		t.Fatalf("evaluated %v, want 1", evaluated)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly 1 (the unresolved-role refusal)", errs)
	}
}
