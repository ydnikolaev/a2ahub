package fold

import "sort"

// Vocabulary is every name this domain can put on a surface: the outcomes,
// the states a subject of each kind can hold, and the transitions.
//
// It exists so a GATE can derive what a renderer is forbidden to spell,
// instead of carrying its own copy of the list. A hand-written list in a
// gate is the defect this epic exists to remove: it is green on the day it
// is written and silently wrong the day a state is added.
type Vocabulary struct {
	// Outcomes is every value OutcomeOf can return, sorted.
	Outcomes []string `json:"outcomes"`
	// States maps each kind to every state a subject of that kind can be
	// in — the union of the states it can REST in and the states a
	// transition row can depart FROM. Both halves are needed: a renderer
	// spelling a mid-transition state is exactly as wrong as one spelling
	// a resting state, and neither enumerator alone contains both.
	States map[string][]string `json:"states"`
	// Transitions is every transition name in the table, plus the
	// transition-free ones, sorted. `note` carries no row and must still
	// be in the vocabulary a surface may name.
	Transitions []string `json:"transitions"`
	// HumanGates maps every always-human-gated transition to the §3.7 gate
	// it sits behind — a copy of HumanGate's own registry, never a second
	// one. It exists so a gate policing which transitions a loop step may
	// route an agent to self-serve can ask the binary instead of carrying
	// its own roster, the same defect this type already exists to remove
	// for outcomes/states/transitions.
	HumanGates map[string]string `json:"human_gates"`
}

// Outcomes returns the five outcome values, sorted. A caller enumerating
// them must not re-list the constants: that is the second hand-written
// list this type exists to prevent.
func Outcomes() []Outcome {
	return []Outcome{OutcomeOpen, OutcomeRefused, OutcomeSettled, OutcomeSuperseded, OutcomeWithdrawn}
}

// BuildVocabulary derives the whole vocabulary from the transition table
// and the outcome map — never from a literal list. Every slice is freshly
// allocated and sorted, so a caller mutating one cannot corrupt the next
// call, the same discipline RestingStates and TransitionRows already keep.
func BuildVocabulary() Vocabulary {
	v := Vocabulary{States: map[string][]string{}, HumanGates: map[string]string{}}
	for transition, gate := range humanGates {
		v.HumanGates[transition] = gate
	}

	for _, o := range Outcomes() {
		v.Outcomes = append(v.Outcomes, string(o))
	}
	sort.Strings(v.Outcomes)

	states := map[Kind]map[State]bool{}
	add := func(k Kind, s State) {
		if s == StateDynamic || s == StateNone {
			return
		}
		if states[k] == nil {
			states[k] = map[State]bool{}
		}
		states[k][s] = true
	}
	for _, krs := range RestingStates() {
		add(krs.Kind, krs.State)
	}
	for _, ks := range SubjectStates() {
		add(ks.Kind, ks.State)
	}
	for kind, set := range states {
		names := make([]string, 0, len(set))
		for s := range set {
			names = append(names, string(s))
		}
		sort.Strings(names)
		v.States[string(kind)] = names
	}

	transitions := map[string]bool{}
	for _, tk := range TransitionRows() {
		transitions[tk.Transition] = true
	}
	for key := range transitionFreeHandlers {
		transitions[key.Transition] = true
	}
	for t := range transitions {
		v.Transitions = append(v.Transitions, t)
	}
	sort.Strings(v.Transitions)

	return v
}
