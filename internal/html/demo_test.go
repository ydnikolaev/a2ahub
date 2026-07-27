package html

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// TestDemoFixtureParses verifies the design demo fixture
// (testdata/demo.json) unmarshals exactly into the Data model — a
// field-name typo in the fixture (or a drifted model) fails via
// DisallowUnknownFields — and that it exercises every drift value, every
// inbox item type, and the hostile-title corner case the dashboard must
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
	for _, it := range data.Inbox {
		if _, ok := wantTypes[it.Type]; ok {
			wantTypes[it.Type] = true
		}
	}
	for typ, seen := range wantTypes {
		if !seen {
			t.Errorf("no Inbox item with type %q in demo.json", typ)
		}
	}

	const hostileTitle = `</script><img src=x onerror=alert(1)>`
	found := false
	for _, it := range data.Inbox {
		if it.Title == hostileTitle {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no Inbox item has the hostile title %q verbatim", hostileTitle)
	}
}

// TestDemoCarriesARollingWindow pins that the demo fixture actually
// DEMONSTRATES the per-version contract lifecycle (P4), which is the whole
// reason `--demo` exists: it is what a designer, a screenshot, and a new
// reader see, and a demo that shows only the pre-P4 shape teaches the wrong
// model to everyone who looks at it before they look at the code.
//
// Three shapes, deliberately, because each renders differently and each is a
// thing a reader will otherwise mistake for a bug:
//   - a live window (several versions, at least one deprecated) — the steady
//     state the whole phase exists to make expressible;
//   - a contract whose every published line is deprecated, so the SUBJECT
//     projects `deprecated` while its versions are individually not retired;
//   - a contract with NO versions recorded at all, which must still render
//     exactly as it did before P4.
func TestDemoCarriesARollingWindow(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	var live, allDeprecated, versionless int
	for _, c := range d.Contracts {
		if len(c.Versions) == 0 {
			versionless++
			continue
		}
		if len(c.Versions) < 2 {
			continue
		}
		published, deprecated := 0, 0
		for _, v := range c.Versions {
			switch v.State {
			case "published":
				published++
			case "deprecated":
				deprecated++
			}
		}
		if published > 0 && deprecated > 0 {
			live++
		}
		if published == 0 && deprecated > 0 {
			allDeprecated++
		}
	}

	if live == 0 {
		t.Error("no demo contract shows a live rolling window (>=1 published AND >=1 deprecated version) — " +
			"the demo teaches the pre-P4 model of one version per contract")
	}
	if allDeprecated == 0 {
		t.Error("no demo contract has every published line deprecated — that is the projection case a reader " +
			"is most likely to read as a bug, so it is the one the demo must show")
	}
	if versionless == 0 {
		t.Error("no demo contract is version-less — a history that predates per-version recording must render " +
			"as it always did, and the demo is where that is visible")
	}
}
