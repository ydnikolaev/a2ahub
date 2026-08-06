package fold

import "sort"

// KindState pairs a Kind with a fromState — one row-table subject.
// SubjectStates is the sole accessor over the pair space this package's
// transition rows admit; there is deliberately no exported Rows()/Row
// accessor, which would invite a caller (internal/pendency, or the next
// one after it) to re-derive "whose move is it" by scanning the raw
// table instead of asking a decision surface.
type KindState struct {
	Kind  Kind
	State State
}

// SubjectStates returns every distinct (Kind, From) pair appearing in
// this package's transition rows, excluding StateNone (the "artifact
// does not exist yet" sentinel used only for the create transition),
// deduplicated and sorted (Kind, then State) for determinism. A fresh
// slice is returned on every call so a caller cannot corrupt a shared
// value by mutating what it gets back.
func SubjectStates() []KindState {
	seen := make(map[KindState]bool)
	for _, r := range rows {
		if r.From == StateNone {
			continue
		}
		seen[KindState{Kind: r.Kind, State: r.From}] = true
	}

	out := make([]KindState, 0, len(seen))
	for ks := range seen {
		out = append(out, ks)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].State < out[j].State
	})
	return out
}
