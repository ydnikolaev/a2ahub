package html

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
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
		// The work-report list these two lines pinned is gone: a checkpoint reads
		// inside the document it describes, inside its thread, and as its own
		// card opened from either — the list was a fourth place, detached from
		// its subject, under a hint that said "in received order" while the
		// server ordered by space and thread.
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

// The Exchange feed order, at the one place it is decided. Two defects sit
// behind these cases. The archive reached the browser in no order at all — a
// 2026-08-19 axon snapshot interleaved a 28.07 document between two 17.08 ones
// — so the window cut kept an arbitrary subset instead of the newest. And the
// screen selects direction ACROSS the three collections, so their concatenation
// showed three ordered runs and no order: "2 weeks, 3 weeks, 1 day, 1 day".
func TestExchangeFeedOrdersByCreationNewestFirst(t *testing.T) {
	t.Parallel()
	item := func(space, id, created string, seq int64, known bool) Item {
		return Item{Space: space, ID: id, CreatedAt: created, CreatedSeq: seq, CreatedOrderKnown: known}
	}

	// Within one space the committed sequence is exact where timestamps tie.
	if !exchangeItemComesBefore(item("a", "x", "2026-08-01T00:00:00Z", 9, true), item("a", "y", "2026-08-01T00:00:00Z", 4, true)) {
		t.Error("a later committed sequence in the same space must come first")
	}
	// It is consulted only when both sides know their committed position:
	// sequence zero is meaningful, absence of the fact is not.
	if !exchangeItemComesBefore(item("a", "x", "2026-08-02T00:00:00Z", 0, false), item("a", "y", "2026-08-01T00:00:00Z", 9, false)) {
		t.Error("without a known committed order the creation instant decides")
	}
	// And the instant LEADS the sequence: getvisa really does commit an older
	// document at a higher sequence, and ranking by sequence there prints a
	// two-week-old row above a two-and-a-half-week-old one while the age column
	// says the opposite. This is the case that decided the order of the two.
	if !exchangeItemComesBefore(item("getvisa", "XS-newer-created", "2026-08-01T18:59:38Z", 40, true), item("getvisa", "XC-older-created", "2026-07-29T16:49:35Z", 48, true)) {
		t.Error("a later creation instant must outrank a later committed sequence")
	}
	if !exchangeItemComesBefore(item("a", "x", "2026-08-02T00:00:00Z", 0, false), item("b", "y", "2026-08-01T00:00:00Z", 0, true)) {
		t.Error("across spaces the newer creation instant comes first")
	}
	// An unrecorded creation instant must never read as the newest thing that
	// happened; it sorts last, deterministically.
	if !exchangeItemComesBefore(item("a", "x", "2026-08-01T00:00:00Z", 0, false), item("a", "y", "", 0, false)) {
		t.Error("an item with no recorded creation instant sorts after one that has it")
	}
	if exchangeItemComesBefore(item("a", "y", "", 0, false), item("a", "x", "2026-08-01T00:00:00Z", 0, false)) {
		t.Error("and the same pair must not reverse when compared the other way round")
	}
	// Ties are broken so the order is total, never snapshot-dependent.
	if !exchangeItemComesBefore(item("alpha", "z", "2026-08-01T00:00:00Z", 0, false), item("beta", "a", "2026-08-01T00:00:00Z", 0, false)) {
		t.Error("equal creation must tie-break by space")
	}
	if !exchangeItemComesBefore(item("alpha", "a", "2026-08-01T00:00:00Z", 0, false), item("alpha", "b", "2026-08-01T00:00:00Z", 0, false)) {
		t.Error("equal creation and space must tie-break by id")
	}

	archive := []Item{
		item("getvisa", "XA-old", "2026-07-28T19:08:22Z", 18, true),
		item("getvisa", "XA-new", "2026-08-18T00:06:38Z", 78, true),
		item("getvisa", "XA-mid", "2026-08-17T22:17:03Z", 76, true),
	}
	sortExchangeFeed(archive)
	if got := []string{archive[0].ID, archive[1].ID, archive[2].ID}; got[0] != "XA-new" || got[1] != "XA-mid" || got[2] != "XA-old" {
		t.Fatalf("archive order = %v, want newest first", got)
	}
}

// The rank is what the browser reads instead of re-deriving the rule, so it has
// to describe the MERGED feed — one dense 1..n sequence across all three
// collections — and it has to be stamped before any window is cut, or a
// truncated collection would carry ranks that no longer interleave.
func TestExchangeFeedRankMergesEveryCollectionBeforeTheWindow(t *testing.T) {
	t.Parallel()
	at := func(day int) string { return time.Date(2026, 8, day, 12, 0, 0, 0, time.UTC).Format(time.RFC3339) }
	inbox := []Item{{Space: "s", ID: "in-1", CreatedAt: at(5)}}
	outbox := []Item{{Space: "s", ID: "out-1", CreatedAt: at(9)}}
	archive := []Item{{Space: "s", ID: "arc-1", CreatedAt: at(7)}, {Space: "s", ID: "arc-2", CreatedAt: at(3)}}
	for _, collection := range [][]Item{inbox, outbox, archive} {
		sortExchangeFeed(collection)
	}
	rankExchangeFeed(inbox, outbox, archive)

	ranks := map[string]int64{}
	for _, collection := range [][]Item{inbox, outbox, archive} {
		for _, item := range collection {
			if item.FeedRank == 0 {
				t.Fatalf("%s carries no feed rank; the browser would have nothing to order by", item.ID)
			}
			ranks[item.ID] = item.FeedRank
		}
	}
	want := map[string]int64{"out-1": 1, "arc-1": 2, "in-1": 3, "arc-2": 4}
	for id, wantRank := range want {
		if ranks[id] != wantRank {
			t.Errorf("%s rank = %d, want %d (ranks: %v)", id, ranks[id], wantRank, ranks)
		}
	}
}

// One document, two projections, one answer. The list row reads Item.Outcome
// and the pane beside it reads ArtifactDetail.Outcome, and until
// cache.ShowResult carried MoveOwed the two asked DIFFERENT questions: the row
// asked fold.OutcomeOfDocument with the pendency fact, the detail asked
// fold.OutcomeOf without it. They disagreed for exactly the pairs that fact
// narrows — a published announcement and a published contract — which on axon
// was six of twenty-five documents, each of them saying "Settled" on the card
// and "Awaiting a move" in the pane at the same time.
//
// This walks every field the two projections share, not just outcome: the
// defect was one instance of a class, and the class is "the same fact derived
// twice". A new field on both types is covered the day it is added.
func TestArtifactDetailNeverContradictsItsListRow(t *testing.T) {
	t.Parallel()
	data, err := DemoData()
	if err != nil {
		t.Fatalf("demo data: %v", err)
	}
	details := make(map[[2]string]ArtifactDetail, len(data.ArtifactDetails))
	for _, detail := range data.ArtifactDetails {
		details[[2]string{detail.Space, detail.ID}] = detail
	}
	checked := 0
	for _, collection := range [][]Item{data.Inbox, data.Outbox, data.Archive} {
		for _, item := range collection {
			detail, ok := details[[2]string{item.Space, item.ID}]
			if !ok {
				continue
			}
			checked++
			if item.Outcome != detail.Outcome {
				t.Errorf("%s/%s: row says outcome %q, its detail says %q — one document cannot be settled in the list and awaiting a move in the pane",
					item.Space, item.ID, item.Outcome, detail.Outcome)
			}
			if item.Terminal != detail.Terminal {
				t.Errorf("%s/%s: row says terminal %v, its detail says %v", item.Space, item.ID, item.Terminal, detail.Terminal)
			}
			if item.State != detail.State {
				t.Errorf("%s/%s: row says state %q, its detail says %q", item.Space, item.ID, item.State, detail.State)
			}
			if item.Type != detail.Type {
				t.Errorf("%s/%s: row says type %q, its detail says %q", item.Space, item.ID, item.Type, detail.Type)
			}
			if item.Thread != detail.Thread {
				t.Errorf("%s/%s: row says thread %q, its detail says %q", item.Space, item.ID, item.Thread, detail.Thread)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no item carried a matching artifact detail — this test would pass vacuously")
	}
	// And the two-fact form really is the wrong answer for those pairs, so a
	// future edit that quietly restores it cannot pass by luck on a fixture
	// where every document happens to owe a move.
	narrowed := 0
	for _, detail := range data.ArtifactDetails {
		if fold.OutcomeOf(fold.Kind(detail.Type), fold.State(detail.State)) != detail.Outcome {
			narrowed++
		}
	}
	if narrowed == 0 {
		t.Error("no demo document exercises the pairs OutcomeOfDocument narrows; the guard above would pass against the old two-fact call")
	}
}
