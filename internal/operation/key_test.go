package operation

import (
	"errors"
	"strings"
	"testing"
)

func TestWorkGoldenVectors(t *testing.T) {
	t.Parallel()

	// Wire preimage: each value is framed as uint64-big-endian byte length
	// followed by its bytes. The frames are, in order:
	// "a2ahub-operation-v1", "work", work ID, uint64-big-endian semantic
	// sequence, action. These vectors lock both the domain and framing.
	tests := []struct {
		name     string
		workID   string
		sequence int64
		action   string
		want     string
	}{
		{"start", "work:01ARZ3NDEKTSV4RRFFQ69G5FAV", 1, WorkActionStart, "op-v1-438c5a0a406bc7a789362f82f53ff5829f0f5a7a6d7753637185937ba3ac047c"},
		{"checkpoint", "work:01ARZ3NDEKTSV4RRFFQ69G5FAV", 2, WorkActionCheckpoint, "op-v1-dee8c2ca3b52d16bc1c2ae844369907d6959b524c8776f78c7db91d4432f825a"},
		{"wait", "work:01K1A2B3C4D5E6F7G8H9J0K1M2", 19, WorkActionWait, "op-v1-c444906d06a107b4758f5e13c283b3c6a5c3e6cfff7cc7e435d24eeb0adf89ea"},
		{"stop", "work:01K1A2B3C4D5E6F7G8H9J0K1M2", 20, WorkActionStop, "op-v1-fd681bd9adaf5a51d8c5b66ab4c1e8fde9ebd77e74a6c839808483a03481c228"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := Work(test.workID, test.sequence, test.action)
			if err != nil {
				t.Fatalf("Work() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Work() = %q, want %q", got, test.want)
			}
			if !Valid(got) || strings.Contains(got, "workop") {
				t.Fatalf("Work() returned non-canonical/private key %q", got)
			}
		})
	}
}

func TestWorkDomainAndTupleSeparation(t *testing.T) {
	t.Parallel()

	const workID = "work:01ARZ3NDEKTSV4RRFFQ69G5FAV"
	base, err := Work(workID, 1, WorkActionStart)
	if err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	tests := []struct {
		name     string
		workID   string
		sequence int64
		action   string
	}{
		{"work id", "work:01ARZ3NDEKTSV4RRFFQ69G5FAW", 1, WorkActionStart},
		{"sequence", workID, 2, WorkActionStart},
		{"action", workID, 1, WorkActionStop},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			changed, changedErr := Work(test.workID, test.sequence, test.action)
			if changedErr != nil {
				t.Fatalf("Work() error = %v", changedErr)
			}
			if changed == base {
				t.Fatalf("changed %s collided with base key %q", test.name, base)
			}
		})
	}

	otherDomain := newEncoder("work-other-domain")
	otherDomain.add(workID)
	var sequence [8]byte
	sequence[7] = 1
	otherDomain.addBytes(sequence[:])
	otherDomain.add(WorkActionStart)
	if got := otherDomain.key(); got == base {
		t.Fatalf("different domain collided with work key %q", base)
	}
}

func TestEncoderLengthFramingSeparatesConcatenationAliases(t *testing.T) {
	t.Parallel()

	left := newEncoder("work-framing-proof")
	left.add("ab")
	left.add("c")
	right := newEncoder("work-framing-proof")
	right.add("a")
	right.add("bc")
	if left.key() == right.key() {
		t.Fatal("length-framed tuples collapsed to the same operation key")
	}
}

func TestWorkRejectsInvalidTuple(t *testing.T) {
	t.Parallel()

	const validID = "work:01ARZ3NDEKTSV4RRFFQ69G5FAV"
	tests := []struct {
		name     string
		workID   string
		sequence int64
		action   string
	}{
		{"empty work id", "", 1, WorkActionStart},
		{"wrong prefix", "workop:01ARZ3NDEKTSV4RRFFQ69G5FAV", 1, WorkActionStart},
		{"lowercase ulid", "work:01arz3ndektsv4rrffq69g5fav", 1, WorkActionStart},
		{"ambiguous ulid character", "work:01ARZ3NDEKTSV4RRFFQ69G5FAI", 1, WorkActionStart},
		{"short ulid", "work:01ARZ3NDEKTSV4RRFFQ69G5FA", 1, WorkActionStart},
		{"zero sequence", validID, 0, WorkActionStart},
		{"negative sequence", validID, -1, WorkActionStart},
		{"empty action", validID, 1, ""},
		{"heartbeat is local only", validID, 1, "heartbeat"},
		{"unknown action", validID, 1, "resume"},
		{"case changed action", validID, 1, "Start"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := Work(test.workID, test.sequence, test.action)
			if !errors.Is(err, ErrInvalidWorkOperation) {
				t.Fatalf("Work() error = %v, want ErrInvalidWorkOperation", err)
			}
			if got != "" {
				t.Fatalf("Work() key = %q on invalid input", got)
			}
		})
	}
}

func TestRespondCanonicalAndSemantic(t *testing.T) {
	t.Parallel()

	a := Respond("axon", "agent", "bot", []string{"B", "A"}, "answered",
		map[string]string{"title": "done", "priority": "p1"}, nil, []byte("body"), RespondIncompleteness{}, nil)
	b := Respond("axon", "agent", "bot", []string{"A", "B"}, "answered",
		map[string]string{"priority": "p1", "title": "done"}, nil, []byte("body"), RespondIncompleteness{}, nil)
	if a != b || !Valid(a) {
		t.Fatalf("canonical keys differ or are invalid: %q %q", a, b)
	}
	if changed := Respond("axon", "agent", "bot", []string{"A", "B"}, "answered",
		map[string]string{"priority": "p1", "title": "done"}, nil, []byte("different"), RespondIncompleteness{}, nil); changed == a {
		t.Fatal("changed body produced the same operation key")
	}
}

// TestRespondRefsAreOrderedAndDistinguishing pins the ONE asymmetry in
// Respond's encoding: parents and field keys are sorted because their order is
// not on the wire, but `refs[]` is an ordered envelope array, so two responses
// carrying the same refs in a different order are two different artifacts and
// must not dedup onto one operation key.
func TestRespondRefsAreOrderedAndDistinguishing(t *testing.T) {
	t.Parallel()

	base := Respond("axon", "agent", "bot", []string{"XW-peer-p"}, "delivered", nil, nil, []byte("b"), RespondIncompleteness{}, nil)

	withRefs := Respond("axon", "agent", "bot", []string{"XW-peer-p"}, "delivered",
		nil, []string{"XH-axon-1", "XH-axon-2"}, []byte("b"), RespondIncompleteness{}, nil)
	if withRefs == base {
		t.Fatal("adding refs did not change the operation key — a response naming a handoff would dedup onto one that names nothing, and its refs[] would never be committed")
	}

	swapped := Respond("axon", "agent", "bot", []string{"XW-peer-p"}, "delivered",
		nil, []string{"XH-axon-2", "XH-axon-1"}, []byte("b"), RespondIncompleteness{}, nil)
	if swapped == withRefs {
		t.Fatal("reordering refs produced the same key — refs[] order IS on the wire, unlike parents and field keys")
	}

	repeat := Respond("axon", "agent", "bot", []string{"XW-peer-p"}, "delivered",
		nil, []string{"XH-axon-1", "XH-axon-2"}, []byte("b"), RespondIncompleteness{}, nil)
	if repeat != withRefs {
		t.Fatal("an identical retry minted a different key — dedup is what makes a retried submit idempotent")
	}

	// A ref set must not be confusable with a field carrying the same text:
	// the encoder writes both, and a length-blind concatenation would let
	// them collide.
	viaField := Respond("axon", "agent", "bot", []string{"XW-peer-p"}, "delivered",
		map[string]string{"XH-axon-1": "XH-axon-2"}, nil, []byte("b"), RespondIncompleteness{}, nil)
	if viaField == withRefs {
		t.Fatal("a field pair and a ref pair with the same strings produced one key — the encoding is ambiguous")
	}
}

// TestVerifyDifferentVerdictsProduceDifferentKeys is the determinism
// requirement P6 wave C's implementor brief names explicitly: two `a2a
// verify` invocations naming the SAME targets with DIFFERENT per-criterion
// judgements must NOT collide onto one operation key, or the second write is
// silently treated as a retry of the first and its distinct verdicts never
// commit.
func TestVerifyDifferentVerdictsProduceDifferentKeys(t *testing.T) {
	t.Parallel()

	a := Verify("axon", "agent", "bot", []string{"XS-axon-1"},
		[]VerdictEntry{{Index: 0, Verdict: "met", CauseOwner: "axon"}})
	b := Verify("axon", "agent", "bot", []string{"XS-axon-1"},
		[]VerdictEntry{{Index: 0, Verdict: "unmet", CauseOwner: "axon"}})
	if a == b {
		t.Fatal("differing verdict on the same index produced the same key")
	}
	if !Valid(a) || !Valid(b) {
		t.Fatalf("keys are not canonical: %q %q", a, b)
	}

	c := Verify("axon", "agent", "bot", []string{"XS-axon-1"},
		[]VerdictEntry{{Index: 0, Verdict: "met", CauseOwner: "beta"}})
	if a == c {
		t.Fatal("differing cause_owner on the same index/verdict produced the same key")
	}

	noVerdicts := Verify("axon", "agent", "bot", []string{"XS-axon-1"}, nil)
	if noVerdicts == a {
		t.Fatal("an empty verdict set collided with a populated one")
	}
}

// TestVerifyVerdictsCanonicalByIndexNotArgumentOrder pins the
// ordered-vs-sorted decision Verify's own doc comment makes: verdicts carry
// their own position via `index`, so two invocations naming the SAME
// judgement set in a DIFFERENT --verdict flag order must mint the IDENTICAL
// key (an identical retry, or a caller assembling flags from an unordered
// map, must not mint a second branch for one judgement set) — the opposite
// of Respond's own refs[], which stays order-sensitive because array order
// is itself part of that document.
func TestVerifyVerdictsCanonicalByIndexNotArgumentOrder(t *testing.T) {
	t.Parallel()

	given := Verify("axon", "agent", "bot", []string{"XS-axon-1"}, []VerdictEntry{
		{Index: 0, Verdict: "met", CauseOwner: "axon"},
		{Index: 1, Verdict: "unmet", CauseOwner: "beta"},
	})
	reordered := Verify("axon", "agent", "bot", []string{"XS-axon-1"}, []VerdictEntry{
		{Index: 1, Verdict: "unmet", CauseOwner: "beta"},
		{Index: 0, Verdict: "met", CauseOwner: "axon"},
	})
	if given != reordered {
		t.Fatalf("reordering --verdict flags (same judgement set) changed the key: %q vs %q", given, reordered)
	}
}

// TestVerifyTargetsAreSetSemantics pins that Verify's targets, like Respond's
// parents, carry no wire order — batching the same response ids in a
// different CLI argument order must mint the same key.
func TestVerifyTargetsAreSetSemantics(t *testing.T) {
	t.Parallel()

	a := Verify("axon", "agent", "bot", []string{"XS-axon-1", "XS-axon-2"}, nil)
	b := Verify("axon", "agent", "bot", []string{"XS-axon-2", "XS-axon-1"}, nil)
	if a != b {
		t.Fatalf("reordering targets changed the key: %q vs %q", a, b)
	}

	c := Verify("axon", "agent", "bot", []string{"XS-axon-1", "XS-axon-3"}, nil)
	if a == c {
		t.Fatal("changing one target produced the same key")
	}
}

func TestContractDeprecateIncludesSuccessor(t *testing.T) {
	t.Parallel()

	a := ContractDeprecate("axon", "XC-axon-widget", "1.0.0", "XC-axon-widget@2.0.0", "2026-12-31")
	b := ContractDeprecate("axon", "XC-axon-widget", "1.0.0", "XC-axon-widget@3.0.0", "2026-12-31")
	if a == b {
		t.Fatal("changed successor produced the same operation key")
	}
	if !Valid(a) || Valid("op-v1-nope") {
		t.Fatalf("Valid accepted/rejected the wrong key: %q", a)
	}
}

// TestContractActivateSatisfiesIsSetSemantics pins the ordering decision
// ContractActivate's own doc comment makes: satisfies[] carries no wire
// order of its own (unlike Respond's refs[]), so reordering --satisfies
// flags for the SAME item set must mint the IDENTICAL key.
func TestContractActivateSatisfiesIsSetSemantics(t *testing.T) {
	t.Parallel()

	given := ContractActivate("axon", "XC-axon-widget", "1.0.0", []string{"endpoint", "registration"}, "")
	reordered := ContractActivate("axon", "XC-axon-widget", "1.0.0", []string{"registration", "endpoint"}, "")
	if given != reordered {
		t.Fatalf("reordering --satisfies (same item set) changed the key: %q vs %q", given, reordered)
	}
	if !Valid(given) {
		t.Fatalf("key is not canonical: %q", given)
	}
}

// TestContractActivateDistinguishesEveryInput is the negative half: changing
// ANY one input — system, contract, version, the satisfies SET, or note —
// must mint a distinct key, or two different activations silently collapse
// onto one write branch.
func TestContractActivateDistinguishesEveryInput(t *testing.T) {
	t.Parallel()

	base := ContractActivate("axon", "XC-axon-widget", "1.0.0", []string{"endpoint"}, "note")
	variants := map[string]string{
		"system":               ContractActivate("beta", "XC-axon-widget", "1.0.0", []string{"endpoint"}, "note"),
		"contract":             ContractActivate("axon", "XC-axon-other", "1.0.0", []string{"endpoint"}, "note"),
		"version":              ContractActivate("axon", "XC-axon-widget", "2.0.0", []string{"endpoint"}, "note"),
		"satisfies":            ContractActivate("axon", "XC-axon-widget", "1.0.0", []string{"registration"}, "note"),
		"note":                 ContractActivate("axon", "XC-axon-widget", "1.0.0", []string{"endpoint"}, "different"),
		"added satisfies item": ContractActivate("axon", "XC-axon-widget", "1.0.0", []string{"endpoint", "registration"}, "note"),
	}
	for name, variant := range variants {
		if variant == base {
			t.Errorf("changing %s produced the same key as the base case", name)
		}
		if !Valid(variant) {
			t.Errorf("%s: key is not canonical: %q", name, variant)
		}
	}
}

// TestVerifyDistinctIndicesDoNotCollide asserts what the index encoding
// actually has to guarantee: two judgements differing only in which criterion
// they name mint different keys, so the second is not silently deduped onto
// the first. Negative values are included not because they are legal — a
// criterion index is non-negative by convention — but because nothing in
// Verify's signature refuses one, and a key function must stay injective over
// the inputs it can actually receive.
//
// It deliberately does NOT assert that a negative index cannot alias a
// positive one through uint64. That claim was written first, and mutation
// refuted it: uint64(-1) is 2^64-1, which no int can hold, so the conversion
// gosec flagged was injective all along. See Verify's own comment.
func TestVerifyDistinctIndicesDoNotCollide(t *testing.T) {
	t.Parallel()

	key := func(index int) string {
		return Verify("axon", "agent", "bot", []string{"XS-axon-1"},
			[]VerdictEntry{{Index: index, Verdict: "met", CauseOwner: "axon"}})
	}
	seen := map[string]int{}
	for _, index := range []int{-1, 0, 1, 2, 11, 1 << 20, 1<<63 - 1} {
		k := key(index)
		if prior, dup := seen[k]; dup {
			t.Fatalf("criterion indices %d and %d minted one operation key", prior, index)
		}
		seen[k] = index
	}
}

// --- Close (B24: the standalone `a2a close` verb's own operation key) ------
//
// Close mirrors Verify's canonicalisation (targets sorted, verdicts
// canonical-by-index) but is its OWN function rather than a call-through to
// Verify — see Close's own doc comment for why reusing Verify's key here
// would be wrong even though the funnel's BranchName already folds `verb`
// into the branch path (verify and close never actually collide there).

// TestCloseDifferentVerdictsProduceDifferentKeys mirrors
// TestVerifyDifferentVerdictsProduceDifferentKeys for Close's own domain.
func TestCloseDifferentVerdictsProduceDifferentKeys(t *testing.T) {
	t.Parallel()

	a := Close("axon", "agent", "bot", []string{"XQ-axon-1"},
		[]VerdictEntry{{Index: 0, Verdict: "met", CauseOwner: "axon"}})
	b := Close("axon", "agent", "bot", []string{"XQ-axon-1"},
		[]VerdictEntry{{Index: 0, Verdict: "unmet", CauseOwner: "axon"}})
	if a == b {
		t.Fatal("differing verdict on the same index produced the same key")
	}
	if !Valid(a) || !Valid(b) {
		t.Fatalf("keys are not canonical: %q %q", a, b)
	}

	c := Close("axon", "agent", "bot", []string{"XQ-axon-1"},
		[]VerdictEntry{{Index: 0, Verdict: "met", CauseOwner: "beta"}})
	if a == c {
		t.Fatal("differing cause_owner on the same index/verdict produced the same key")
	}

	noVerdicts := Close("axon", "agent", "bot", []string{"XQ-axon-1"}, nil)
	if noVerdicts == a {
		t.Fatal("an empty verdict set collided with a populated one")
	}
}

// TestCloseVerdictsCanonicalByIndexNotArgumentOrder mirrors
// TestVerifyVerdictsCanonicalByIndexNotArgumentOrder for Close's own domain.
func TestCloseVerdictsCanonicalByIndexNotArgumentOrder(t *testing.T) {
	t.Parallel()

	given := Close("axon", "agent", "bot", []string{"XQ-axon-1"}, []VerdictEntry{
		{Index: 0, Verdict: "met", CauseOwner: "axon"},
		{Index: 1, Verdict: "unmet", CauseOwner: "beta"},
	})
	reordered := Close("axon", "agent", "bot", []string{"XQ-axon-1"}, []VerdictEntry{
		{Index: 1, Verdict: "unmet", CauseOwner: "beta"},
		{Index: 0, Verdict: "met", CauseOwner: "axon"},
	})
	if given != reordered {
		t.Fatalf("reordering --verdict flags (same judgement set) changed the key: %q vs %q", given, reordered)
	}
}

// TestCloseTargetsAreSetSemantics mirrors TestVerifyTargetsAreSetSemantics
// for Close's own domain — a batch close's ids carry no wire order.
func TestCloseTargetsAreSetSemantics(t *testing.T) {
	t.Parallel()

	a := Close("axon", "agent", "bot", []string{"XQ-axon-1", "XQ-axon-2"}, nil)
	b := Close("axon", "agent", "bot", []string{"XQ-axon-2", "XQ-axon-1"}, nil)
	if a != b {
		t.Fatalf("reordering targets changed the key: %q vs %q", a, b)
	}

	c := Close("axon", "agent", "bot", []string{"XQ-axon-1", "XQ-axon-3"}, nil)
	if a == c {
		t.Fatal("changing one target produced the same key")
	}
}

// TestCloseKeyDiffersFromVerifyKeyForIdenticalInput proves Close occupies
// its OWN key domain: given the byte-identical (system, actorKind,
// actorName, ids/targets, verdicts) tuple, Close and Verify must NOT mint
// the same key. Not a collision-safety claim about the funnel (BranchName
// already folds `verb` into the branch, so the two never actually collide
// on a real write) — a domain-separation claim about the key itself, which
// is what makes Close's own doc comment about "not a call-through to
// Verify" true rather than aspirational.
func TestCloseKeyDiffersFromVerifyKeyForIdenticalInput(t *testing.T) {
	t.Parallel()

	targets := []string{"XQ-axon-1"}
	verdicts := []VerdictEntry{{Index: 0, Verdict: "met", CauseOwner: "axon"}}

	closeKey := Close("axon", "agent", "bot", targets, verdicts)
	verifyKey := Verify("axon", "agent", "bot", targets, verdicts)
	if closeKey == verifyKey {
		t.Fatal("Close and Verify minted the same key for identical input — the two verbs no longer occupy distinct key domains")
	}
}

// TestRespondIncompletenessSeparatesOtherwiseIdenticalResponses is the
// defect P2 reported and did not work around: before the facts fed this key,
// two responses differing ONLY in `unmet[]` minted one key, so a corrected
// response deduped as already-done at the funnel's idempotency layer and its
// correction was never committed.
func TestRespondIncompletenessSeparatesOtherwiseIdenticalResponses(t *testing.T) {
	t.Parallel()

	base := func(inc RespondIncompleteness) string {
		return Respond("axon", "agent", "bot", []string{"XW-peer-20260819-aaaa"}, "partial",
			map[string]string{"title": "partial answer"}, nil, []byte("body"), inc, nil)
	}

	none := base(RespondIncompleteness{})
	unmetOne := base(RespondIncompleteness{Unmet: []int{1}})
	unmetTwo := base(RespondIncompleteness{Unmet: []int{2}})
	standing := base(RespondIncompleteness{Standing: "provisional"})
	blocked := base(RespondIncompleteness{BlockedByReason: "out-of-scope", BlockedByOwner: "seomatrix"})

	for name, pair := range map[string][2]string{
		"unmet vs none":      {unmetOne, none},
		"unmet 1 vs 2":       {unmetOne, unmetTwo},
		"standing vs none":   {standing, none},
		"blocked_by vs none": {blocked, none},
	} {
		if pair[0] == pair[1] {
			t.Errorf("%s: keys collide (%s) — a corrected response would dedup as already-done", name, pair[0])
		}
	}

	// Flag ORDER is not part of what the operation IS: `unmet[]` carries no
	// wire ordering the way `refs[]` does, so two calls differing only in the
	// order the indices were given are the SAME operation.
	if a, b := base(RespondIncompleteness{Unmet: []int{3, 1}}), base(RespondIncompleteness{Unmet: []int{1, 3}}); a != b {
		t.Errorf("unmet order changed the key: %s != %s — a retry with the flags in a different order must dedup", a, b)
	}
}

// TestRespondZeroIncompletenessIsByteIdenticalToTheHistoricalKey pins the
// guarantee `refs` earned before it: a caller declaring none of the three
// derives EXACTLY the key it derived before this parameter existed. The
// literal is the pre-change value, recomputed by hand from the same inputs.
func TestRespondZeroIncompletenessIsByteIdenticalToTheHistoricalKey(t *testing.T) {
	t.Parallel()

	withZero := Respond("axon", "agent", "bot", []string{"XQ-a"}, "answered", nil, nil, nil, RespondIncompleteness{}, nil)
	withEmptyFields := Respond("axon", "agent", "bot", []string{"XQ-a"}, "answered", map[string]string{}, []string{}, nil, RespondIncompleteness{}, nil)
	if withZero != withEmptyFields {
		t.Errorf("nil and empty collections disagree: %s != %s", withZero, withEmptyFields)
	}
	// The NUL-tagged section must be absent, not merely empty: an empty
	// canonical writes nothing, which is what keeps historical keys stable.
	if same := Respond("axon", "agent", "bot", []string{"XQ-a"}, "answered", nil, nil, nil, RespondIncompleteness{Standing: ""}, nil); same != withZero {
		t.Errorf("an all-empty RespondIncompleteness changed the key: %s != %s", same, withZero)
	}
}

// TestRespondDeliversSeparatesOtherwiseIdenticalResponses pins the third use
// of this file's section discipline, and it pins the same two properties the
// refs and incompleteness sections earned before it.
//
// The distinguishing half: `delivers[]` names the data packages a response
// announces, so two responses on ONE parent differing only in it are two
// different acts. Sharing a key means sharing a branch, and the second write
// then meets the funnel's already-open short-circuit and is read as a retry of
// the first — a second delivery on one work_request disappearing in silence.
//
// The stability half, which is the one worth being explicit about: an EMPTY
// delivers must write nothing at all, so every key derived before this
// parameter existed is byte-identical. No response authored before
// judge-the-thing-2026-08 P1 carries the field, so nothing already committed
// in any space re-derives a different branch id.
//
// Order is content, not noise: `delivers[]` is a sequence on the wire and the
// document's own seed carries it in GIVEN order, so two orderings are two
// documents and must be two keys.
func TestRespondDeliversSeparatesOtherwiseIdenticalResponses(t *testing.T) {
	t.Parallel()

	base := func(delivers []string) string {
		return Respond("axon", "agent", "bot", []string{"XW-peer-20260819-aaaa"}, "delivered",
			map[string]string{"title": "here it is"}, nil, []byte("body"), RespondIncompleteness{}, delivers)
	}

	none := base(nil)
	one := base([]string{"DP-axon-20260821-aaaa"})
	other := base([]string{"DP-axon-20260821-bbbb"})
	both := base([]string{"DP-axon-20260821-aaaa", "DP-axon-20260821-bbbb"})
	reversed := base([]string{"DP-axon-20260821-bbbb", "DP-axon-20260821-aaaa"})

	for name, pair := range map[string][2]string{
		"one package vs none":  {one, none},
		"one package vs other": {one, other},
		"two packages vs one":  {both, one},
		"order is content":     {both, reversed},
	} {
		if pair[0] == pair[1] {
			t.Errorf("%s: same operation key %s — the second response collapses onto the first's branch", name, pair[0])
		}
	}

	// Absent, not merely empty. An empty slice must take the same path nil
	// does, or a caller passing `[]string{}` silently renames its branch.
	if empty := base([]string{}); empty != none {
		t.Errorf("an empty delivers changed the key: %s != %s", empty, none)
	}
}
