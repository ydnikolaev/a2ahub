package html

import (
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

func TestThreadRowOrderHasExplicitTies(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	if !threadRowComesBefore(at.Add(time.Minute), "z", "z", at, "a", "a") {
		t.Fatal("newer activity must sort first")
	}
	if !threadRowComesBefore(at, "alpha", "z", at, "beta", "a") {
		t.Fatal("equal activity must tie-break by space")
	}
	if !threadRowComesBefore(at, "alpha", "a", at, "alpha", "b") {
		t.Fatal("equal activity and space must tie-break by thread id")
	}
}

func TestExchangeItemKeepsCreationAndActivityClocksSeparate(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	activity := created.Add(30 * time.Hour)
	now := activity.Add(time.Hour)

	got := toItem(cache.Item{
		Space: "getvisa", ID: "XA-axon-20260801-order", Type: "announcement",
		Title: "Older announcement", From: "axon", To: []string{"seomatrix"},
		CreatedAt: created, CreatedSeq: 7, CreatedOrderKnown: true,
		LatestEventAt: activity, LatestEventSeq: 19, LatestEventID: "event-late-ack",
	}, now, "axon", openItemIndex{})

	if got.CreatedAt != created.Format(time.RFC3339) || got.CreatedSeq != 7 || !got.CreatedOrderKnown {
		t.Fatalf("creation key = (%q, %d, %v), want (%q, 7, true)", got.CreatedAt, got.CreatedSeq, got.CreatedOrderKnown, created.Format(time.RFC3339))
	}
	if got.MovedAt != activity.Format(time.RFC3339) || got.ActivitySeq != 19 || got.ActivityEventID != "event-late-ack" {
		t.Fatalf("activity key = (%q, %d, %q), want late acknowledge", got.MovedAt, got.ActivitySeq, got.ActivityEventID)
	}
	if want := humanizeAge(now, created); got.Age != want {
		t.Fatalf("Age = %q, want creation age %q", got.Age, want)
	}
}

func TestExchangeFeedComparatorUsesCreationNotActivity(t *testing.T) {
	t.Parallel()
	source := string(placeholderTemplate)
	start := strings.Index(source, "const byNewest =")
	end := strings.Index(source, "const workTypes =")
	if start < 0 || end <= start {
		t.Fatal("Exchange feed comparator is missing from the embedded dashboard")
	}
	comparator := source[start:end]
	for _, required := range []string{"createdOrderKnown", "createdSeq", "createdAt"} {
		if !strings.Contains(comparator, required) {
			t.Errorf("Exchange feed comparator does not use immutable creation key %q", required)
		}
	}
	for _, forbidden := range []string{"movedAt", "activitySeq", "activityEventId"} {
		if strings.Contains(comparator, forbidden) {
			t.Errorf("Exchange feed comparator uses lifecycle activity key %q", forbidden)
		}
	}
}
