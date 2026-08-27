// pathcatalogue_classification.go declares no-silent-yes-2026-08/P3 stage
// 2's own conformance path: the "restricted ⇒ bilateral space" rule
// (spec 03 §8 AC 4/5, plan/10-security.md:60). Its own file, mirroring
// stage 1's pathcatalogue_format.go — it proves no fold-transition-
// legality refusal (every other family in this catalogue's own six
// domain groupings), so it stays out of pathcatalogue_paths.go.
//
// # POL-025 was here; it is gone, not renamed
//
// Wave 1 of this epic also declared "restricted-classification-capability-
// miss-refused" here (POL-025, D9's first consumer, spec 03 §8 AC 13):
// a question drafted with classification=restricted, refused at SUBMIT
// naming POL-025 (reject) when the Resolver could not enumerate the
// space's ACTIVE participants. At the time NO concrete Resolver
// implemented ActiveParticipantLister, so the capability miss was what
// EVERY restricted submission actually hit — POL-025 was genuinely driven.
//
// The wave-1 fix then wired the capability into BOTH internal/cli's and
// internal/mcp's MirrorResolver (each with a var _ compile-time gate,
// ADR-019's detection half). POL-025 INVERTED that same day: unreachable
// through a correctly-wired binary, which is the point of wiring it — but
// D9's own text never asked for a SECOND reject code for the capability-
// miss branch; it asked for UNMEASURED to ride "alongside an ordinary
// reject", and the rule's own ordinary reject is POL-024
// (internal/validate/classification.go). So this wave folds the
// capability-miss branch back into POL-024 + POL-026 (D9's exact pair) and
// deletes the path that named POL-025: with POL-025 gone the path names no
// code at all, and its own observed behaviour in THIS catalogue's topology
// (catalogue.go's SystemA/SystemB, DefaultRepo's one provisioned space,
// active participants = {A,B} = {from} ∪ to exactly — bilateral) is
// ACCEPTANCE, not a refusal — never a shape this file declares a path for.
package livee2e

import "github.com/ydnikolaev/a2ahub/internal/fold"

// classificationBilateralPaths returns stage 2's own honest-limitation
// path (POL-024) — appended into ConformancePaths() by
// pathcatalogue_paths.go.
func classificationBilateralPaths() []Path {
	return []Path{
		{
			ID: "restricted-classification-exceeds-bilateral-refused",
			Intent: "US-2/US-3 (spec 03 §8 AC 4/5, plan/10-security.md:60 " +
				"\"the validator enforces 'restricted ⇒ bilateral space'\"): the rule " +
				"this stage actually closes a plan-vs-code mismatch for — a " +
				"`restricted` artifact whose space carries an ACTIVE participant " +
				"outside {from} ∪ to is refused naming POL-024. " +
				"GENUINELY DRIVEN, both layers closed: the capability gap this " +
				"path's own sibling used to name was already CLOSED — both " +
				"internal/cli's and internal/mcp's MirrorResolver implement " +
				"ActiveParticipantLister for real (var _ compile-time gates, " +
				"ADR-019's detection half; internal/cli/adapters_test.go's own " +
				"TestSubmitValidatorAdapterRestrictedClassificationExceedsBilateralRefused " +
				"and internal/mcp's sibling already prove POL-024 fires end to end " +
				"through the real MirrorResolver against a genuinely three-" +
				"participant manifest; internal/e2e/testdata/t3/submit_refusal.txtar " +
				"proves it through the real `a2a` binary too). The ONE remaining " +
				"gap — this catalogue's own two-system harness topology " +
				"(catalogue.go: \"SystemA... SystemB\", DefaultRepo's one " +
				"provisioned space) had no THIRD active participant to exceed " +
				"{from} ∪ to against, so a from=A/to=[B] submission's own " +
				"{from} ∪ to already equalled the space's entire active " +
				"membership — is CLOSED too: this path now runs over its OWN " +
				"dedicated space (logic_runner_live_test.go's " +
				"classificationHarness) carrying a third, always-active, " +
				"never-addressed participant added at GENESIS " +
				"(provision_live.go's AddInertParticipantGenesis, scaffold.go's " +
				"AddParticipant) — nobody ever authenticates as it, addresses it, " +
				"or names it in `to`/`required_approvers`, so it trips none of " +
				"REF-006's guards. The same durable-manifest-edit-onto-a-" +
				"dedicated-space shape Family 15's SetParticipantStatusMidPath " +
				"uses (pathcatalogue_paths.go's own Family 15 doc comment), " +
				"driven separately from the ordinary round-robin split " +
				"(pathdrivability.go's " +
				"classificationBilateralDedicatedSpacePathIDs(), " +
				"pathdriver_live.go's runClassificationBilateralPaths) for the " +
				"identical reason Family 15's own three ids are. THIS PATH'S OWN " +
				"ASSERTION (a substring match on \"POL-024\") PASSES when driven " +
				"under -tags=livee2e.",
			Steps: []Step{
				{
					Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TCreate,
					Predicates: []Predicate{FoldedState("question", fold.StateDraft)},
				},
				{
					Actor: SystemA, Kind: fold.KindQuestion, Transition: fold.TSubmit,
					Refused: &Refusal{Code: "POL-024"},
				},
			},
		},
	}
}
