package html

import (
	"github.com/ydnikolaev/a2ahub/internal/agentprompt"
	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// prompt.go composes AgentPrompt — the facts behind the dashboard's "prompt for
// the agent" button. A human who spots an arriving document before their agent
// does copies a prompt from here and hands the move back to the agent.
//
// The protocol tables and the selection logic itself moved to
// internal/agentprompt (spec space-notify-2026-08 P2), so a caller outside
// the presentation layer can compose the same prompt without importing
// internal/html. What stays here is the pendency gate that decides WHETHER a
// row gets a prompt at all, and the type-converting wrapper that keeps
// assemble.go and demo.go's cache-shaped call sites unchanged.

// agentPromptOf converts html's own ThreadNextAction slice into
// agentprompt.Action and delegates the composition to internal/agentprompt —
// the wire-facing wrapper that keeps assemble.go and demo.go's call sites
// (which pass []ThreadNextAction) unchanged by this extraction.
func agentPromptOf(actions []ThreadNextAction, artifactType, self string, outgoing bool) *AgentPrompt {
	converted := make([]agentprompt.Action, 0, len(actions))
	for _, action := range actions {
		converted = append(converted, agentprompt.Action{Transition: action.Transition, By: action.By})
	}
	return agentprompt.Of(converted, artifactType, self, outgoing)
}

// agentPrompt is agentPromptOf over a cache open item, for the rows whose
// thread never becomes a rendered conversation — gated on PENDENCY, not on
// legality.
//
// A prompt is an instruction to an agent. `next_actions` is the list of moves
// the protocol would ACCEPT, and it stays exactly as it is: a published
// contract's owner may always publish a successor or deprecate it, and the
// dashboard is right to say so. But nobody is waiting for either, and turning
// "you may" into "here is your move" is the same defect this phase exists to
// end — a surface telling an agent to act where the relation says nothing is
// owed.
//
// It was reachable on a real space: XC-axon-getvisa-ingest, published and
// adopted and settled, carried `waiting_on: []` and no expected transition,
// and still offered `publish, deprecate` to any agent reading the dashboard.
//
// So the prompt appears only where pendency names THIS system as owing the
// next move. WaitingOn is the relation's own answer (internal/pendency), never
// re-derived here.
func agentPrompt(item cache.OpenItem, self string, outgoing bool) *AgentPrompt {
	actions := make([]ThreadNextAction, 0, len(item.NextActions))
	for _, action := range item.NextActions {
		actions = append(actions, ThreadNextAction{Transition: action.Transition, By: action.By})
	}
	return agentPromptFor(item.WaitingOn, self, actions, item.Type, outgoing)
}

// agentPromptFor is the ONE gate, so the thread view and the exchange rows
// cannot answer differently for the same artifact. They already had: the
// settled getvisa contract stopped prompting on its exchange row while its
// thread still offered `publish, deprecate`.
//
// waitingOn is internal/pendency's own verdict, carried through cache and
// never re-derived here.
func agentPromptFor(waitingOn []string, self string, actions []ThreadNextAction, artifactType string, outgoing bool) *AgentPrompt {
	if !containsString(waitingOn, self) {
		return nil
	}
	return agentPromptOf(actions, artifactType, self, outgoing)
}

// openItemIndex is every open item the visible artifacts fold to, keyed by
// space and artifact id.
type openItemIndex map[string]map[string]cache.OpenItem

func (idx openItemIndex) put(space string, items []cache.OpenItem) {
	if idx[space] == nil {
		idx[space] = map[string]cache.OpenItem{}
	}
	for _, item := range items {
		idx[space][item.ID] = item
	}
}

func (idx openItemIndex) get(space, id string) (cache.OpenItem, bool) {
	item, ok := idx[space][id]
	return item, ok
}
