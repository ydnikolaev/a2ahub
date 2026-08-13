package cache

import (
	"reflect"
	"testing"
	"time"
)

func TestOrderAttentionTierExhaustive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Minute)
	older := now.Add(-time.Hour)
	items := []Item{
		{ID: "tier6-zero", Space: "sp1"},
		{ID: "tier5-stale", Space: "sp1", SyncStale: true, LatestEventAt: recent},
		{ID: "tier4-overdue", Space: "sp1", NeededBy: "2026-08-12", LatestEventAt: recent},
		{ID: "tier3-gate", Space: "sp1", HumanGate: "G3", LatestEventAt: recent},
		{ID: "tier2-blocking", Space: "sp1", Blocking: true, LatestEventAt: recent},
		// Matching every lower tier must not dislodge YourMove from tier 1.
		{ID: "tier1-multi", Space: "sp1", YourMove: true, Blocking: true, HumanGate: "G3", NeededBy: "2026-08-12", SyncStale: true},
		{ID: "tier6-recent", Space: "sp1", LatestEventAt: recent},
		{ID: "tier6-older", Space: "sp1", LatestEventAt: older},
		// Priority is deliberately not a tier: this remains in recency tier 6.
		{ID: "tier6-p1", Space: "sp1", Priority: "p1", LatestEventAt: older},
		// Tier 1 ties by id and then space, never by input order.
		{ID: "tier1-a", Space: "sp2", YourMove: true},
		{ID: "tier1-a", Space: "sp1", YourMove: true},
		// A deadline on the comparison day has not passed under overdueAt's
		// end-of-day rule and therefore remains in tier 6.
		{ID: "tier6-due-today", Space: "sp1", NeededBy: "2026-08-14", LatestEventAt: recent},
	}
	original := append([]Item(nil), items...)

	got := OrderAttention(items, now)
	wantIDs := []string{
		"tier1-a@sp1", "tier1-a@sp2", "tier1-multi@sp1",
		"tier2-blocking@sp1",
		"tier3-gate@sp1",
		"tier4-overdue@sp1",
		"tier5-stale@sp1",
		"tier6-due-today@sp1", "tier6-recent@sp1",
		"tier6-older@sp1", "tier6-p1@sp1",
		"tier6-zero@sp1",
	}
	gotIDs := make([]string, 0, len(got))
	for _, item := range got {
		gotIDs = append(gotIDs, item.ID+"@"+item.Space)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("OrderAttention tiers\n got: %v\nwant: %v", gotIDs, wantIDs)
	}
	if !reflect.DeepEqual(items, original) {
		t.Fatalf("OrderAttention mutated input\n got: %#v\nwant: %#v", items, original)
	}
	if len(items) > 0 && &got[0] == &items[0] {
		t.Fatal("OrderAttention returned the input backing array, want a fresh slice")
	}
}

func TestOrderAttentionDeterministicFreshAndEmpty(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	equal := now.Add(-time.Hour)
	items := []Item{
		{ID: "same", Space: "z-space", LatestEventAt: equal},
		{ID: "same", Space: "a-space", LatestEventAt: equal},
	}
	first := OrderAttention(items, now)
	second := OrderAttention(items, now)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same input produced different orders\nfirst: %#v\nsecond: %#v", first, second)
	}
	if got := []string{first[0].Space, first[1].Space}; !reflect.DeepEqual(got, []string{"a-space", "z-space"}) {
		t.Fatalf("equal id/time cross-space tie = %v, want [a-space z-space]", got)
	}
	if len(first) > 0 && &first[0] == &second[0] {
		t.Fatal("two OrderAttention calls share a backing array")
	}

	empty := OrderAttention(nil, now)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("OrderAttention(nil) = %#v, want a fresh non-nil empty slice", empty)
	}
}
