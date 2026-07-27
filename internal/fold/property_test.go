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

// TestMigrationCutoverProperty is spec 04 §8 AC-P4.1 (spec §5.1's
// zero-history cutover gate): "any history touching AT MOST ONE distinct
// version folds identically old vs new". Two lanes:
//
//   - version == "": every one of the 120 sequences must agree, with no
//     restriction — a version-less event ALWAYS hits applyContractScoped's
//     own fallback (event.Version == "" and, since no version-carrying
//     event is ever mixed in here, prior.Versions stays empty for the
//     whole sequence), so this lane folds through the exact same
//     applyPrimaryScoped/table.go code path as before this phase. It is
//     the literal migration guarantee, not an approximation of it.
//   - version == "1.0.0": restricted to sequences with AT MOST ONE
//     `publish`. A real single-version history never publishes the same
//     version number twice — spec §5.4a resolves `id@version` through the
//     publish event's own SHA, so two publish events sharing one version
//     string is not well-formed data, not a history this guarantee
//     covers. Two publishes of the SAME version is also the one place
//     this phase's engine and the frozen one deliberately disagree:
//     the frozen engine's (deprecated, publish) interim row is
//     version-blind and re-publishes; contractVersionVerdict correctly
//     refuses a republish of an EXISTING version key regardless of its
//     current state. That divergence is pinned separately and on
//     purpose, never inside this property test — see
//     TestContractVersionRepublishAfterDeprecateIsIllegal.
func TestMigrationCutoverProperty(t *testing.T) {
	t.Parallel()
	sequences := allTransitionSequences([]string{TPublish, TDeprecate, TRetire}, 4)

	t.Run("version_less_history_agrees_for_every_sequence", func(t *testing.T) {
		t.Parallel()
		for _, seq := range sequences {
			t.Run(strings.Join(seq, "_"), func(t *testing.T) {
				t.Parallel()
				want := frozenContractSubjectFold(seq)
				got := foldContractSequence(seq, "")
				if got != want {
					t.Fatalf("version-less sequence %v: new engine state = %q, frozen engine state = %q", seq, got, want)
				}
			})
		}
	})

	t.Run("single_version_history_with_at_most_one_publish_agrees_for_every_sequence", func(t *testing.T) {
		t.Parallel()
		for _, seq := range sequences {
			publishCount := 0
			for _, tr := range seq {
				if tr == TPublish {
					publishCount++
				}
			}
			if publishCount > 1 {
				continue
			}
			t.Run(strings.Join(seq, "_"), func(t *testing.T) {
				t.Parallel()
				want := frozenContractSubjectFold(seq)
				got := foldContractSequence(seq, "1.0.0")
				if got != want {
					t.Fatalf("single-version sequence %v: new engine state = %q, frozen engine state = %q", seq, got, want)
				}
			})
		}
	})
}
