package fold

import "sort"

// TransitionKey identifies one (Kind, From, Transition) triple this
// package's transition rows admit — TransitionRows' sole element type.
// Unlike KindState, StateNone IS in scope here: a caller driving a path
// (internal/livee2e, or the next one after it) can perform a `create`
// transition, so the "artifact does not exist yet" fromState is a real,
// coverable subject rather than the sentinel SubjectStates excludes.
type TransitionKey struct {
	Kind       Kind
	From       State
	Transition string
}

// TransitionRows returns every distinct (Kind, From, Transition) triple
// appearing in this package's transition rows, deduplicated and sorted
// (Kind, then From, then Transition) for determinism. A fresh slice is
// returned on every call so a caller cannot corrupt a shared value by
// mutating what it gets back.
//
// The triple ONLY — deliberately no Role, no To. This is the same
// refusal SubjectStates made: a public accessor carrying Role would
// invite the next caller (a path runner, most plausibly) to re-derive
// "who may drive this transition" from the table instead of asking
// LegalNextFor, the actual decision surface. TransitionRows exists so a
// caller can enumerate the transition universe to check for coverage —
// not so it can compute legality from the raw rows.
func TransitionRows() []TransitionKey {
	seen := make(map[TransitionKey]bool)
	for _, r := range rows {
		seen[TransitionKey{Kind: r.Kind, From: r.From, Transition: r.Transition}] = true
	}

	out := make([]TransitionKey, 0, len(seen))
	for tk := range seen {
		out = append(out, tk)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Transition < out[j].Transition
	})
	return out
}
