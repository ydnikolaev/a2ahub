package livee2e

import (
	"context"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// TestTemplatePlannerCellsAgreeOnHEAD is universe 3's own live cell,
// production shape: the REAL `a2a template list --json` / `a2a template
// show <type>` roster, driven in-process, crossed against the real
// publication planner. Green on HEAD: 3539ac is fixed (template.
// ShowGeneration prints the fresh-authoring generation), so the one
// eligible row (contract) shows exactly what the planner accepts at its
// own floor.
func TestTemplatePlannerCellsAgreeOnHEAD(t *testing.T) {
	entries, err := liveTemplateRosterEntries(context.Background())
	if err != nil {
		t.Fatalf("liveTemplateRosterEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("a2a template list --json returned no rows — nothing to cross")
	}

	evaluated, errs := templatePlannerCells(entries, contract.ContractPublicationFloor)
	if len(evaluated) == 0 {
		t.Fatal("templatePlannerCells constructed no cells at all — this gate is guarding nothing (measured HEAD: `contract` should always qualify)")
	}
	if !verbAgreementContainsString(evaluated, "contract") {
		t.Fatalf("evaluated %v does not include \"contract\" — contract is the one shipped type whose preview declares a top-level artifacts inventory", evaluated)
	}
	if len(errs) != 0 {
		t.Fatalf("templatePlannerCells disagreed on HEAD: %v", errs)
	}
	t.Logf("crossed %d template/planner cells (of %d roster rows), all agree", len(evaluated), len(entries))
}

// TestTemplatePlannerCellsAreDerivedFromTheRoster is AC-8/AC-14 at the
// mechanism level: templatePlannerCells reacts to whatever roster entries
// it is given — a FIXTURE row here, naming a template type the real binary
// does not ship, never a second template-name list maintained inside
// internal/livee2e — proving the cross-product is driven by its
// PARAMETER, not a literal this file carries. The fixture row's own show
// bytes declare an artifacts inventory (the SAME structural fact that
// selects `contract` on HEAD), so it is in scope, and its declared schema
// deliberately disagrees with what the planner requires at the given
// floor — reproducing 3539ac's own shape for a template the schema corpus
// does not yet publish.
func TestTemplatePlannerCellsAreDerivedFromTheRoster(t *testing.T) {
	const fixtureType = "a-template-the-real-binary-does-not-ship-yet"
	fixtureShow := []byte("---\n" +
		"schema: envelope/v1\n" +
		"schema_format: json-schema-2020-12\n" +
		"artifacts:\n" +
		"  - path: schema/x.json\n" +
		"    role: schema\n" +
		"---\nbody\n")

	entries := []templateRosterEntry{
		{Type: fixtureType, EnvelopeSchema: "envelope/v1", ShowBytes: fixtureShow},
	}
	evaluated, errs := templatePlannerCells(entries, contract.ContractPublicationFloor)
	if len(evaluated) != 1 || evaluated[0] != fixtureType {
		t.Fatalf("evaluated %v, want exactly [%q] — the cross-product is not reacting to its own parameter", evaluated, fixtureType)
	}
	if len(errs) == 0 {
		t.Fatal("expected the fixture row (v1, at/above the publication floor) to red — a cell that always passes is a cell nothing drove")
	}
	t.Logf("fixture template reds as expected (roster grew, no Go edit): %v", errs)
}

// TestTemplatePlannerCellsSkipsARowWithNoArtifactsInventory proves the
// derivation half: a roster row whose own preview declares NO top-level
// artifacts inventory (every shipped type except contract, measured at
// HEAD 2026-08-29) gets no cell at all — never a silent pass, never a
// hand-written exclusion naming the type.
func TestTemplatePlannerCellsSkipsARowWithNoArtifactsInventory(t *testing.T) {
	entries := []templateRosterEntry{
		{Type: "question", EnvelopeSchema: "envelope/v1", ShowBytes: []byte("---\ntype: question\n---\nbody\n")},
	}
	evaluated, errs := templatePlannerCells(entries, contract.ContractPublicationFloor)
	if len(evaluated) != 0 {
		t.Fatalf("evaluated %v, want none — this row declares no artifacts inventory, so it has no planner comparand", evaluated)
	}
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
}
