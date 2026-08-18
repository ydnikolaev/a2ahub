package html

import (
	"os"
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

func TestExchangeFeedPreservesServerCarriedOrder(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../web/design-source/ExchangeView.dc.html")
	if err != nil {
		t.Fatalf("read ExchangeView: %v", err)
	}
	source := string(raw)
	shipped := dashboardTemplateCorpus(t)
	for _, required := range []string{
		`const carried = aggregate ? (aggregate.items || []) : collectionFor(tab);`,
		// P5 (wave 3) added the facet axes, so the in-scope list became the BASE
		// the facets filter, and gained its name. Still no sort: the order is
		// whatever the server carried, all the way to the row.
		`const workListBase = aggregate ? carried : carried.filter(item => this.inScope(item.space));`,
		`const workReportRows = workReports.map(report =>`,
		`workReportsHint:ru ? "в порядке поступления" : "in received order"`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("Exchange view is missing carried-order contract %q", required)
		}
		if !strings.Contains(shipped, required) {
			t.Errorf("shipped dashboard is missing carried-order contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`const byNewest =`,
		`createdOrderKnown`,
		`Number(right.commit_sequence || 0) - Number(left.commit_sequence || 0)`,
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("Exchange view still owns a local ordering rule %q", forbidden)
		}
	}
}
