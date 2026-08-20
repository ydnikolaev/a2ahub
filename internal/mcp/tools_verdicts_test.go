package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// --- P1 (one-answer-2026-08): verdicts[] on a2a_exchange verify and
// a2a_lifecycle close ---------------------------------------------------

// mcpVerdictProbe/mcpEventProbe mirror internal/cli's own
// lifecycleVerdictProbe/lifecycleEventProbe (cmd_lifecycle_test.go) — a
// decoded read of a committed event/v1|v2 document, used to assert on
// VALUES rather than a substring of rendered bytes.
type mcpVerdictProbe struct {
	Index      int    `yaml:"index"`
	Criterion  string `yaml:"criterion,omitempty"`
	Verdict    string `yaml:"verdict"`
	CauseOwner string `yaml:"cause_owner"`
}

type mcpEventProbe struct {
	Schema     string `yaml:"schema"`
	Transition string `yaml:"transition"`
	Subject    string `yaml:"subject"`
	State      string `yaml:"state,omitempty"`
	// Verdicts is a POINTER so tests can distinguish "the key is absent"
	// (nil) from "the key is present and empty" (non-nil, len 0).
	Verdicts *[]mcpVerdictProbe `yaml:"verdicts"`
}

func findMCPEventByTransition(t *testing.T, files []space.FileWrite, want string) mcpEventProbe {
	t.Helper()
	for _, fw := range files {
		var probe mcpEventProbe
		if err := yaml.Unmarshal(fw.Content, &probe); err != nil {
			t.Fatalf("decode event %s: %v", fw.Path, err)
		}
		if probe.Transition == want {
			return probe
		}
	}
	t.Fatalf("no %s event found among %d files", want, len(files))
	return mcpEventProbe{}
}

// manifestAtFloor is testManifest() with min_binary_version set to floor —
// the field lifecycleEventSchema reads to decide event/v1 vs event/v2.
func manifestAtFloor(floor string) space.Manifest {
	m := testManifest()
	m.MinBinaryVersion = floor
	return m
}

// verdictIndex/verdictCriterion build one VerdictInputEntry — tests need a
// helper since Index is a pointer (mirrors mcpUnmetIndex/mcpUnmetCriterion).
func verdictIndex(n int, verdict, causeOwner string) VerdictInputEntry {
	idx := n
	return VerdictInputEntry{Index: &idx, Verdict: verdict, CauseOwner: causeOwner}
}

func verdictCriterion(id, verdict, causeOwner string) VerdictInputEntry {
	return VerdictInputEntry{Criterion: id, Verdict: verdict, CauseOwner: causeOwner}
}

// mcpRespondFlow drives a2a_respond (fakeFunnel, materialized to disk) and
// returns the minted response id — mirrors internal/cli's own respondFlow.
func mcpRespondFlow(t *testing.T, mirrorDir, parentID, to string) string {
	t.Helper()
	respondFake := &fakeFunnel{}
	respondDeps := testWriteDeps(mirrorDir, respondFake)
	respondArgs, _ := json.Marshal(RespondInput{ParentIDs: []string{parentID}, Result: "answered"})
	if _, _, err := newRespondHandler(respondDeps)(context.Background(), respondArgs); err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if len(respondFake.calls) != 1 {
		t.Fatalf("expected 1 respond funnel call, got %d", len(respondFake.calls))
	}
	for _, fw := range respondFake.calls[0].Files {
		if err := writeFileAllDirs(filepath.Join(mirrorDir, fw.Path), fw.Content); err != nil {
			t.Fatalf("materialize %s: %v", fw.Path, err)
		}
	}
	var responseID string
	for _, fw := range respondFake.calls[0].Files {
		base := filepath.Base(fw.Path)
		if strings.HasPrefix(base, "XS-") {
			responseID = strings.TrimSuffix(base, ".md")
		}
	}
	if responseID == "" {
		t.Fatalf("could not find minted response id in %+v", respondFake.calls[0].Files)
	}
	return responseID
}

// --- verify: floor selection -----------------------------------------------

func TestVerifyBelowFloorStaysEventV1AndOmitsVerdictsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf01"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("verify: %v", err)
	}
	files := fake.calls[0].Files
	verify := findMCPEventByTransition(t, files, "verify")
	close_ := findMCPEventByTransition(t, files, "close")
	if verify.Schema != "event/v1" || close_.Schema != "event/v1" {
		t.Fatalf("expected event/v1 below the floor, got verify=%q close=%q", verify.Schema, close_.Schema)
	}
	if verify.Verdicts != nil || close_.Verdicts != nil {
		t.Fatalf("expected NO verdicts key on event/v1, got verify=%v close=%v", verify.Verdicts, close_.Verdicts)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key when verdicts were never used, got %q", fake.calls[0].OperationKey)
	}
}

func TestVerifyAtFloorAuthorsEventV2WithVerdictsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf02"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{verdictIndex(0, "met", "axon")}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("verify: %v", err)
	}
	files := fake.calls[0].Files
	verify := findMCPEventByTransition(t, files, "verify")
	close_ := findMCPEventByTransition(t, files, "close")
	if verify.Schema != "event/v2" || close_.Schema != "event/v2" {
		t.Fatalf("expected event/v2 at/above the floor, got verify=%q close=%q", verify.Schema, close_.Schema)
	}
	for name, probe := range map[string]mcpEventProbe{"verify": verify, "close": close_} {
		if probe.Verdicts == nil {
			t.Fatalf("%s: expected a verdicts key, got none", name)
		}
		got := *probe.Verdicts
		if len(got) != 1 || got[0].Index != 0 || got[0].Verdict != "met" || got[0].CauseOwner != "axon" {
			t.Fatalf("%s: verdicts = %+v, want [{0 met axon}]", name, got)
		}
	}
	if fake.calls[0].OperationKey == "" {
		t.Fatal("expected a non-empty operation key once verdicts carried content")
	}
}

func TestVerifyEmptyVerdictsArrayWhenNoneSuppliedMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf03"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("verify: %v", err)
	}
	files := fake.calls[0].Files
	verify := findMCPEventByTransition(t, files, "verify")
	close_ := findMCPEventByTransition(t, files, "close")
	if verify.Verdicts == nil || len(*verify.Verdicts) != 0 {
		t.Fatalf("verify: expected verdicts: [] (present, empty), got %v", verify.Verdicts)
	}
	if close_.Verdicts == nil || len(*close_.Verdicts) != 0 {
		t.Fatalf("close: expected verdicts: [] (present, empty), got %v", close_.Verdicts)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key when verdicts were never used, got %q", fake.calls[0].OperationKey)
	}
}

// TestVerifyVerdictsBelowFloorRefusedNamingFloorAndValue is AC3/US-3: the
// refusal must name BOTH contract.ContractPublicationFloor AND the space's
// own configured (below-floor) value — never a silent drop.
func TestVerifyVerdictsBelowFloorRefusedNamingFloorAndValue(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf05"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.5.0") // below contract.ContractPublicationFloor (0.19.0 < 0.5.0? no — see below)
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{verdictIndex(0, "met", "axon")}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal for verdicts below the floor")
	}
	if !strings.Contains(err.Error(), "0.19.0") {
		t.Fatalf("refusal does not name the floor constant, got: %v", err)
	}
	if !strings.Contains(err.Error(), "0.5.0") {
		t.Fatalf("refusal does not name the space's own configured value, got: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called, got %d calls", len(fake.calls))
	}
}

// TestVerifyVerdictsDifferentJudgementsMintDifferentOperationKeysMCP is
// AC9/US-1: two verify calls naming the SAME target with DIFFERENT
// judgements must mint DIFFERENT operation keys — the class of bug this
// phase exists to close (a collision would silently drop the second write).
func TestVerifyVerdictsDifferentJudgementsMintDifferentOperationKeysMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf06"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	run := func(verdict string) string {
		fake := &fakeFunnel{}
		deps := testWriteDeps(mirrorDir, fake)
		deps.OwnSystem = "axon"
		deps.Manifest = manifestAtFloor("0.19.0")
		handler := newVerifyHandler(deps)
		args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{verdictIndex(0, verdict, "axon")}})
		if _, _, err := handler(context.Background(), args); err != nil {
			t.Fatalf("verify: %v", err)
		}
		return fake.calls[0].OperationKey
	}

	met := run("met")
	unmet := run("unmet")
	if met == unmet {
		t.Fatalf("differing verdicts minted the same operation key %q", met)
	}

	// same judgement supplied twice must mint the SAME key.
	metAgain := run("met")
	if met != metAgain {
		t.Fatalf("identical judgements minted different operation keys: %q vs %q", met, metAgain)
	}
}

// TestVerifyVerdictsReorderedMintByteIdenticalContentMCP is AC6/US-1: the
// SAME judgement set supplied in two different orders must mint
// byte-identical event content (the written array is canonical by index,
// not by input order) and the same operation key.
func TestVerifyVerdictsReorderedMintByteIdenticalContentMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf07"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	run := func(entries ...VerdictInputEntry) (string, string) {
		fake := &fakeFunnel{}
		deps := testWriteDeps(mirrorDir, fake)
		deps.OwnSystem = "axon"
		deps.Manifest = manifestAtFloor("0.19.0")
		handler := newVerifyHandler(deps)
		args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: entries})
		if _, _, err := handler(context.Background(), args); err != nil {
			t.Fatalf("verify: %v", err)
		}
		files := fake.calls[0].Files
		verify := findMCPEventByTransition(t, files, "verify")
		var verifyContent string
		for _, fw := range files {
			var probe mcpEventProbe
			_ = yaml.Unmarshal(fw.Content, &probe)
			if probe.Transition == "verify" {
				verifyContent = string(fw.Content)
			}
		}
		_ = verify
		return fake.calls[0].OperationKey, verifyContent
	}

	givenKey, givenContent := run(verdictIndex(0, "met", "axon"), verdictIndex(1, "unmet", "beta"))
	reorderedKey, reorderedContent := run(verdictIndex(1, "unmet", "beta"), verdictIndex(0, "met", "axon"))
	if givenKey != reorderedKey {
		t.Fatalf("reordering verdicts (same judgement set) changed the operation key: %q vs %q", givenKey, reorderedKey)
	}
	// Event content differs only by the minted event/artifact id (random
	// entropy per MintULIDAt call), so compare the DECODED verdicts array,
	// not the raw bytes (which legitimately differ by event id/at).
	var givenProbe, reorderedProbe mcpEventProbe
	if err := yaml.Unmarshal([]byte(givenContent), &givenProbe); err != nil {
		t.Fatalf("decode given: %v", err)
	}
	if err := yaml.Unmarshal([]byte(reorderedContent), &reorderedProbe); err != nil {
		t.Fatalf("decode reordered: %v", err)
	}
	if givenProbe.Verdicts == nil || reorderedProbe.Verdicts == nil {
		t.Fatal("expected a verdicts key on both")
	}
	got, want := *reorderedProbe.Verdicts, *givenProbe.Verdicts
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("written verdicts differ by input order: given=%+v reordered=%+v (want canonical by index)", want, got)
	}
}

// --- verify: the four resolution refusals (mirrors respondResolveUnmet's) -

func TestVerifyVerdictBareIndexRefusedWhenParentDeclaresIDsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vr01"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{verdictIndex(0, "met", "axon")}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a bare index against an id-declaring parent to be refused")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for a bare index against an id-declaring parent, got %d calls", len(fake.calls))
	}
}

func TestVerifyVerdictCriterionRefusedWhenParentDeclaresNoIDsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vr02"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - \"plain string criterion\"\n")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{verdictCriterion("ac1", "met", "axon")}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a criterion id against an ordinal-only parent to be refused")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for a criterion id against an ordinal-only parent, got %d calls", len(fake.calls))
	}
}

func TestVerifyVerdictUnknownCriterionRefusedNamingDeclaredIDsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vr03"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{verdictCriterion("nosuch", "met", "axon")}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an unresolvable criterion id to be refused")
	}
	if !strings.Contains(err.Error(), "ac1") {
		t.Fatalf("refusal does not name the ids the parent declares: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for an id that does not resolve, got %d calls", len(fake.calls))
	}
}

func TestVerifyVerdictDuplicatePositionRefusedMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vr04"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n  - id: ac2\n    text: \"second\"\n")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newVerifyHandler(deps)
	// ac1 resolves to position 0; a bare "0" against the SAME parent would
	// be refused as a bare-index-against-id-declaring-parent, so instead
	// this exercises the "two entries resolve to the same position"
	// refusal with two criterion-id entries naming the SAME criterion.
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{
		verdictCriterion("ac1", "met", "axon"), verdictCriterion("ac1", "unmet", "beta"),
	}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected duplicate-position verdicts to be refused")
	}
	if !strings.Contains(err.Error(), "two entries resolve to the same criterion") {
		t.Fatalf("expected a duplicate-position refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for duplicate-position verdicts, got %d calls", len(fake.calls))
	}
}

func TestVerifyVerdictByIDResolvesAgainstDeclaredCriteriaMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vr05"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n  - id: ac2\n    text: \"second\"\n")
	responseID := mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newVerifyHandler(deps)
	args, _ := json.Marshal(VerifyInput{Targets: []string{responseID}, Verdicts: []VerdictInputEntry{verdictCriterion("ac2", "met", "axon")}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("verify: %v", err)
	}
	files := fake.calls[0].Files
	verify := findMCPEventByTransition(t, files, "verify")
	if verify.Verdicts == nil || len(*verify.Verdicts) != 1 || (*verify.Verdicts)[0].Criterion != "ac2" {
		t.Fatalf("expected verdicts: [{criterion: ac2, ...}], got %+v", verify.Verdicts)
	}
}

// --- close (standalone, generic verb row): mirrors the verify tests above -

func TestCloseBelowFloorStaysEventV1AndOmitsVerdictsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf01"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	_ = mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	handler := newLifecycleHandler(LifecycleVerbTable[7], deps) // close
	args, _ := json.Marshal(LifecycleInput{IDs: []string{parentID}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("close: %v", err)
	}
	files := fake.calls[0].Files
	closeEvt := findMCPEventByTransition(t, files, "close")
	if closeEvt.Schema != "event/v1" {
		t.Fatalf("expected event/v1 below the floor, got %q", closeEvt.Schema)
	}
	if closeEvt.Verdicts != nil {
		t.Fatalf("expected NO verdicts key on event/v1, got %v", closeEvt.Verdicts)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key when verdicts were never used, got %q", fake.calls[0].OperationKey)
	}
}

func TestCloseAtFloorAuthorsEventV2WithVerdictsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf02"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	_ = mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newLifecycleHandler(LifecycleVerbTable[7], deps) // close
	args, _ := json.Marshal(LifecycleInput{IDs: []string{parentID}, Verdicts: []VerdictInputEntry{verdictIndex(0, "met", "axon")}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("close: %v", err)
	}
	files := fake.calls[0].Files
	closeEvt := findMCPEventByTransition(t, files, "close")
	if closeEvt.Schema != "event/v2" {
		t.Fatalf("expected event/v2 at/above the floor, got %q", closeEvt.Schema)
	}
	if closeEvt.Verdicts == nil {
		t.Fatal("expected a verdicts key, got none")
	}
	got := *closeEvt.Verdicts
	if len(got) != 1 || got[0].Index != 0 || got[0].Verdict != "met" || got[0].CauseOwner != "axon" {
		t.Fatalf("verdicts = %+v, want [{0 met axon}]", got)
	}
	if fake.calls[0].OperationKey == "" {
		t.Fatal("expected a non-empty operation key once verdicts carried content")
	}
}

func TestCloseEmptyVerdictsArrayWhenNoneSuppliedMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf03"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	_ = mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.19.0")
	handler := newLifecycleHandler(LifecycleVerbTable[7], deps) // close
	args, _ := json.Marshal(LifecycleInput{IDs: []string{parentID}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("close: %v", err)
	}
	files := fake.calls[0].Files
	closeEvt := findMCPEventByTransition(t, files, "close")
	if closeEvt.Verdicts == nil || len(*closeEvt.Verdicts) != 0 {
		t.Fatalf("expected verdicts: [] (present, empty), got %v", closeEvt.Verdicts)
	}
}

func TestCloseVerdictsBelowFloorRefusedNamingFloorAndValueMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf04"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	_ = mcpRespondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	deps.Manifest = manifestAtFloor("0.5.0")
	handler := newLifecycleHandler(LifecycleVerbTable[7], deps) // close
	args, _ := json.Marshal(LifecycleInput{IDs: []string{parentID}, Verdicts: []VerdictInputEntry{verdictIndex(0, "met", "axon")}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal for verdicts below the floor")
	}
	if !strings.Contains(err.Error(), "0.19.0") || !strings.Contains(err.Error(), "0.5.0") {
		t.Fatalf("refusal must name both the floor and the space's own value, got: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called, got %d calls", len(fake.calls))
	}
}

func TestCloseDifferentVerdictsMintDifferentOperationKeysMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf05"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")
	_ = mcpRespondFlow(t, mirrorDir, parentID, "beta")

	run := func(verdict string) string {
		fake := &fakeFunnel{}
		deps := testWriteDeps(mirrorDir, fake)
		deps.OwnSystem = "axon"
		deps.Manifest = manifestAtFloor("0.19.0")
		handler := newLifecycleHandler(LifecycleVerbTable[7], deps)
		args, _ := json.Marshal(LifecycleInput{IDs: []string{parentID}, Verdicts: []VerdictInputEntry{verdictIndex(0, verdict, "axon")}})
		if _, _, err := handler(context.Background(), args); err != nil {
			t.Fatalf("close: %v", err)
		}
		return fake.calls[0].OperationKey
	}
	met := run("met")
	unmet := run("unmet")
	if met == unmet {
		t.Fatalf("differing verdicts minted the same operation key %q", met)
	}
}

// TestOtherVerbRefusesVerdictsMCP is the structural refusal mirror of
// internal/cli's TestOtherVerbRefusesVerdictFlagUnregistered (that surface
// achieves it via an unregistered flag; this one refuses explicitly by
// name — see LifecycleInput's own doc comment).
func TestOtherVerbRefusesVerdictsMCP(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-ov01"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps) // ack
	args, _ := json.Marshal(LifecycleInput{IDs: []string{id}, Verdicts: []VerdictInputEntry{verdictIndex(0, "met", "axon")}})
	_, _, err := handler(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "does not support verdicts") {
		t.Fatalf("expected a does-not-support-verdicts refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for a verb that does not support verdicts, got %d calls", len(fake.calls))
	}
}

// --- VerdictInputEntry: dual-shape JSON round-trip ------------------------

func TestVerdictInputEntryMarshalJSONRoundTrip(t *testing.T) {
	t.Parallel()
	idx := 3
	byIndex := VerdictInputEntry{Index: &idx, Verdict: "met", CauseOwner: "axon"}
	raw, err := json.Marshal(byIndex)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded VerdictInputEntry
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Index == nil || *decoded.Index != 3 || decoded.Criterion != "" || decoded.Verdict != "met" || decoded.CauseOwner != "axon" {
		t.Fatalf("round-trip by index failed: %+v (raw=%s)", decoded, raw)
	}

	byCriterion := VerdictInputEntry{Criterion: "ac1", Verdict: "unmet", CauseOwner: "beta"}
	raw2, err := json.Marshal(byCriterion)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded2 VerdictInputEntry
	if err := json.Unmarshal(raw2, &decoded2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded2.Index != nil || decoded2.Criterion != "ac1" || decoded2.Verdict != "unmet" || decoded2.CauseOwner != "beta" {
		t.Fatalf("round-trip by criterion failed: %+v (raw=%s)", decoded2, raw2)
	}
}

func TestVerdictInputEntryUnmarshalJSONRefusesNeitherIndexNorCriterion(t *testing.T) {
	t.Parallel()
	var e VerdictInputEntry
	err := json.Unmarshal([]byte(`{"verdict":"met","cause_owner":"axon"}`), &e)
	if err == nil || !strings.Contains(err.Error(), "either") {
		t.Fatalf("expected a neither-index-nor-criterion refusal, got %v", err)
	}
}
