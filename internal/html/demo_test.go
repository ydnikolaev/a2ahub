package html

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestDemoFixtureParses verifies the design demo fixture
// (testdata/demo.json) unmarshals exactly into the Data model — a
// field-name typo in the fixture (or a drifted model) fails via
// DisallowUnknownFields — and that it exercises every drift value, every
// artifact type across the complete demo, and the hostile-title corner case the dashboard must
// render safely.
func TestDemoFixtureParses(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/demo.json")
	if err != nil {
		t.Fatalf("read testdata/demo.json: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var data Data
	if err := dec.Decode(&data); err != nil {
		t.Fatalf("decode testdata/demo.json into Data: %v", err)
	}

	if len(data.Nodes) < 5 {
		t.Errorf("len(Nodes) = %d, want >= 5", len(data.Nodes))
	}
	if data.Meta.Schema != "a2a-design-demo/v3" || !data.Meta.Synthetic {
		t.Fatalf("demo metadata = %+v, want admitted synthetic v3 contract", data.Meta)
	}
	wantCounts := map[string][2]int{
		"spaces": {len(data.Spaces), 4}, "nodes": {len(data.Nodes), 10},
		"contracts": {len(data.Contracts), 12}, "contractEdges": {len(data.ContractEdges), 15},
		"exchangeEdges": {len(data.ExchangeEdges), 20}, "inbox": {len(data.Inbox), 11},
		"outbox": {len(data.Outbox), 8}, "threadViews": {len(data.ThreadViews), 7},
		"artifactDetails": {len(data.ArtifactDetails), 12}, "unavailable": {len(data.Unavailable), 4},
	}
	for name, gotWant := range wantCounts {
		if gotWant[0] != gotWant[1] {
			t.Errorf("demo %s count = %d, want %d", name, gotWant[0], gotWant[1])
		}
	}

	wantDrifts := map[string]bool{
		"current": false, "behind": false, "deprecated": false,
		"retired": false, "dangling": false,
	}
	for _, e := range data.ContractEdges {
		if _, ok := wantDrifts[e.Drift]; ok {
			wantDrifts[e.Drift] = true
		}
	}
	for drift, seen := range wantDrifts {
		if !seen {
			t.Errorf("no ContractEdge with drift %q in demo.json", drift)
		}
	}

	wantTypes := map[string]bool{
		"question": false, "work_request": false, "contract": false,
		"requirement": false, "decision": false, "handoff": false,
		"response": false, "announcement": false,
	}
	for _, it := range append(append([]Item{}, data.Inbox...), data.Outbox...) {
		if _, ok := wantTypes[it.Type]; ok {
			wantTypes[it.Type] = true
		}
	}
	for _, detail := range data.ArtifactDetails {
		if _, ok := wantTypes[detail.Type]; ok {
			wantTypes[detail.Type] = true
		}
	}
	for typ, seen := range wantTypes {
		if !seen {
			t.Errorf("no demo record with type %q in demo.json", typ)
		}
	}

	found := false
	for _, it := range append(append([]Item{}, data.Inbox...), data.Outbox...) {
		if strings.Contains(it.Title, `</script><img`) && strings.Contains(it.Title, "onerror=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no demo item carries the hostile script/image-handler title verbatim")
	}

	for _, flag := range data.Flags {
		if strings.HasPrefix(flag.Code, "V") {
			t.Errorf("demo flag %q looks like an invented validation code; demo may only carry mounted fold/cache-index facts", flag.Code)
		}
		if flag.Source != "fold" && flag.Source != "cache-index" {
			t.Errorf("demo flag %q has unsupported source %q", flag.Code, flag.Source)
		}
	}
}

func TestDemoOperationalSnapshotUsesSharedProjection(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	if d.Operational.SchemaVersion != 1 || d.Operational.Revision == "" || len(d.Operational.Timeline) == 0 {
		t.Fatalf("operational snapshot is not a canonical shared projection: %#v", d.Operational)
	}
	foundConcurrentWork := false
	for _, row := range d.Operational.Timeline {
		if row.Space == "" || row.Thread == "" || row.Title == "" {
			t.Fatalf("operational row lacks qualified process identity: %+v", row)
		}
		if len(row.Work) > 1 {
			foundConcurrentWork = true
		}
	}
	if !foundConcurrentWork {
		t.Fatal("demo snapshot does not exercise simultaneous work on one process")
	}
}

func TestDemoDataCarriesEmbeddedReleaseNotes(t *testing.T) {
	t.Parallel()
	data, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	if len(data.ReleaseNotes) == 0 {
		t.Fatal("a2a html --demo must demonstrate the embedded release-note surface")
	}
	latest := data.ReleaseNotes[len(data.ReleaseNotes)-1]
	foundCurrentIssue := false
	for _, change := range latest.Changes {
		if change.ID == "KI-MACOS-ADHOC-SIGNING" {
			foundCurrentIssue = true
		}
	}
	if !foundCurrentIssue {
		t.Fatalf("latest HTML release note omits the standing known issue: %+v", latest)
	}
}

// TestDemoCarriesARollingWindow pins that the demo fixture actually
// DEMONSTRATES the per-version contract lifecycle (P4), which is the whole
// reason `--demo` exists: it is what a designer, a screenshot, and a new
// reader see, and a demo that shows only the pre-P4 shape teaches the wrong
// model to everyone who looks at it before they look at the code.
//
// Three shapes, deliberately, because each renders differently:
//   - a live window (several versions, at least one deprecated) — the steady
//     state the whole phase exists to make expressible;
//   - a window that contains a retired line beside a live successor;
//   - a fully retired legacy contract. The dense v4 fixture intentionally
//     models every contract with a version window; version-less compatibility
//     remains a unit concern rather than taking a design-demo slot.
func TestDemoCarriesARollingWindow(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	var live, retiredLine, fullyRetired int
	for _, c := range d.Contracts {
		if len(c.Versions) < 2 {
			if len(c.Versions) == 1 && c.Versions[0].State == "retired" && c.State == "retired" {
				fullyRetired++
			}
			continue
		}
		published, deprecated, retired := 0, 0, 0
		for _, v := range c.Versions {
			switch v.State {
			case "published":
				published++
			case "deprecated":
				deprecated++
			case "retired":
				retired++
			}
		}
		if published > 0 && deprecated > 0 {
			live++
		}
		if published > 0 && retired > 0 {
			retiredLine++
		}
	}

	if live == 0 {
		t.Error("no demo contract shows a live rolling window (>=1 published AND >=1 deprecated version) — " +
			"the demo teaches the pre-P4 model of one version per contract")
	}
	if retiredLine == 0 {
		t.Error("no demo contract shows a retired line beside a live successor")
	}
	if fullyRetired == 0 {
		t.Error("no demo contract shows a fully retired legacy window")
	}

	// The window is rendered in TWO places, and the first revision shipped
	// only one of them: under "You provide". The demo's own `self` provided
	// nothing, so that block never rendered at all and the feature was
	// invisible in the only page anyone looks at before reading code. Both
	// sides are pinned here because the consumer's side is the one that
	// matters more — during a sunset the question is not "what exists" but
	// "what is happening to the line I am pinned to".
	var selfProvidesAWindow, selfConsumesAWindow bool
	byID := map[string][]ContractVersion{}
	for _, c := range d.Contracts {
		byID[c.ID] = c.Versions
		if c.Provider == d.Self && len(c.Versions) >= 2 {
			selfProvidesAWindow = true
		}
	}
	for _, e := range d.ContractEdges {
		if e.From == d.Self && len(byID[e.Contract]) >= 2 {
			selfConsumesAWindow = true
		}
	}
	if !selfProvidesAWindow {
		t.Errorf("no contract PROVIDED by the demo's own system (%q) has a version window — the "+
			"\"You provide\" block is where the producer's own window renders, and it does not render at all "+
			"when self provides nothing", d.Self)
	}
	if !selfConsumesAWindow {
		t.Errorf("no contract CONSUMED by the demo's own system (%q) has a version window — that is the "+
			"dependency row, and it is where a consumer reads which of the provider's lines exist beside "+
			"their own pinned one", d.Self)
	}
}

// TestDemoOwnershipIsDerivedNotAuthored guards the rule that put a whose-move
// panel over an empty list: the fixture states WaitingOn, and Pending /
// WaitingOthers are computed from it. Decoding a fixture that predates a
// derived field yields its zero value, which reads as "nothing is pending" —
// silently, and only on the demo surface.
func TestDemoOwnershipIsDerivedNotAuthored(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	pending := 0
	waitingByThread := map[string][]string{}
	for _, tv := range d.ThreadViews {
		for _, oi := range tv.OpenItems {
			if oi.Pending != (len(oi.WaitingOn) > 0) {
				t.Fatalf("%s: Pending=%v with WaitingOn=%v — Pending must be derived from WaitingOn", oi.ID, oi.Pending, oi.WaitingOn)
			}
			if !oi.Pending {
				continue
			}
			pending++
			waitingByThread[tv.Thread] = append(waitingByThread[tv.Thread], oi.WaitingOn...)
		}
	}
	if pending == 0 {
		t.Fatal("no open item in the demo fixture is pending; the whose-move panel would render empty on every thread")
	}

	for _, th := range d.Threads {
		for i, who := range th.WaitingOthers {
			if who == d.Self {
				t.Fatalf("%s: WaitingOthers contains self %q — YourMove already carries that", th.ID, d.Self)
			}
			if i > 0 && th.WaitingOthers[i-1] >= who {
				t.Fatalf("%s: WaitingOthers is not sorted and deduped: %v", th.ID, th.WaitingOthers)
			}
			if !containsString(waitingByThread[th.ID], who) {
				t.Fatalf("%s: WaitingOthers names %q, which owes no pending move on that thread", th.ID, who)
			}
		}
		if th.Settled && len(th.WaitingOthers) > 0 {
			t.Fatalf("%s: settled thread still names %v as owing a move", th.ID, th.WaitingOthers)
		}
	}
}

// TestDemoRowFactsAreDerivedNotAuthored is the same guard one field further on.
// The exchange list sorts on CreatedAt and the prompt button appears on
// YourMove; both decode to their zero value from a fixture that predates them,
// and a zeroed sort key looks like an order rather than like a bug.
func TestDemoRowFactsAreDerivedNotAuthored(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	open := map[string]ThreadOpenItem{}
	for _, tv := range d.ThreadViews {
		for _, oi := range tv.OpenItems {
			open[tv.Space+"/"+oi.ID] = oi
		}
	}

	rows, dated, ours := 0, 0, 0
	for _, list := range [][]Item{d.Inbox, d.Outbox, d.Archive} {
		for _, it := range list {
			rows++
			if it.Age == "" {
				if it.CreatedAt != "" {
					t.Errorf("%s: no age but CreatedAt=%q", it.ID, it.CreatedAt)
				}
				continue
			}
			if it.CreatedAt == "" {
				t.Errorf("%s: age %q with no CreatedAt — the exchange list would sort it at random", it.ID, it.Age)
				continue
			}
			created, parseErr := time.Parse(time.RFC3339, it.CreatedAt)
			if parseErr != nil {
				t.Errorf("%s: CreatedAt %q does not parse: %v", it.ID, it.CreatedAt, parseErr)
				continue
			}
			if got := humanizeAge(d.GeneratedAt, created); got != it.Age {
				t.Errorf("%s: CreatedAt reformats to %q, but the row is labelled %q", it.ID, got, it.Age)
			}
			dated++

			oi, hasOpen := open[it.Space+"/"+it.ID]
			if want := hasOpen && containsString(oi.WaitingOn, d.Self); it.YourMove != want {
				t.Errorf("%s: YourMove=%v, but the folded item waits on %v", it.ID, it.YourMove, oi.WaitingOn)
			}
			if it.YourMove {
				ours++
				if it.Prompt == nil {
					t.Errorf("%s: the move is ours and the button would have nothing to copy", it.ID)
					continue
				}
				if len(it.Prompt.Moves) == 0 || it.Prompt.Loop == "" {
					t.Errorf("%s: prompt facts are incomplete: %+v", it.ID, *it.Prompt)
				}
				for _, ask := range it.Prompt.AskFirst {
					if !containsString(it.Prompt.Moves, ask) {
						t.Errorf("%s: askFirst %q is not one of the offered moves %v", it.ID, ask, it.Prompt.Moves)
					}
				}
			}
		}
	}
	if dated != rows {
		t.Fatalf("only %d of %d exchange rows carry a sort key", dated, rows)
	}
	if ours == 0 {
		t.Fatal("no exchange row in the demo is ours to move on; the prompt button would never appear")
	}
}
