// pathcatalogue_format.go declares no-silent-yes-2026-08/P3 stage 1's own
// conformance path: the SCH-012 format-assertion refusal (spec 03 §8 AC 1,
// US-1). It is its own file rather than folded into pathcatalogue_paths.go's
// six domain families (that file's own header comment) because it proves a
// SCHEMA-CLASS refusal — a malformed field VALUE — not a fold-transition-
// legality refusal: every other Refused step in this catalogue is an
// illegal-transition (LFC-*) or a referential/policy check; this is the
// catalogue's first schema-class one.
package livee2e

import "github.com/ydnikolaev/a2ahub/internal/fold"

// formatAssertionPaths returns P3 stage 1's own declared path — appended
// into ConformancePaths() by pathcatalogue_paths.go.
func formatAssertionPaths() []Path {
	return []Path{
		{
			ID: "work-request-bad-needed-by-format-refused",
			Intent: "US-1/AC-1 (spec 03 §8, DECISIONS.md § D3/§ D9): a work_request " +
				"drafted with needed_by=\"next tuesday\" (base.schema.json's own " +
				"`format: date` on that field) is refused at SUBMIT (V2), naming " +
				"SCH-012. D3's own forced ordering is why SCH-012 is registered " +
				"BEFORE AssertFormat is enabled in internal/schema/corpus.go: " +
				"internal/schema/keyword_test.go's TestFormatIsAnnotationOnly " +
				"documents that enabling assertion with no matching registry code " +
				"hard-errors ValidateDraft (an UNMAPPABLE violation) rather than " +
				"reporting one. work_request (not requirement, the schema's OTHER " +
				"needed_by-bearing type) is chosen deliberately: requirement is a " +
				"standingDraftTypes member and Draft refuses it without --slug " +
				"(draftfields.go), and this path needs no standing id. " +
				"HONEST LIMITATION, recorded here rather than behind a passing " +
				"gate: this path resolves to SCH-012 only once " +
				"internal/validate/schema_class.go's schemaCode maps the " +
				"\"format\" keyword to it. That file sits outside P3 stage 1's own " +
				"allowlist (reported as an off-limits file the stage wanted) — " +
				"until the mapping lands, the real binary still refuses the " +
				"submit (the unmapped-keyword hard error internal/validate " +
				"already raises), but its message names no registry code, so " +
				"THIS PATH'S OWN ASSERTION (a substring match on \"SCH-012\") REDS " +
				"when actually driven under -tags=livee2e, until schema_class.go " +
				"is granted and wired.",
			Steps: []Step{
				{
					Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TCreate,
					Predicates: []Predicate{FoldedState("work-request", fold.StateDraft)},
				},
				{
					Actor: SystemA, Kind: fold.KindWorkRequest, Transition: fold.TSubmit,
					Refused: &Refusal{Code: "SCH-012"},
				},
			},
		},
	}
}
