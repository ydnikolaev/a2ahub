package html

import (
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// prompt.go composes AgentPrompt — the facts behind the dashboard's "prompt for
// the agent" button. A human who spots an arriving document before their agent
// does copies a prompt from here and hands the move back to the agent.
//
// The only fact that matters is which moves this system may legally take, and
// that is the fold engine's, resolved against the space manifest by
// cache.OpenItem. Nothing here re-derives it; this file selects our own moves
// out of the answer and attaches the two pointers into the skill that tell an
// agent where the rules for that move are written down.

// promptDoc maps an artifact type to the skill file that owns its shape. Keyed
// by fold.Kind so a new artifact type fails to compile rather than silently
// pointing an agent at nothing.
var promptDoc = map[fold.Kind]string{
	fold.KindQuestion:     "reference/authoring/question.md",
	fold.KindWorkRequest:  "reference/authoring/work_request.md",
	fold.KindResponse:     "reference/authoring/response.md",
	fold.KindDecision:     "reference/authoring/decision.md",
	fold.KindHandoff:      "reference/authoring/handoff.md",
	fold.KindAnnouncement: "reference/authoring/announcement.md",
	fold.KindContract:     "reference/authoring/contract.md",
	fold.KindRequirement:  "reference/authoring/requirement.md",
}

// promptDrafts maps a move to the artifact TAKING it produces, for the moves
// that produce one at all. Keyed on the move and never on the subject: an
// agent that responds to a work request drafts a *response*, and pointing it
// at work_request.md for the shape of its reply points it at the wrong file.
// A publish carries a new contract version; a deprecation is announced with an
// announcement (loops.md §8.4). Every other move records an event against the
// document already there and drafts nothing.
var promptDrafts = map[string]fold.Kind{
	fold.TRespond:   fold.KindResponse,
	fold.TPublish:   fold.KindContract,
	fold.TDeprecate: fold.KindAnnouncement,
}

// promptAskFirst lists the moves an agent must put to its human before taking.
// The discriminator is the transition and never the document type: each of
// these either commits the system's own resources or forecloses an option that
// cannot be reopened by another move. Everything absent from this map is
// routine protocol upkeep an agent takes on its own — acknowledging receipt,
// noting, verifying, answering, starting work already accepted.
var promptAskFirst = map[string]bool{
	fold.TAccept:    true, // commits engineering time
	fold.TDecline:   true, // refuses on the system's behalf
	fold.TReject:    true,
	fold.TDispute:   true,
	fold.TBlock:     true, // stops somebody else's work
	fold.TApprove:   true, // speaks for the system in a decision
	fold.TClose:     true, // ends the exchange
	fold.TDeprecate: true, // starts a clock on every consumer
	fold.TRetire:    true, // breaks whoever is still pinned
	fold.TPublish:   true, // outward-facing, and a published version never unpublishes
	fold.TCancel:    true, // escape hatches: withdrawing a promise already made
	fold.TWithdraw:  true,
	fold.TSupersede: true,
}

// promptLoop names the loop in the skill that governs this move. A contract has
// two loops depending on which end of it you stand at; everything else splits
// on direction, because "something arrived for me" and "I need something" are
// different jobs regardless of type.
func promptLoop(kind fold.Kind, outgoing bool) string {
	if kind == fold.KindContract {
		if outgoing {
			return "loops.md §8.4 (contract-owner loop)"
		}
		return "loops.md §8.4a (consumer loop)"
	}
	if outgoing {
		return "loops.md §8.2 (send loop)"
	}
	return "loops.md §8.3 (receive loop)"
}

// agentPromptOf selects self's own moves out of one open item's legal next
// actions and returns the fact set, or nil when the move belongs to somebody
// else. A prompt with no move in it would tell an agent to go and do nothing.
func agentPromptOf(actions []ThreadNextAction, artifactType, self string, outgoing bool) *AgentPrompt {
	moves := make([]string, 0, len(actions))
	askFirst := []string{}
	drafts := []string{}
	seen := map[string]bool{}
	for _, action := range actions {
		if !containsString(action.By, self) || seen[action.Transition] {
			continue
		}
		seen[action.Transition] = true
		moves = append(moves, action.Transition)
		if promptAskFirst[action.Transition] {
			askFirst = append(askFirst, action.Transition)
		}
		if doc := promptDoc[promptDrafts[action.Transition]]; doc != "" && !containsString(drafts, doc) {
			drafts = append(drafts, doc)
		}
	}
	if len(moves) == 0 {
		return nil
	}
	kind := fold.Kind(artifactType)
	return &AgentPrompt{
		Moves: moves, AskFirst: askFirst,
		Doc: promptDoc[kind], Drafts: drafts,
		Loop: promptLoop(kind, outgoing),
	}
}

// agentPrompt is agentPromptOf over a cache open item, for the rows whose
// thread never becomes a rendered conversation.
func agentPrompt(item cache.OpenItem, self string, outgoing bool) *AgentPrompt {
	actions := make([]ThreadNextAction, 0, len(item.NextActions))
	for _, action := range item.NextActions {
		actions = append(actions, ThreadNextAction{Transition: action.Transition, By: action.By})
	}
	return agentPromptOf(actions, item.Type, self, outgoing)
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
