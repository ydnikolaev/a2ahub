package livee2e

import (
	"strings"
	"testing"
)

// TestUndeclaredTierRowsFindsAMissingDeclaration is undeclaredTierRows' own
// TEETH: a row that never sets Tier at all (the zero value) must be caught,
// because the fail-closed direction here is "we do not know", never
// "logic".
func TestUndeclaredTierRowsFindsAMissingDeclaration(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "declared", Tier: TierLogic},
		{Name: "forgotten"}, // zero value — never declared
	}
	got := undeclaredTierRows(scenarios)
	if len(got) != 1 || got[0] != "forgotten" {
		t.Fatalf("undeclaredTierRows = %v, want [forgotten]", got)
	}
}

func TestUndeclaredTierRowsEmptyWhenEveryRowDeclares(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{{Name: "a", Tier: TierLogic}, {Name: "b", Tier: TierProvider}}
	if got := undeclaredTierRows(scenarios); len(got) != 0 {
		t.Fatalf("undeclaredTierRows = %v, want empty", got)
	}
}

// TestProviderAssertionsOnProviderRowsFindsTheIncoherentCarveOut is
// providerAssertionsOnProviderRows' own TEETH: a TierProvider row's
// assertions are ALL provider, so listing a subset in ProviderAssertions is
// incoherent and must be refused rather than interpreted.
func TestProviderAssertionsOnProviderRowsFindsTheIncoherentCarveOut(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "provider-with-carveout", Tier: TierProvider, Assertions: []string{"a"}, ProviderAssertions: []string{"a"}},
		{Name: "logic-with-carveout", Tier: TierLogic, Assertions: []string{"a"}, ProviderAssertions: []string{"a"}},
	}
	got := providerAssertionsOnProviderRows(scenarios)
	if len(got) != 1 || got[0] != "provider-with-carveout" {
		t.Fatalf("providerAssertionsOnProviderRows = %v, want [provider-with-carveout]", got)
	}
}

func TestProviderAssertionsOnProviderRowsEmptyWhenCarveOutsStayOnLogicRows(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "logic-with-carveout", Tier: TierLogic, Assertions: []string{"a"}, ProviderAssertions: []string{"a"}},
		{Name: "provider-row", Tier: TierProvider},
	}
	if got := providerAssertionsOnProviderRows(scenarios); len(got) != 0 {
		t.Fatalf("providerAssertionsOnProviderRows = %v, want empty", got)
	}
}

// TestUndeclaredProviderAssertionsFindsATypo is undeclaredProviderAssertions'
// own TEETH: a carve-out naming an assertion the row does not have would
// silently exempt nothing, so it must be caught.
func TestUndeclaredProviderAssertionsFindsATypo(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "row", Tier: TierLogic, Assertions: []string{"real-assertion"}, ProviderAssertions: []string{"typo-assertion"}},
	}
	got := undeclaredProviderAssertions(scenarios)
	want := []string{`row: "typo-assertion"`}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("undeclaredProviderAssertions = %v, want %v", got, want)
	}
}

func TestUndeclaredProviderAssertionsEmptyWhenEveryEntryIsDeclared(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "row", Tier: TierLogic, Assertions: []string{"a", "b"}, ProviderAssertions: []string{"b"}},
	}
	if got := undeclaredProviderAssertions(scenarios); len(got) != 0 {
		t.Fatalf("undeclaredProviderAssertions = %v, want empty", got)
	}
}

// TestDuplicateProviderAssertionsFindsTheRepeat is duplicateProviderAssertions'
// own TEETH.
func TestDuplicateProviderAssertionsFindsTheRepeat(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "row", Tier: TierLogic, Assertions: []string{"a"}, ProviderAssertions: []string{"a", "a"}},
	}
	got := duplicateProviderAssertions(scenarios)
	want := []string{`row: "a"`}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("duplicateProviderAssertions = %v, want %v", got, want)
	}
}

func TestDuplicateProviderAssertionsEmptyWhenEveryEntryIsUnique(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "row", Tier: TierLogic, Assertions: []string{"a", "b"}, ProviderAssertions: []string{"a", "b"}},
	}
	if got := duplicateProviderAssertions(scenarios); len(got) != 0 {
		t.Fatalf("duplicateProviderAssertions = %v, want empty", got)
	}
}

// TestJudgeableByLogicRowIsJudgeableByEitherTier is JudgeableBy's own TEETH
// for its permissive half.
func TestJudgeableByLogicRowIsJudgeableByEitherTier(t *testing.T) {
	t.Parallel()
	s := Scenario{Name: "s", Tier: TierLogic}
	if !s.JudgeableBy(TierLogic) {
		t.Error("a logic-tier row must be judgeable by the logic tier")
	}
	if !s.JudgeableBy(TierProvider) {
		t.Error("a logic-tier row must also be judgeable by the provider tier")
	}
}

// TestJudgeableByProviderRowIsJudgeableOnlyByTheProviderTier is JudgeableBy's
// own TEETH for its restrictive half — the whole point of §5's gate.
func TestJudgeableByProviderRowIsJudgeableOnlyByTheProviderTier(t *testing.T) {
	t.Parallel()
	s := Scenario{Name: "s", Tier: TierProvider}
	if s.JudgeableBy(TierLogic) {
		t.Error("a provider-tier row must NOT be judgeable by the logic tier — that is a fake deciding an answer only a provider can give")
	}
	if !s.JudgeableBy(TierProvider) {
		t.Error("a provider-tier row must be judgeable by the provider tier")
	}
}

// TestJudgeableByUndeclaredRowFailsClosedToTheProviderTierOnly proves the
// fail-closed reading of "we do not know" applies here too: a scenario whose
// Tier was never validly declared must not be treated as the permissive
// (logic) case.
func TestJudgeableByUndeclaredRowFailsClosedToTheProviderTierOnly(t *testing.T) {
	t.Parallel()
	var s Scenario // zero value: Tier is undeclared
	if s.JudgeableBy(TierLogic) {
		t.Error("an undeclared-tier row must not be judgeable by the logic tier")
	}
	if !s.JudgeableBy(TierProvider) {
		t.Error("an undeclared-tier row must still be judgeable by the provider tier")
	}
}

func TestFilterJudgeableByKeepsDeclarationOrder(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "a", Tier: TierLogic},
		{Name: "b", Tier: TierProvider},
		{Name: "c", Tier: TierLogic},
	}
	got := FilterJudgeableBy(scenarios, TierLogic)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("FilterJudgeableBy(logic) = %v, want [a c]", got)
	}
	if got := FilterJudgeableBy(scenarios, TierProvider); len(got) != 3 {
		t.Fatalf("FilterJudgeableBy(provider) = %v, want all 3 rows", got)
	}
}

// TestRealCatalogueDeclaresEveryTierCoherently is the real-catalogue half of
// this gate (spec 09 §5, wave W5 brief §3): every one of the 47 declared
// rows must pass all four D-3 rules, and the 30 logic / 17 provider split
// the wave brief transcribes must hold exactly — so a row added later
// without a Tier, or with an incoherent ProviderAssertions carve-out, cannot
// slip in behind a green suite.
func TestRealCatalogueDeclaresEveryTierCoherently(t *testing.T) {
	t.Parallel()
	catalogue := Catalogue()

	if got := len(catalogue); got != 47 {
		t.Fatalf("Catalogue() has %d rows, want 47", got)
	}
	if offenders := undeclaredTierRows(catalogue); len(offenders) > 0 {
		t.Errorf("rows with no declared tier: %s", strings.Join(offenders, ", "))
	}
	if offenders := providerAssertionsOnProviderRows(catalogue); len(offenders) > 0 {
		t.Errorf("provider rows carrying an incoherent ProviderAssertions carve-out: %s", strings.Join(offenders, ", "))
	}
	if offenders := undeclaredProviderAssertions(catalogue); len(offenders) > 0 {
		t.Errorf("ProviderAssertions entries absent from the row's own Assertions: %s", strings.Join(offenders, ", "))
	}
	if offenders := duplicateProviderAssertions(catalogue); len(offenders) > 0 {
		t.Errorf("duplicate ProviderAssertions entries: %s", strings.Join(offenders, ", "))
	}

	var logic, provider int
	for _, s := range catalogue {
		switch s.Tier {
		case TierLogic:
			logic++
		case TierProvider:
			provider++
		}
	}
	t.Logf("tier split: logic=%d provider=%d total=%d", logic, provider, len(catalogue))
	if logic != 30 {
		t.Errorf("logic rows = %d, want 30", logic)
	}
	if provider != 17 {
		t.Errorf("provider rows = %d, want 17", provider)
	}
	if logic+provider != len(catalogue) {
		t.Errorf("logic(%d) + provider(%d) = %d, want %d (every row must declare exactly one of the two)", logic, provider, logic+provider, len(catalogue))
	}

	// Belt and suspenders: FilterJudgeableBy is the function W4 will
	// actually dispatch the logic runner on, so it must agree with the
	// tally above against the REAL 47 rows, not just the synthetic slices
	// TestFilterJudgeableByKeepsDeclarationOrder already covers.
	if got := len(FilterJudgeableBy(catalogue, TierLogic)); got != 30 {
		t.Errorf("FilterJudgeableBy(catalogue, TierLogic) = %d rows, want 30", got)
	}
	if got := len(FilterJudgeableBy(catalogue, TierProvider)); got != 47 {
		t.Errorf("FilterJudgeableBy(catalogue, TierProvider) = %d rows, want 47 (the provider tier may judge every row)", got)
	}
}
