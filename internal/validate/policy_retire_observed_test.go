package validate

// policy_retire_observed_test.go is §8 criterion 2's evidence
// (spec 03-observed-consumption.md, judge-the-thing-2026-08): observed
// consumption INFORMS the retire decision and GATES NOTHING.
//
// The criterion's own "how to verify" is "the retire refusal set is
// unchanged; a grep proves no new reject code". A grep proves the absence
// of a literal; this file proves the absence of the BEHAVIOUR, which is
// the thing that matters: CheckRetirePrecondition's verdict is asserted
// byte-identical with Observed empty and with Observed populated, over
// every branch of the check.

import (
	"reflect"
	"strings"
	"testing"
)

// TestObservedConsumptionNeverChangesTheRetireVerdict is §8 criterion 2.
//
// TEETH: add a single `if len(p.Observed) > 0 { ... }` branch to
// CheckRetirePrecondition — a refusal, an extra un-acked entry, anything —
// and this reds on whichever case the branch touches. Populating Observed
// must be provably inert.
func TestObservedConsumptionNeverChangesTheRetireVerdict(t *testing.T) {
	t.Parallel()

	observed := []ObservedConsumer{
		{System: "axon", Packages: 3},
		{System: "delta", Packages: 1},
	}

	cases := []struct {
		name string
		base RetirePrecondition
	}{
		{"clean: no registered consumer at all", RetirePrecondition{}},
		{"clean: every consumer acked", RetirePrecondition{
			Consumers: []RegisteredConsumer{{System: "beta", Acked: true}},
		}},
		{"blocked: an un-acked consumer, no override", RetirePrecondition{
			Consumers: []RegisteredConsumer{{System: "beta"}},
		}},
		{"blocked: override requested, preconditions unmet", RetirePrecondition{
			Consumers: []RegisteredConsumer{{System: "beta"}},
			Override:  true,
		}},
		{"overridden: every §5.4 precondition met", RetirePrecondition{
			Consumers:    []RegisteredConsumer{{System: "beta"}},
			Override:     true,
			SunsetPassed: true,
			HasReminder:  true,
			ActorIsHuman: true,
		}},
		{"left consumers are excluded", RetirePrecondition{
			Consumers: []RegisteredConsumer{{System: "beta", Left: true}},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			without := tc.base
			withObserved := tc.base
			withObserved.Observed = observed

			vWithout, oWithout := CheckRetirePrecondition(without)
			vWith, oWith := CheckRetirePrecondition(withObserved)

			if !reflect.DeepEqual(vWithout, vWith) {
				t.Fatalf("observed consumption changed the violation:\n without: %+v\n    with: %+v\n"+
					"§8 criterion 2 (US-3): observed consumption never blocks retire — no code path may refuse on it alone", vWithout, vWith)
			}
			if !reflect.DeepEqual(oWithout, oWith) {
				t.Fatalf("observed consumption changed the overridden set: without=%v with=%v", oWithout, oWith)
			}
		})
	}
}

// TestObservedConsumptionNoticeIsSilentWithoutObservation is §8 criterion
// 4's own half of this package: with nothing observed there is no line at
// all, so a plain retire's output is byte-identical to today's.
func TestObservedConsumptionNoticeIsSilentWithoutObservation(t *testing.T) {
	t.Parallel()

	for _, p := range []RetirePrecondition{
		{},
		{Consumers: []RegisteredConsumer{{System: "beta", Acked: true}}},
		{Observed: []ObservedConsumer{}},
	} {
		if got := ObservedConsumptionNotice(p); got != "" {
			t.Fatalf("ObservedConsumptionNotice(%+v) = %q, want \"\" — nothing observed means nothing said", p, got)
		}
	}
}

// TestObservedConsumptionNoticeNamesEverySystemAndItsPackageCount is §8
// criterion 1's output half (US-1): the producer sees WHICH systems and HOW
// MANY packages, plus the declared count the observation is being weighed
// against, and the line says out loud that it is not a block.
func TestObservedConsumptionNoticeNamesEverySystemAndItsPackageCount(t *testing.T) {
	t.Parallel()

	got := ObservedConsumptionNotice(RetirePrecondition{
		// Deliberately unsorted, and with a `left` consumer that must not
		// be counted as declared (§5.4 bullet (a)).
		Consumers: []RegisteredConsumer{{System: "gamma", Left: true}},
		Observed:  []ObservedConsumer{{System: "delta", Packages: 1}, {System: "axon", Packages: 3}},
	})
	if got == "" {
		t.Fatal("ObservedConsumptionNotice returned \"\" with two observed consumers")
	}
	for _, want := range []string{"axon (3 packages)", "delta (1 package)", "0 declared", "2 observed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("notice %q does not contain %q", got, want)
		}
	}
	if strings.Index(got, "axon") > strings.Index(got, "delta") {
		t.Fatalf("notice %q is not sorted by system — a retire's own record must be deterministic", got)
	}
	if !strings.Contains(got, "never blocks retire") {
		t.Fatalf("notice %q does not say observed consumption is not a block — §9 requires the producer to get a named risk, not a lock", got)
	}
	if !strings.Contains(got, "adopt") || !strings.Contains(got, "acknowledg") {
		t.Fatalf("notice %q does not name the consumer's two existing exits (US-4: no new verb is minted)", got)
	}
}

// TestObservedConsumptionNoticeDedupesAndBoundsTheList keeps the line
// readable and deterministic the same way POL-006's own message already
// does: sorted, deduped, and capped with a "+N more" suffix.
func TestObservedConsumptionNoticeDedupesAndBoundsTheList(t *testing.T) {
	t.Parallel()

	var observed []ObservedConsumer
	for _, s := range []string{"s9", "s8", "s7", "s6", "s5", "s4", "s3", "s2", "s1", "s0"} {
		observed = append(observed, ObservedConsumer{System: s, Packages: 1})
	}
	observed = append(observed, ObservedConsumer{System: "s0", Packages: 1}) // duplicate

	got := ObservedConsumptionNotice(RetirePrecondition{Observed: observed})
	if !strings.Contains(got, "10 observed") {
		t.Fatalf("notice %q does not report 10 distinct observed systems (the duplicate must collapse)", got)
	}
	if !strings.Contains(got, "(+2 more)") {
		t.Fatalf("notice %q does not bound the rendered list at 8 with a +N suffix", got)
	}
	if strings.Contains(got, "s9 (") {
		t.Fatalf("notice %q rendered past the cap", got)
	}
}
