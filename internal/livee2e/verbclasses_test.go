package livee2e

import (
	"strings"
	"testing"
)

// TestAssertDirectionalCell_ConservativePreviewNeverReds is AC-2: a
// previewing verb that refuses where the actor passes must NOT red. The
// converse direction is legitimate by the class's own definition.
func TestAssertDirectionalCell_ConservativePreviewNeverReds(t *testing.T) {
	t.Parallel()
	if err := assertDirectionalCell(pairPreviewingActing, "preview", false, "actor", true); err != nil {
		t.Fatalf("a conservative preview refusing where the actor accepts must not red, got: %v", err)
	}
}

// TestAssertDirectionalCell_PreviewPassActorRefuseReds is AC-3: a
// previewing verb that PASSES where the actor REFUSES reds, naming both
// verbs.
func TestAssertDirectionalCell_PreviewPassActorRefuseReds(t *testing.T) {
	t.Parallel()
	err := assertDirectionalCell(pairPreviewingActing, "a2a contract preflight", true, "a2a contract publish", true)
	if err == nil {
		t.Fatal("expected a directional disagreement, got nil")
	}
	if !strings.Contains(err.Error(), "a2a contract preflight") || !strings.Contains(err.Error(), "a2a contract publish") {
		t.Fatalf("error must name BOTH verbs, got: %v", err)
	}
}

// TestAssertDirectionalCell_CheckingPolarityReds is AC-4: a CHECKING verb
// refusing an input the actor already accepted reds — the polarity a
// two-class model would have exempted by filing the checker as a
// "conservative preview".
func TestAssertDirectionalCell_CheckingPolarityReds(t *testing.T) {
	t.Parallel()
	err := assertDirectionalCell(pairActingChecking, "a2a contract publish", true, "a2a contract verify-export", true)
	if err == nil {
		t.Fatal("expected the acting->checking disagreement to red, got nil")
	}
	if !strings.Contains(err.Error(), string(pairActingChecking)) {
		t.Fatalf("error must name its own class so a two-class model cannot silently exempt it, got: %v", err)
	}
}

// TestAssertDirectionalCell_CheckingConverseNeverReds is the acting-
// >checking class's own converse: a checker refusing an input the ACTOR
// ALSO refused (nothing was accepted to check) must not red.
func TestAssertDirectionalCell_CheckingConverseNeverReds(t *testing.T) {
	t.Parallel()
	if err := assertDirectionalCell(pairActingChecking, "actor", false, "checker", true); err != nil {
		t.Fatalf("a checker refusing an input the actor also refused must not red, got: %v", err)
	}
}

// TestAssertReaderAgreement_DisagreementReds is AC-5: two readers
// returning different classes for one path reds, with no actor involved.
func TestAssertReaderAgreement_DisagreementReds(t *testing.T) {
	t.Parallel()
	err := assertReaderAgreement("is this path a legitimate data-package payload?", map[string]bool{
		"validator":      true,
		"mirror-decoder": false,
	}, nil)
	if err == nil {
		t.Fatal("expected a reader disagreement, got nil")
	}
	if !strings.Contains(err.Error(), "validator") || !strings.Contains(err.Error(), "mirror-decoder") {
		t.Fatalf("error must name both readers, got: %v", err)
	}
}

// TestAssertReaderAgreement_AgreementNeverReds proves the shape does not
// fire when every reader agrees.
func TestAssertReaderAgreement_AgreementNeverReds(t *testing.T) {
	t.Parallel()
	err := assertReaderAgreement("q", map[string]bool{"a": true, "b": true, "c": true}, nil)
	if err != nil {
		t.Fatalf("agreeing readers must not red, got: %v", err)
	}
}

// TestAssertReaderAgreement_DeclaredExemptionIsNotSilentlyUnequal is the
// Testing requirements table's own "roster" edge case: a reader legitimately
// out of scope for a path must be DECLARED (via exempt), not silently
// excluded so its disagreement never surfaces, and not counted as a
// disagreement once declared.
func TestAssertReaderAgreement_DeclaredExemptionIsNotSilentlyUnequal(t *testing.T) {
	t.Parallel()
	err := assertReaderAgreement("q", map[string]bool{
		"validator":      true,
		"mirror-decoder": true,
		"out-of-scope":   false,
	}, map[string]string{"out-of-scope": "this reader never inspects this path shape at all"})
	if err != nil {
		t.Fatalf("a declared exemption must not be reported as a disagreement, got: %v", err)
	}
}

// TestUnclassifiedCatalogueVerbReds is AC-11 at the mechanism level: a
// catalogue verb referenced by a declared pair but absent from
// verbCatalogue() (classified nowhere, declared nowhere) must be named as
// unclassified rather than silently ignored.
func TestUnclassifiedCatalogueVerbReds(t *testing.T) {
	t.Parallel()
	got := unclassifiedCatalogueVerbs([]string{"a2a note (advertiser)", "a brand new verb nobody classified"}, verbCatalogue())
	if len(got) != 1 || got[0] != "a brand new verb nobody classified" {
		t.Fatalf("unclassifiedCatalogueVerbs = %v, want exactly [\"a brand new verb nobody classified\"]", got)
	}
}

// TestVerbCatalogueIsWellFormed is the roster's own well-formedness gate:
// every entry carries exactly one of {Role, DeclaredReason}, mirroring
// TestIncidentReplaysAreWellFormed's discipline for the sibling registry.
func TestVerbCatalogueIsWellFormed(t *testing.T) {
	t.Parallel()
	if errs := catalogueWellFormednessErrors(verbCatalogue()); len(errs) > 0 {
		t.Fatalf("verbCatalogue() is not well-formed: %v", errs)
	}
}

// TestCatalogueWellFormednessErrors_CatchesBothMalformedShapes is the
// mutation proof for catalogueWellFormednessErrors itself: an entry
// carrying both a Role and a DeclaredReason, and an entry carrying
// neither, must both be reported.
func TestCatalogueWellFormednessErrors_CatchesBothMalformedShapes(t *testing.T) {
	t.Parallel()
	errs := catalogueWellFormednessErrors([]catalogueVerb{
		{Verb: "both", Role: roleReader, DeclaredReason: "should not carry both"},
		{Verb: "neither", Role: "", DeclaredReason: ""},
		{Verb: "fine", Role: roleActor},
	})
	if len(errs) != 2 {
		t.Fatalf("catalogueWellFormednessErrors returned %d errors, want 2: %v", len(errs), errs)
	}
}
