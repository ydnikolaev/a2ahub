package livee2e

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// digestProfileFixtureDescriptor/digestProfileFixtureCandidates are a
// minimal valid contract-set-v2 descriptor+candidate pair — the three
// required roles (schema, valid_fixture, invalid_fixture) and nothing
// else — so BuildCarriedSet can actually succeed under EVERY profile that
// has a builder, exercising real cross-profile behaviour rather than a
// structurally-refused input that would pass by accident.
func digestProfileFixtureDescriptor() contract.Descriptor {
	return contract.Descriptor{
		SchemaFormat: "json-schema-2020-12",
		Artifacts: []contract.Entry{
			{Path: "schema/order.schema.json", Role: contract.RoleSchema, Normative: true, MediaType: "application/schema+json"},
			{Path: "fixtures/valid/order.json", Role: contract.RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/order.schema.json"},
			{Path: "fixtures/invalid/missing-id.json", Role: contract.RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/order.schema.json"},
		},
	}
}

func digestProfileFixtureCandidates() []contract.CandidateFile {
	return []contract.CandidateFile{
		{Path: "schema/order.schema.json", Kind: contract.CandidateRegular, Raw: []byte(`{"type":"object"}`)},
		{Path: "fixtures/valid/order.json", Kind: contract.CandidateRegular, Raw: []byte(`{"id":"1"}`)},
		{Path: "fixtures/invalid/missing-id.json", Kind: contract.CandidateRegular, Raw: []byte(`{}`)},
	}
}

// TestDigestProfileCellsAgreeOnHEAD is universe 4's own live cell,
// production shape: contract.DigestProfiles()'s REAL three registered
// profiles, crossed against the SAME descriptor bytes — never a second
// hand-written profile list here.
func TestDigestProfileCellsAgreeOnHEAD(t *testing.T) {
	profiles := contract.DigestProfiles()
	if len(profiles) == 0 {
		t.Fatal("contract.DigestProfiles() returned none — nothing to cross")
	}
	evaluated, errs := digestProfileCells(profiles, []byte("final descriptor bytes\n"), digestProfileFixtureDescriptor(), digestProfileFixtureCandidates())
	if len(evaluated) != len(profiles) {
		t.Fatalf("evaluated %d profiles, want %d (one driven id per registered profile)", len(evaluated), len(profiles))
	}
	if len(errs) != 0 {
		t.Fatalf("digestProfileCells disagreed on HEAD: %v", errs)
	}
	for _, p := range profiles {
		if !verbAgreementContainsString(evaluated, string(p)) {
			t.Errorf("evaluated %v does not carry a driven id for registered profile %q", evaluated, p)
		}
	}
	t.Logf("crossed %d registered digest profiles against the descriptor's own bytes, all agree", len(evaluated))
}

// TestDigestProfileCellsAreDerivedFromTheRegistry is AC-9/AC-14 at the
// mechanism level: digestProfileCells reacts to whatever profiles it is
// given — a FIXTURE slice here, carrying a profile the real contract
// package does not register, never a second profile list maintained inside
// internal/livee2e — proving the cross-product (and its own driven id per
// profile) is driven by its PARAMETER, not a literal this file carries.
func TestDigestProfileCellsAreDerivedFromTheRegistry(t *testing.T) {
	realProfiles := contract.DigestProfiles()
	fixtureProfiles := append(append([]contract.DigestProfile(nil), realProfiles...), contract.DigestProfile("a-profile-the-real-registry-does-not-declare-yet"))

	evaluated, errs := digestProfileCells(fixtureProfiles, []byte("final descriptor bytes\n"), digestProfileFixtureDescriptor(), digestProfileFixtureCandidates())
	if len(evaluated) != len(fixtureProfiles) {
		t.Fatalf("evaluated %v, want %d driven ids (one per fixture profile)", evaluated, len(fixtureProfiles))
	}
	if !verbAgreementContainsString(evaluated, "a-profile-the-real-registry-does-not-declare-yet") {
		t.Fatalf("evaluated %v does not include the fixture profile's own driven id — the cross-product is not reacting to its own parameter", evaluated)
	}
	// The fictitious profile IS honestly refused (BuildCarriedSet's own
	// default case names it in an IssueUnsupportedProfile) — this cell must
	// stay green for it, because "unregistered profile, declared refusal" is
	// exactly the legitimate half of the uniform rule, not a disagreement.
	if len(errs) != 0 {
		t.Fatalf("digestProfileCells(%v) = %v, want no disagreement — an unregistered profile refusing itself by name is the legitimate half of the rule", fixtureProfiles, errs)
	}
}

// TestDigestProfileCellsRedsOnASilentSubstitution proves the assertion
// actually bites: a BuildCarriedSet that HONOURED no issue but returned a
// set built under a DIFFERENT profile than requested must red, naming both.
// contract.BuildCarriedSet does not do this on HEAD (there is nothing to
// seed a live red from without editing internal/contract, off this wave's
// allowlist), so this proves the ASSERTION's own shape directly, the same
// way assertDirectionalCell/assertReaderAgreement are proven in
// verbclasses_test.go rather than only through a live fixture.
func TestDigestProfileCellsRedsOnASilentSubstitution(t *testing.T) {
	// unsupportedProfileIssue is exercised directly here: a Detail naming a
	// DIFFERENT profile than the one being checked must not be accepted as
	// that profile's own declared refusal.
	issues := []contract.Issue{{Kind: contract.IssueUnsupportedProfile, Detail: `unregistered digest profile "some-other-profile"`}}
	if unsupportedProfileIssue(issues, "the-profile-under-test") {
		t.Fatal("unsupportedProfileIssue matched a refusal naming a DIFFERENT profile — a refusal must name the profile it excuses")
	}
	if !unsupportedProfileIssue(issues, "some-other-profile") {
		t.Fatal("unsupportedProfileIssue did not match its own profile's declared refusal")
	}
}
