package validate

import (
	"strings"
	"testing"
)

// TestCheckRetirePrecondition exercises the §5.4/D-022 retire-precondition
// policy check (spec 08 T1 "contract retire", §8 AC-202.2/AC-202.3;
// CC-081/CC-082/CC-086). Not part of TestRegistryClosure's own closure
// scan (registry_test.go is a sibling phase's file, off-limits to this
// phase's allowlist — see this phase's Deviations report): this test
// covers the check's own behavior; producing POL-006 for the registry
// closure scan is a follow-up one-line addition to that file.
func TestCheckRetirePrecondition(t *testing.T) {
	t.Parallel()

	t.Run("clean_ack_set_is_ungated", func(t *testing.T) {
		t.Parallel()
		v, overridden := CheckRetirePrecondition(RetirePrecondition{
			Consumers: []RegisteredConsumer{
				{System: "axon", Acked: true},
				{System: "seomatrix", Acked: true},
			},
		})
		if v != nil {
			t.Fatalf("got violation %+v, want none", v)
		}
		if overridden != nil {
			t.Fatalf("got overridden %v, want none", overridden)
		}
	})

	t.Run("left_consumers_excluded_from_the_ack_set", func(t *testing.T) {
		t.Parallel()
		v, overridden := CheckRetirePrecondition(RetirePrecondition{
			Consumers: []RegisteredConsumer{
				{System: "axon", Acked: true},
				{System: "gone", Acked: false, Left: true},
			},
		})
		if v != nil {
			t.Fatalf("got violation %+v, want none (left consumer excluded, §5.4 bullet (a))", v)
		}
		if overridden != nil {
			t.Fatalf("got overridden %v, want none", overridden)
		}
	})

	t.Run("unacked_no_override_blocked", func(t *testing.T) {
		t.Parallel()
		v, overridden := CheckRetirePrecondition(RetirePrecondition{
			Consumers: []RegisteredConsumer{
				{System: "axon", Acked: false},
			},
		})
		if v == nil {
			t.Fatal("got no violation, want POL-006 (AC-202.2)")
		}
		if v.Code != "POL-006" {
			t.Fatalf("got code %q, want POL-006", v.Code)
		}
		if overridden != nil {
			t.Fatalf("got overridden %v, want none (override not requested)", overridden)
		}
	})

	t.Run("override_before_sunset_still_blocked", func(t *testing.T) {
		t.Parallel()
		v, _ := CheckRetirePrecondition(RetirePrecondition{
			Consumers:    []RegisteredConsumer{{System: "axon", Acked: false}},
			Override:     true,
			SunsetPassed: false,
			HasReminder:  true,
			ActorIsHuman: true,
		})
		if v == nil || v.Code != "POL-006" {
			t.Fatalf("got %+v, want POL-006 (AC-202.3 first clause: sunset not passed)", v)
		}
	})

	t.Run("override_no_reminder_still_blocked", func(t *testing.T) {
		t.Parallel()
		v, _ := CheckRetirePrecondition(RetirePrecondition{
			Consumers:    []RegisteredConsumer{{System: "axon", Acked: false}},
			Override:     true,
			SunsetPassed: true,
			HasReminder:  false,
			ActorIsHuman: true,
		})
		if v == nil || v.Code != "POL-006" {
			t.Fatalf("got %+v, want POL-006 (AC-202.3 first clause: no reminder recorded)", v)
		}
	})

	t.Run("override_agent_actor_still_blocked", func(t *testing.T) {
		t.Parallel()
		v, _ := CheckRetirePrecondition(RetirePrecondition{
			Consumers:    []RegisteredConsumer{{System: "axon", Acked: false}},
			Override:     true,
			SunsetPassed: true,
			HasReminder:  true,
			ActorIsHuman: false,
		})
		if v == nil || v.Code != "POL-006" {
			t.Fatalf("got %+v, want POL-006 (AC-202.3 first clause: agent actor)", v)
		}
	})

	t.Run("override_full_precondition_set_succeeds_and_flags_overridden", func(t *testing.T) {
		t.Parallel()
		v, overridden := CheckRetirePrecondition(RetirePrecondition{
			Consumers: []RegisteredConsumer{
				{System: "zebra", Acked: false},
				{System: "axon", Acked: false},
				{System: "acked-one", Acked: true},
			},
			Override:     true,
			SunsetPassed: true,
			HasReminder:  true,
			ActorIsHuman: true,
		})
		if v != nil {
			t.Fatalf("got violation %+v, want none (AC-202.3 second clause: full precondition set met)", v)
		}
		want := []string{"axon", "zebra"}
		if len(overridden) != len(want) {
			t.Fatalf("got overridden %v, want %v", overridden, want)
		}
		for i, s := range want {
			if overridden[i] != s {
				t.Fatalf("got overridden %v, want %v (sorted, deterministic)", overridden, want)
			}
		}
	})
}

// TestCheckRetireVersionScope_NonCanonicalSpellingIsNotAPhantomVersion is the
// discriminating repro for an audit finding, and it is written to FAIL against
// the raw-string implementation it replaced.
//
// `publish` records its own parsed value ("1.0.0"); `deprecate` and `retire`
// record the operator's `--version` verbatim, so a perfectly reasonable
// operator typing "01.0.0" produced two map keys for one version. The guard
// then reported the contract's OWN sole version as "also published" and told
// the operator the refusal was "not a mistake in your command" — an
// authoritative wrong answer, which this project treats as worse than the
// condition it reports.
//
// The fixture must use DIFFERENT spellings across the events; using the same
// literal twice passes against the bug (the deprecate already unsets the key
// the delete would have removed) and proves nothing.
func TestCheckRetireVersionScope_NonCanonicalSpellingIsNotAPhantomVersion(t *testing.T) {
	t.Parallel()

	got := CheckRetireVersionScope([]ContractVersionEvent{
		{Subject: "XC-axon-ingest", Transition: "publish", Version: "1.0.0"},
		{Subject: "XC-axon-ingest", Transition: "deprecate", Version: "01.0.0"},
	}, "XC-axon-ingest", "01.0.0")
	if got != nil {
		t.Fatalf("phantom POL-011 on the contract's own sole version: %s", got.Message)
	}
}

// TestCheckRetireVersionScope_StillFiresOnAGenuineSecondVersion guards the
// other direction: canonicalising must not make the check blind. Without this,
// the test above could be satisfied by a function that never fires at all.
func TestCheckRetireVersionScope_StillFiresOnAGenuineSecondVersion(t *testing.T) {
	t.Parallel()

	got := CheckRetireVersionScope([]ContractVersionEvent{
		{Subject: "XC-axon-ingest", Transition: "publish", Version: "1.0.0"},
		{Subject: "XC-axon-ingest", Transition: "publish", Version: "2.0.0"},
		{Subject: "XC-axon-ingest", Transition: "deprecate", Version: "01.0.0"},
	}, "XC-axon-ingest", "1.0.0")
	if got == nil {
		t.Fatal("expected POL-011: 2.0.0 is still published while 1.0.0 retires")
	}
	if !strings.Contains(got.Message, "2.0.0") {
		t.Errorf("message must name the still-published version, got: %s", got.Message)
	}
}
