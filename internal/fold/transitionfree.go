package fold

// freeKey names a (Kind, Transition) pair whose events change no artifact
// state. A ZERO Kind means "every kind": `note` is transition-free
// everywhere (D-025), while `acknowledge` is transition-free only on an
// announcement and genuinely state-moving on a requirement.
type freeKey struct {
	Kind       Kind
	Transition string
}

// transitionFreeHandler applies one transition-free event. It takes the
// same arguments as every other apply path so Apply's dispatch reads
// uniformly, even where a particular handler ignores one of them.
type transitionFreeHandler func(env Envelope, result *Result, event Event, membership MembershipView)

// transitionFreeHandlers is the single source for BOTH halves of the
// transition-free fact: which events Apply routes away from the transition
// table, and what TransitionFree() answers about them.
//
// They used to be two facts. Apply carried two hardcoded `case` clauses
// and any surface that needed the predicate re-derived it by guessing —
// the dashboard's guess was that a `note` is a protocol event, so it
// rendered a note as the latest protocol move on the home card. Deriving
// the predicate from the dispatch means a new transition-free kind cannot
// be added to one and forgotten in the other.
var transitionFreeHandlers = map[freeKey]transitionFreeHandler{
	{Transition: TNote}: applyNote,
	{Kind: KindAnnouncement, Transition: TAcknowledge}: applyBroadcastAck,
	{Kind: KindContract, Transition: TActivate}:        applyContractActivation,
}

// lookupTransitionFree resolves the handler for (kind, transition),
// preferring a kind-specific entry over the any-kind one. The two-step
// lookup is what lets `acknowledge` be free on an announcement without
// making it free everywhere.
func lookupTransitionFree(kind Kind, transition string) (transitionFreeHandler, bool) {
	if h, ok := transitionFreeHandlers[freeKey{Kind: kind, Transition: transition}]; ok {
		return h, true
	}
	h, ok := transitionFreeHandlers[freeKey{Transition: transition}]
	return h, ok
}

// TransitionFree reports whether a transition changes no artifact state.
// Derived from the same handler set Apply itself dispatches on, so the
// predicate and the behaviour cannot drift apart.
//
// Today this is `note` (D-025) and broadcast-acknowledge. The second is
// why the signature takes a Kind: acknowledge is state-moving on a
// requirement and transition-free on an announcement, so a name-only
// answer is wrong for one of the two.
func TransitionFree(kind Kind, transition string) bool {
	_, ok := lookupTransitionFree(kind, transition)
	return ok
}
