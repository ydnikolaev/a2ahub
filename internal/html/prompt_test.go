package html

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/skill"
)

// TestPromptDocsResolveInTheShippedSkill is the gate behind the pointers the
// agent prompt hands out. The prompt tells an agent to read a named file; if
// the skill tree is reorganised and this table is not, the prompt sends it to a
// path that does not exist and the failure lands on the agent, not on us.
func TestPromptDocsResolveInTheShippedSkill(t *testing.T) {
	t.Parallel()
	kinds := []fold.Kind{
		fold.KindQuestion, fold.KindWorkRequest, fold.KindResponse, fold.KindDecision,
		fold.KindHandoff, fold.KindAnnouncement, fold.KindContract, fold.KindRequirement,
	}
	for _, kind := range kinds {
		doc, ok := promptDoc[kind]
		if !ok {
			t.Errorf("%s has no authoring doc: an agent asked to draft one gets no pointer", kind)
			continue
		}
		if _, err := fs.Stat(skill.Files, "a2ahub/"+doc); err != nil {
			t.Errorf("%s points at %q, which is not in the shipped skill: %v", kind, doc, err)
		}
	}
	if _, err := fs.Stat(skill.Files, "a2ahub/loops.md"); err != nil {
		t.Fatalf("every prompt cites loops.md, which is not in the shipped skill: %v", err)
	}
	for _, kind := range []fold.Kind{fold.KindContract, fold.KindQuestion} {
		for _, outgoing := range []bool{true, false} {
			if !strings.HasPrefix(promptLoop(kind, outgoing), "loops.md §") {
				t.Errorf("promptLoop(%s, outgoing=%v) does not name a loops.md section", kind, outgoing)
			}
		}
	}
}

// TestAgentPromptKeepsOnlyOurOwnMoves: the prompt exists to hand OUR move to an
// agent. A move another system may take is not ours to describe, and a prompt
// with nothing in it is worse than no button.
func TestAgentPromptKeepsOnlyOurOwnMoves(t *testing.T) {
	t.Parallel()
	actions := []ThreadNextAction{
		{Transition: fold.TAcknowledge, By: []string{"checkout"}},
		{Transition: fold.TDecline, By: []string{"checkout", "atlas"}},
		{Transition: fold.TClose, By: []string{"atlas"}},
	}
	got := agentPromptOf(actions, string(fold.KindQuestion), "checkout", false)
	if got == nil {
		t.Fatal("checkout can acknowledge and decline; the prompt must not be nil")
	}
	if strings.Join(got.Moves, ",") != fold.TAcknowledge+","+fold.TDecline {
		t.Fatalf("moves must be ours only, in table order: %v", got.Moves)
	}
	if strings.Join(got.AskFirst, ",") != fold.TDecline {
		t.Fatalf("declining refuses on the system's behalf and must be flagged; got %v", got.AskFirst)
	}
	if len(got.Drafts) != 0 {
		t.Fatalf("acknowledging and declining draft nothing; got %v", got.Drafts)
	}
	for _, ask := range got.AskFirst {
		if !containsString(got.Moves, ask) {
			t.Fatalf("askFirst %q is not one of the moves offered: %v", ask, got.Moves)
		}
	}
	if agentPromptOf(actions, string(fold.KindQuestion), "ledger", false) != nil {
		t.Fatal("ledger may take none of these moves and must get no prompt at all")
	}
}

// TestPromptPointsAtWhatGetsDrafted is the check the path-existence test above
// cannot make. Answering a work request drafts a RESPONSE, and a prompt that
// cites work_request.md for "the shape of anything you draft" sends the agent
// to the file for the document it is holding, not the one it is writing.
func TestPromptPointsAtWhatGetsDrafted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		subject    fold.Kind
		move       string
		wantDoc    string
		wantDrafts []string
	}{
		{"responding to a work request drafts a response", fold.KindWorkRequest, fold.TRespond,
			"reference/authoring/work_request.md", []string{"reference/authoring/response.md"}},
		{"acknowledging a question drafts nothing", fold.KindQuestion, fold.TAcknowledge,
			"reference/authoring/question.md", nil},
		{"accepting a work request drafts nothing", fold.KindWorkRequest, fold.TAccept,
			"reference/authoring/work_request.md", nil},
		{"approving a decision drafts nothing", fold.KindDecision, fold.TApprove,
			"reference/authoring/decision.md", nil},
		{"deprecating a contract is announced", fold.KindContract, fold.TDeprecate,
			"reference/authoring/contract.md", []string{"reference/authoring/announcement.md"}},
		{"publishing carries a contract version", fold.KindContract, fold.TPublish,
			"reference/authoring/contract.md", []string{"reference/authoring/contract.md"}},
	}
	for _, tc := range cases {
		got := agentPromptOf([]ThreadNextAction{{Transition: tc.move, By: []string{"checkout"}}}, string(tc.subject), "checkout", false)
		if got == nil {
			t.Errorf("%s: no prompt at all", tc.name)
			continue
		}
		if got.Doc != tc.wantDoc {
			t.Errorf("%s: subject doc = %q, want %q", tc.name, got.Doc, tc.wantDoc)
		}
		if strings.Join(got.Drafts, ",") != strings.Join(tc.wantDrafts, ",") {
			t.Errorf("%s: drafts = %v, want %v", tc.name, got.Drafts, tc.wantDrafts)
		}
	}
}

// TestAskFirstNamesRealTransitions keeps the ask-first list anchored to the
// fold table. A typo here silently downgrades a resource-committing move to one
// an agent takes on its own.
func TestAskFirstNamesRealTransitions(t *testing.T) {
	t.Parallel()
	kinds := []fold.Kind{
		fold.KindQuestion, fold.KindWorkRequest, fold.KindResponse, fold.KindDecision,
		fold.KindHandoff, fold.KindAnnouncement, fold.KindContract, fold.KindRequirement,
	}
	states := []fold.State{
		fold.StateNone, fold.StateDraft, fold.StatePublished, fold.StateDeprecated,
		fold.StateRetired, fold.StateAcknowledged, fold.StateSatisfied, fold.StateSubmitted,
		fold.StateAccepted, fold.StateInProgress, fold.StateBlocked, fold.StateResponded,
		fold.StateProposed,
	}
	known := map[string]bool{fold.TNote: true} // transition-free by D-025, never in the table
	for _, kind := range kinds {
		for _, state := range states {
			for _, mv := range fold.LegalNext(kind, state) {
				known[mv.Transition] = true
			}
		}
	}
	for transition := range promptAskFirst {
		if !known[transition] {
			t.Errorf("promptAskFirst names %q, which is not a transition in the fold table", transition)
		}
	}
	for _, routine := range []string{fold.TAcknowledge, fold.TNote, fold.TRespond, fold.TStart} {
		if promptAskFirst[routine] {
			t.Errorf("%q is routine protocol upkeep; making an agent ask before it stalls the exchange", routine)
		}
	}
}
