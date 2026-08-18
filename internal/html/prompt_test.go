package html

import (
	"reflect"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/agentprompt"
	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestAgentPromptOfDelegatesToAgentprompt is the wiring test left behind by
// the space-notify-2026-08 P2 extraction: the protocol tables and the
// selection logic itself moved to internal/agentprompt (see
// internal/agentprompt's own tests for that behaviour), and this package now
// only converts its own ThreadNextAction into agentprompt.Action and hands
// the call through. This asserts the conversion carries the same answer
// agentprompt.Of would give directly, so the wrapper cannot silently diverge
// from the package it delegates to.
func TestAgentPromptOfDelegatesToAgentprompt(t *testing.T) {
	t.Parallel()
	actions := []ThreadNextAction{
		{Transition: fold.TAcknowledge, By: []string{"checkout"}},
		{Transition: fold.TDecline, By: []string{"checkout", "atlas"}},
		{Transition: fold.TClose, By: []string{"atlas"}},
	}
	converted := make([]agentprompt.Action, 0, len(actions))
	for _, action := range actions {
		converted = append(converted, agentprompt.Action{Transition: action.Transition, By: action.By})
	}

	got := agentPromptOf(actions, string(fold.KindQuestion), "checkout", false)
	want := agentprompt.Of(converted, string(fold.KindQuestion), "checkout", false)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agentPromptOf = %+v, want the same answer as agentprompt.Of directly: %+v", got, want)
	}
	if got == nil {
		t.Fatal("checkout can acknowledge and decline; the prompt must not be nil")
	}

	if got := agentPromptOf(actions, string(fold.KindQuestion), "ledger", false); got != nil {
		t.Fatalf("ledger may take none of these moves and must get no prompt at all, got %+v", got)
	}
}

// TestAgentPromptForGatesOnWaitingOn is the pendency gate that STAYS in this
// package: a prompt appears only where pendency names this system as owing
// the next move, never merely because a move is legal (spec P2, and the
// XC-axon-getvisa-ingest defect the surrounding comment describes).
func TestAgentPromptForGatesOnWaitingOn(t *testing.T) {
	t.Parallel()
	actions := []ThreadNextAction{{Transition: fold.TPublish, By: []string{"checkout"}}}

	if got := agentPromptFor(nil, "checkout", actions, string(fold.KindContract), true); got != nil {
		t.Fatalf("waitingOn empty: no prompt is owed, got %+v", got)
	}
	if got := agentPromptFor([]string{"checkout"}, "checkout", actions, string(fold.KindContract), true); got == nil {
		t.Fatal("checkout is named in waitingOn and can publish; the prompt must not be nil")
	}
}
