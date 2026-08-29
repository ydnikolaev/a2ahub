package livee2e

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// scenarios_verb_agreement_test.go is P10's own gate over
// verbAgreementReplays() (scenarios_incidents.go), coverage.go's instance
// roster, and scenarios_verb_agreement.go's universe-2 wiring — the same
// discipline incidents_test.go already applies to its own, separate
// registry one file over.

const verbAgreementRepoRoot = "../.."

var verbAgreementShaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var verbAgreementProofRefPattern = regexp.MustCompile(`^(.+\.go):([A-Za-z0-9_]+)$`)

// verbAgreementContainsString is a package-local helper — deliberately not
// named containsString, which scenarios_operational_confidence_live.go
// already defines behind //go:build livee2e; this file must build both
// tagged and untagged (make check runs the plain suite, this wave's own
// self-verify adds the tagged one), so it cannot share that symbol.
func verbAgreementContainsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func findVerbAgreementReplay(t *testing.T, id string) verbAgreementReplay {
	t.Helper()
	for _, r := range verbAgreementReplays() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("verbAgreementReplays(): no entry with ID %q", id)
	return verbAgreementReplay{}
}

// TestVerbAgreementReplaysAreWellFormed refuses a stale or bare replay
// entry — mirrors TestIncidentReplaysAreWellFormed's own checks for the
// sibling registry, plus the Class/PreFix* fields this registry adds.
func TestVerbAgreementReplaysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range verbAgreementReplays() {
		if r.ID == "" {
			t.Fatalf("verbAgreementReplays(): an entry has an empty ID (Name=%q)", r.Name)
		}
		if seen[r.ID] {
			t.Errorf("verbAgreementReplays() lists %q twice", r.ID)
		}
		seen[r.ID] = true

		if strings.TrimSpace(r.Name) == "" {
			t.Errorf("%s: empty Name", r.ID)
		}
		if len(r.Instances) == 0 {
			t.Errorf("%s: no Instances — a replay entry must discharge at least one fb-... id", r.ID)
		}
		if strings.TrimSpace(r.FixedByPhase) == "" {
			t.Errorf("%s: empty FixedByPhase", r.ID)
		}
		if strings.TrimSpace(r.CorpusEvidence) == "" {
			t.Errorf("%s: empty CorpusEvidence", r.ID)
		}
		if !verbAgreementShaPattern.MatchString(r.FixCommit) {
			t.Errorf("%s: FixCommit %q is not a 40-char lowercase hex sha", r.ID, r.FixCommit)
		}
		if !verbAgreementShaPattern.MatchString(r.FixCommitParent) {
			t.Errorf("%s: FixCommitParent %q is not a 40-char lowercase hex sha", r.ID, r.FixCommitParent)
		}
		if r.FixCommit == r.FixCommitParent {
			t.Errorf("%s: FixCommit and FixCommitParent are identical — a fix commit is never its own parent", r.ID)
		}
		if strings.TrimSpace(r.ArchaeologyCommand) == "" {
			t.Errorf("%s: empty ArchaeologyCommand", r.ID)
		}
		if strings.TrimSpace(r.ArchaeologyFinding) == "" {
			t.Errorf("%s: empty ArchaeologyFinding", r.ID)
		}
		if len(r.ProofRefs) == 0 {
			t.Errorf("%s: no ProofRefs", r.ID)
		}
		if strings.TrimSpace(r.BinaryReplayStatus) == "" {
			t.Errorf("%s: empty BinaryReplayStatus", r.ID)
		}

		switch r.Class {
		case pairPreviewingActing, pairActingChecking:
			if r.PreFixLeftVerb == "" || r.PreFixRightVerb == "" {
				t.Errorf("%s: Class=%s requires PreFixLeftVerb and PreFixRightVerb", r.ID, r.Class)
			}
			if len(r.PreFixReaderVerdicts) != 0 {
				t.Errorf("%s: Class=%s must not carry PreFixReaderVerdicts", r.ID, r.Class)
			}
		case pairReaderReader:
			if len(r.PreFixReaderVerdicts) < 2 {
				t.Errorf("%s: Class=pairReaderReader requires at least 2 PreFixReaderVerdicts entries", r.ID)
			}
			if r.PreFixLeftVerb != "" || r.PreFixRightVerb != "" {
				t.Errorf("%s: Class=pairReaderReader must not carry PreFixLeftVerb/PreFixRightVerb", r.ID)
			}
		default:
			t.Errorf("%s: unrecognised Class %q", r.ID, r.Class)
		}
	}
}

// TestVerbAgreementReplayCorpusEvidenceExists proves every CorpusEvidence
// path resolves to a real file on disk (feedback/inbox/*, not a private
// corpus path — no skip needed).
func TestVerbAgreementReplayCorpusEvidenceExists(t *testing.T) {
	for _, r := range verbAgreementReplays() {
		full := filepath.Join(verbAgreementRepoRoot, r.CorpusEvidence)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("%s: CorpusEvidence %q: %v", r.ID, r.CorpusEvidence, err)
		}
	}
}

// TestVerbAgreementReplayProofRefsExistAndDefineTheNamedFunc mirrors
// TestIncidentReplayProofRefsExistAndDefineTheNamedFunc for this registry.
func TestVerbAgreementReplayProofRefsExistAndDefineTheNamedFunc(t *testing.T) {
	for _, r := range verbAgreementReplays() {
		for _, ref := range r.ProofRefs {
			m := verbAgreementProofRefPattern.FindStringSubmatch(ref)
			if m == nil {
				t.Errorf("%s: ProofRefs entry %q is not \"<path>:<TestFuncName>\"", r.ID, ref)
				continue
			}
			path, funcName := m[1], m[2]
			full := filepath.Join(verbAgreementRepoRoot, path)
			raw, err := os.ReadFile(full)
			if err != nil {
				t.Errorf("%s: ProofRefs %q: %v", r.ID, ref, err)
				continue
			}
			needle := "func " + funcName + "("
			if !strings.Contains(string(raw), needle) {
				t.Errorf("%s: ProofRefs %q: %s does not define %q", r.ID, ref, path, needle)
			}
		}
	}
}

// TestAdvertiserLegalityReplayRedsOnPreFixFacts is AC-12: the
// advertiser/legality pair (fb-20260801-457629) is declared, and its cell
// reds when fed the pre-fix facts this registry's own archaeology
// established — "run against the pre-fix tree" (spec 10 §11's 2026-08-29
// amendment), never against HEAD, which no longer reproduces the bug.
func TestAdvertiserLegalityReplayRedsOnPreFixFacts(t *testing.T) {
	r := findVerbAgreementReplay(t, "advertiser-legality-note-submitted")
	if r.Class != pairPreviewingActing {
		t.Fatalf("advertiser/legality Class = %s, want %s (spec 10 §11: \"That is US-1 (previewing -> acting) exactly\")", r.Class, pairPreviewingActing)
	}
	if !verbAgreementContainsString(r.Instances, "fb-20260801-457629") {
		t.Fatalf("advertiser/legality replay does not name fb-20260801-457629 in Instances: %v", r.Instances)
	}
	err := assertDirectionalCell(r.Class, r.PreFixLeftVerb, r.PreFixLeftAccepted, r.PreFixRightVerb, r.PreFixRightRefused)
	if err == nil {
		t.Fatal("expected the pre-fix advertiser/legality facts to red — a gate that has never been red is a gate nobody has tested (spec 10 §10)")
	}
	t.Logf("pre-fix replay reds as expected: %v", err)
}

// TestReaderReaderReplayRedsOnPreFixFacts is the reader/reader "START FROM
// RED" cell (spec 10 plan's Implementor entry point): the packed-README
// path-shape disagreement between the validator and the mirror/doctor
// readers, discharged the same way — its own PreFixReaderVerdicts, run
// through assertReaderAgreement, must red.
func TestReaderReaderReplayRedsOnPreFixFacts(t *testing.T) {
	r := findVerbAgreementReplay(t, "data-package-readme-reader-disagreement")
	if r.Class != pairReaderReader {
		t.Fatalf("Class = %s, want %s", r.Class, pairReaderReader)
	}
	for _, want := range []string{"fb-20260812-d31acb", "fb-20260812-f9cfac"} {
		if !verbAgreementContainsString(r.Instances, want) {
			t.Fatalf("reader/reader replay does not name %s in Instances: %v", want, r.Instances)
		}
	}
	err := assertReaderAgreement("is this path shape a legitimate data-package payload?", r.PreFixReaderVerdicts, nil)
	if err == nil {
		t.Fatal("expected the pre-fix reader/reader facts to red")
	}
	t.Logf("pre-fix replay reds as expected: %v", err)
}

// TestActingCheckingReplaysRedOnPreFixFacts covers the remaining
// acting->checking instances (a84550 — this epic's own flagship for the
// polarity a two-class model would exempt — plus 5c73a9 and d1e370): each
// entry's own pre-fix facts must red through the SAME assertion function a
// live cell would use.
func TestActingCheckingReplaysRedOnPreFixFacts(t *testing.T) {
	for _, id := range []string{
		"verify-export-descriptor-reserialization",
		"data-package-payload-pol-002",
		"submit-carries-unvalidated-companion",
	} {
		id := id
		t.Run(id, func(t *testing.T) {
			r := findVerbAgreementReplay(t, id)
			if r.Class != pairActingChecking {
				t.Fatalf("%s: Class = %s, want %s", id, r.Class, pairActingChecking)
			}
			err := assertDirectionalCell(r.Class, r.PreFixLeftVerb, r.PreFixLeftAccepted, r.PreFixRightVerb, r.PreFixRightRefused)
			if err == nil {
				t.Fatalf("%s: expected the pre-fix facts to red", id)
			}
		})
	}
}

// TestPreviewingActingReplaysRedOnPreFixFacts covers the remaining
// previewing->acting instances (c6ad38, 3539ac).
func TestPreviewingActingReplaysRedOnPreFixFacts(t *testing.T) {
	for _, id := range []string{
		"contract-baseline-artifacts-arm-missing",
		"template-show-stale-generation",
	} {
		id := id
		t.Run(id, func(t *testing.T) {
			r := findVerbAgreementReplay(t, id)
			if r.Class != pairPreviewingActing {
				t.Fatalf("%s: Class = %s, want %s", id, r.Class, pairPreviewingActing)
			}
			err := assertDirectionalCell(r.Class, r.PreFixLeftVerb, r.PreFixLeftAccepted, r.PreFixRightVerb, r.PreFixRightRefused)
			if err == nil {
				t.Fatalf("%s: expected the pre-fix facts to red", id)
			}
		})
	}
}

// TestVerbAgreementInstanceCoverage is AC-13: each of the EIGHT instances
// is accounted for, by id, against coverage.go's own roster — an honest
// count, never padded.
func TestVerbAgreementInstanceCoverage(t *testing.T) {
	ids := verbAgreementInstanceIDs()
	if len(ids) != 8 {
		t.Fatalf("verbAgreementInstanceIDs() returned %d ids, want exactly 8 (epic README's own corrected C3 row)", len(ids))
	}
	covered, uncovered := coveredVerbAgreementInstances()
	if len(uncovered) != 0 {
		t.Errorf("uncovered instances (honest, not padded): %v", uncovered)
	}
	t.Logf("covered %d/%d instances: %v", len(covered), len(ids), covered)

	nonInstances := verbAgreementNonInstances()
	if len(nonInstances) != 2 {
		t.Fatalf("verbAgreementNonInstances() returned %d entries, want exactly 2 (README: \"corrected from ten\")", len(nonInstances))
	}
	for _, n := range nonInstances {
		if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.Reason) == "" {
			t.Errorf("non-instance %+v: both ID and Reason are required", n)
		}
	}
}

// TestAdvertiserLegalityNoteCellsAreGreenOnHEAD proves the production call
// — fold.BuildVocabulary(), never a fixture — currently agrees across
// every state a response can occupy, which is exactly what AC-4's own
// 2026-08-28 amendment requires implementers to check before claiming a
// cell reproduces on HEAD: "an implementer who builds this cell expecting
// the tree to redden it will get a green cell and no signal". This one is
// EXPECTED green: the fix already generalised across the whole state
// universe, not only the reported one.
func TestAdvertiserLegalityNoteCellsAreGreenOnHEAD(t *testing.T) {
	states, errs := advertiserLegalityNoteCells(fold.BuildVocabulary())
	if len(states) == 0 {
		t.Fatal("advertiserLegalityNoteCells evaluated zero states — fold.BuildVocabulary() no longer publishes any KindResponse state")
	}
	if len(errs) != 0 {
		t.Fatalf("advertiserLegalityNoteCells(fold.BuildVocabulary()) = %v, want no disagreement on HEAD", errs)
	}
}

// TestAdvertiserLegalityNoteCellsAreDerivedFromVocabulary is AC-7/AC-14 at
// the mechanism level: advertiserLegalityNoteCells reacts to whatever
// vocab.States carries — a FIXTURE Vocabulary here, never fold's own
// package edited, and never a second state list maintained inside
// internal/livee2e — proving the cross-product is driven by its
// PARAMETER, not a literal this file carries.
func TestAdvertiserLegalityNoteCellsAreDerivedFromVocabulary(t *testing.T) {
	fixture := fold.Vocabulary{States: map[string][]string{
		string(fold.KindResponse): {"draft", "submitted", "a-state-the-real-fold-package-does-not-publish-yet"},
	}}
	states, _ := advertiserLegalityNoteCells(fixture)
	got := append([]string(nil), states...)
	sort.Strings(got)
	want := []string{"a-state-the-real-fold-package-does-not-publish-yet", "draft", "submitted"}
	if len(got) != len(want) {
		t.Fatalf("evaluated %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("evaluated %v, want %v", got, want)
		}
	}
}

// TestDeclaredTransitionExceptionsAreWellFormed guards universe 2's own
// classify-or-declare exception roster.
func TestDeclaredTransitionExceptionsAreWellFormed(t *testing.T) {
	exceptions := declaredTransitionExceptions()
	if len(exceptions) == 0 {
		t.Fatal("declaredTransitionExceptions() is empty — spec 10 §11's 2026-08-28 amendment requires at least the P5 funnel-guard cell")
	}
	for _, e := range exceptions {
		if e.Kind == "" || e.State == "" || e.Transition == "" {
			t.Errorf("%+v: Kind, State and Transition are all required", e)
		}
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("%+v: empty Reason — an exception must be announced, never silent", e)
		}
	}
}

// TestVerbCatalogueCoversDeclaredPairVerbs is AC-11 wired to this phase's
// REAL declared pairs (not a synthetic fixture — see
// TestUnclassifiedCatalogueVerbReds in verbclasses_test.go for the
// mechanism-level proof): every verb name verbAgreementReplays() actually
// cites must be classified or declared in verbCatalogue().
func TestVerbCatalogueCoversDeclaredPairVerbs(t *testing.T) {
	unclassified := unclassifiedCatalogueVerbs(declaredPairVerbs(), verbCatalogue())
	if len(unclassified) != 0 {
		t.Fatalf("verbs referenced by a declared pair but absent from verbCatalogue(): %v", unclassified)
	}
}
