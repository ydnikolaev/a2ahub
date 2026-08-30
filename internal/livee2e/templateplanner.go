package livee2e

import (
	"context"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// templateplanner.go wires universe 3 (answers-that-hold-2026-08 spec 10
// §"The input universes", row 3) to the previewing->acting assertion shape:
// the template roster (`a2a template list --json` / `a2a template show
// <type>`'s own underlying answer, driven IN-PROCESS via internal/template
// — the exact functions internal/cli's TemplateCommand.Run itself calls,
// skipping only the flag-parsing/JSON-encoding layer around them — so this
// file runs on every `go test ./internal/livee2e/...` with no built binary
// and no per-run `go build`) crossed against the publication PLANNER
// (validate.CheckContractDescriptorShape — the same floor-aware rule `a2a
// contract preflight` and the merge gate both run, per its own doc comment:
// "the space's own publication planner"). Not docs<->template, which
// internal/e2e's TestAuthoringPagesMatchTheTemplatesTheyDocument already
// covers (spec 10: "nothing compares template<->planner, which is the pair
// that defect actually needed").
//
// This file used to drive `a2a template list --json` / `a2a template show`
// through internal/cli's own TemplateCommand — a real cross-layer import
// the never-built validator-backlog:478 rule (ADR-001's Rules paragraph:
// "core packages never import cli") caught once ADR-023 built it
// (docs/decisions.md). internal/template.Types/AuthoringEnvelopeSchema/
// ShowGeneration are the identical call sites TemplateCommand.Run reaches
// for "list"/"show" — this is still the binary's own answer, one layer
// closer to its source, not a second hand-written roster.
//
// Which roster rows carry a planner comparand at all is DERIVED, never a
// template-name list (AC-14): a row is in scope only if its own preview
// bytes (`a2a template show <type>`), parsed as a descriptor, declare a
// non-empty top-level `artifacts:` inventory — the one structural fact
// contract.CheckContractDescriptorShape's whole rule turns on. Measured at
// HEAD 2026-08-29: of the 8 shipped types, only `contract`'s own v2 preview
// declares one; every other type's preview parses (contract.ParseDescriptor
// tolerates a non-descriptor frontmatter document) but always with zero
// Artifacts, so it is skipped as "no planner comparand for this template" —
// not silently, but because the predicate that selects a cell is the same
// fact the planner rule reads, not a name.

// templateRosterEntry is one row of `a2a template list --json` plus the raw
// bytes `a2a template show <type>` renders for it — everything
// templatePlannerCells needs, gathered by the production caller via
// liveTemplateRosterEntries or supplied as a literal fixture by a test.
type templateRosterEntry struct {
	Type           string
	EnvelopeSchema string
	ShowBytes      []byte
}

// liveTemplateRosterEntries drives the same underlying calls `a2a template
// list --json` / `a2a template show <type>` make (template.Types,
// template.AuthoringEnvelopeSchema, template.ShowGeneration — internal/cli's
// TemplateCommand.Run's own call sites) directly, IN-PROCESS — the binary's
// own answer, never a second hand-written roster, and never a cross-layer
// import of internal/cli itself (see this file's header).
//
// ctx is accepted for symmetry with the rest of this package's production
// callers, none of which this function's own reads (an embedded FS walk)
// need to cancel.
func liveTemplateRosterEntries(_ context.Context) ([]templateRosterEntry, error) {
	types := template.Types()
	entries := make([]templateRosterEntry, 0, len(types))
	for _, typ := range types {
		envelopeSchema, err := template.AuthoringEnvelopeSchema(typ, validate.IsJSONSchemaFormat)
		if err != nil {
			return nil, fmt.Errorf("template.AuthoringEnvelopeSchema(%s): %w", typ, err)
		}
		showBytes, err := template.ShowGeneration(typ, "", validate.IsJSONSchemaFormat)
		if err != nil {
			return nil, fmt.Errorf("template.ShowGeneration(%s): %w", typ, err)
		}
		entries = append(entries, templateRosterEntry{
			Type:           typ,
			EnvelopeSchema: envelopeSchema,
			ShowBytes:      append([]byte(nil), showBytes...),
		})
	}
	return entries, nil
}

// templatePlannerCells is universe 3's own cross-product. entries is a
// PARAMETER — the production caller passes liveTemplateRosterEntries'
// real answer; a fixture test (AC-8/AC-14) passes a synthetic roster row
// (a template type the real binary does not ship, with its own declared
// artifacts inventory) to prove this function reacts to whatever the
// roster carries with no edit to this file.
//
// floor is the space authoring floor the planner is asked to judge against
// (contract.ContractPublicationFloor in production — AT the floor, the
// exact shape 3539ac happened in: a space that had just raised to it).
//
// leftAccepted is always true — the "preview" here is the roster's own
// claim (`a2a template show <type>` IS what an author follows, exactly
// ShowGeneration's own doc comment's incident). rightRefused is
// CheckContractDescriptorShape's verdict for real: nil is accepted,
// non-nil is refused — so a template whose own shown preview the planner
// would refuse reproduces 3539ac's shape and reds, naming both.
func templatePlannerCells(entries []templateRosterEntry, floor string) (evaluated []string, errs []error) {
	for _, entry := range entries {
		descriptor, err := contract.ParseDescriptor(entry.ShowBytes)
		if err != nil || len(descriptor.Artifacts) == 0 {
			// No planner comparand for this template: it declares no
			// top-level artifacts inventory, so
			// CheckContractDescriptorShape's own rule has nothing to judge.
			// Derived from the SAME bytes the roster publishes, never a
			// second template-name list.
			continue
		}
		evaluated = append(evaluated, entry.Type)

		violation := validate.CheckContractDescriptorShape(validate.DescriptorShapeInput{
			SpaceMinBinaryVersion: floor,
			ContractID:            entry.Type,
			DescriptorSchema:      entry.EnvelopeSchema,
			DeclaresArtifacts:     true,
			SchemaFormat:          descriptor.SchemaFormat,
		})
		if err := assertDirectionalCell(
			pairPreviewingActing,
			fmt.Sprintf("a2a template show %s (preview of the accepted shape)", entry.Type),
			true,
			"the space's publication planner (validate.CheckContractDescriptorShape)",
			violation != nil,
		); err != nil {
			if violation != nil {
				err = fmt.Errorf("%w: %s", err, violation.Message)
			}
			errs = append(errs, err)
		}
	}
	return evaluated, errs
}
