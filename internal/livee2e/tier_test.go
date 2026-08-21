package livee2e

import (
	"os"
	"path/filepath"
	"strconv"
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
// this gate (spec 09 §5, wave W5 brief §3): every one of the 49 declared
// rows must pass all four D-3 rules, and the 35 logic / 14 provider split
// the wave brief transcribes must hold exactly — so a row added later
// without a Tier, or with an incoherent ProviderAssertions carve-out, cannot
// slip in behind a green suite.
func TestRealCatalogueDeclaresEveryTierCoherently(t *testing.T) {
	t.Parallel()
	catalogue := Catalogue()

	if got := len(catalogue); got != 50 {
		t.Fatalf("Catalogue() has %d rows, want 50", got)
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
	// 36/14 as of judge-the-thing-2026-08 P9 (the declared-companion row),
	// from 35/14 as of agent-exchange P11 wave D (the two `bytes` rows), from
	// 33/14 as of P9 W7, from 29/18.
	//
	// The earlier note is kept because its lesson outlived its number: 29/18
	// was itself a correction of 30/17. LE-OC-05 had been declared TierLogic
	// with a carve-out on "visibility-public", and the FIRST full local matrix
	// run showed that wrong — its other assertion,
	// "visibility-higher-classification", waits on a doctor WARN that doctor
	// only reaches once it has DETERMINED the repository is public. Both rest
	// on the same provider fact, so the whole row went provider. The number
	// moved because running the thing beat reasoning about it again.
	//
	// W7 moved four happy rows the other way, and they are the four that had
	// ADMITTED they were placeholders: space-init, lifecycle-transitions,
	// contract-publish-deprecate-retire and cross-system-visibility each
	// carried "TierProvider is PROVISIONAL here, not a considered verdict"
	// because a legacy row with no Assertions list has nothing to carve a
	// ProviderAssertions subset from. Populating that list is the whole of
	// W7, and two of the four then needed a one-entry carve-out rather than
	// a whole-row verdict (protection-armed, gate-concludes-success).
	//
	// This number falling is the direction that needs evidence, not the one
	// that needs permission: every row that leaves the provider tier is a row
	// a fake is now trusted to judge. Each of the four was decided from what
	// its body actually asserts, and both carve-outs were verified against
	// the fake's own behaviour rather than its documentation — fakegithub
	// carries NO branch-protection route (its router's default arm 404s), and
	// it seeds CheckConclusion: "success" as a constant.
	if logic != 36 {
		t.Errorf("logic rows = %d, want 36", logic)
	}
	if provider != 14 {
		t.Errorf("provider rows = %d, want 14", provider)
	}
	if logic+provider != len(catalogue) {
		t.Errorf("logic(%d) + provider(%d) = %d, want %d (every row must declare exactly one of the two)", logic, provider, logic+provider, len(catalogue))
	}

	// Belt and suspenders: FilterJudgeableBy is the function W4 will
	// actually dispatch the logic runner on, so it must agree with the
	// tally above against the REAL 49 rows, not just the synthetic slices
	// TestFilterJudgeableByKeepsDeclarationOrder already covers.
	if got := len(FilterJudgeableBy(catalogue, TierLogic)); got != 36 {
		t.Errorf("FilterJudgeableBy(catalogue, TierLogic) = %d rows, want 36", got)
	}
	if got := len(FilterJudgeableBy(catalogue, TierProvider)); got != 50 {
		t.Errorf("FilterJudgeableBy(catalogue, TierProvider) = %d rows, want 50 (the provider tier may judge every row)", got)
	}
}

func TestAssertionJudgeableByHonoursTheCarveOutOnlyForTheLogicTier(t *testing.T) {
	t.Parallel()

	rows := []Scenario{
		{
			Name: "LE-OC-05", Branch: "baseline", Tier: TierLogic,
			Assertions:         []string{"visibility-public", "visibility-higher-classification"},
			ProviderAssertions: []string{"visibility-public"},
		},
		{Name: "plain-row", Tier: TierLogic, Assertions: []string{"a", "b"}},
	}

	if AssertionJudgeableBy(rows, "LE-OC-05", "baseline", "visibility-public", TierLogic) {
		t.Error("the logic tier may not judge a carved-out assertion — declaring the carve-out and then asserting it anyway is how LE-OC-05 failed every local run for a reason that was never the product's")
	}
	if !AssertionJudgeableBy(rows, "LE-OC-05", "baseline", "visibility-higher-classification", TierLogic) {
		t.Error("a row's OTHER assertions stay judgeable — sending the whole row away over one assertion is what the per-assertion tag exists to avoid")
	}
	if !AssertionJudgeableBy(rows, "LE-OC-05", "baseline", "visibility-public", TierProvider) {
		t.Error("the provider tier judges everything, including carved-out assertions — that is what a carve-out MEANS")
	}
	if !AssertionJudgeableBy(rows, "LE-OC-05", "baseline", "visibility-public", "") {
		t.Error("the zero tier is the live tier (report.go's own convention) and must judge everything")
	}
	if !AssertionJudgeableBy(rows, "plain-row", "", "a", TierLogic) {
		t.Error("a row with no carve-out has every assertion judgeable in either tier")
	}
	if !AssertionJudgeableBy(rows, "no-such-row", "", "a", TierLogic) {
		t.Error("an undeclared row must fall back to asserting as usual, not to exempting itself")
	}
}

// TestEveryCarveOutIsConsultedBySomeScenarioBody is the gate that stops a
// carve-out from being declared and then ignored — the exact gap that made
// LE-OC-05 fail locally and LE-OC-03 pass locally for the wrong reason. It
// walks the scenario sources for AssertionJudgeableBy call sites and requires
// each declared ProviderAssertions entry to appear in one.
func TestEveryCarveOutIsConsultedBySomeScenarioBody(t *testing.T) {
	t.Parallel()

	sources, err := filepath.Glob("scenarios_*_live.go")
	if err != nil || len(sources) == 0 {
		t.Fatalf("glob scenarios_*_live.go: %v (found %d)", err, len(sources))
	}
	var corpus strings.Builder
	for _, path := range sources {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		corpus.Write(raw)
	}
	body := corpus.String()

	var declared int
	for _, s := range Catalogue() {
		for _, assertion := range s.ProviderAssertions {
			declared++
			if !strings.Contains(body, strconv.Quote(assertion)) {
				t.Errorf("%s/%s declares provider assertion %q, but no scenario body mentions it — a carve-out nothing consults either fails the row against a fake that lacks the fact, or passes it because the condition it guards cannot occur here",
					s.Name, s.Branch, assertion)
			}
		}
	}
	if declared == 0 {
		t.Fatal("no ProviderAssertions declared anywhere — this gate would hold vacuously; if the last carve-out was removed on purpose, remove this test with it")
	}
	if !strings.Contains(body, "AssertionJudgeableBy") {
		t.Error("no scenario body calls AssertionJudgeableBy — the carve-outs are declared and unconsulted, which is the state this gate exists to refuse")
	}
}
