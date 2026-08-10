package fold

import "testing"

// TestVocabularyOutcomesMatchTheMap is the reason Outcomes() is a function
// and not a var: it must equal the keys the outcome map can actually
// produce. A constant added to the const block and forgotten here would
// make the gate that reads this vocabulary blind to it.
func TestVocabularyOutcomesMatchTheMap(t *testing.T) {
	t.Parallel()

	declared := map[Outcome]bool{}
	for _, o := range Outcomes() {
		declared[o] = true
	}

	produced := map[Outcome]bool{}
	for _, krs := range RestingStates() {
		if o := OutcomeOf(krs.Kind, krs.State); o != "" {
			produced[o] = true
		}
	}

	for o := range produced {
		if !declared[o] {
			t.Errorf("OutcomeOf can return %q but Outcomes() does not list it — a gate deriving its vocabulary here would not know the value exists", o)
		}
	}
	for o := range declared {
		if !produced[o] {
			t.Errorf("Outcomes() lists %q but no resting pair produces it — either a state lost its meaning or this is a value the domain cannot reach", o)
		}
	}
}

// TestVocabularyCoversEveryTableName walks the table and the transition-free
// registry, asserting the built vocabulary contains every name a surface
// could legitimately spell. Derived from the same sources the fold itself
// dispatches on, so a new row or a new transition-free kind joins the
// vocabulary the day it lands.
func TestVocabularyCoversEveryTableName(t *testing.T) {
	t.Parallel()

	v := BuildVocabulary()

	transitions := map[string]bool{}
	for _, name := range v.Transitions {
		transitions[name] = true
	}
	for _, r := range rows {
		if !transitions[r.Transition] {
			t.Errorf("vocabulary omits transition %q, which the table carries a row for", r.Transition)
		}
	}
	if !transitions[TNote] {
		t.Error("vocabulary omits `note` — it carries no row by design (D-025) and is still a name a surface may render")
	}

	has := func(kind Kind, state State) bool {
		for _, s := range v.States[string(kind)] {
			if s == string(state) {
				return true
			}
		}
		return false
	}

	for _, krs := range RestingStates() {
		if !has(krs.Kind, krs.State) {
			t.Errorf("vocabulary omits (%s, %s), which RestingStates() yields", krs.Kind, krs.State)
		}
	}
	// The SubjectStates half is asserted SEPARATELY and not as a
	// formality: it is the only source of the states a subject passes
	// THROUGH rather than rests in. `draft` is the clearest case — it is a
	// From for every kind's first row and a To for none of them, so
	// RestingStates yields it for `decision` alone (via its zero-events
	// fallback) and for nobody else.
	//
	// Dropping this loop was a mutation that stayed GREEN until this
	// assertion existed: a renderer spelling a mid-transition state is
	// exactly as wrong as one spelling a resting state, and the gate that
	// reads this vocabulary would have policed neither.
	for _, ks := range SubjectStates() {
		if !has(ks.Kind, ks.State) {
			t.Errorf("vocabulary omits (%s, %s), a state some transition row departs FROM — a surface may spell it, so a gate must know it", ks.Kind, ks.State)
		}
	}
}

// TestVocabularyExcludesTheSentinel keeps the internal marker out of a
// surface vocabulary. StateDynamic is a table-row sentinel resolved at
// apply time; a gate told it is a legal state name would let a component
// print `__dynamic__` and call it conformant.
func TestVocabularyExcludesTheSentinel(t *testing.T) {
	t.Parallel()

	for kind, states := range BuildVocabulary().States {
		for _, s := range states {
			if s == string(StateDynamic) || s == string(StateNone) {
				t.Errorf("vocabulary for %s contains the internal marker %q", kind, s)
			}
		}
	}
}

// TestVocabularyIsFreshEachCall pins the same determinism contract the
// three enumerators already keep: a caller mutating what it got back
// cannot corrupt what the next caller sees.
func TestVocabularyIsFreshEachCall(t *testing.T) {
	t.Parallel()

	first := BuildVocabulary()
	if len(first.Outcomes) == 0 || len(first.Transitions) == 0 || len(first.States) == 0 {
		t.Fatal("BuildVocabulary returned an empty vocabulary — a gate deriving from this would police nothing")
	}
	first.Outcomes[0] = "corrupted"
	first.Transitions[0] = "corrupted"
	for k := range first.States {
		first.States[k] = []string{"corrupted"}
	}
	for k := range first.HumanGates {
		first.HumanGates[k] = "corrupted"
	}

	second := BuildVocabulary()
	if second.Outcomes[0] == "corrupted" || second.Transitions[0] == "corrupted" {
		t.Fatal("BuildVocabulary shares backing storage across calls")
	}
	for kind, states := range second.States {
		if len(states) == 1 && states[0] == "corrupted" {
			t.Fatalf("BuildVocabulary shares the %s state slice across calls", kind)
		}
	}
	for transition, gate := range second.HumanGates {
		if gate == "corrupted" {
			t.Fatalf("BuildVocabulary shares the HumanGates map across calls (transition %q)", transition)
		}
	}
}

// TestVocabularyHumanGatesMatchesTheRegistry pins HumanGates to HumanGate's
// own map so a caller reading the vocabulary sees exactly what a direct call
// to HumanGate would answer — a second, silently divergent copy is the
// defect this field exists to prevent.
func TestVocabularyHumanGatesMatchesTheRegistry(t *testing.T) {
	t.Parallel()

	v := BuildVocabulary()
	if len(v.HumanGates) == 0 {
		t.Fatal("BuildVocabulary returned no human gates — a gate deriving from this would police nothing")
	}
	for transition, gate := range v.HumanGates {
		if got := HumanGate(transition); got != gate {
			t.Errorf("vocabulary reports HumanGates[%q] = %q, but HumanGate(%q) = %q", transition, gate, transition, got)
		}
	}
	for _, transition := range []string{TApprove, TReject} {
		if _, ok := v.HumanGates[transition]; !ok {
			t.Errorf("vocabulary.HumanGates omits %q, which HumanGate() gates", transition)
		}
	}
}

// TestVocabularyHumanGatesAreRealTransitions catches a typo in the registry
// that nothing else would: a gated verb spelled wrong would never match any
// row in the table and would silently gate nothing.
func TestVocabularyHumanGatesAreRealTransitions(t *testing.T) {
	t.Parallel()

	v := BuildVocabulary()
	known := map[string]bool{}
	for _, name := range v.Transitions {
		known[name] = true
	}
	for transition := range v.HumanGates {
		if !known[transition] {
			t.Errorf("vocabulary.HumanGates names %q, which is not in Transitions — a typo here would gate nothing", transition)
		}
	}
}
