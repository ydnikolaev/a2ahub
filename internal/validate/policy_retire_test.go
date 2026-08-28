package validate

import (
	"slices"
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

	t.Run("blocked_names_sorted_consumers_and_keeps_full_machine_set", func(t *testing.T) {
		t.Parallel()
		consumers := make([]RegisteredConsumer, 0, 11)
		for _, system := range []string{"k", "j", "i", "h", "g", "f", "e", "d", "c", "b", "a"} {
			consumers = append(consumers, RegisteredConsumer{System: system})
		}
		v, _ := CheckRetirePrecondition(RetirePrecondition{Consumers: consumers})
		if v == nil {
			t.Fatal("got no violation")
		}
		want := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
		if !slices.Equal(v.Subjects, want) {
			t.Fatalf("Subjects = %v, want full sorted set %v", v.Subjects, want)
		}
		if !strings.Contains(v.Message, "a, b, c, d, e, f, g, h (+3 more)") {
			t.Fatalf("bounded Message does not name the actionable prefix/count: %q", v.Message)
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

// TestObservedConsumptionNoticeNamesTheVersion is spec 06 §8 criteria 3/4
// (answers-that-hold-2026-08): retire is per-version, so the notice must
// name the version each observed consumer pinned, not just the system.
func TestObservedConsumptionNoticeNamesTheVersion(t *testing.T) {
	t.Parallel()

	got := ObservedConsumptionNotice(RetirePrecondition{
		Observed: []ObservedConsumer{{System: "axon", Version: "1.0.0", Packages: 2}},
	})
	if !strings.Contains(got, "1.0.0") {
		t.Fatalf("notice %q does not name the pinned version", got)
	}
	if !strings.Contains(got, "axon (2 packages @ 1.0.0)") {
		t.Fatalf("notice %q does not render system, count and version together: %q", got, "axon (2 packages @ 1.0.0)")
	}
}

// TestObservedConsumptionNoticeReportsTwoVersionsDistinctly is spec 06 §8
// criterion 5, and the exact case a naive map[string]int keyed by System
// alone gets wrong: the SAME system pinning TWO different versions of ONE
// contract must render as two separate entries, never one collapsed count.
//
// TEETH: key the notice's own dedup map by System alone (drop Version) and
// this reds — one of the two versions silently disappears, its own count
// dropped rather than shown.
func TestObservedConsumptionNoticeReportsTwoVersionsDistinctly(t *testing.T) {
	t.Parallel()

	got := ObservedConsumptionNotice(RetirePrecondition{
		Observed: []ObservedConsumer{
			{System: "axon", Version: "1.0.0", Packages: 2},
			{System: "axon", Version: "2.0.0", Packages: 1},
		},
	})
	if !strings.Contains(got, "2 observed and undeclared") {
		t.Fatalf("notice %q does not count the two (system, version) pairs distinctly: %q", got, "2 observed and undeclared")
	}
	if !strings.Contains(got, "axon (2 packages @ 1.0.0)") {
		t.Fatalf("notice %q is missing the 1.0.0 entry", got)
	}
	if !strings.Contains(got, "axon (1 package @ 2.0.0)") {
		t.Fatalf("notice %q is missing the 2.0.0 entry", got)
	}
}

// TestObservedConsumptionNoticeVersionIsOptional pins the empty-Version
// case (a delivery whose pinned reference carried no `@version` at all):
// the rendered line must fall back to the pre-version-field shape rather
// than printing a stray " @ ".
func TestObservedConsumptionNoticeVersionIsOptional(t *testing.T) {
	t.Parallel()

	got := ObservedConsumptionNotice(RetirePrecondition{
		Observed: []ObservedConsumer{{System: "axon", Packages: 1}},
	})
	if !strings.Contains(got, "axon (1 package)") {
		t.Fatalf("notice %q does not render the no-version case as before: %q", got, "axon (1 package)")
	}
	if strings.Contains(got, "@") {
		t.Fatalf("notice %q renders a stray version marker with no version present", got)
	}
}
