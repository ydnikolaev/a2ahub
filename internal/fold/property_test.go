package fold

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// sampleSequence returns a canonically-ordered, all-legal event sequence
// exercising a representative slice of the question lifecycle: submit,
// acknowledge, accept, start, block, unblock, respond (x2), verify one
// response, dispute the other, cancel is deliberately NOT reached (would
// terminate the sequence) — this sequence is long enough to make chunk
// boundaries meaningful.
func sampleSequence(env Envelope) []Event {
	return []Event{
		{ULID: "01SEQ0000000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TSubmit, Actor: Actor{System: env.From}},
		{ULID: "01SEQ0000000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}},
		{ULID: "01SEQ0000000000000000003", CommitSeq: 3, Subject: env.ID, Transition: TAccept, Actor: Actor{System: env.To0()}},
		{ULID: "01SEQ0000000000000000004", CommitSeq: 4, Subject: env.ID, Transition: TStart, Actor: Actor{System: env.To0()}},
		{ULID: "01SEQ0000000000000000005", CommitSeq: 5, Subject: env.ID, Transition: TBlock, Actor: Actor{System: env.To0()}},
		{ULID: "01SEQ0000000000000000006", CommitSeq: 6, Subject: env.ID, Transition: TUnblock, Actor: Actor{System: env.To0()}},
		{ULID: "01SEQ0000000000000000007", CommitSeq: 7, Subject: env.ID, Transition: TRespond, ResponseID: "XS-fixture-1", Actor: Actor{System: env.To0()}},
		{ULID: "01SEQ0000000000000000008", CommitSeq: 8, Subject: env.ID, Transition: TRespond, ResponseID: "XS-fixture-2", Actor: Actor{System: env.To0()}},
		{ULID: "01SEQ0000000000000000009", CommitSeq: 9, Subject: "XS-fixture-1", Transition: TVerify, Actor: Actor{System: env.From}},
		{ULID: "01SEQ0000000000000000010", CommitSeq: 10, Subject: "XS-fixture-2", Transition: TDispute, Actor: Actor{System: env.From}},
	}
}

// TestFoldOrderIndependence is spec 04 §8 AC 4: the same canonically
// ordered event set folds to the identical result regardless of arrival
// grouping — all-at-once (Fold), fully incremental (Apply per event), or
// chunked (random partition boundaries) — since incremental fold's
// contract requires events "in canonical order" (§T1), the property here
// is chunking/grouping invariance, not permutation of settled commit
// order (that is covered separately by CC-022).
func TestFoldOrderIndependence(t *testing.T) {
	t.Parallel()
	env := rowEnv(KindQuestion)
	env.Kind = KindQuestion
	events := sampleSequence(env)

	allAtOnce := Fold(KindQuestion, env, events, alwaysMember)

	fullyIncremental := NewResult(KindQuestion)
	for _, e := range events {
		fullyIncremental = Apply(KindQuestion, env, fullyIncremental, e, alwaysMember)
	}

	seed := int64(20260721)
	t.Logf("chunk-boundary seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))

	for trial := 0; trial < 25; trial++ {
		chunks := randomChunks(rng, len(events))
		got := NewResult(KindQuestion)
		idx := 0
		for _, size := range chunks {
			batch := events[idx : idx+size]
			idx += size
			// Each chunk is either Fold'd as its own contiguous batch
			// (re-sorted internally, a no-op here since it's already
			// canonical) or applied event-by-event — both continue the
			// running `got` carrier identically, which is exactly what
			// "any legal arrival grouping" (all-at-once vs incremental
			// vs chunked) must guarantee.
			for _, e := range batch {
				got = Apply(KindQuestion, env, got, e, alwaysMember)
			}
		}
		if !reflect.DeepEqual(allAtOnce, got) {
			t.Fatalf("trial %d: chunked fold diverged from full fold.\n all-at-once: %+v\n chunked:     %+v", trial, allAtOnce, got)
		}
	}

	if !reflect.DeepEqual(allAtOnce, fullyIncremental) {
		t.Fatalf("fully incremental fold diverged from full fold.\n all-at-once: %+v\n incremental: %+v", allAtOnce, fullyIncremental)
	}
}

func randomChunks(rng *rand.Rand, n int) []int {
	var chunks []int
	remaining := n
	for remaining > 0 {
		size := 1 + rng.Intn(remaining)
		chunks = append(chunks, size)
		remaining -= size
	}
	return chunks
}

// TestFoldIdempotentReplay is spec 04 §8 AC 5: replaying a duplicate
// event (same ULID) is a no-op, and a full re-fold from scratch agrees
// with an incremental continuation.
func TestFoldIdempotentReplay(t *testing.T) {
	t.Parallel()
	env := rowEnv(KindQuestion)
	events := sampleSequence(env)

	once := Fold(KindQuestion, env, events, alwaysMember)

	t.Run("duplicate_event_replay_is_a_no_op", func(t *testing.T) {
		t.Parallel()
		// Replay the last event (a `dispute`) a second time.
		last := events[len(events)-1]
		twice := Apply(KindQuestion, env, once, last, alwaysMember)
		if !reflect.DeepEqual(once, twice) {
			t.Fatalf("replaying a duplicate ULID changed the result.\n before: %+v\n after:  %+v", once, twice)
		}
	})

	t.Run("duplicate_close_event_is_a_no_op", func(t *testing.T) {
		t.Parallel()
		closeEvent := Event{ULID: "01SEQCLOSE0000000000001", CommitSeq: 11, Subject: env.ID, Transition: TClose, Actor: Actor{System: env.From}}
		// dispute reopened the parent to in_progress, so close is
		// illegal here — deliberately: replay must still be a no-op
		// even for a flagged (non-fatal) event.
		afterClose := Apply(KindQuestion, env, once, closeEvent, alwaysMember)
		afterCloseTwice := Apply(KindQuestion, env, afterClose, closeEvent, alwaysMember)
		if !reflect.DeepEqual(afterClose, afterCloseTwice) {
			t.Fatalf("replaying a duplicate close event changed the result.\n before: %+v\n after:  %+v", afterClose, afterCloseTwice)
		}
	})

	t.Run("full_refold_matches_incremental_continuation", func(t *testing.T) {
		t.Parallel()
		prefix := events[:len(events)-1]
		last := events[len(events)-1]

		fromScratch := Fold(KindQuestion, env, events, alwaysMember)
		continuation := Apply(KindQuestion, env, Fold(KindQuestion, env, prefix, alwaysMember), last, alwaysMember)

		if !reflect.DeepEqual(fromScratch, continuation) {
			t.Fatalf("full re-fold diverged from incremental continuation.\n from scratch: %+v\n continuation: %+v", fromScratch, continuation)
		}
	})

	t.Run("full_replay_of_the_same_set_twice_is_stable", func(t *testing.T) {
		t.Parallel()
		again := Fold(KindQuestion, env, events, alwaysMember)
		if !reflect.DeepEqual(once, again) {
			t.Fatalf("Fold is not deterministic across repeated calls with the same input")
		}
	})
}

// frozenContractSubjectFold is a deliberately hand-written
// re-implementation of TODAY's six-row contract subject-state machinery
// (table.go's contractRows(), as it stands going into this phase) — the
// "old" side of spec 04 §5.1's cutover-audit property test. It must NEVER
// be refactored to share code with contract.go/table.go: its entire
// evidentiary value is that it does NOT track this phase's new
// per-version rules, so any agreement found between it and the real
// engine below is a genuine cutover guarantee, not a tautology from
// shared implementation.
//
// State machine (contractRows(), reproduced verbatim, version-blind):
//
//	none       --create-->    draft        (not reachable via this
//	                                         helper — it starts at draft,
//	                                         matching NewResult(KindContract))
//	draft      --publish-->   published
//	published  --publish-->   published
//	published  --deprecate--> deprecated
//	deprecated --publish-->   published    (the interim row, table.go)
//	deprecated --retire-->    retired
//
// Anything else is an illegal transition under this frozen model: no
// state change (the real engine would additionally record a flag; this
// reference only needs the resulting STATE for the comparison).
func frozenContractSubjectFold(transitions []string) State {
	state := StateDraft
	for _, tr := range transitions {
		switch {
		case state == StateDraft && tr == TPublish:
			state = StatePublished
		case state == StatePublished && tr == TPublish:
			state = StatePublished
		case state == StatePublished && tr == TDeprecate:
			state = StateDeprecated
		case state == StateDeprecated && tr == TPublish:
			state = StatePublished
		case state == StateDeprecated && tr == TRetire:
			state = StateRetired
		}
	}
	return state
}

// allTransitionSequences returns every non-empty sequence over alphabet
// of length 1..maxLen (a brute-force enumeration; alphabet has 3 members
// and maxLen is 4 in the caller below, so this is 3+9+27+81 = 120
// sequences — small enough that clarity beats cleverness here).
func allTransitionSequences(alphabet []string, maxLen int) [][]string {
	var out [][]string
	var build func(prefix []string)
	build = func(prefix []string) {
		if len(prefix) > 0 {
			out = append(out, append([]string(nil), prefix...))
		}
		if len(prefix) == maxLen {
			return
		}
		for _, t := range alphabet {
			build(append(append([]string(nil), prefix...), t))
		}
	}
	build(nil)
	return out
}

// foldContractSequence folds transitions (each carrying the same version
// string) against a fresh contract via the real engine (Fold, canonical
// commit order) and returns the resulting subject-level State.
func foldContractSequence(transitions []string, version string) State {
	env := Envelope{ID: "XC-migration-fixture", Kind: KindContract, From: "acme"}
	events := make([]Event, 0, len(transitions))
	for i, tr := range transitions {
		events = append(events, Event{
			ULID:       fmt.Sprintf("01MIGRATION%014d", i+1),
			CommitSeq:  int64(i + 1),
			Subject:    env.ID,
			Transition: tr,
			Version:    version,
			Actor:      Actor{System: env.From},
		})
	}
	return Fold(KindContract, env, events, alwaysMember).State
}

// cutoverSequenceDepth is how deep the enumeration below goes. It is 7,
// and the number is load-bearing rather than arbitrary.
//
// It was 4, and wave 4's audit is why it is not. A THIRD divergence class
// (`deprecated -> retired`) first appears at length 5 and was therefore
// invisible: the table below claimed completeness, the claim was true for
// the depth tested, and the AC-11 release-notes list it exists to generate
// would have been silently short by one entry.
//
// 7 is past a PROVABLE ceiling, not merely past an observed plateau —
// which matters, because "flat for three depths" is the same kind of
// argument that made 4 look sufficient. Both engines are deterministic
// functions of (joint state, next transition), and an illegal transition
// is a self-loop in each, so the pair forms a finite automaton over this
// 3-symbol alphabet. Its reachable part is SEVEN joint states with
// diameter FIVE: every reachable joint state is entered within 5
// transitions, so depth 6 already takes every outgoing edge from every
// reachable state at least once, and no longer sequence can produce an
// edge the enumeration has not already walked. 7 keeps a step of margin.
// (Established by the wave-4 re-audit, which also confirmed it
// empirically to depth 12 — ~531k sequences — and checked that adding
// `create` to the alphabet produces no new class, since
// isContractVersionTransition excludes it and neither engine has a row
// for it from `draft`.)
//
// 3279 sequences per lane at this depth. The loop below is deliberately
// NOT one subtest per sequence: 6558 parallel subtests cost more in test
// harness overhead than the folds themselves, and the failure message
// names its own sequence anyway.
const cutoverSequenceDepth = 7

// expectedCutoverDivergences is the COMPLETE set of ways the per-version
// engine's answer differs from the frozen pre-P4 engine's, over every
// sequence of up to cutoverSequenceDepth contract transitions.
// Keyed "<frozen> -> <live>".
//
// This is the migration's own audit (spec §5.1, epic AC-11: "every changed
// subject state is named in the release notes") expressed as data a test
// can enforce, rather than prose somebody has to remember to write. The
// test below fails BOTH ways: an unlisted divergence fails, and a listed
// divergence that stops occurring fails too, so this table cannot rot into
// a stale claim.
//
// Both entries have ONE cause: the interim `(deprecated, publish)` row is
// gone (P4 wave 4), and `contractVersionVerdict` refuses a republish of a
// version key that already exists. The frozen engine let a publish from
// `deprecated` resurrect the contract; nothing does that now.
var expectedCutoverDivergences = map[string]string{
	"published -> deprecated": "A publish from `deprecated` no longer resurrects the contract. Under the " +
		"frozen engine the interim row silently re-published it — which is exactly the version-blind " +
		"shortcut this phase removes.",
	"published -> retired": "The sequence publish, deprecate, publish, retire. The frozen engine's bogus " +
		"republish moved the subject back to `published`, where `retire` has no row and was refused; with " +
		"the republish correctly refused, the version is still `deprecated` and the retire LANDS. The " +
		"§5.4 cycle completing is the whole point of the phase, and here it is as a state change.",
	"deprecated -> retired": "The same cause as the entry above, observed one step later — e.g. publish, " +
		"deprecate, publish, retire, deprecate. The frozen engine ends `deprecated` because its resurrected " +
		"version was still there to deprecate again; the live engine already retired the only version, so " +
		"the trailing deprecate has nothing published to act on and is correctly refused. Invisible until " +
		"the enumeration went past length 4 — see cutoverSequenceDepth.",
}

// TestMigrationCutoverProperty is spec 04 §8 AC-P4.1 and §5.1's cutover
// gate. It compares every sequence of up to four contract transitions
// against frozenContractSubjectFold, in two lanes — version-less events
// and single-version events — and requires one of two things per sequence:
// the two engines agree, or they disagree in a way named in
// expectedCutoverDivergences above.
//
// It used to SKIP the sequences that diverge (the single-version lane
// excluded anything with more than one publish). That hid the interesting
// half. A cutover audit whose job is to enumerate what changed cannot get
// there by declining to look at the changes, so the skip is gone and the
// divergences are declared instead — which also means the release notes'
// AC-11 list is derived from a running test rather than from memory.
//
// Both lanes produce the SAME two divergence classes, which is itself
// worth knowing: one behavioural change reached through two different code
// paths (the deleted table row for version-less events, the
// already-exists refusal for version-carrying ones).
func TestMigrationCutoverProperty(t *testing.T) {
	t.Parallel()
	sequences := allTransitionSequences([]string{TPublish, TDeprecate, TRetire}, cutoverSequenceDepth)

	lanes := []struct {
		name    string
		version string
	}{
		{name: "version_less_history", version: ""},
		{name: "single_version_history", version: "1.0.0"},
	}

	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			t.Parallel()

			seen := map[string]string{} // divergence key -> the first sequence producing it

			for _, seq := range sequences {
				want := frozenContractSubjectFold(seq)
				got := foldContractSequence(seq, lane.version)
				if got == want {
					continue
				}
				key := string(want) + " -> " + string(got)
				if _, declared := expectedCutoverDivergences[key]; !declared {
					t.Fatalf("sequence %v folds %q under the frozen pre-P4 engine and %q under this one, "+
						"and that divergence is not declared in expectedCutoverDivergences. Either it is a "+
						"regression, or it is an intended change nobody wrote down — and an undeclared "+
						"state change is exactly what AC-11 forbids shipping.", seq, want, got)
				}
				if _, ok := seen[key]; !ok {
					seen[key] = strings.Join(seq, ",")
				}
			}

			for key, reason := range expectedCutoverDivergences {
				first, ok := seen[key]
				if !ok {
					t.Errorf("declared divergence %q never occurred in lane %s — the table is claiming a "+
						"change the engine no longer makes, which is how a cutover audit rots into fiction",
						key, lane.name)
					continue
				}
				t.Logf("%s: %s (first at %s) — %s", lane.name, key, first, reason)
			}
		})
	}
}
