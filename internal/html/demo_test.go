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
