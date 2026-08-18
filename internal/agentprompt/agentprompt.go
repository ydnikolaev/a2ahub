// Package agentprompt composes the fact set behind an agent prompt — the
// dashboard's "prompt for the agent" button today, and any other caller (a
// chat notifier, the CI plane) that needs to hand an agent its own next move
// without importing the presentation package to get it.
//
// The only fact that matters is which moves this system may legally take,
// and that is the fold engine's, resolved by the caller against whatever
// read surface it has. Nothing here re-derives it; this package selects our
// own moves out of the answer and attaches the two pointers into the skill
// that tell an agent where the rules for that move are written down.
package agentprompt

import "github.com/ydnikolaev/a2ahub/internal/fold"

// AgentPrompt is the fact set a copied agent prompt is assembled from. The
// facts are composed here and the sentences in the browser, for one reason:
// which moves this system may legally make next is the fold engine's answer,
// and restating it client-side would make the page a second source of protocol
// truth — the rule SKILL.md puts above every other rule it carries.
type AgentPrompt struct {
	// Moves are the transitions Data.Self may take on this artifact right now,
	// in the fold table's own order.
	Moves []string `json:"moves"`
	// AskFirst is the subset of Moves an agent must put to its human before
	// taking, because they commit the system's resources or foreclose an option
	// that cannot be reopened. The discriminator is the transition, never the
	// document type.
	AskFirst []string `json:"askFirst,omitempty"`
	// Doc and Loop are paths inside the installed a2ahub skill: the file that
	// owns this artifact type's shape, and the loop this move belongs to. Empty
	// rather than guessed when the type is one the skill does not document.
	Doc  string `json:"doc,omitempty"`
	Loop string `json:"loop,omitempty"`
	// Drafts are the authoring files for what TAKING one of these moves
	// produces, which is a different document from the one being read:
	// responding to a work request drafts a response. Empty when every move
	// available here only records an event against the document already there.
	Drafts []string `json:"drafts,omitempty"`
}

// Action identifies an available transition and its allowed actors — this
// package's own input type, so that internal/agentprompt depends on nothing
// but internal/fold instead of on the cache- or html-shaped next-action
// projection. Callers convert their own representation (html.ThreadNextAction,
// cache's next-action list, ...) into a slice of these.
type Action struct {
	Transition string
	By         []string
}

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
// The page names below are the pages those sections actually live on since
// P13 split loops.md on 2026-08-12. The `§` is what makes the pointer usable
// either way — every loop page keeps its section number, and loops.md's own
// selector table maps `§` to page — but naming the file the reader must open
// is the difference between a pointer and a riddle. `prompt_test.go` asserts
// only the `§` prefix, so nothing here was red: the strings were true-looking,
// inert, and pointed at a page the sections had left.
func promptLoop(kind fold.Kind, outgoing bool) string {
	if kind == fold.KindContract {
		if outgoing {
			return "loops/contract-change.md §8.4 (contract-owner loop)"
		}
		return "loops/contract-change.md §8.4a (consumer loop)"
	}
	if outgoing {
		return "loops/send.md §8.2 (send loop)"
	}
	return "loops/receive.md §8.3 (receive loop)"
}

// Of selects self's own moves out of one open item's legal next actions and
// returns the fact set, or nil when the move belongs to somebody else. A
// prompt with no move in it would tell an agent to go and do nothing.
func Of(actions []Action, artifactType, self string, outgoing bool) *AgentPrompt {
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

func containsString(in []string, want string) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}
